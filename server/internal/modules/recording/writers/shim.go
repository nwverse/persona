package writers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"github.com/nwverse/persona/server/internal/textfmt"
)

// The writers were written as methods of the knowledge base's Engine and Store. Here those two are this package's
// own engine and store: they hold the storage port (recordingwire.Core) and give the writers every member they used
// under the name it had, each a call of the port. So the writers' bodies are the ones that were in
// internal/kb, and this file is the whole of the adaptation: what a name means here is read below, not in the body.

// engine is the knowledge base's engine as the writers see it: the port behind Store, the models and the evening
// clock as the port has them when it is made (Writers.engine makes one a call), and the module's callbacks.
type engine struct {
	Store *store
	// ModelFor is the engine's model for a purpose (recordingwire.Writer.Model), nil when the engine has no model at
	// all; Model is always nil here (the writers ask "is there any model" of the two together).
	Model    Model
	ModelFor func(string) Model
	// MinutesWritten is told when a recording's minutes for its current revision are written; DayRecapWritten once a
	// day of the recap the day's push goes with (took: the push ids it took; an error leaves the push due).
	MinutesWritten  func(ctx context.Context, r Recording, m Minutes)
	DayRecapWritten func(ctx context.Context, r Recording, d DayRecap) (took []string, err error)
	// DayRecapClock is the evening routine's time of day (recordingwire.Writer.DayRecapClock).
	DayRecapClock func() (hour, minute int)

	// jobLeft is what a job has left of its time (tests); nil: ctx's deadline.
	jobLeft func(ctx context.Context) time.Duration
	// minutesCall and minutesPart are the runes of transcript one minutes call and one part read (tests); 0:
	// minutesOneCall and minutesChunk.
	minutesCall, minutesPart int
}

// store is the knowledge base's store as the writers see it: the port, and the store's locks under their names.
type store struct {
	core     recordingwire.Core
	readOnly bool
	// recMu guards a recording's read-compare-write, recFilesMu its files and the commit recovery, metaMu every
	// entry's meta.json: the store's own locks (recordingwire.Writer.Locks), acquired in this order.
	recMu, recFilesMu, metaMu sync.Locker
}

func newStore(core recordingwire.Core) *store {
	l := core.Locks()
	return &store{core: core, readOnly: core.ReadOnly(), recMu: l.Recordings, recFilesMu: l.Files, metaMu: l.Meta}
}

// The types the writers name, under the names they had.
type (
	Model             = recordingwire.Model
	Tagging           = kbwire.Tagging
	RecordingSegment  = kbwire.RecordingSegment
	Recording         = recordingwire.Recording
	RecordingUpload   = recordingwire.RecordingUpload
	MeetingRef        = recordingwire.MeetingRef
	CoverageGap       = recordingwire.CoverageGap
	RecapTarget       = recordingwire.RecapTarget
	RecapSpan         = recordingwire.RecapSpan
	RecapTodo         = recordingwire.RecapTodo
	DayRecap          = recordingwire.DayRecap
	DayRecapEntry     = recordingwire.DayRecapEntry
	Minutes           = recordingwire.Minutes
	MinutesItem       = recordingwire.MinutesItem
	MinutesPoint      = recordingwire.MinutesPoint
	MinutesAction     = recordingwire.MinutesAction
	MinutesOverview   = recordingwire.MinutesOverview
	MinutesConclusion = recordingwire.MinutesConclusion
	MinutesGroup      = recordingwire.MinutesGroup
	MinutesTopic      = recordingwire.MinutesTopic
	MinutesBullet     = recordingwire.MinutesBullet
	photoBook         = recordingwire.PhotoBook
)

// calendarEntry is a calendar entry as the writers read it (recordingwire.CalendarItem, its kind and id as the text
// the stored meeting reference keeps); the writers call it Item.
type calendarEntry struct {
	Kind, ID, Title string
	At              time.Time
}

type Item = calendarEntry

// The constants and errors the writers name.
const (
	KindRecording = recordingwire.KindRecording
	KindMeeting   = recordingwire.KindMeeting
	KindEvent     = recordingwire.KindEvent

	RecordingDay   = recordingwire.RecordingDay
	MinutesPending = recordingwire.MinutesPending
	MinutesReady   = recordingwire.MinutesReady
	MinutesFailed  = recordingwire.MinutesFailed
	PartPhotos     = recordingwire.PartPhotos
	AITitleMinutes = recordingwire.AITitleMinutes

	GapPaused      = recordingwire.GapPaused
	GapInterrupted = recordingwire.GapInterrupted
	GapStopped     = recordingwire.GapStopped
	GapPending     = recordingwire.GapPending
	GapLost        = recordingwire.GapLost
	GapErased      = recordingwire.GapErased

	RecapDueEvening  = recordingwire.RecapDueEvening
	RecapDueComplete = recordingwire.RecapDueComplete
	RecapDueCutoff   = recordingwire.RecapDueCutoff
	RecapDueAsked    = recordingwire.RecapDueAsked
	RecapDueRepair   = recordingwire.RecapDueRepair
	FinalComplete    = recordingwire.FinalComplete
	FinalCutoff      = recordingwire.FinalCutoff
	RecapMeeting     = recordingwire.RecapMeeting
	RecapOutside     = recordingwire.RecapOutside

	recapFile      = recordingwire.RecapFile
	recapMarkdown  = recordingwire.RecapMarkdown
	recapNotesFile = recordingwire.RecapNotesFile

	// PurposeMeetingMinutes is the model purpose every call of a minutes job asks for (#7980): the whole transcript, or
	// each part's notes and their merge, with the summary card in the same answers.
	PurposeMeetingMinutes = recordingwire.PurposeMeetingMinutes
)

var (
	ErrNotFound           = kbwire.ErrNotFound
	ErrReadOnly           = kbwire.ErrReadOnly
	ErrTranscriptExpired  = recordingwire.ErrTranscriptExpired
	ErrTranscriptionFinal = recordingwire.ErrTranscriptionFinal
)

// The rules of a recording's stored state, which the knowledge base applies too (recordingwire/rules.go).
var (
	MinutesTaskKey     = recordingwire.MinutesTaskKey
	RecordingID        = recordingwire.RecordingID
	dayLocation        = recordingwire.DayLocation
	clockAt            = recordingwire.ClockAt
	coverageTo         = recordingwire.CoverageTo
	dayStart           = recordingwire.DayStart
	hourStart          = recordingwire.HourStart
	isMeeting          = recordingwire.GetsMinutes
	minutesPhotos      = recordingwire.MinutesPhotos
	minutesSpan        = recordingwire.MinutesSpan
	photoLine          = recordingwire.PhotoLine
	planMinutes        = recordingwire.PlanMinutes
	planRecap          = recordingwire.PlanRecap
	recapFailed        = recordingwire.RecapFailed
	recordingEnd       = recordingwire.RecordingEnd
	sortDaySegments    = recordingwire.SortDaySegments
	transcriptionDone  = recordingwire.TranscriptionDone
	transcriptionHolds = recordingwire.TranscriptionHolds
	taggingFieldLocked = kbwire.TaggingFieldLocked
	clip               = textfmt.Clip
)

// Small helpers the knowledge base has under the same names (store.go, edit.go, language.go, hub_segments_read.go).

func calendarTitle(r *Recording) string     { return r.CalendarTitle() }
func hubMode(m *recordingwire.Meeting) bool { return m != nil && m.Transcription.Hub() }
func storedLang(lang string) i18n.Lang      { return i18n.Lang(lang).Or(i18n.ZhHans) }
func seconds(sec float64) time.Duration     { return time.Duration(sec * float64(time.Second)) }
func oneLine(s string) string               { return clip(strings.Join(strings.Fields(s), " "), 200) }
func clipRunes(s string, n int) string      { return clip(strings.Join(strings.Fields(s), " "), n) }
func removeStrings(list, remove []string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(v string) bool { return slices.Contains(remove, v) })
}

func addUnique(list []string, v ...string) []string {
	for _, x := range v {
		if x != "" && !slices.Contains(list, x) {
			list = append(list, x)
		}
	}
	return list
}

// readJSON reads the JSON file p into out; false when there is no such file.
func readJSON(p string, out any) (bool, error) {
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, out)
}

// titleRunes and titleLine are the title writers' (internal/kb/recording_title.go, which moves here with PR 4 of
// docs/plans/plan-kb-recording-port.md and replaces this copy): a title a model gave, as one clean line.
const titleRunes = 60

func titleLine(s string) string {
	s = strings.Join(strings.Fields(stripMarks(s)), " ")
	s = strings.TrimSpace(strings.Trim(s, "\"'“”‘’「」『』《》"))
	return clipAtMost(s, titleRunes)
}

// The photo book's two questions (recordingwire.PhotoBook).

func dropUnlistedPhotos(m *Minutes, b photoBook)                { b.DropUnlisted(m) }
func citedChanged(b photoBook, before, after map[int]bool) bool { return b.CitedChanged(before, after) }

// The store's members the writers call, each the port's. The writers name ids, files, kinds and changes as text,
// as they did; here each becomes the port's type, and a text that is none of the port's values is a defect of this
// package (known panics on it: the names are literals of the writers' code).

func known[T interface{ Known() bool }](v T) T {
	if !v.Known() {
		panic(fmt.Sprintf("recording writers: %T %v is not one the storage port takes", v, v))
	}
	return v
}

func textFiles(files recordingwire.Files) map[string][]byte {
	if files == nil {
		return nil
	}
	out := make(map[string][]byte, len(files))
	for f, data := range files {
		out[string(f)] = data
	}
	return out
}

func (s *store) ReadOnly() bool       { return s.core.ReadOnly() }
func (s *store) Zone() *time.Location { return s.core.Zone() }
func (s *store) RecordingZone(r *Recording) *time.Location {
	return recordingwire.RecordingZone(r, s.core.Zone())
}
func (s *store) Recording(id string) (*Recording, bool) {
	return s.core.Recording(recordingwire.ID(id))
}
func (s *store) Recordings() []*Recording { return s.core.Recordings() }
func (s *store) Items(kind string) []*Item {
	items := s.core.CalendarItems(known(recordingwire.CalendarKind(kind)))
	out := make([]*Item, len(items))
	for i, it := range items {
		out[i] = &Item{Kind: string(it.Kind), ID: string(it.ID), Title: it.Title, At: it.At}
	}
	return out
}
func (s *store) ReadMinutes(id string) (*Minutes, error) {
	return s.core.ReadMinutes(recordingwire.ID(id))
}
func (s *store) readRecap(id string) (*DayRecap, error) {
	return s.core.ReadRecap(recordingwire.ID(id))
}
func (s *store) readPhotos(id string) (photoBook, error) {
	return s.core.PhotoBook(recordingwire.ID(id))
}

// recordingName is r's name in lists. The knowledge base's store names a day recording without the module's name
// for the day; the one caller here (matchRecordings) never has a day recording, so the engine's name is the same.
func (s *store) recordingName(r *Recording) string { return s.core.RecordingName(r) }
func (s *store) recoverRecordingLocked(id string) error {
	return s.core.RecoverRecording(recordingwire.ID(id))
}
func (s *store) bumpPhotosRev(id string, start time.Time) error {
	return s.core.BumpPhotosRev(recordingwire.ID(id), start)
}
func (s *store) writeFile(p string, data []byte) error { return s.core.WriteFile(p, data) }
func (s *store) photoWords(p recordingwire.PhotoText, clipped bool) string {
	return s.core.PhotoWords(p, clipped)
}
func (s *store) TranscriptOfRecord(hubMode bool, device, hub []RecordingSegment) []RecordingSegment {
	return s.core.TranscriptOfRecord(hubMode, device, hub)
}
func (s *store) recordingSnapshot(id string) (map[string][]byte, error) {
	files, err := s.core.RecordingSnapshot(recordingwire.ID(id))
	return textFiles(files), err
}
func (s *store) recordSnapshot(id string) (map[string][]byte, *recordingwire.Meeting, recordingwire.HubTranscript, error) {
	files, m, hub, err := s.core.RecordSnapshot(recordingwire.ID(id))
	return textFiles(files), m, hub, err
}

// path, entityPath, notify, putEntity and putSummary take the entry's kind as the knowledge base's do; the writers
// only ever name a recording.
func (s *store) path(_, id string, file string) string {
	return s.core.Path(recordingwire.ID(id), known(recordingwire.File(file)))
}
func (s *store) entityPath(_, id string) string {
	return s.core.Path(recordingwire.ID(id), recordingwire.FileMeta)
}
func (s *store) notify(_, id, what string) {
	s.core.Notify(recordingwire.ID(id), known(recordingwire.Change(what)))
}
func (s *store) putEntity(_, _ string, r *Recording, _ *Tagging, _ bool) error {
	return s.core.PutMeta(r)
}
func (s *store) putSummary(_, id, text string, _ bool) error {
	return s.core.PutSummary(recordingwire.ID(id), text)
}

// The engine's members the writers call.

func (e *engine) now() time.Time                    { return e.Store.core.Now() }
func (e *engine) logf(f string, a ...any)           { e.Store.core.Logf(f, a...) }
func (e *engine) recordingName(r *Recording) string { return e.Store.core.RecordingName(r) }
func (e *engine) PhotoTexts(id string) []recordingwire.PhotoText {
	return e.Store.core.PhotoTexts(recordingwire.ID(id))
}
func (e *engine) dropAudio(id string, why error) error {
	return e.Store.core.DropAudio(recordingwire.ID(id), why)
}
func (e *engine) meetingChanged(id string, parts []string) {
	typed := make([]recordingwire.MeetingPart, len(parts))
	for i, p := range parts {
		typed[i] = known(recordingwire.MeetingPart(p))
	}
	e.Store.core.MeetingChanged(recordingwire.ID(id), typed)
}
func (e *engine) outputLang(r *Recording) i18n.Lang { return i18n.Lang(e.Store.core.OutputLang(r)) }
func (e *engine) daySpan(r *Recording, now time.Time) (time.Time, time.Time) {
	return recordingwire.DaySpan(r, now, e.Store.core.Zone())
}
func (e *engine) recordedWithin(r *Recording, now, from, to time.Time) bool {
	return recordingwire.RecordedWithin(r, now, from, to, e.Store.core.Zone())
}
func (e *engine) index(_, id, name string, t Tagging, one string, updated time.Time) {
	e.Store.core.Index(recordingwire.IndexEntry{ID: recordingwire.ID(id), Name: name, Tagging: t, OneLine: one, Updated: updated})
}
func (e *engine) writingOwner(lang i18n.Lang) prompts.KBOwner {
	o := e.Store.core.WritingOwner(recordingwire.Language(lang))
	return prompts.KBOwner{Owner: o.Name, Language: o.Language}
}

// modelFor is the model for purpose id: the engine's model per purpose, else its model.
func (e *engine) modelFor(id string) Model {
	if e.ModelFor != nil {
		return e.ModelFor(id)
	}
	return e.Model
}
