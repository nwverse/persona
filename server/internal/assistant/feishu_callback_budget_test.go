package assistant

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/feishubot"
)

type heldCardRestoreStore struct {
	*memStore
	armed   atomic.Bool
	entered chan struct{}
	hold    func()
	once    sync.Once
}

func (st *heldCardRestoreStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == botStateKey && st.armed.Load() {
		st.once.Do(func() { close(st.entered) })
		st.hold()
	}
	return st.memStore.PutSetting(ctx, key, value)
}

// The new nonfinal false branch can recheck history and persist tracking. Neither
// a slow lookup nor the store's independent timeout may spend the callback budget.
func TestNonfinalQuestionPressReturnsBeforeRestore(t *testing.T) {
	for _, storage := range []bool{false, true} {
		name := "slow history lookup"
		if storage {
			name = "slow tracking persistence"
		}
		t.Run(name, func(t *testing.T) {
			hold, let := heldAnswer(t)
			entered := make(chan struct{})
			st := &heldCardRestoreStore{memStore: &memStore{}, entered: entered, hold: hold}
			f, _ := feishuHub("ou_a", time.Unix(100, 0))
			f.settings = &Settings{MachineID: "server", Main: "m1"}
			s := newServiceOn(t, f, st)
			_, _ = s.feishuStatus(context.Background())
			s.fbot.cards = map[string]botCard{}
			ix := qCard("ix_slow_restore", false, "Beijing", "Shanghai")
			var gets, attempts atomic.Int64
			f.answerAppBlocking(AppInteractions, InteractionsGet, func(json.RawMessage) (any, error) {
				if gets.Add(1) == 1 {
					return map[string]any{"ok": true, "interaction": ix}, nil
				}
				if !storage {
					close(entered)
					hold()
				}
				return map[string]any{"ok": false, "status": "pending"}, nil
			})
			f.answerApp(AppInteractions, InteractionsResolve, func(json.RawMessage) (any, error) {
				attempts.Add(1)
				st.armed.Store(storage)
				return map[string]any{"ok": false, "status": "pending"}, nil
			})
			f.answerApp(AppFeishu, FeishuText, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
			seedCard(s, ix)
			ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
			defer cancel()
			response := make(chan *feishubot.CardResponse, 1)
			go func() { response <- s.botCardAction(ctx, ixPress(ix.ID, "Shanghai"), "oa_fake") }()
			select {
			case <-entered:
			case r := <-response:
				t.Fatalf("fixture never reached restoration: %+v", r)
			case <-ctx.Done():
				t.Fatal("fixture never reached restoration before the deadline")
			}
			var r *feishubot.CardResponse
			timely := false
			select {
			case r = <-response:
				timely = ctx.Err() == nil
			case <-ctx.Done():
			}
			let()
			if r == nil {
				select {
				case r = <-response:
				case <-time.After(2 * time.Second):
					t.Fatal("callback did not finish after restore was released")
				}
			}
			if !timely {
				t.Fatal("nonfinal restoration exhausted the callback deadline before returning feedback")
			}
			if r == nil || r.Toast == nil || r.Toast.Type != "info" || r.Card != nil {
				t.Fatalf("expected working toast while restore is pending: %+v", r)
			}
			waitUntil(t, func() bool { return len(f.appParams(AppFeishu, FeishuText)) == 1 })
			s.fbot.mu.Lock()
			_, tracked := s.fbot.cards[ix.ID]
			s.fbot.mu.Unlock()
			if !tracked || attempts.Load() != 1 || len(f.appParams(AppFeishu, FeishuUpdateCard)) != 0 {
				t.Fatalf("restoration lost state or redelivered: tracked=%v attempts=%d", tracked, attempts.Load())
			}
		})
	}
}
