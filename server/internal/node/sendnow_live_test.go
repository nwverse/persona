//go:build herdrlive

package node

import (
	"context"
	"os"
	"testing"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
)

// Against a working claude pane with a message queued (it changes the session: the queue goes in now):
// PERSONA_LIVE_PANE=<pane> go test -tags herdrlive -run TestLiveSendQueuedNow ./internal/node/ -v — logs the send-now
// hint read off the screen, presses its keys, and passes once the hint is gone (#2467).
func TestLiveSendQueuedNow(t *testing.T) {
	pane := os.Getenv("PERSONA_LIVE_PANE")
	if pane == "" {
		t.Skip("PERSONA_LIVE_PANE not set")
	}
	n := &Node{hosts: herdrHosts(herdr.New()), agents: map[string]host.Agent{"live": {Agent: "claude", PaneID: pane}},
		paths: map[string]string{}, metas: map[string]quickMeta{}, bgScans: map[string]*bgScan{}}
	text, err := n.readPane(context.Background(), pane, "visible", 80)
	if err != nil {
		t.Fatal(err)
	}
	hint, keys := sendNowOffer(text)
	t.Logf("hint %q, keys %q", hint, keys)
	if err := n.sendQueuedNow(context.Background(), "live"); err != nil {
		t.Fatalf("send now: %v", err)
	}
	t.Log("the hint is gone: sent")
}
