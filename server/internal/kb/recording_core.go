package kb

import (
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The storage port's Core side (docs/plans/plan-kb-recording-port.md, sections 3.5 and 3.6): engine e as
// recordingwire.Core, which the recording module's writers reach the store through. Every method is the function the
// writers called while they lived in this package: the read group is the knowledge base's own readers, the write
// group its write primitives, locks and settings. Nothing here is new behaviour.

// RecordingCore is engine e as the storage port.
func RecordingCore(e *Engine) recordingwire.Core { return recordingCore{e} }

type recordingCore struct{ e *Engine }

// The read group.

func (c recordingCore) ReadOnly() bool       { return c.e.Store.ReadOnly() }
func (c recordingCore) Zone() *time.Location { return c.e.Store.Zone() }
func (c recordingCore) Recording(id recordingwire.ID) (*Recording, bool) {
	return c.e.Store.Recording(string(id))
}
func (c recordingCore) Recordings() []*Recording          { return c.e.Store.Recordings() }
func (c recordingCore) RecordingName(r *Recording) string { return c.e.recordingName(r) }
func (c recordingCore) RecordingSnapshot(id recordingwire.ID) (recordingwire.Files, error) {
	files, err := c.e.Store.recordingSnapshot(string(id))
	return portFiles(files), err
}
func (c recordingCore) RecordSnapshot(id recordingwire.ID) (recordingwire.Files, *Meeting, HubTranscript, error) {
	files, m, hub, err := c.e.Store.recordSnapshot(string(id))
	return portFiles(files), m, hub, err
}
func (c recordingCore) TranscriptOfRecord(hubMode bool, device, hub []RecordingSegment) []RecordingSegment {
	return TranscriptOfRecord(hubMode, device, hub)
}
func (c recordingCore) ReadMinutes(id recordingwire.ID) (*Minutes, error) {
	return c.e.Store.ReadMinutes(string(id))
}
func (c recordingCore) ReadRecap(id recordingwire.ID) (*DayRecap, error) {
	return c.e.Store.readRecap(string(id))
}
func (c recordingCore) PhotoTexts(id recordingwire.ID) []PhotoText  { return c.e.PhotoTexts(string(id)) }
func (c recordingCore) PhotoWords(p PhotoText, clipped bool) string { return photoWords(p, clipped) }
func (c recordingCore) PhotoBook(id recordingwire.ID) (recordingwire.PhotoBook, error) {
	b, err := c.e.Store.readPhotos(string(id))
	return storedPhotos{b, c.e.Store}, err
}
func (c recordingCore) CalendarItems(kind recordingwire.CalendarKind) []recordingwire.CalendarItem {
	items := c.e.Store.Items(string(kind))
	out := make([]recordingwire.CalendarItem, len(items))
	for i, it := range items {
		out[i] = recordingwire.CalendarItem{Kind: recordingwire.CalendarKind(it.Kind), ID: recordingwire.ItemID(it.ID), Title: it.Title, At: it.At}
	}
	return out
}
func (c recordingCore) Path(id recordingwire.ID, f recordingwire.File) string {
	return c.e.Store.path(KindRecording, string(id), string(f))
}

// portFiles is a snapshot's files under the port's type; nil stays nil.
func portFiles(files map[string][]byte) recordingwire.Files {
	if files == nil {
		return nil
	}
	out := make(recordingwire.Files, len(files))
	for name, data := range files {
		out[recordingwire.File(name)] = data
	}
	return out
}

// storedPhotos is a photo book as the minutes' writer asks it (recordingwire.PhotoBook).
type storedPhotos struct {
	b photoBook
	s *Store
}

func (p storedPhotos) DropUnlisted(m *Minutes) { p.s.dropUnlistedPhotos(m, p.b) }
func (p storedPhotos) CitedChanged(before, after map[int]bool) bool {
	return citedChanged(p.b, before, after)
}

// The write-and-lock group.

func (c recordingCore) Locks() recordingwire.Locks {
	s := c.e.Store
	return recordingwire.Locks{Recordings: &s.recMu, Files: &s.recFilesMu, Meta: &s.metaMu}
}
func (c recordingCore) RecoverRecording(id recordingwire.ID) error {
	return c.e.Store.recoverRecordingLocked(string(id))
}
func (c recordingCore) WriteFile(path string, data []byte) error { return writeFile(path, data) }
func (c recordingCore) PutMeta(r *Recording) error {
	return c.e.Store.putEntity(KindRecording, r.ID, r, &r.Tagging, false)
}
func (c recordingCore) PutSummary(id recordingwire.ID, text string) error {
	return c.e.Store.putSummary(KindRecording, string(id), text, false)
}
func (c recordingCore) BumpPhotosRev(id recordingwire.ID, start time.Time) error {
	return c.e.Store.bumpPhotosRev(string(id), start)
}
func (c recordingCore) Notify(id recordingwire.ID, what recordingwire.Change) {
	c.e.Store.notify(KindRecording, string(id), string(what))
}
func (c recordingCore) Index(en recordingwire.IndexEntry) {
	c.e.index(KindRecording, string(en.ID), en.Name, en.Tagging, en.OneLine, en.Updated)
}
func (c recordingCore) MeetingChanged(id recordingwire.ID, parts []recordingwire.MeetingPart) {
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = string(p)
	}
	c.e.meetingChanged(string(id), names)
}
func (c recordingCore) DropAudio(id recordingwire.ID, why error) error {
	return c.e.dropAudio(string(id), why)
}
func (c recordingCore) Now() time.Time                  { return c.e.now() }
func (c recordingCore) Logf(format string, args ...any) { c.e.logf(format, args...) }
func (c recordingCore) Model(p recordingwire.Purpose) recordingwire.Model {
	if m := c.e.modelFor(string(p)); m != nil {
		return m
	}
	return nil
}
func (c recordingCore) HasModel() bool { return c.e.Model != nil || c.e.ModelFor != nil }
func (c recordingCore) OutputLang(r *Recording) recordingwire.Language {
	return recordingwire.Language(c.e.outputLang(r))
}
func (c recordingCore) WritingOwner(lang recordingwire.Language) recordingwire.Owner {
	o := c.e.writingOwner(i18n.Lang(lang))
	return recordingwire.Owner{Name: o.Owner, Language: o.Language}
}
func (c recordingCore) DayRecapClock() (recordingwire.Clock, bool) {
	if c.e.DayRecapClock == nil {
		return recordingwire.Clock{}, false
	}
	h, m := c.e.DayRecapClock()
	return recordingwire.Clock{Hour: h, Minute: m}, true
}
