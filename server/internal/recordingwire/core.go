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
	Recording(id string) (*Recording, bool)
	Recordings() []*Recording
	// RecordingName is r's name in lists as it stands now (a day recording's from the day).
	RecordingName(r *Recording) string
	// RecordingSnapshot is recording id's committed files; RecordSnapshot the same with its meeting.json and the
	// hub's transcript; TranscriptOfRecord the transcript the minutes are made from.
	RecordingSnapshot(id string) (map[string][]byte, error)
	RecordSnapshot(id string) (map[string][]byte, *Meeting, HubTranscript, error)
	TranscriptOfRecord(hubMode bool, device, hub []kbwire.RecordingSegment) []kbwire.RecordingSegment
	// ReadMinutes is recording id's minutes.json; ReadRecap its recap.json.
	ReadMinutes(id string) (*Minutes, error)
	ReadRecap(id string) (*DayRecap, error)
	// PhotoTexts is the photos of recording id with their words; PhotoWords one photo's words as a line has them;
	// PhotoBook the photos as stored, for the two questions the minutes ask of them.
	PhotoTexts(id string) []PhotoText
	PhotoWords(p PhotoText, clipped bool) string
	PhotoBook(id string) (PhotoBook, error)
	// CalendarItems is the calendar entries of kind (KindEvent, KindMeeting) a recording is matched to.
	CalendarItems(kind string) []*CalendarItem
	// Path is the path of a file of recording id (no parts: its directory).
	Path(id string, parts ...string) string
}

// Writer is the write-and-lock group: the store's write primitives for a recording, its locks, and what the
// writers took from the engine (clock, log, models, the owner's language, the change feed).
type Writer interface {
	// Locks is the store's locks the writers take themselves. Transitional: the order is the store's and must not
	// change (Recordings, then Files, then Meta; the meeting, live and audio locks sit between Recordings and Files
	// and are not the writers' to take).
	Locks() Locks
	// RecoverRecording finishes or undoes an interrupted commit of recording id; the caller holds Locks().Files.
	RecoverRecording(id string) error
	// WriteFile writes data to path atomically (a temporary file renamed), making missing directories.
	WriteFile(path string, data []byte) error
	// PutMeta writes r's meta.json as the knowledge base writes every entry's (its tagging rules, the commit
	// recovery, the change feed). PutSummary writes recording id's summary.md unless the user wrote it.
	PutMeta(r *Recording) error
	PutSummary(id, text string) error
	// BumpPhotosRev moves recording id's photos revision; the caller holds Locks().Recordings.
	BumpPhotosRev(id string, start time.Time) error
	// Notify tells the change feed that what of recording id changed; Index puts recording id into the entry index;
	// MeetingChanged tells the meeting's listeners which parts of id changed.
	Notify(id, what string)
	Index(id, name string, t kbwire.Tagging, oneLine string, updated time.Time)
	MeetingChanged(id string, parts []string)
	// DropAudio deletes recording id's hub-mode audio, why. It is the audio writers' and leaves this port when they
	// move (PR 4).
	DropAudio(id string, why error) error
	// Now is the engine's clock; Logf its log.
	Now() time.Time
	Logf(format string, args ...any)
	// Models is the engine's model and its model per purpose (either may be nil).
	Models() (Model, func(purpose string) Model)
	// OutputLang is the language r's minutes and recap are written in; WritingOwner how the owner is named to the
	// model and, when it is not the default, the language to write in.
	OutputLang(r *Recording) string
	WritingOwner(lang string) (owner, language string)
	// DayRecapClock is the evening routine's time of day, nil when the engine has none.
	DayRecapClock() func() (hour, minute int)
}

// Locks is the store's locks as the writers take them (Writer.Locks).
type Locks struct {
	// Recordings guards a recording's read-compare-write; Files its files and the commit recovery; Meta every
	// entry's meta.json. Acquire in this order.
	Recordings, Files, Meta sync.Locker
}

// Model is the language model the writers call: a JSON answer of at most maxTokens tokens into out.
type Model interface {
	JSONMax(ctx context.Context, system, user string, maxTokens int, out any) error
}

// PhotoBook is a recording's photos as stored (Reader.PhotoBook).
type PhotoBook interface {
	// DropUnlisted takes out of m every item citing a photo that is no longer listed.
	DropUnlisted(m *Minutes)
	// CitedChanged: the photos the minutes cite differ between before and after.
	CitedChanged(before, after map[int]bool) bool
}

// CalendarItem is a calendar entry of the knowledge base as a recording is matched to it.
type CalendarItem struct {
	Kind, ID, Title string
	At              time.Time
}
