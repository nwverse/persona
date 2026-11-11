package devices

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
)

// A preview the hub opened on a node (preview.open, a headless browser tab on a stream) ends on the node when a new
// bus connection says hello, like a forwarded port: the node closes the tab only when its stream closes.
func TestANewBusConnectionEndsPreviewStreams(t *testing.T) {
	d := newTestDevices(t)
	n := &testNode{}
	connectedMachine(t, d, "box", n)
	if _, err := d.Call(context.Background(), devicewire.NodeCall{MachineID: "box", Method: "preview.open",
		Params: json.RawMessage(`{"id":"v1","port":5173}`)}); err != nil {
		t.Fatal(err)
	}
	d.Hello(json.RawMessage(`{}`))
	eventually(t, 2*time.Second, "the preview stream closed on the node", func() bool {
		return slices.Contains(n.eventsSeen(), `stream.close {"id":"v1"}`)
	})
}
