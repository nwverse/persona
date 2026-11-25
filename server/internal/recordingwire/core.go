package recordingwire

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
)

// Core is the storage port: what the recording module's writers ask of the knowledge base that holds the recordings
// (docs/plans/plan-kb-recording-port.md, sections 3.5 and 3.6). The knowledge base implements it (an adapter in
// internal/kb) and hands it to the module in its hook; the module's writers reach the store through nothing else. It
// has two groups: Reader is the knowledge base's own read path, the same functions every other reader calls, and
// Writer is the write primitives, the store's locks and the engine's settings the writers were using while they
// lived in internal/kb. It is wider than a port should be (the minutes and day-recap writers read and write the store
// in many places); it shrinks as the writers stop needing a primitive, and it never grows a second way to read.
type Core interface {
	Reader
	Writer
}

// Reader is the read group: what the writers read of the knowledge base. Every method is a reader the knowledge base
// has with or without the module; none of them writes.
type Reader interface {
	// ReadOnly: the knowledge base was opened read-only; nothing is written.
	ReadOnly() bool
	// Zone is the knowledge base's zone (the user's).
	Zone() *time.Location
	// Recording is recording id as committed; Recordings every recording.
	Recording(id ID) (*Recording, bool)
	Recordings() []*Recording
	// RecordingName is r's name in lists as it stands now (a day recording's from the day).
	RecordingName(r *Recording) string
	// RecordingSnapshot is recording id's committed files; RecordSnapshot the same with its meeting.json and the
	// hub's transcript; TranscriptOfRecord the transcript the minutes are made from.
	RecordingSnapshot(id ID) (Files, error)
	RecordSnapshot(id ID) (Files, *Meeting, HubTranscript, error)
	TranscriptOfRecord(hubMode bool, device, hub []kbwire.RecordingSegment) []kbwire.RecordingSegment
	// ReadMinutes is recording id's minutes.json; ReadRecap its recap.json.
	ReadMinutes(id ID) (*Minutes, error)
	ReadRecap(id ID) (*DayRecap, error)
	// PhotoTexts is the photos of recording id with their words; PhotoWords one photo's words as a line has them;
	// PhotoBook the photos as stored, for the two questions the minutes ask of them.
	PhotoTexts(id ID) []PhotoText
	PhotoWords(p PhotoText, clipped bool) string
	PhotoBook(id ID) (PhotoBook, error)
	// CalendarItems is the calendar entries of kind a recording is matched to.
	CalendarItems(kind CalendarKind) []CalendarItem
	// Path is where file f of recording id is on disk.
	Path(id ID, f File) string
}

// Writer is the write-and-lock group: the store's write primitives for a recording, its locks, and what the
// writers took from the engine (clock, log, models, the owner's language, the change feed).
type Writer interface {
	// Locks is the store's locks the writers take themselves. Transitional: the order is the store's and must not
	// change (Recordings, then Files, then Meta; the meeting, live and audio locks sit between Recordings and Files
	// and are not the writers' to take).
	Locks() Locks
	// RecoverRecording finishes or undoes an interrupted commit of recording id; the caller holds Locks().Files.
	RecoverRecording(id ID) error
	// WriteFile writes data to path (one Reader.Path gave) atomically, a temporary file renamed, making missing
	// directories.
	WriteFile(path string, data []byte) error
	// PutMeta writes r's meta.json as the knowledge base writes every entry's (its tagging rules, the commit
	// recovery, the change feed). PutSummary writes recording id's summary.md unless the user wrote it.
	PutMeta(r *Recording) error
	PutSummary(id ID, text string) error
	// BumpPhotosRev moves recording id's photos revision; the caller holds Locks().Recordings.
	BumpPhotosRev(id ID, start time.Time) error
	// Notify tells the change feed that part what of recording id changed; Index puts the recording into the entry
	// index; MeetingChanged tells the meeting's listeners which parts of id changed.
	Notify(id ID, what Change)
	Index(e IndexEntry)
	MeetingChanged(id ID, parts []MeetingPart)
	// DropAudio deletes recording id's hub-mode audio, why. It is the audio writers' and leaves this port when they
	// move (PR 4).
	DropAudio(id ID, why error) error
	// Now is the engine's clock; Logf its log (a format and its arguments, as fmt takes them).
	Now() time.Time
	Logf(format string, args ...any)
	// Model is the engine's model for purpose p, nil when it has none; HasModel whether it has any model at all.
	Model(p Purpose) Model
	HasModel() bool
	// OutputLang is the language r's minutes and recap are written in; WritingOwner how the owner is named to the
	// model and, when lang is not the default, the language to write in.
	OutputLang(r *Recording) Language
	WritingOwner(lang Language) Owner
	// DayRecapClock is the evening routine's time of day; ok false when the engine has none.
	DayRecapClock() (c Clock, ok bool)
}

// ID is a recording's id as stored: a lowercase UUID (ParseID). The stored JSON and the knowledge base's own code
// keep it as text; the port's methods take this type so that no other text is passed for one.
type ID string

// ParseID is raw as a recording's id; false when it is not one.
func ParseID(raw string) (ID, bool) {
	id, ok := RecordingID(raw)
	return ID(id), ok
}

// File is a file of a recording the writers read or write, by its name next to meta.json.
type File string

const (
	FileMeta            File = "meta.json"
	FileSegments        File = "segments.json"
	FileSummary         File = "summary.md"
	FileMinutes         File = "minutes.json"
	FileMinutesMarkdown File = "minutes.md"
	FileRecap           File = RecapFile
	FileRecapMarkdown   File = RecapMarkdown
	FileRecapNotes      File = RecapNotesFile
)

// Known: f is one of the files above.
func (f File) Known() bool {
	switch f {
	case FileMeta, FileSegments, FileSummary, FileMinutes, FileMinutesMarkdown, FileRecap, FileRecapMarkdown, FileRecapNotes:
		return true
	}
	return false
}

// Files is a recording's committed files by name (a snapshot may hold more than the writers' own, e.g. the transcript).
type Files map[File][]byte

// Change is the part of a recording the change feed is told of.
type Change string

const (
	ChangeSummary  Change = "summary"
	ChangeMetadata Change = "metadata"
)

// Known: c is one of the changes above.
func (c Change) Known() bool { return c == ChangeSummary || c == ChangeMetadata }

// MeetingPart is a part of meeting.json's view whose revision moved (the Part constants).
type MeetingPart string

// MeetingPartPhotos: the photos, and which of them the minutes cite.
const MeetingPartPhotos MeetingPart = PartPhotos

// Known: p is a part the writers may report.
func (p MeetingPart) Known() bool { return p == MeetingPartPhotos }

// CalendarKind is a kind of calendar entry a recording is matched to.
type CalendarKind string

const (
	CalendarEvents   CalendarKind = KindEvent   // the scheduled ones
	CalendarMeetings CalendarKind = KindMeeting // the video calls held, imported
)

// Known: k is one of the kinds above.
func (k CalendarKind) Known() bool { return k == CalendarEvents || k == CalendarMeetings }

// ItemID is a calendar entry's id in the knowledge base.
type ItemID string

// CalendarItem is a calendar entry of the knowledge base as a recording is matched to it.
type CalendarItem struct {
	Kind  CalendarKind
	ID    ItemID
	Title string
	At    time.Time
}

// IndexEntry is a recording as the entry index lists it.
type IndexEntry struct {
	ID      ID
	Name    string
	Tagging kbwire.Tagging
	OneLine string
	Updated time.Time
}

// Purpose is what a model call of the writers is for; the engine chooses the model by it.
type Purpose string

const (
	PurposeMinutes  Purpose = PurposeMeetingMinutes // a minutes job's calls
	PurposeCompress Purpose = "kb.compress"         // the day recap's (Information processing)
)

// Known: p is one of the purposes above.
func (p Purpose) Known() bool { return p == PurposeMinutes || p == PurposeCompress }

// Language is the language something is written in, as the knowledge base's translations name it ("zh-Hans", "en",
// "ja"); the knowledge base computes it (Writer.OutputLang) and the writers hand it back unchanged.
type Language string

// Owner is how the owner is named to the model, and the language to write in when it is not the default ("": the
// default).
type Owner struct {
	Name     string
	Language string
}

// Clock is a time of day.
type Clock struct{ Hour, Minute int }

// Locks is the store's locks as the writers take them (Writer.Locks).
type Locks struct {
	// Recordings guards a recording's read-compare-write; Files its files and the commit recovery; Meta every
	// entry's meta.json. Acquire in this order.
	Recordings, Files, Meta sync.Locker
}

// Model is the language model the writers call: a JSON answer of at most maxTokens tokens decoded into out (a
// pointer to the answer's type, as encoding/json takes one).
type Model interface {
	JSONMax(ctx context.Context, system, user string, maxTokens int, out any) error
}

// PhotoBook is a recording's photos as stored (Reader.PhotoBook).
type PhotoBook interface {
	// DropUnlisted takes out of m every item citing a photo that is no longer listed.
	DropUnlisted(m *Minutes)
	// CitedChanged: the photos the minutes cite (by number) differ between before and after.
	CitedChanged(before, after map[int]bool) bool
}
