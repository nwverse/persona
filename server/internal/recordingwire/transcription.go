package recordingwire

import "errors"

// TranscriptionView is transcription on GET …/live and in detail.meta.
type TranscriptionView struct {
	Mode     string        `json:"mode"`
	State    string        `json:"state,omitempty"`
	Gaps     *[][2]float64 `json:"gaps,omitempty"` // hub: always present, [] when none
	Problem  string        `json:"problem,omitempty"`
	Fallback bool          `json:"fallback,omitempty"`
	// FinalDone and End: while the final pass runs (finishing, the pass started), how far it is and the recording's
	// end, both in recording seconds (#13461); absent otherwise. FinalDone is 0 as the pass starts.
	FinalDone *float64 `json:"final_done,omitempty"`
	End       *float64 `json:"end,omitempty"`
}

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
	// MaxAudioSeconds is the most audio the hub keeps for one recording: none from this recording second on.
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
	// ErrBadAudio: audio that is not whole 16-bit samples, or a position that is not 0 or more seconds (400, or the
	// socket's bad_frame).
	ErrBadAudio = errors.New("audio must be whole 16-bit samples")
)
