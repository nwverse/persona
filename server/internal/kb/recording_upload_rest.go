package kb

// What the upload and the summary round, which are still in this package (docs/plans/plan-kb-recording-port.md, PR 5),
// use of the files whose writers moved to the recording module with PR 4 (recording_title.go, transcription.go). It
// goes with them.

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// What wrote a recording's AITitle (Recording.AITitleFrom) that the summary round names; the title writers have the
// rest (internal/modules/recording/writers).
const (
	AITitleSummary = "summary"
	AITitleMinutes = recordingwire.AITitleMinutes
)

// titleRunes is the most of a model's title kept (a cut ends on "…"): a list row shows one line of it.
const titleRunes = 60

// putNamed puts r's name in the answer, with where it came from; neither while it is named only by when it started.
func putNamed(p *RecordingPut, r *Recording) {
	if p.NameSource = r.NameSource(); p.NameSource != "" {
		p.Name = r.Name
	}
}

// titleLine is a title the model wrote, on one line, without marks or the quotes around it, at most titleRunes.
func titleLine(s string) string {
	s = strings.Join(strings.Fields(stripMarks(s)), " ")
	s = strings.TrimSpace(strings.Trim(s, "\"'“”‘’「」『』《》"))
	return clipAtMost(s, titleRunes)
}

// stripMarks and clipAtMost are the minutes overview's helpers titleLine uses; the overview moved to the recording
// module with the minutes' writers, and these copies go there with the title writers (plan-kb-recording-port.md, PR 4).
func stripMarks(s string) string { return strings.ReplaceAll(s, "**", "") }

func clipAtMost(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return clip(s, n-1)
}

// transcriptionTimeline is t's timeline (empty without one).
func transcriptionTimeline(t *MeetingTranscription) Timeline {
	if t == nil || t.Timeline == nil {
		return Timeline{}
	}
	return *t.Timeline
}

// ---- the upload ----

// putTranscriptionFields applies what upload u (cleaned) says of the transcription to m, recording id's meeting.json
// under meetMu: the mode of the first meeting upload that names one (hub only while TranscriptionReady says so, else
// device with TranscriptionUnavailable), and on every hub-mode upload the timeline's union, CompleteAt and End (from
// the complete upload's duration, or the end of the audio held when that is later, while the socket said no end). A
// note or day upload's are ignored.
func (e *Engine) putTranscriptionFields(id string, m *Meeting, u *RecordingUpload, now time.Time) {
	asked := transcriptionMode(u.Transcription)
	if u.Kind != RecordingMeeting {
		return
	}
	t := m.Transcription
	if t == nil && asked != "" {
		mode := asked
		if mode == TranscriptionHub && (e.TranscriptionReady == nil || !e.TranscriptionReady()) {
			mode, u.TranscriptionProblem = TranscriptionDevice, TranscriptionUnavailable
		}
		t = &MeetingTranscription{Mode: mode}
		m.Transcription = t
	}
	if !t.Hub() {
		return
	}
	if u.Timeline != nil {
		tl := transcriptionTimeline(t)
		tl.Merge(*u.Timeline)
		t.Timeline = &tl
	}
	if u.Complete {
		if t.CompleteAt.IsZero() {
			t.CompleteAt = now.UTC()
		}
		if t.End == 0 && finite(u.Duration) && u.Duration > 0 {
			// The longer of the upload's and the audio held (#9587): a phone killed mid-segment uploads less.
			t.End = max(u.Duration, e.RecordingProvider().AudioHeldEnd(recordingwire.ID(id)))
		}
	}
	recordingwire.SettleTranscription(t, now)
	if !t.AudioClosed() {
		t.Gaps = e.RecordingProvider().AudioGaps(recordingwire.ID(id), t.End)
	}
}

// transcriptionOf is the transcription mode in effect after an upload that asked for asked (the stored one, device
// when none) and why it is not the one asked for: problem when the upload had one (TranscriptionUnavailable when it
// decided device for want of the hub), else TranscriptionNotMeeting for hub asked of a note or a day with no mode, and
// TranscriptionUnavailable for hub asked of a recording that has another (the mode never changes mid-recording).
func transcriptionOf(m *Meeting, asked, kind, problem string) (mode, why string) {
	mode = TranscriptionDevice
	if m != nil && m.Transcription != nil && m.Transcription.Mode != "" {
		mode = m.Transcription.Mode
	}
	asked = transcriptionMode(asked)
	switch {
	case problem != "":
		return mode, problem
	case asked != TranscriptionHub || mode == TranscriptionHub:
		return mode, ""
	case kind != RecordingMeeting && (m == nil || m.Transcription == nil):
		return mode, TranscriptionNotMeeting
	}
	return mode, TranscriptionUnavailable
}

// transcriptionMode is s as a mode ("" when it names none).
func transcriptionMode(s string) string { return recordingwire.TranscriptionMode(s) }
