package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

type filingHTTPAction struct {
	name, path, body string
	bulk, reopen     bool
}

func TestInboxBulkDoesNotAcknowledgePendingFiling(t *testing.T) {
	firsts := []filingHTTPAction{
		{"single done", "/api/v1/inbox/item/done", `{"done":true}`, false, false},
		{"single dismiss", "/api/v1/inbox/item/done", `{"done":true,"dismiss":true}`, false, false},
		{"bulk done", "/api/v1/inbox/bulk", `{"action":"done","ids":["item"]}`, true, false},
		{"bulk dismiss", "/api/v1/inbox/bulk", `{"action":"dismiss","ids":["item"]}`, true, false},
		{"single reopen", "/api/v1/inbox/item/done", `{"done":false}`, false, true},
		{"bulk reopen", "/api/v1/inbox/bulk", `{"action":"reopen","ids":["item"]}`, true, true},
	}
	for _, first := range firsts {
		seconds := []filingHTTPAction{
			{"bulk done", "/api/v1/inbox/bulk", `{"action":"done","ids":["item"]}`, true, false},
			{"bulk dismiss", "/api/v1/inbox/bulk", `{"action":"dismiss","ids":["item"]}`, true, false},
			{"legacy bulk done", "/api/v1/inbox/done", `{"done":true,"items":[{"id":"item"}]}`, true, false},
			{"filter done", "/api/v1/inbox/bulk", `{"action":"done","filter":{"source":"mail"},"as_of":%q}`, true, false},
			{"filter dismiss", "/api/v1/inbox/bulk", `{"action":"dismiss","filter":{"source":"mail"},"as_of":%q}`, true, false},
		}
		if first.reopen {
			seconds = []filingHTTPAction{
				{"bulk reopen", "/api/v1/inbox/bulk", `{"action":"reopen","ids":["item"]}`, true, true},
				{"legacy bulk reopen", "/api/v1/inbox/done", `{"done":false,"items":[{"id":"item"}]}`, true, true},
			}
		}
		for _, second := range seconds {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/firstFail=%v", first.name, second.name, fail), func(t *testing.T) {
					h, st := newBulkFailHub(t)
					it := filingItem(h, "item")
					filingConversation(h, it, "filing-session")
					it.Agent.Pending, it.Agent.told = nil, 0
					if first.reopen {
						h.inbox.file(it, time.Now().Add(-time.Minute), "user")
						h.inbox.closeHandling(it, "user", time.Now().Add(-time.Minute))
						it.Agent.Pending = nil
					}
					if failed := h.saveInbox(context.Background(), []*InboxItem{it}); len(failed) != 0 {
						t.Fatal("seeding the existing stored row failed")
					}
					before := stateOf(h, it)
					st.imu.Lock()
					storedBefore := slices.Clone(st.rows[it.ID])
					st.imu.Unlock()
					body := second.body
					filter := second.name == "filter done" || second.name == "filter dismiss"
					if filter {
						body = fmt.Sprintf(body, time.Now().UTC().Format(time.RFC3339Nano))
					}
					if fail {
						st.failOnce = map[string]bool{it.ID: true}
					}
					mux := h.routes()
					var once sync.Once
					var competing map[string]any
					probe := &filingProbeStore{inboxPutFailStore: st, before: func(string, []byte) {
						once.Do(func() {
							code, out := inboxCall(t, mux, "POST", second.path, body)
							if code != 200 {
								t.Errorf("competing bulk HTTP status = %d", code)
							}
							competing = out
						})
					}}
					h.store = func() Store { return probe }
					code, out := inboxCall(t, mux, "POST", first.path, first.body)
					wantCode := 200
					if fail && !first.bulk {
						wantCode = 500
					}
					if code != wantCode || first.bulk && (len(jsonIDs(out["failed"])) == 1) != fail {
						t.Fatalf("first request = HTTP %d %v, want HTTP %d with failure %v", code, out, wantCode, fail)
					}
					if !slices.Equal(jsonIDs(competing["failed"]), []string{it.ID}) || len(jsonIDs(competing["unchanged"])) != 0 ||
						len(competing["changed"].([]any)) != 0 || len(jsonIDs(competing["ids"])) != 0 || competing["undo"] != nil {
						t.Fatalf("competing bulk acknowledged an uncommitted state: %v", competing)
					}
					if fail {
						st.imu.Lock()
						storedAfter := slices.Clone(st.rows[it.ID])
						st.imu.Unlock()
						if stateOf(h, it) != before || !bytes.Equal(storedBefore, storedAfter) {
							t.Fatal("the failed first request changed its existing row or nested state")
						}
					}
					h.deliverNotes(context.Background(), it)
					wantNotes := 0
					if !fail && !first.reopen {
						wantNotes = 1
					}
					if len(h.f.sentToAgents()) != wantNotes {
						t.Fatalf("pending bulk queued a stop note: %q", h.f.sentToAgents())
					}
					code, retry := inboxCall(t, mux, "POST", second.path, body)
					if code != 200 || len(jsonIDs(retry["failed"])) != 0 {
						t.Fatalf("retry after resolution failed: HTTP %d %v", code, retry)
					}
					if fail && len(retry["changed"].([]any)) != 1 || !fail && !filter && !slices.Equal(jsonIDs(retry["unchanged"]), []string{it.ID}) {
						t.Fatalf("retry did not distinguish committed from failed filing: %v", retry)
					}
					h.deliverNotes(context.Background(), it)
					wantNotes = 0
					if !second.reopen {
						wantNotes = 1
					}
					if it.open() != second.reopen || len(h.f.sentToAgents()) != wantNotes {
						t.Fatalf("retry final state/notes = %s / %q", stateOf(h, it), h.f.sentToAgents())
					}
					st.imu.Lock()
					var row InboxItem
					err := json.Unmarshal(st.rows[it.ID], &row)
					st.imu.Unlock()
					if err != nil || stateOf(h, &row) != stateOf(h, it) {
						t.Fatalf("retry left storage behind memory: %s / %s, %v", stateOf(h, &row), stateOf(h, it), err)
					}
				})
			}
		}
	}
}

func TestInboxBulkPendingFilingDoesNotBlockOtherSuccesses(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("firstFail=", fail), func(t *testing.T) {
			h, st := newBulkFailHub(t)
			pending, other := filingItem(h, "item"), filingItem(h, "other")
			if failed := h.saveInbox(context.Background(), []*InboxItem{pending, other}); len(failed) != 0 {
				t.Fatal("seeding stored rows failed")
			}
			if fail {
				st.failOnce = map[string]bool{pending.ID: true}
			}
			mux := h.routes()
			second := make(chan map[string]any, 1)
			var once sync.Once
			probe := &filingProbeStore{inboxPutFailStore: st, before: func(string, []byte) {
				once.Do(func() {
					go func() {
						code, out := inboxCall(t, mux, "POST", "/api/v1/inbox/bulk", `{"action":"dismiss","ids":["item","other"]}`)
						if code != 200 {
							t.Errorf("competing bulk status = %d", code)
						}
						second <- out
					}()
					waitForItem(t, h, other.ID, func(it *InboxItem) bool { return it.DoneBy == "dismissed" })
				})
			}}
			h.store = func() Store { return probe }
			inboxCall(t, mux, "POST", "/api/v1/inbox/item/done", `{"done":true}`)
			select {
			case out := <-second:
				if !slices.Equal(jsonIDs(out["failed"]), []string{pending.ID}) || !slices.Equal(jsonIDs(out["ids"]), []string{other.ID}) ||
					len(jsonIDs(out["unchanged"])) != 0 || !slices.Equal(jsonIDs(out["undo"].(map[string]any)["ids"]), []string{other.ID}) {
					t.Fatalf("mixed pending/committed bulk result: %v", out)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("competing bulk did not finish")
			}
			if other.open() || other.DoneBy != "dismissed" || pending.open() != fail {
				t.Fatalf("mixed bulk changed the wrong rows: pending=%s other=%s", stateOf(h, pending), stateOf(h, other))
			}
			if fail {
				for _, n := range pending.Agent.Pending {
					if n.Name == prompts.NoteClosed {
						t.Fatal("a failed pending row queued a stop note")
					}
				}
			}
		})
	}
}
