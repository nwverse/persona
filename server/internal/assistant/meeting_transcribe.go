package assistant

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
)

// Hub transcription (#3862 phase 5, docs/assistant/meeting-minutes.md "Phase 5: hub transcription"): the meeting's
// audio socket, its gap uploads, and the live sentences the hub makes of them. Stubs until the manager's part is in.

// errPhase5Stub is what a stub of hub transcription answers until it is implemented.
var errPhase5Stub = errors.New("phase 5 stub")

// liveInterim is the latest caption of a meeting the hub transcribes: interim on GET …/live, while it is at most 10
// seconds old (At, the manager's clock).
type liveInterim struct {
	Start float64   `json:"start"`
	Text  string    `json:"text"`
	At    time.Time `json:"-"`
}

// hubInterim is recording id's latest caption; nil without one.
func (s *Service) hubInterim(id string) *liveInterim {
	return nil
}

// hubFlush asks the transcription of recording id for its sentences up to recording second at; true once every
// sentence before at is in the live log, false when ctx ended first.
func (s *Service) hubFlush(ctx context.Context, id string, at float64) bool {
	return false
}

// ServeSocket serves the assistant's WebSocket routes (assistantwire.SocketRoutes), which the hub relays to the
// manager's extensions layer and that hands here (extmgr.Options.AssistantSocket).
func (s *Service) ServeSocket(w http.ResponseWriter, r *http.Request) {
	apierr.Write(w, http.StatusNotImplemented, errPhase5Stub)
}

// audioPut is PUT /api/v1/recordings/{id}/audio?at=: audio the hub lacks, uploaded after the meeting.
func (s *Service) audioPut(w http.ResponseWriter, r *http.Request) {
	apierr.Write(w, http.StatusNotImplemented, errPhase5Stub)
}
