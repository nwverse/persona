package recordingwire

import (
	"time"
)

// RecordingBookmark is a mark the user set while recording: seconds from the start.
type RecordingBookmark struct {
	At    float64 `json:"at"`
	Label string  `json:"label,omitempty"`
}

// RecordingCalendar is the event of the phone's own calendar a recording belongs to: a hint for its name and the
// minutes' attendees and place (docs/assistant/meeting-minutes.md "Field contract").
type RecordingCalendar struct {
	Title     string   `json:"title"`
	Attendees []string `json:"attendees,omitempty"`
	Location  string   `json:"location,omitempty"`
}

// MeetingRef is the knowledge base's calendar item (meetings or events) a recording overlaps.
type MeetingRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RecordingState is a recording as the phone reconciles against it (GET /api/v1/recordings).
type RecordingState struct {
	ID           string    `json:"id"`
	Revision     int64     `json:"revision"`
	Complete     bool      `json:"complete"`
	StartedAt    time.Time `json:"started_at"`
	MinutesState string    `json:"minutes_state,omitempty"`
	// TrimmedAt: only its minutes are kept (Recording.TrimmedAt). Listed all the same, at the revision it had, so a phone
	// that still holds it sees the hub has it and sends nothing (an upload of it would be expired).
	TrimmedAt *time.Time `json:"trimmed_at,omitempty"`
	// An all-day recording's only (recording_day.go): Next and Base always, 0 when lost (an older manager rewrote its
	// meta.json; the phone sends a PUT), so an entry of kind day without base is an older manager's.
	Kind         string     `json:"kind,omitempty"`
	Day          string     `json:"day,omitempty"`
	Part         int        `json:"part,omitempty"`
	Next         *int       `json:"next,omitempty"`
	Base         *int64     `json:"base,omitempty"`
	RecapState   string     `json:"recap_state,omitempty"`
	RecapFinal   bool       `json:"recap_final,omitempty"`
	RecapFinalAt *time.Time `json:"recap_final_at,omitempty"`
	// Name is what the hub calls it and NameSource where that came from (#8873, NameSource); both absent while it is
	// called only by when it started.
	Name       string `json:"name,omitempty"`
	NameSource string `json:"name_source,omitempty"`
	// Space is the track a recording was saved into instead (#13501, RecordingUpload.Space): set by the manager on the
	// ones it lists beside these, never by RecordingStates.
	Space string `json:"space,omitempty"`
}

// Recording kinds: a meeting gets minutes (minutes.go); a note (or none) only the summary; an all-day recording
// (recording_day.go) a recap of its day and never minutes nor a summary.
const (
	RecordingNote    = "note"
	RecordingMeeting = "meeting"
	RecordingDay     = "day"
)

// Minutes states (meta.json's minutes_state; "" when the recording is not a meeting).
const (
	MinutesPending = "pending"
	MinutesReady   = "ready"
	MinutesFailed  = "failed"
)

// What PutRecording did.
const (
	RecordingStored  = "stored"
	RecordingStale   = "stale"
	RecordingExpired = "expired"
)

// Where a recording's name comes from (RecordingState.NameSource, #8873): an open set, "" when it is named only by
// when it started (or, an all-day recording, by its day).
const (
	NamedByRename   = "renamed"  // the owner's name given on the web (Renamed, #8877)
	NamedByTitle    = "title"    // the upload's title, not its calendar hint's
	NamedByCalendar = "calendar" // the phone's calendar hint, or the knowledge base's calendar item it overlaps
	NamedByModel    = "model"    // AITitle
)
