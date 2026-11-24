package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// gatedNode holds the first call of method that says on == hold until release is closed, so a test can make a tell
// that read the old setting slower than one that read the newer one.
type gatedNode struct {
	nodeCaller
	method          string
	hold            bool
	once, newerOnce sync.Once
	holding         atomic.Bool   // the held call is at the node
	arrived         chan struct{} // closed when the held call reaches the node
	newer           chan struct{} // closed when a call that says !hold reaches it while the held one waits
	release         chan struct{}
	done            chan struct{} // closed when the held call has been answered
}

func (g *gatedNode) Call(ctx context.Context, method string, params, out any) error {
	name, raw := method, params
	if c, ok := params.(proto.PluginNodeCall); ok { // a node with built-in programs: cua's methods without "computer."
		name, raw = "computer."+c.Method, c.Params
	}
	b, _ := json.Marshal(raw)
	var p struct {
		On bool `json:"on"`
	}
	_ = json.Unmarshal(b, &p)
	// Only while the held call waits: a tell started by the setup (SetEnabled's computerChanged) can read !hold
	// before the config says hold and reach the node just before the held call, in order, which is no overtaking.
	if name == g.method && p.On != g.hold && g.holding.Load() {
		g.newerOnce.Do(func() { close(g.newer) })
	}
	if name == g.method && p.On == g.hold {
		held := false
		g.once.Do(func() { held = true })
		if held {
			g.holding.Store(true)
			close(g.arrived)
			<-g.release
			defer close(g.done)
		}
	}
	return g.nodeCaller.Call(ctx, method, params, out)
}

var computerTellOrderSequence atomic.Uint64

// The plugin's changes tell each Mac its no-auto-lock and cursor settings from goroutines that run at once: the node
// must end up with the newest setting even when a tell that read the older one is slower to reach it.
func TestComputerTellsReachTheMacInOrder(t *testing.T) {
	for _, c := range []struct {
		name, method string
		told         map[string][]bool
		tell         func(*Manager, context.Context, *Machine)
		on, off      string
	}{
		{"nolock", "computer.nolock", fakeNoLock, (*Manager).tellNoLock,
			`{"machines": [%[1]q], "no_auto_lock": [%[1]q]}`, `{"machines": [%[1]q]}`},
		{"cursor", "computer.cursor", fakeCursor, (*Manager).tellCursor,
			`{"machines": [%[1]q]}`, `{"machines": [%[1]q], "show_cursor": false}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, _ := extensionTestHub(t)
			ctx := context.Background()
			id := fmt.Sprintf("mac-order-%s-%d", c.name, computerTellOrderSequence.Add(1))
			mac, _ := computerMac(t, h, id)
			fakeNoLockMu.Lock()
			delete(c.told, id)
			fakeNoLockMu.Unlock()
			config := func(s string) {
				t.Helper()
				if err := h.extensions.reg.SetConfig(ctx, cuaExtensionID, json.RawMessage(s), "test"); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.extensions.reg.SetEnabled(ctx, cuaExtensionID, true, "test"); err != nil {
				t.Fatal(err)
			}
			config(fmt.Sprintf(c.on, id))
			told := func() []bool { // what the Mac was told
				fakeNoLockMu.Lock()
				defer fakeNoLockMu.Unlock()
				return append([]bool(nil), c.told[id]...)
			}

			g := &gatedNode{method: c.method, hold: true, arrived: make(chan struct{}), newer: make(chan struct{}),
				release: make(chan struct{}), done: make(chan struct{})}
			var released sync.Once
			release := func() { released.Do(func() { close(g.release) }) }
			t.Cleanup(release)
			mac.mu.Lock()
			g.nodeCaller, mac.peer = mac.peer, g
			mac.mu.Unlock()
			var wg sync.WaitGroup
			wg.Add(1)
			go func() { defer wg.Done(); c.tell(h, ctx, mac) }() // reads on, then is held at the node
			select {
			case <-g.arrived:
			case <-time.After(10 * time.Second):
				t.Fatal("the first tell never reached the node")
			}
			before := len(told())
			config(fmt.Sprintf(c.off, id)) // its own tells start too (computerChanged)
			wg.Add(1)
			go func() { defer wg.Done(); c.tell(h, ctx, mac) }() // reads off
			// Without the per-Mac order the newer tell gets through now; with it, it waits for the held one.
			select {
			case <-g.newer: // and once it is told, the held tell follows
				for deadline := time.Now().Add(10 * time.Second); !slices.Contains(told()[before:], false); time.Sleep(5 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatal("the newer tell never got to the Mac")
					}
				}
			case <-time.After(300 * time.Millisecond):
			}
			release()
			<-g.done // the held tell may be one of the plugin's own, not ours
			wg.Wait()
			// Since the held tell got to the node, it may say on once, before the off of every later tell
			// (computerChanged's own may still be on their way: those say off too), never after one.
			if seen := told()[before:]; len(seen) == 0 || seen[len(seen)-1] || slices.Index(seen, true) > slices.Index(seen, false) {
				t.Fatalf("the Mac was told on after the setting went off: %v", seen)
			}
		})
	}
}
