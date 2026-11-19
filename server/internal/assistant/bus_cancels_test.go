package assistant

import (
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
)

// A switch to Persona Link that stops waiting for the hub's begin tells the hub, so a begin still waiting for the
// machine gives up instead of switching a computer nobody installs (hub/link_switch.go, #10662).
func TestBusCancelsWhatItStopsWaitingFor(t *testing.T) {
	b := NewBusHub(BusOptions{Hub: "http://127.0.0.1:1", Token: "jb_x", Devices: &fakeDevices{}})
	for _, method := range []string{assistantwire.MethodApp, devicewire.MethodLinkSwitchBegin} {
		if !b.client.Cancels[method] {
			t.Errorf("%s: the hub is not told when the manager stops waiting", method)
		}
	}
}
