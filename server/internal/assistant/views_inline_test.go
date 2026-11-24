package assistant

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestInlineViewsUseOnlyTrustedAssistantItems(t *testing.T) {
	connection := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, connection, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	f := newFakeHub().withMachine()
	conversation := "inline-" + randomHex(8)
	f.setSessions("server", proto.Session{SessionID: conversation, Assistant: true}, proto.Session{SessionID: "ordinary", Assistant: false})
	s := newServiceOn(t, f, database)
	card := `{"fallback":"Choose","sections":[{"items":[{"type":"button","title":"Yes","action":{"id":"yes","reply":"Trusted inline reply"}}]}]}`
	text := "```persona-view\ninvalid\n```\n~~~persona-view\n" + card + "\n~~~\n"
	items := []proto.Item{
		{ID: "user#0", Role: "user", Kind: proto.KindText, Text: text},
		{ID: "tool#0", Role: "assistant", Kind: proto.KindToolResult, Text: text},
		{ID: "example#0", Role: "assistant", Kind: proto.KindText, Text: "````markdown\n```persona-view\n" + card + "\n```\n````"},
		{ID: "partial#0", Role: "assistant", Kind: proto.KindText, Text: "```persona-view\n" + card},
		{ID: "truncated#0", Role: "assistant", Kind: proto.KindText, Text: text, FullSize: 100000},
		{ID: "trusted#0", Role: "assistant", Kind: proto.KindText, Text: text},
	}
	s.SessionItems(ctx, SessionItems{MachineID: "server", SessionID: conversation, Items: items})
	keys, err := database.AssistantViewBlockKeys(ctx, conversation)
	if err != nil || len(keys) != 1 || keys[0] != "trusted#0/1" {
		t.Fatalf("keys %v %v", keys, err)
	}
	if strings.Count(f.eventTypes(), "assistant_view_block") != 1 {
		t.Fatalf("registration events %q", f.eventTypes())
	}
	s.SessionItems(ctx, SessionItems{MachineID: "server", SessionID: conversation, Items: items})
	if strings.Count(f.eventTypes(), "assistant_view_block") != 1 {
		t.Fatalf("replay broadcast another registration: %q", f.eventTypes())
	}
	s.SessionItems(ctx, SessionItems{MachineID: "server", SessionID: "ordinary", Items: items})
	keys, err = database.AssistantViewBlockKeys(ctx, "ordinary")
	if err != nil || len(keys) != 0 {
		t.Fatalf("ordinary keys %v %v", keys, err)
	}
	path := "/api/v1/assistant/conversations/" + conversation + "/views/" + url.PathEscape("trusted#0/1") + "/actions"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{"action":"yes","client_msg_id":"client"}`)))
	if w.Code != 200 {
		t.Fatalf("escaped key submit %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		a, err := database.AssistantViewAction(ctx, conversation, "trusted#0/1")
		if err != nil {
			t.Fatal(err)
		}
		if a != nil && a.Status != "pending" {
			if !strings.HasPrefix(a.Text, "Trusted inline reply") {
				t.Fatalf("message %q", a.Text)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inline action delivery did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Source errors are diagnostics, not another executable view; valid content retains its block index.
	feedback, err := database.PeekAssistantViewErrors(ctx, conversation, "server", 5)
	if err != nil || len(feedback) < 1 {
		t.Fatalf("feedback %v %v", feedback, err)
	}
	var action map[string]any
	if json.Unmarshal(w.Body.Bytes(), &action) != nil || action["action"] == nil {
		t.Fatal("no action result")
	}
}

type inlineFullHub struct {
	*fakeHub
	full proto.Item
}

func (h inlineFullHub) DeviceCall(ctx context.Context, p assistantwire.DeviceCall) (json.RawMessage, error) {
	if p.Method == "session.item" {
		return json.Marshal(h.full)
	}
	return h.fakeHub.DeviceCall(ctx, p)
}

func TestInlineTurnRegistersWithoutAnAppAndReadsFullText(t *testing.T) {
	connection := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, connection, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	f := newFakeHub().withMachine()
	conversation := "unwatched-" + randomHex(8)
	f.setSessions("server", proto.Session{SessionID: conversation, Assistant: true}, proto.Session{SessionID: "ordinary"})
	f.setItems(proto.Item{ID: "long#0", Role: "assistant", Kind: proto.KindText, Text: "A long conclusion before the card", FullSize: 100000})
	h := inlineFullHub{fakeHub: f, full: proto.Item{ID: "long#0", Role: "assistant", Kind: proto.KindText, Text: "Conclusion\n```persona-view\n{\"fallback\":\"Finished\",\"sections\":[]}\n```\n"}}
	s := withSettings(t, f, New(Options{Hub: h, Store: func() Store { return database }, Logf: testLogf(t)}))
	ref := SessionRef{MachineID: "server", SessionID: conversation}
	s.registerInlineTurn(ctx, SessionRef{MachineID: "server", SessionID: "ordinary"})
	keys, err := database.AssistantViewBlockKeys(ctx, "ordinary")
	if err != nil || len(keys) != 0 {
		t.Fatalf("ordinary turn keys %v %v", keys, err)
	}
	s.registerInlineTurn(ctx, ref)
	keys, err = database.AssistantViewBlockKeys(ctx, conversation)
	if err != nil || len(keys) != 1 || keys[0] != "long#0/0" {
		t.Fatalf("full reply keys %v %v", keys, err)
	}
}

type memInlineViewStore struct {
	*memStore
	vmu    sync.Mutex
	blocks map[string]map[string]db.AssistantViewBlockRow
}

var (
	_ Store           = (*memInlineViewStore)(nil)
	_ inlineViewStore = (*memInlineViewStore)(nil)
)

func newMemInlineViewStore() *memInlineViewStore {
	return &memInlineViewStore{
		memStore: &memStore{},
		blocks:   map[string]map[string]db.AssistantViewBlockRow{},
	}
}

func (m *memInlineViewStore) RegisterAssistantViewBlock(_ context.Context, row db.AssistantViewBlockRow) (bool, error) {
	m.vmu.Lock()
	defer m.vmu.Unlock()
	conv := m.blocks[row.Conversation]
	if conv == nil {
		conv = map[string]db.AssistantViewBlockRow{}
		m.blocks[row.Conversation] = conv
	}
	if _, exists := conv[row.Key]; exists {
		return false, nil
	}
	conv[row.Key] = row
	return true, nil
}

func (m *memInlineViewStore) AssistantViewBlockKeys(_ context.Context, conversation string) ([]string, error) {
	m.vmu.Lock()
	defer m.vmu.Unlock()
	conv := m.blocks[conversation]
	out := make([]string, 0, len(conv))
	for k := range conv {
		out = append(out, k)
	}
	slices.Sort(out)
	return out, nil
}

func (m *memInlineViewStore) RecordAssistantViewErrors(_ context.Context, _, _, _ string, _ []string, _ time.Time) error {
	return nil
}

func (m *memInlineViewStore) Feed(context.Context, string, time.Time, int) ([]db.FeedRow, error) {
	return nil, nil
}

const antigravityInlineCard = "```persona-view\n" +
	`{"fallback":"Choose","sections":[{"items":[{"type":"row","title":"Use option A","action":{"id":"choose-a","reply":"Use option A"}}]}]}` + "\n```"

func TestAntigravityInlineTurnRecoversLateExport(t *testing.T) {
	for _, priorTurns := range []bool{false, true} {
		name := "first turn"
		if priorTurns {
			name = "follow-up already visible"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				f := newFakeHub().withMachine()
				f.ownModels = true
				st := newMemInlineViewStore()
				s := newServiceOn(t, f, st)
				r := SessionRef{MachineID: "server", SessionID: "agy-fixture"}
				f.setSessions(r.MachineID, proto.Session{SessionID: r.SessionID, Assistant: true, Status: proto.StatusDone, Rev: 8})
				final := proto.Item{ID: "8@0123456789abcdef0123#1", Seq: 8, TS: time.Now(), Role: "assistant", Kind: proto.KindText, Text: antigravityInlineCard}
				temporary := final
				temporary.ID = "8@abcdef0123456789abcd#0"
				var exported bool
				f.page = func(turns int) []proto.Item {
					if priorTurns && turns == 1 {
						return nil
					}
					if exported {
						return []proto.Item{final}
					}
					return []proto.Item{temporary}
				}
				s.registerInlineTurn(ctx, r)
				s.registerInlineTurnSettled(ctx, r.MachineID, r.SessionID)
				elapse(5 * time.Second) // after the original settle read and first retry
				key := wire.AssistantBlockKey(final.ID, 0)
				if keys, _ := st.AssistantViewBlockKeys(ctx, r.SessionID); slices.Contains(keys, key) {
					t.Fatalf("exported key acknowledged before export: %v", keys)
				}
				exported = true // same session revision, no app watch or list event
				elapse(15 * time.Second)
				if keys, _ := st.AssistantViewBlockKeys(ctx, r.SessionID); !slices.Contains(keys, key) {
					t.Fatalf("missing final key after retry: %v", keys)
				}
				row := st.blocks[r.SessionID][key]
				if row.ItemID != final.ID || row.MachineID != r.MachineID {
					t.Fatalf("wrong registration provenance: %+v", row)
				}
				view, err := wire.ParseAssistantView(row.View)
				if err != nil {
					t.Fatal(err)
				}
				if text, _, err := viewReplyMessage(view, key, viewActionInput{Action: "choose-a"}); err != nil || !strings.HasPrefix(text, "Use option A") {
					t.Fatalf("declared reply: %q, %v", text, err)
				}
				if _, _, err := viewReplyMessage(view, key, viewActionInput{Action: "undeclared"}); err == nil {
					t.Fatal("accepted an undeclared action")
				}
				events := f.eventTypes()
				s.reconcileInlineTurn(ctx, r)
				if got := f.eventTypes(); got != events {
					t.Fatalf("replay broadcast again: %q -> %q", events, got)
				}
			})
		})
	}
}

func TestInlineViewPageRepairsHistoricalCardAndFeed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		f := newFakeHub().withMachine()
		f.ownModels = true
		st := newMemInlineViewStore()
		s := newServiceOn(t, f, st)
		bus := newNoticeBus(t, s).b
		f.setSessions("server",
			proto.Session{SessionID: "agy-history", Assistant: true, Status: proto.StatusIdle},
			proto.Session{SessionID: "agy-working", Assistant: true, Status: proto.StatusWorking},
			proto.Session{SessionID: "agy-ordinary", Status: proto.StatusIdle},
		)
		card := proto.Item{ID: "7@abcdef0123456789abcd#1", Seq: 7, Role: "assistant", Kind: proto.KindText, Text: antigravityInlineCard}
		for _, sid := range []string{"agy-history", "agy-working", "agy-ordinary"} {
			p := SessionItems{MachineID: "server", SessionID: sid, Items: []proto.Item{
				card, {ID: "user-text", Role: "user", Kind: proto.KindText, Text: antigravityInlineCard},
			}}
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := bus.event(ctx, busEvent{name: assistantwire.EventInlineViewPage, params: raw}); err != nil {
					t.Fatal(err)
				}
			}
			synctest.Wait()
			w := serve(s, "GET", "/api/v1/assistant/conversations/"+sid+"/feed", "")
			var feed struct {
				Keys []string `json:"view_blocks"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &feed) != nil {
				t.Fatalf("feed for %s: %d %s", sid, w.Code, w.Body.String())
			}
			var want []string
			if sid == "agy-history" {
				want = []string{wire.AssistantBlockKey(card.ID, 0)}
			}
			if !slices.Equal(feed.Keys, want) {
				t.Fatalf("feed for %s: keys %v, want %v", sid, feed.Keys, want)
			}
		}
		if got := strings.Count(f.eventTypes(), "assistant_view_block"); got != 1 {
			t.Fatalf("historical card ack count = %d, want 1", got)
		}
	})
}

func TestInlineTurnRegistersFinalIDAfterSettleAndIsIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFakeHub().withMachine()
		f.ownModels = true
		f.settings = &Settings{Timezone: "Asia/Shanghai"}
		st := newMemInlineViewStore()
		s := newServiceOn(t, f, st)
		t.Cleanup(func() {
			if q := s.fleet.q.ch; q != nil {
				close(q)
			}
		})
		const delay = time.Minute
		f.mu.Lock()
		f.hello.DoneDelayMS = delay.Milliseconds()
		f.mu.Unlock()

		ctx := context.Background()
		conversation := "agy-settle-test"
		f.setSessions("server", proto.Session{SessionID: conversation, Assistant: true, Status: proto.StatusDone})
		card := `{"fallback":"Choose","sections":[{"items":[{"type":"button","title":"Yes","action":{"id":"yes","reply":"Choose yes"}}]}]}`
		text := "Options:\n```persona-view\n" + card + "\n```\n"

		// At TurnEnded, agy has only the temporary unexported item id (before native transcript flush).
		f.setItems(proto.Item{ID: "1785#0", Seq: 1785, Role: "assistant", Kind: proto.KindText, Text: text})
		s.TurnEnded(ctx, SessionRef{MachineID: "server", SessionID: conversation})
		synctest.Wait()

		keys, err := st.AssistantViewBlockKeys(ctx, conversation)
		if err != nil || !slices.Equal(keys, []string{"1785#0/0"}) {
			t.Fatalf("after TurnEnded: keys=%v err=%v", keys, err)
		}
		if got := strings.Count(f.eventTypes(), "assistant_view_block"); got != 1 {
			t.Fatalf("after TurnEnded: assistant_view_block count=%d, want 1 (%q)", got, f.eventTypes())
		}

		// Native transcript flushes with the final persisted item id before settleDelay elapses.
		f.setItems(proto.Item{ID: "1790@abcd#1", Seq: 1790, Role: "assistant", Kind: proto.KindText, Text: text})
		elapse(delay - time.Millisecond)
		keys, err = st.AssistantViewBlockKeys(ctx, conversation)
		if err != nil || !slices.Equal(keys, []string{"1785#0/0"}) {
			t.Fatalf("before settleDelay: keys=%v err=%v", keys, err)
		}

		// Once settleDelay elapses, afterSettle registers the final persisted item id and broadcasts it once.
		elapse(time.Millisecond)
		keys, err = st.AssistantViewBlockKeys(ctx, conversation)
		if err != nil || !slices.Equal(keys, []string{"1785#0/0", "1790@abcd#1/0"}) {
			t.Fatalf("after settleDelay: keys=%v err=%v", keys, err)
		}
		if got := strings.Count(f.eventTypes(), "assistant_view_block"); got != 2 {
			t.Fatalf("after settleDelay: assistant_view_block count=%d, want 2 (%q)", got, f.eventTypes())
		}

		// Re-running registration for the same final item id is idempotent and broadcasts no duplicate event.
		s.registerInlineTurnSettled(ctx, "server", conversation)
		keys, err = st.AssistantViewBlockKeys(ctx, conversation)
		if err != nil || !slices.Equal(keys, []string{"1785#0/0", "1790@abcd#1/0"}) {
			t.Fatalf("after idempotent replay: keys=%v err=%v", keys, err)
		}
		if got := strings.Count(f.eventTypes(), "assistant_view_block"); got != 2 {
			t.Fatalf("after idempotent replay: assistant_view_block count=%d, want 2 (%q)", got, f.eventTypes())
		}
	})
}

func TestInlineTurnRegistersWhenStoppedAssistantRevAdvances(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFakeHub()
		f.ownModels = true
		f.settings = &Settings{Timezone: "Asia/Shanghai"}
		d := newFleetDevs()
		d.add("mac", "Mac")
		f.setSessions("mac") // transcript reads use the same machine the fleet lists
		st := newMemInlineViewStore()
		s := New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), Devices: d})
		ft := &fleetTest{t: t, s: s, d: d, f: f}
		t.Cleanup(func() {
			if q := s.fleet.q.ch; q != nil {
				close(q)
			}
		})
		const delay = time.Minute
		f.mu.Lock()
		f.hello.DoneDelayMS = delay.Milliseconds()
		f.hello.Notify = assistantwire.NotifyTimings{DoneDelayMS: delay.Milliseconds(), LongTurnMS: 60_000}
		f.mu.Unlock()

		ctx := context.Background()
		f.setSessions("mac") // the node's transcript reads go through the hub
		machine, ok := s.fleetMachine("mac")
		if !ok {
			t.Fatal("missing fleet machine mac")
		}
		conversation := "agy-rev-advance"
		card := `{"fallback":"Choose","sections":[{"items":[{"type":"button","title":"Yes","action":{"id":"yes","reply":"Choose yes"}}]}]}`
		text := "Options:\n```persona-view\n" + card + "\n```\n"

		ft.sessions("mac", proto.Session{
			SessionID: conversation,
			Agent:     "agy",
			Cwd:       machine.assistantCwd(),
			Status:    proto.StatusWorking,
			Rev:       1780,
			UpdatedAt: time.Now(),
		})
		synctest.Wait()

		// Turn ends before native transcript export; only the temporary item id is available.
		f.setItems(proto.Item{ID: "1785#0", Seq: 1785, Role: "assistant", Kind: proto.KindText, Text: text})
		ft.sessions("mac", proto.Session{
			SessionID: conversation,
			Agent:     "agy",
			Cwd:       machine.assistantCwd(),
			Status:    proto.StatusDone,
			Rev:       1785,
			LastText:  "Options",
			UpdatedAt: time.Now(),
		})
		synctest.Wait()

		keys, err := st.AssistantViewBlockKeys(ctx, conversation)
		if err != nil || !slices.Equal(keys, []string{"1785#0/0"}) {
			t.Fatalf("after TurnEnded: keys=%v err=%v", keys, err)
		}
		if got := strings.Count(f.eventTypes(), "assistant_view_block"); got != 1 {
			t.Fatalf("after TurnEnded: assistant_view_block count=%d, want 1 (%q)", got, f.eventTypes())
		}

		// Native transcript flushes while still within the settle window, advancing rev to 1790.
		f.setItems(proto.Item{ID: "1790@abcd#1", Seq: 1790, Role: "assistant", Kind: proto.KindText, Text: text})
		ft.sessions("mac", proto.Session{
			SessionID: conversation,
			Agent:     "agy",
			Cwd:       machine.assistantCwd(),
			Status:    proto.StatusDone,
			Rev:       1790,
			LastText:  "Options",
			UpdatedAt: time.Now(),
		})
		synctest.Wait()

		keys, err = st.AssistantViewBlockKeys(ctx, conversation)
		if err != nil || !slices.Equal(keys, []string{"1785#0/0", "1790@abcd#1/0"}) {
			t.Fatalf("after rev advance: keys=%v err=%v", keys, err)
		}
		if got := strings.Count(f.eventTypes(), "assistant_view_block"); got != 2 {
			t.Fatalf("after rev advance: assistant_view_block count=%d, want 2 (%q)", got, f.eventTypes())
		}

		// Once afterSettle fires later, re-registration is idempotent and emits no duplicate event.
		elapse(delay)
		keys, err = st.AssistantViewBlockKeys(ctx, conversation)
		if err != nil || !slices.Equal(keys, []string{"1785#0/0", "1790@abcd#1/0"}) {
			t.Fatalf("after settleDelay: keys=%v err=%v", keys, err)
		}
		if got := strings.Count(f.eventTypes(), "assistant_view_block"); got != 2 {
			t.Fatalf("after settleDelay: assistant_view_block count=%d, want 2 (%q)", got, f.eventTypes())
		}
	})
}
