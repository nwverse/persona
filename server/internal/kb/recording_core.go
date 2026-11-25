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

func (c recordingCore) ReadOnly() bool                         { return c.e.Store.ReadOnly() }
func (c recordingCore) Zone() *time.Location                   { return c.e.Store.Zone() }
func (c recordingCore) Recording(id string) (*Recording, bool) { return c.e.Store.Recording(id) }
func (c recordingCore) Recordings() []*Recording               { return c.e.Store.Recordings() }
func (c recordingCore) RecordingName(r *Recording) string      { return c.e.recordingName(r) }
func (c recordingCore) RecordingSnapshot(id string) (map[string][]byte, error) {
	return c.e.Store.recordingSnapshot(id)
}
func (c recordingCore) RecordSnapshot(id string) (map[string][]byte, *Meeting, HubTranscript, error) {
	return c.e.Store.recordSnapshot(id)
}
func (c recordingCore) TranscriptOfRecord(hubMode bool, device, hub []RecordingSegment) []RecordingSegment {
	return TranscriptOfRecord(hubMode, device, hub)
}
func (c recordingCore) ReadMinutes(id string) (*Minutes, error)     { return c.e.Store.ReadMinutes(id) }
func (c recordingCore) ReadRecap(id string) (*DayRecap, error)      { return c.e.Store.readRecap(id) }
func (c recordingCore) PhotoTexts(id string) []PhotoText            { return c.e.PhotoTexts(id) }
func (c recordingCore) PhotoWords(p PhotoText, clipped bool) string { return photoWords(p, clipped) }
func (c recordingCore) PhotoBook(id string) (recordingwire.PhotoBook, error) {
	b, err := c.e.Store.readPhotos(id)
	return storedPhotos{b, c.e.Store}, err
}
func (c recordingCore) CalendarItems(kind string) []*recordingwire.CalendarItem {
	items := c.e.Store.Items(kind)
	out := make([]*recordingwire.CalendarItem, len(items))
	for i, it := range items {
		out[i] = &recordingwire.CalendarItem{Kind: it.Kind, ID: it.ID, Title: it.Title, At: it.At}
	}
	return out
}
func (c recordingCore) Path(id string, parts ...string) string {
	return c.e.Store.path(KindRecording, id, parts...)
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
func (c recordingCore) RecoverRecording(id string) error         { return c.e.Store.recoverRecordingLocked(id) }
func (c recordingCore) WriteFile(path string, data []byte) error { return writeFile(path, data) }
func (c recordingCore) PutMeta(r *Recording) error {
	return c.e.Store.putEntity(KindRecording, r.ID, r, &r.Tagging, false)
}
func (c recordingCore) PutSummary(id, text string) error {
	return c.e.Store.putSummary(KindRecording, id, text, false)
}
func (c recordingCore) BumpPhotosRev(id string, start time.Time) error {
	return c.e.Store.bumpPhotosRev(id, start)
}
func (c recordingCore) Notify(id, what string) { c.e.Store.notify(KindRecording, id, what) }
func (c recordingCore) Index(id, name string, t Tagging, oneLine string, updated time.Time) {
	c.e.index(KindRecording, id, name, t, oneLine, updated)
}
func (c recordingCore) MeetingChanged(id string, parts []string) { c.e.meetingChanged(id, parts) }
func (c recordingCore) DropAudio(id string, why error) error     { return c.e.dropAudio(id, why) }
func (c recordingCore) Now() time.Time                           { return c.e.now() }
func (c recordingCore) Logf(format string, args ...any)          { c.e.logf(format, args...) }
func (c recordingCore) Models() (recordingwire.Model, func(string) recordingwire.Model) {
	if c.e.ModelFor == nil {
		return c.e.Model, nil
	}
	return c.e.Model, func(purpose string) recordingwire.Model { return c.e.ModelFor(purpose) }
}
func (c recordingCore) OutputLang(r *Recording) string { return string(c.e.outputLang(r)) }
func (c recordingCore) WritingOwner(lang string) (owner, language string) {
	o := c.e.writingOwner(i18n.Lang(lang))
	return o.Owner, o.Language
}
func (c recordingCore) DayRecapClock() func() (hour, minute int) { return c.e.DayRecapClock }
