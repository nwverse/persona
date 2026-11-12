package assistant

import (
	"context"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/extwire"
)

// transcribers reaches the transcription contributions (docs/assistant/meeting-minutes.md "The `transcription`
// extension point"); internal/extmgr.Manager implements it. ref is meeting_transcription_extension: "" the built-in
// voice extension's, else "<extension id>/<contribution id>".
type transcribers interface {
	TranscriptionInfo(ctx context.Context, ref string) (extwire.TranscriptionInfo, error)
	// TranscriptionDial opens one job socket; done ends the extension's open request (host.Proc.BeginHTTP).
	TranscriptionDial(ctx context.Context, ref string) (conn *websocket.Conn, done func(), err error)
	IsTranscriptionContribution(ref string) bool
}

// The extensions in this process are the manager's transcribers.
var _ transcribers = (*extmgr.Manager)(nil)
