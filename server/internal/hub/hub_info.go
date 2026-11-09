package hub

import (
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/lazyre"
	"os"
	"strings"
)

// containerHost is what Docker names a container's host by default: its id, which says nothing to a person.
var containerHost = lazyre.New(`^[0-9a-f]{12}([0-9a-f]{52})?$`)

// hubName is what people see this hub as (docs/archive/operations/plan-hub-mesh.md 2.6; the link sends it to the Mac menu bar,
// docs/operations/link.md): hub.json's name (as the admin UI last set it: configuredHubName, live); else defaultHubName.
func (h *Hub) hubName() string {
	if h.cfg == nil { // a bare hub in unit tests
		return "hub"
	}
	if n := oneLine(h.configuredHubName()); n != "" {
		return clip(n, 64)
	}
	return h.defaultHubName()
}

// defaultHubName is the short host name of its machine; in a container, whose host name is its id,
// hub-<the first 6 characters of hub_id>.
func (h *Hub) defaultHubName() string {
	hostname := os.Hostname
	if h.hostname != nil {
		hostname = h.hostname
	}
	host, _ := hostname()
	short := strings.TrimSpace(strings.Split(host, ".")[0])
	if short == "" || short == "localhost" || containerHost.MatchString(strings.ToLower(short)) || identity.Getenv("PERSONA_LOCAL_NODE") == "0" {
		id := h.hubID()
		return "hub-" + id[:min(6, len(id))]
	}
	return short
}

// meshHubCount is how many hubs of the mesh group this hub is in are online, itself included (1 without peers); the
// links hear it again when it changes (meshViewChanged, sendLinkEndpoints).
func (h *Hub) meshHubCount() int { return reachableHubs(h.meshView()) }
