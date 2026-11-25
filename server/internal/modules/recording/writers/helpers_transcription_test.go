package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"github.com/nwverse/persona/server/internal/kb"
	"testing"
	"time"
)

// hubEngine is an engine whose manager can transcribe on the hub (TranscriptionReady), collecting MeetingChanged.
func hubEngine(t *testing.T, m kb.Model) (*testEngine, *[][]string) {
	t.Helper()
	e := goldenEngine(t, m)
	// newTestEngine hooked the module's provider: hub transcription is the recording module's: retention reaches its audio and its sweep
	e.TranscriptionReady = func() bool { return true }
	var changed [][]string
	e.MeetingChanged = func(_ string, parts []string) { changed = append(changed, parts) }
	return e, &changed
}

// hubUpload is a meeting upload in hub mode with the phone's lines (10 seconds each) and timeline tl.
func hubUpload(rev int64, start time.Time, complete bool, tl *kb.Timeline, texts ...string) RecordingUpload {
	u := recUpload(rev, start, complete, texts...)
	u.Kind = kb.RecordingMeeting
	u.SetTranscription(kb.TranscriptionHub, tl)
	return u
}

// transcriptionNow is recording id's meeting.json transcription.
func transcriptionNow(t *testing.T, e *testEngine, id string) *kb.MeetingTranscription {
	t.Helper()
	m, _, err := e.MeetingRead(id)
	if err != nil {
		t.Fatal(err)
	}
	return m.Transcription
}
