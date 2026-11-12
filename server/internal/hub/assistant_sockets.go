package hub

import (
	"errors"
	"net/http"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// assistantSocketRoutesRelayed relays the assistant's WebSocket routes (assistantwire.SocketRoutes: a meeting's audio
// socket, docs/assistant/meeting-minutes.md "Audio from the phone") to its manager as the extensions' sockets are
// (relayToManager): over a relay stream, never as http.request, which cannot upgrade. Who asked goes in the hub's
// headers, the credential (Bearer or ?token=) stays here, revoking the device ends the socket (trackRelayed), and
// while the manager is away the upgrade is refused 503 assistant_offline. Full credentials only: the auth already
// refuses an answer-only device and an extension's token (no extwire.RouteScopes entry opens these), and so does this,
// before anything goes to the manager.
func (h *Hub) assistantSocketRoutesRelayed(api *http.ServeMux) {
	for _, pattern := range assistantwire.SocketRoutes {
		api.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if principalOf(r.Context()).Role != roleFull {
				writeErr(w, http.StatusForbidden, errors.New(http.StatusText(http.StatusForbidden)))
				return
			}
			h.relayToManager(w, r, false)
		})
	}
}
