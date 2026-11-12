package extmgr

import (
	"context"
	"errors"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/extwire"
)

// Meeting transcription (#3862 phase 5, docs/assistant/meeting-minutes.md "The `transcription` extension point"): the
// manager's access to the transcription contributions, for the assistant in this process. Stubs until the
// extensions' part is in.

// errPhase5Stub is what a stub of hub transcription returns until it is implemented.
var errPhase5Stub = errors.New("phase 5 stub")

// ErrNoTranscriber: the transcription contribution ref names is not installed, not enabled or not running.
var ErrNoTranscriber = errors.New("no transcription contribution is running")

// TranscriptionInfo is ref's GET /transcription/{cid}/info. ref is the assistant's meeting_transcription_extension:
// "" the built-in voice extension's contribution, else "<extension id>/<contribution id>".
func (h *Manager) TranscriptionInfo(ctx context.Context, ref string) (extwire.TranscriptionInfo, error) {
	return extwire.TranscriptionInfo{}, errPhase5Stub
}

// TranscriptionDial opens one job socket of ref's (GET /transcription/{cid}/ws); done ends the extension's open
// request, once the job is over.
func (h *Manager) TranscriptionDial(ctx context.Context, ref string) (conn *websocket.Conn, done func(), err error) {
	return nil, nil, errPhase5Stub
}

// IsTranscriptionContribution says whether ref names an installed extension's transcription contribution (whether
// or not it is enabled).
func (h *Manager) IsTranscriptionContribution(ref string) bool {
	return false
}
