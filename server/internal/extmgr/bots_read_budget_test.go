package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/rpc"
)

func readBudgetWorld(t *testing.T) *botWorld {
	t.Helper()
	w := newBotWorld(t)
	w.pair(t)
	w.readOn(t)
	w.send(t, askEvent("om_budget", "ou_me", "@_user_1 /read inspect alerts", atBot, nil))
	eventually(t, 5*time.Second, "read source handed over", func() bool { return len(w.handed()) == 1 })
	return w
}

func readBudgetCard(key string) map[string]any {
	return map[string]any{"reply_to": "om_budget", "card": feishubot.NewCard("", "", "answer").Markdown("answer").JSON(), "uuid": key}
}

func readBudgetUsed(w *botWorld) int {
	return w.settings().aiSource("om_budget", time.Now()).Replies
}

func TestReadReplyBudgetKeyedRetries(t *testing.T) {
	w := readBudgetWorld(t)
	for i := 0; i < botReadReplies+2; i++ {
		if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("piece-0")); err != nil {
			t.Fatalf("same card retry %d: %v", i, err)
		}
	}
	if got := readBudgetUsed(w); got != 1 {
		t.Fatalf("same card used %d budget slots, want 1", got)
	}
	for i := 1; i < botReadReplies; i++ {
		if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard(fmt.Sprintf("piece-%d", i))); err != nil {
			t.Fatalf("distinct card %d: %v", i, err)
		}
	}
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("piece-0")); err != nil {
		t.Fatalf("same card at cap: %v", err)
	}
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("extra")); err == nil {
		t.Fatal("distinct card exceeded cap")
	}
	if got := readBudgetUsed(w); got != botReadReplies {
		t.Fatalf("used %d, want %d", got, botReadReplies)
	}
}

func TestReadReplyBudgetUntrustedKeysCountAttempts(t *testing.T) {
	for _, key := range []string{"", "invalid key", strings.Repeat("x", feishuMaxUUID-3)} {
		t.Run(fmt.Sprintf("length-%d", len(key)), func(t *testing.T) {
			w := readBudgetWorld(t)
			for i := 0; i < botReadReplies; i++ {
				if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard(key)); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard(key)); err == nil {
				t.Fatal("untrusted key bypassed attempt cap")
			}
		})
	}
}

type readBudgetTransport func(*http.Request) (*http.Response, error)

func (f readBudgetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The fake accepts an ambiguous attempt but loses its response. It deduplicates by the actual wire UUID;
// the manager must reuse its reservation, including after reconstruction from persisted settings.
func TestReadReplyBudgetFailedAttemptAndReconstruction(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprintf("ambiguous-%v", ambiguous), func(t *testing.T) {
			w := readBudgetWorld(t)
			var mu sync.Mutex
			tries := 0
			delivered := map[string]bool{}
			client := feishubot.New("cli_test", "s3cret", w.api.srv.URL).WithHTTP(&http.Client{Transport: readBudgetTransport(func(r *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(r.URL.Path, "/reply") {
					return http.DefaultTransport.RoundTrip(r)
				}
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					return nil, err
				}
				mu.Lock()
				defer mu.Unlock()
				tries++
				body := `{"code":0,"data":{"message_id":"om_receipt"}}`
				if ambiguous && tries <= 3 {
					delivered[in["uuid"].(string)] = true
					return nil, context.DeadlineExceeded // all three HTTP attempts land, but their receipts are lost
				}
				if tries == 1 {
					body = `{"code":230002,"msg":"bot not in chat"}`
				} else {
					delivered[in["uuid"].(string)] = true
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})})
			w.h.bots.mu.Lock()
			w.h.bots.api = client
			w.h.bots.mu.Unlock()
			if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("retry-card")); err == nil {
				t.Fatal("fake first failure was not returned")
			} else if ambiguous && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost receipt must remain retryable: %v", err)
			}
			if got := readBudgetUsed(w); got != 1 {
				t.Fatalf("failure used %d slots", got)
			}
			var saved FeishuBotSettings
			if ok, err := w.h.store.Setting(context.Background(), botSettingsKey, &saved); err != nil || !ok {
				t.Fatalf("saved settings: %v %v", ok, err)
			}
			rebuilt := newTestManager(t)
			rebuilt.store = w.h.store
			rebuilt.bots.set, rebuilt.bots.api = saved, client
			w.h = rebuilt
			if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("retry-card")); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if got := readBudgetUsed(w); got != 1 {
				t.Fatalf("retry after reconstruction used %d slots, want 1", got)
			}
			mu.Lock()
			n := len(delivered)
			mu.Unlock()
			if n != 1 {
				t.Fatalf("%d distinct wire UUIDs delivered", n)
			}
			for i := 1; i < botReadReplies; i++ {
				if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard(fmt.Sprintf("remaining-%d", i))); err != nil {
					t.Fatalf("remaining card %d: %v", i, err)
				}
			}
			if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("retry-card")); err != nil {
				t.Fatalf("failed card retry at cap: %v", err)
			}
			if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("extra")); err == nil {
				t.Fatal("failed attempt enlarged cap")
			}

		})
	}
}

func TestReadReplyBudgetSourceAndRouteIsolation(t *testing.T) {
	w := readBudgetWorld(t)
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("route-card")); err != nil {
		t.Fatal(err)
	}
	p := readBudgetCard("route-card")
	p["in_thread"] = true
	if err := w.sendErr(assistantwire.FeishuPost, p); err != nil {
		t.Fatal(err)
	}
	if got := readBudgetUsed(w); got != 2 {
		t.Fatalf("two routes used %d slots", got)
	}
	p["chat_id"] = "oc_other"
	if err := w.sendErr(assistantwire.FeishuPost, p); err == nil {
		t.Fatal("retry authorized another chat")
	}
	if got := readBudgetUsed(w); got != 2 {
		t.Fatal("refused chat consumed a slot")
	}
	w.send(t, askEvent("om_other_source", "ou_me", "@_user_1 /read inspect another alert", atBot, nil))
	eventually(t, 5*time.Second, "second source", func() bool { return len(w.handed()) == 2 })
	p = readBudgetCard("route-card")
	p["reply_to"] = "om_other_source"
	if err := w.sendErr(assistantwire.FeishuPost, p); err != nil {
		t.Fatal(err)
	}
	if got := w.settings().aiSource("om_other_source", time.Now()).Replies; got != 1 {
		t.Fatalf("other source used %d slots", got)
	}
	p["reply_to"] = "om_unrecognized"
	if err := w.sendErr(assistantwire.FeishuPost, p); err == nil {
		t.Fatal("key authorized unknown source")
	}
	w.h.bots.mu.Lock()
	w.h.bots.set.aiSource("om_budget", time.Now()).At = time.Now().Add(-botAISourcesKeepFor - time.Hour)
	w.h.bots.mu.Unlock()
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("route-card")); err == nil {
		t.Fatal("key authorized expired source")
	}
}

func TestReadReplyBudgetConcurrentKeys(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprintf("same-key-%v", same), func(t *testing.T) {
			w := readBudgetWorld(t)
			if same {
				for i := 0; i < botReadReplies-1; i++ {
					if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard(fmt.Sprintf("before-%d", i))); err != nil {
						t.Fatal(err)
					}
				}
			}
			const attempts = botReadReplies * 3
			start := make(chan struct{})
			results := make(chan error, attempts)
			for i := 0; i < attempts; i++ {
				key := "same-card"
				if !same {
					key = fmt.Sprintf("card-%d", i)
				}
				go func() { <-start; results <- w.sendErr(assistantwire.FeishuPost, readBudgetCard(key)) }()
			}
			close(start)
			success := 0
			for range attempts {
				if <-results == nil {
					success++
				}
			}
			wantSuccess, wantSlots := attempts, botReadReplies
			if !same {
				wantSuccess, wantSlots = botReadReplies, botReadReplies
			}
			if success != wantSuccess || readBudgetUsed(w) != wantSlots {
				t.Fatalf("success=%d slots=%d, want %d/%d", success, readBudgetUsed(w), wantSuccess, wantSlots)
			}
		})
	}
}

func TestReadReplyBudgetDeduplicationWindow(t *testing.T) {
	w := readBudgetWorld(t)
	now := time.Now().UTC()
	w.h.bots.mu.Lock()
	w.h.bots.now = func() time.Time { return now }
	w.h.bots.mu.Unlock()
	send := func() {
		t.Helper()
		if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("time-card")); err != nil {
			t.Fatal(err)
		}
	}
	send()
	w.h.bots.mu.Lock()
	now = now.Add(time.Hour - time.Second)
	w.h.bots.mu.Unlock()
	send()
	if got := readBudgetUsed(w); got != 1 {
		t.Fatalf("retry within hour used %d slots", got)
	}
	w.h.bots.mu.Lock()
	now = now.Add(time.Second)
	w.h.bots.mu.Unlock()
	send()
	if got := readBudgetUsed(w); got != 2 {
		t.Fatalf("retry after hour used %d slots, want 2", got)
	}
	// Settings snapshots must not share the mutable reservations with the live manager.
	copy := w.settings()
	copy.AISources[0].ReadCards[0].Key = "changed"
	if w.settings().AISources[0].ReadCards[0].Key == "changed" {
		t.Fatal("snapshot shares reservations")
	}
}

func TestReadReplyBudgetTextKeepsAttemptLimit(t *testing.T) {
	w := readBudgetWorld(t)
	for i := 0; i < botReadReplies; i++ {
		if err := w.sendErr(assistantwire.FeishuText, map[string]any{"reply_to": "om_budget", "text": "file omitted", "uuid": "same-text"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.sendErr(assistantwire.FeishuText, map[string]any{"reply_to": "om_budget", "text": "file omitted", "uuid": "same-text"}); err == nil {
		t.Fatal("keyed text bypassed existing attempt cap")
	}
}

func TestReadReplyBudgetLegacyAndExpiredReservations(t *testing.T) {
	w := readBudgetWorld(t)
	// Old saved settings have a count but no key history. Never guess which attempt an old count represented.
	w.h.bots.mu.Lock()
	src := w.h.bots.set.aiSource("om_budget", time.Now())
	src.Replies = botReadReplies - 1
	w.h.bots.mu.Unlock()
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("last-slot")); err != nil {
		t.Fatal(err)
	}
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("last-slot")); err != nil {
		t.Fatal(err)
	}
	w.h.bots.mu.Lock()
	later := time.Now().Add(time.Hour)
	w.h.bots.now = func() time.Time { return later }
	w.h.bots.mu.Unlock()
	before := w.api.total()
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("last-slot")); err == nil {
		t.Fatal("expired UUID reused a slot at cap")
	}
	if got := w.api.total(); got != before {
		t.Fatal("expired retry reached transport")
	}
}

// A failed write can have committed before its response was lost. In either case the new reservation must not
// authorize HTTP until a successful write (or reconstruction from the committed reservation) confirms it.
type readBudgetFailStore struct {
	Store
	mu        sync.Mutex
	failures  int
	committed bool
}

func (s *readBudgetFailStore) PutSetting(ctx context.Context, key string, value any) error {
	s.mu.Lock()
	fail := key == botSettingsKey && s.failures > 0
	if fail {
		s.failures--
	}
	committed := s.committed
	s.mu.Unlock()
	if !fail {
		return s.Store.PutSetting(ctx, key, value)
	}
	if committed {
		if err := s.Store.PutSetting(ctx, key, value); err != nil {
			return err
		}
		return context.DeadlineExceeded
	}
	return errors.New("synthetic persistence failure")
}

func TestReadReplyBudgetPersistenceFailure(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed-%v", committed), func(t *testing.T) {
			w := readBudgetWorld(t)
			st := &readBudgetFailStore{Store: w.h.store, failures: 2, committed: committed}
			w.h.store = st
			before := w.api.total()
			for range 2 {
				err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("persisted-card"))
				if err == nil {
					t.Fatal("failed reservation write allowed sending")
				}
				var re *rpc.Error
				if !errors.As(err, &re) || re.Code != assistantwire.CodeFeishuReplyBudgetUnavailable {
					t.Fatalf("failure did not return retryable budget code: %v", err)
				}
				if got := w.api.total(); got != before {
					t.Fatalf("failed write sent HTTP: %d -> %d", before, got)
				}
				if got := readBudgetUsed(w); got != 0 {
					t.Fatalf("failed write left %d in-memory slots", got)
				}
				if got := len(w.settings().aiSource("om_budget", time.Now()).ReadCards); got != 0 {
					t.Fatalf("failed write left %d in-memory keys", got)
				}
			}
			var saved FeishuBotSettings
			if ok, err := st.Setting(context.Background(), botSettingsKey, &saved); err != nil || !ok {
				t.Fatalf("read persisted source: %v %v", ok, err)
			}
			want := 0
			if committed {
				want = 1
			}
			if got := saved.aiSource("om_budget", time.Now()).Replies; got != want {
				t.Fatalf("persisted slots %d, want %d", got, want)
			}
			if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("persisted-card")); err != nil {
				t.Fatalf("recovery: %v", err)
			}
			if readBudgetUsed(w) != 1 {
				t.Fatal("recovery did not reserve exactly one slot")
			}
			if ok, err := st.Setting(context.Background(), botSettingsKey, &saved); err != nil || !ok {
				t.Fatalf("read successful reservation: %v %v", ok, err)
			}
			rebuilt := newTestManager(t)
			rebuilt.store = st
			rebuilt.bots.set, rebuilt.bots.api = saved, w.h.bots.api
			w.h = rebuilt
			if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("persisted-card")); err != nil {
				t.Fatalf("retry after reconstruction: %v", err)
			}
			if readBudgetUsed(w) != 1 {
				t.Fatal("reconstructed retry consumed another slot")
			}
		})
	}
}

func TestReadReplyBudgetUnkeyedPersistenceKeepsLegacySemantics(t *testing.T) {
	w := readBudgetWorld(t)
	w.h.store = &readBudgetFailStore{Store: w.h.store, failures: 1}
	before := w.api.total()
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("")); err != nil {
		t.Fatal(err)
	}
	if readBudgetUsed(w) != 1 || w.api.total() == before {
		t.Fatal("unkeyed attempt changed its historical accounting or sending behavior")
	}
}

func TestReadReplyBudgetRestartAfterAmbiguousSave(t *testing.T) {
	w := readBudgetWorld(t)
	st := &readBudgetFailStore{Store: w.h.store, failures: 1, committed: true}
	w.h.store = st
	before := w.api.total()
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("committed-card")); err == nil {
		t.Fatal("ambiguous save allowed sending")
	}
	if w.api.total() != before {
		t.Fatal("ambiguous save sent HTTP")
	}
	var saved FeishuBotSettings
	if ok, err := st.Setting(context.Background(), botSettingsKey, &saved); err != nil || !ok {
		t.Fatalf("persisted reservation: %v %v", ok, err)
	}
	rebuilt := newTestManager(t)
	rebuilt.store = st
	rebuilt.bots.set, rebuilt.bots.api = saved, w.h.bots.api
	w.h = rebuilt
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("committed-card")); err != nil {
		t.Fatalf("restart retry: %v", err)
	}
	if readBudgetUsed(w) != 1 {
		t.Fatal("committed reservation was charged twice")
	}
}

func TestReadReplyBudgetNoStoreRefusesNewKey(t *testing.T) {
	w := readBudgetWorld(t)
	w.h.store = nil
	before := w.api.total()
	if err := w.sendErr(assistantwire.FeishuPost, readBudgetCard("no-store")); err == nil {
		t.Fatal("new key allowed without persistent storage")
	}
	if w.api.total() != before || readBudgetUsed(w) != 0 {
		t.Fatal("missing store sent HTTP or retained an unsaved slot")
	}
}
