package hub

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func (h *Hub) hasElicitationClient() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.has("elicitation") {
			return true
		}
	}
	return false
}

// requestHasElicitation binds REST opt-in to an authenticated connection's hello.
// Client headers alone never declare support, and legacy requests have no platform.
func (h *Hub) requestHasElicitation(r *http.Request) bool {
	who, rc := principalOf(r.Context()), requestClientOf(r.Context())
	if rc.Platform == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.mu.Lock()
		matches := c.device == who.ID && c.meta.Platform == rc.Platform && c.meta.Version == rc.Version
		c.mu.Unlock()
		if matches && c.has("elicitation") {
			return true
		}
	}
	return false
}

func visibleInteractions(ixs []proto.Interaction, elicitation bool) []proto.Interaction {
	out := make([]proto.Interaction, 0, len(ixs))
	for _, ix := range ixs {
		if ix.Kind != proto.IxElicitation || elicitation {
			out = append(out, ix)
		}
	}
	return out
}

// withMuted stamps each card with whether its agent or space is muted on this hub (#4360): an answer-only device
// (the watch) cannot read /api/v1/mutes, so it filters by this flag — a muted card is neither shown nor buzzes for.
// The phone keeps its own mutes and filters those itself. The flag is omitempty, so older clients decode the list
// exactly as before.
func (h *Hub) withMuted(ixs []proto.Interaction) []proto.Interaction {
	for i := range ixs {
		ixs[i].Muted = h.muted(ixs[i].MachineID, ixs[i].SessionID)
	}
	return ixs
}

// sendPendingInteractions queues the pending cards for c. The snapshot is taken and queued under h.mu, where
// broadcastTo queues: an interaction_resolved for a card in it is queued after it, never before (#3937).
func (h *Hub) sendPendingInteractions(c *client, elicitationOnly bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ix := range h.pendingInteractions() {
		if ix.Kind == proto.IxElicitation {
			if !c.has("elicitation") {
				continue
			}
		} else if elicitationOnly {
			continue
		}
		c.sendJSON(map[string]any{"type": "interaction", "interaction": ix})
	}
}

// interactionRoutes list the pending cards the request may see.
func (h *Hub) interactionRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/interactions", func(w http.ResponseWriter, r *http.Request) {
		ixs := visibleInteractions(h.pendingInteractions(), h.requestHasElicitation(r))
		if em := extensionManager(r); em != "" { // an extension's token: its manager's cards only
			ixs = slices.DeleteFunc(ixs, func(ix proto.Interaction) bool { return cmp.Or(ix.Manager, assistantwire.AssistantID) != em })
		}
		writeJSON(w, map[string]any{"interactions": h.withMuted(ixs)})
	})
}
