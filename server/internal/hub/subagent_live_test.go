package hub

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

const subagentSID = "aaaaaaaa-0000-0000-0000-000000000002@a1b2c3d4e5f60718"

// #3139: a page open on a subagent's conversation ("<parent>@<agent>") is watched like a session: the node is told to
// follow it, its pushes reach the page, and the node is told to stop when the page goes. Also when the page asked by the
// id an absorbed hub.json entry used to have.
func TestSubagentSubscriptionIsWatchedAndHearsThePushes(t *testing.T) {
	for _, asked := range []string{"canonical", "alias"} {
		t.Run(asked, func(t *testing.T) {
			h := newTestHub(t)
			var mu sync.Mutex
			var calls []string
			m := boardNode(t, h, "canonical", func(_ context.Context, method string, raw json.RawMessage) (any, error) {
				var p struct {
					SessionID string `json:"session_id"`
				}
				_ = json.Unmarshal(raw, &p)
				mu.Lock()
				calls = append(calls, method+" "+p.SessionID)
				mu.Unlock()
				if method == "session.items" {
					return map[string]any{"items": []proto.Item{}, "history": "h1", "to_seq": 0}, nil
				}
				return map[string]any{"ok": true}, nil
			})
			h.devices.absorbed = map[string]string{"alias": "canonical"}
			c := &client{out: make(chan []byte, 16), subs: map[[2]string]bool{}}
			addTestClient(h, c)
			sub := func(kind string) {
				h.onClientMessage(context.Background(), c, []byte(`{"type":"`+kind+`","machine_id":"`+asked+`","session_id":"`+subagentSID+`"}`))
			}
			called := func(want string) bool {
				mu.Lock()
				defer mu.Unlock()
				for _, c := range calls {
					if c == want {
						return true
					}
				}
				return false
			}
			wait := func(what string, ok func() bool) {
				t.Helper()
				for deadline := time.Now().Add(2 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatalf("timed out waiting for %s; node calls %v", what, calls)
					}
				}
			}

			sub("subscribe")
			wait("session.watch for the subagent", func() bool { return called("session.watch " + subagentSID) })

			// What the node pushes for it (its transcript grew) reaches the page, as the node sent it.
			from, to := 4, 5
			params, _ := json.Marshal(map[string]any{"session_id": subagentSID, "history": "h1", "from_seq": from, "to_seq": to,
				"items": []proto.Item{{ID: "i5", Seq: 5, Role: "assistant", Kind: proto.KindText, Text: "grown"}}})
			m.onEvent("session.items", params)
			deadline := time.After(2 * time.Second)
			for heard := false; !heard; {
				select {
				case b := <-c.out:
					heard = strings.Contains(string(b), `"grown"`) && strings.Contains(string(b), subagentSID)
				case <-deadline:
					t.Fatalf("the page never heard the subagent's push; subscriptions %v", c.subs)
				}
			}

			sub("unsubscribe")
			wait("session.unwatch for the subagent", func() bool { return called("session.unwatch " + subagentSID) })
		})
	}
}
