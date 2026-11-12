package kb

import (
	"encoding/json"
	"errors"
	"time"
)

// Hub transcription (#3862 phase 5, docs/assistant/meeting-minutes.md "Phase 5: hub transcription"): the names the
// manager builds against. The bodies are stubs until the knowledge base's part is in; then these declarations move
// into their own files (transcription.go, echo.go, spans.go, hub_segments.go, recording_audio.go) and this file goes.

// errPhase5Stub is what a stub of hub transcription returns until it is implemented.
var errPhase5Stub = errors.New("phase 5 stub")

// Modes, states, parts, limits.
const (
	// The transcription modes (meeting.json's transcription.mode, the upload's transcription).
	TranscriptionDevice, TranscriptionHub = "device", "hub"
	// The states of a hub-mode recording's transcription.
	TranscriptionLive, TranscriptionFinishing, TranscriptionFinal, TranscriptionFailed = "live", "finishing", "final", "failed"
	// The upload's transcription_problem: the hub was asked for and is not ready, or the recording is not a meeting.
	TranscriptionUnavailable, TranscriptionNotMeeting = "unavailable", "not_meeting"
	// The meeting parts the apps read again (GET …/live?parts=, the meeting_live event): PartTranscription's revision
	// is kept in meeting.json, PartInterim's is the caption's time in milliseconds (the manager's, never stored).
	PartTranscription, PartInterim = "transcription", "interim"
	// The timeline's limits: role changes and speaking spans.
	MaxTimelineRoles, MaxTimelineSpans = 100, 1000
	// AudioRate is the stored audio's sample rate (PCM16 little-endian, mono).
	AudioRate = 16000
	// MaxAudioSeconds is the most audio the hub keeps for one recording.
	MaxAudioSeconds = 12 * 3600
	// The passes that wrote the hub's transcript (HubTranscript.Pass).
	HubPassLive, HubPassFinal = "live", "final"
	// A segment's source in the transcript of record (RecordingSegment.Source) and a live line's (LiveSegment.Source).
	SourceHub, SourceDevice = "hub", "device"
)

var (
	// ErrHubTranscribes: POST …/live on a recording the hub transcribes (its live log is the hub's).
	ErrHubTranscribes = errors.New("the hub transcribes this meeting")
	// ErrNotHub: the recording's mode is not hub (the audio socket, PUT …/audio).
	ErrNotHub = errors.New("this recording is not transcribed on the hub")
	// ErrTranscriptionFinal: the final pass has started; the hub takes no more audio.
	ErrTranscriptionFinal = errors.New("the final pass has started")
	// ErrAudioFull: the recording holds MaxAudioSeconds of audio.
	ErrAudioFull = errors.New("the hub holds the most audio a recording may have")
	// ErrBadAudio: audio that is not whole 16-bit samples (400, or the socket's bad_frame).
	ErrBadAudio = errors.New("audio must be whole 16-bit samples")
)

// TimelineRole is one role change of the timeline: the role from recording second At on.
type TimelineRole struct {
	At         float64 `json:"at"`
	Attendance string  `json:"attendance"`
}

// Timeline is a recording's role changes and the spans Persona spoke in, in recording seconds, as the phone keeps
// them ("The timeline"); meeting.json keeps the union of every one received.
type Timeline struct {
	Roles    []TimelineRole `json:"roles,omitempty"`
	Speaking [][2]float64   `json:"speaking,omitempty"`
}

// ParseTimeline reads a timeline as the upload and the socket's start carry it; ok false when it is malformed or over
// the limits (it is then ignored whole, never an error).
func ParseTimeline(raw json.RawMessage) (t Timeline, ok bool) {
	return Timeline{}, false
}

// Merge adds o to t (one role change per at, the latest received winning; overlapping or touching spans merged);
// changed says whether t moved.
func (t *Timeline) Merge(o Timeline) (changed bool) {
	return false
}

// RoleAt is the role at recording second at: the last change at or before it, else otherwise (the meeting's role).
func (t Timeline) RoleAt(at float64, otherwise string) string {
	return ""
}

// MeetingTranscription is meeting.json's "transcription" (never meta.json).
type MeetingTranscription struct {
	Mode         string       `json:"mode"`
	State        string       `json:"state,omitempty"`    // hub only
	Problem      string       `json:"problem,omitempty"`  // the audio socket's standing problem
	Fallback     bool         `json:"fallback,omitempty"` // the live log is filled from the phone
	Timeline     *Timeline    `json:"timeline,omitempty"`
	End          float64      `json:"end,omitempty"`        // recording seconds: the socket's end.at, else the complete upload's duration
	CompleteAt   time.Time    `json:"complete_at,omitzero"` // the first complete:true upload (the gap and minutes waits)
	FinalFrom    time.Time    `json:"final_from,omitzero"`  // the final pass started: transcription_final from here
	FinalAt      time.Time    `json:"final_at,omitzero"`
	FinalTries   int          `json:"final_tries,omitempty"`
	Gaps         [][2]float64 `json:"gaps,omitempty"`  // frozen once final
	Chars        int          `json:"chars,omitempty"` // the transcript of record's
	AudioGoneAt  time.Time    `json:"audio_gone_at,omitzero"`
	UsageSeconds float64      `json:"usage_seconds,omitempty"` // the upstream's billed seconds, diagnostics only
}

// TranscriptionView is transcription on GET …/live and in detail.meta.
type TranscriptionView struct {
	Mode     string        `json:"mode"`
	State    string        `json:"state,omitempty"`
	Gaps     *[][2]float64 `json:"gaps,omitempty"` // hub: always present, [] when none
	Problem  string        `json:"problem,omitempty"`
	Fallback bool          `json:"fallback,omitempty"`
}

// TranscriptionView is m's transcription as the apps read it, with gaps the spans the hub holds no audio for; nil
// while no mode was decided.
func (m Meeting) TranscriptionView(gaps [][2]float64) *TranscriptionView {
	return nil
}

// HubTranscribed: r's meeting.json has mode hub (an unreadable meeting.json: false).
func (r Recording) HubTranscribed() bool {
	return false
}

// SetTranscription gives an upload a transcription mode and a timeline (tests and the HTTP layer build uploads in Go).
func (u *RecordingUpload) SetTranscription(mode string, t *Timeline) {
	u.transcription, u.timeline = mode, t
}

// TranscriptionUpdate is MeetingUpdate on the transcription part: fn runs under the meeting's lock (on a
// MeetingTranscription that has its mode), and MeetingChanged is told after it.
func (e *Engine) TranscriptionUpdate(rawID string, fn func(t *MeetingTranscription, r Recording) error) (MeetingTranscription, error) {
	return MeetingTranscription{}, errPhase5Stub
}

// HubRecordings are the hub-mode recordings still needing the manager: state not final or failed, or audio not gone.
func (e *Engine) HubRecordings() ([]string, error) {
	return nil, errPhase5Stub
}

// ---- the echo rule, in one place (meeting-minutes.md phase 4 "Echo") ----

// EchoSentence is a sentence the echo rule judges, in recording seconds.
type EchoSentence struct {
	Start, End float64
	Text       string
}

// EchoSpan is a span Persona spoke in; Text is what it said, when known (unused today).
type EchoSpan struct {
	From, To float64
	Text     string
}

// EchoVerdict is what the echo rule makes of a sentence.
type EchoVerdict string

// The echo rule's verdicts.
const (
	EchoKeep EchoVerdict = "keep"
	EchoDrop EchoVerdict = "drop"
)

// EchoMargin widens every speaking span on each side, in seconds.
const EchoMargin = 0.5

// EchoSpans are the timeline's speaking spans as the echo rule reads them.
func EchoSpans(t Timeline) []EchoSpan {
	return nil
}

// EchoOf is the echo rule: today, drop when at least half of [Start, End] is inside the spans widened by EchoMargin
// on each side.
func EchoOf(s EchoSentence, spans []EchoSpan) EchoVerdict {
	return EchoKeep
}

// EchoHeld: a sentence that ends after an open span began (less EchoMargin) waits for the span to close.
func EchoHeld(s EchoSentence, openFrom float64) bool {
	return false
}

// ---- spans ----

// UnionSpans is the union of spans, sorted, overlapping and touching ones merged.
func UnionSpans(spans [][2]float64) [][2]float64 {
	return nil
}

// CoveredShare is the share of [start, end] inside the union of by.
func CoveredShare(start, end float64, by [][2]float64) float64 {
	return 0
}

// GapsIn are the spans of [from, to] that held does not cover, merged when less than 0.1 s apart.
func GapsIn(held [][2]float64, from, to float64) [][2]float64 {
	return nil
}

// ---- the hub's transcript (hub_segments.json beside meta.json: never searched, backed up, trimmed) ----

// HubTranscript is hub_segments.json: the hub's own sentences, with their own revision.
type HubTranscript struct {
	Rev      int64              `json:"rev"`
	Pass     string             `json:"pass"` // HubPassLive | HubPassFinal
	Segments []RecordingSegment `json:"segments"`
}

// HubTranscript reads recording rawID's hub_segments.json.
func (e *Engine) HubTranscript(rawID string) (HubTranscript, error) {
	return HubTranscript{}, errPhase5Stub
}

// HubTranscriptPut replaces the hub's transcript (Source set to hub), renders transcript.md again from the transcript
// of record, sets Transcription.Chars and indexes; ErrNotFound when the recording was deleted or trimmed meanwhile.
func (e *Engine) HubTranscriptPut(rawID, pass string, segs []RecordingSegment) (rev int64, err error) {
	return 0, errPhase5Stub
}

// TranscriptOfRecord is a recording's text of record: in device mode the upload's segments; in hub mode the hub's
// sentences and each upload segment the hub's cover no more than half of (counting their union), sorted, with
// Source set (SourceHub, SourceDevice).
func TranscriptOfRecord(hubMode bool, device, hub []RecordingSegment) []RecordingSegment {
	return nil
}

// RecordOf is recording rawID's transcript of record (the final pass's reference frames).
func (e *Engine) RecordOf(rawID string) ([]RecordingSegment, error) {
	return nil, errPhase5Stub
}

// ---- the live log ----

// LiveAppendHub appends the hub's sentences at the end of the live log (no from and no role check: the caller applied
// the timeline), within its limits (ErrLiveFull) and with the deleted and trimmed refusals; first is the position of
// segs[0]. It tells LiveChanged and MeetingChanged (PartLines).
func (e *Engine) LiveAppendHub(rawID string, segs []LiveSegment) (first int, err error) {
	return 0, errPhase5Stub
}

// ---- audio (recordings/<id>/attachments/audio/: never journalled, searched or backed up) ----

// AudioWriter stores one recording's audio from its socket; one is open at a time per recording.
type AudioWriter struct{}

// AudioOpen opens recording rawID's audio for writing. Its refusals, in the socket's order: ErrRecordingDeleted,
// ErrNotFound, ErrReadOnly, ErrNotHub, ErrTranscriptExpired, ErrTranscriptionFinal.
func (e *Engine) AudioOpen(rawID string) (*AudioWriter, error) {
	return nil, errPhase5Stub
}

// Write stores the audio of pcm from recording second at, only what is not held yet; ErrBadAudio, ErrAudioFull.
func (w *AudioWriter) Write(at float64, pcm []byte) error {
	return errPhase5Stub
}

// Sync writes the index; ackAt is the end of the contiguous audio held around the last write.
func (w *AudioWriter) Sync() (ackAt float64, err error) {
	return 0, errPhase5Stub
}

// HeldTo is the end of the latest span held (the socket's ready.from).
func (w *AudioWriter) HeldTo() float64 {
	return 0
}

// Close syncs and closes the writer.
func (w *AudioWriter) Close() error {
	return errPhase5Stub
}

// AudioHeld are the spans of recording rawID's audio the hub holds.
func (e *Engine) AudioHeld(rawID string) ([][2]float64, error) {
	return nil, errPhase5Stub
}

// AudioPut stores a gap upload's audio from recording second at (only what is not held) and returns the gaps left;
// its refusals are AudioOpen's.
func (e *Engine) AudioPut(rawID string, at float64, pcm []byte) ([][2]float64, error) {
	return nil, errPhase5Stub
}

// AudioRead reads the held audio of [from, from+len(p)/2/AudioRate) into p for the final pass; n < len(p) at a hole.
func (e *Engine) AudioRead(rawID string, from float64, p []byte) (int, error) {
	return 0, errPhase5Stub
}

// AudioDelete deletes recording rawID's audio and sets AudioGoneAt; deleting it again is no error.
func (e *Engine) AudioDelete(rawID string) error {
	return errPhase5Stub
}

// FinalTranscriptStored is told once the final pass stored its text: pending minutes stop waiting, and written ones
// are made again quietly. The caller wakes the minutes lane.
func (e *Engine) FinalTranscriptStored(rawID string) error {
	return errPhase5Stub
}
