package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestInboxReplayToolForwardsBoundedReplay(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.in.mu.Lock()
	h.in.asked = nil
	h.in.mu.Unlock()
	args := `{"chat_id":"oc_example_chat","start":"2026-11-13T10:00:00Z","end":"2026-11-13T10:30:00Z","message_ids":["om_first","om_second"]}`
	if _, err := h.toolInboxReplay(ctx, json.RawMessage(args)); err != nil {
		t.Fatal(err)
	}
	h.in.mu.Lock()
	asked := append([]AppCall(nil), h.in.asked...)
	h.in.mu.Unlock()
	var calls []AppCall
	for _, call := range asked {
		if call.AppID == assistantwire.AppInbox && call.Method == assistantwire.InboxCollect {
			calls = append(calls, call)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("InboxCollect calls = %d, want 1; all calls: %+v", len(calls), asked)
	}
	var req assistantwire.InboxCollectRequest
	if err := json.Unmarshal(calls[0].Params, &req); err != nil {
		t.Fatal(err)
	}
	if req.Round != "" || req.LarkReplay == nil || req.LarkReplay.ChatID != "oc_example_chat" ||
		req.LarkReplay.Start != "2026-11-13T10:00:00Z" || req.LarkReplay.End != "2026-11-13T10:30:00Z" ||
		strings.Join(req.LarkReplay.MessageIDs, ",") != "om_first,om_second" {
		t.Fatalf("InboxCollect replay request = %+v", req)
	}
}

func TestInboxReplayToolReadOnlyFiltersExactRetainedItems(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	base := "larkcli:chat:oc_example_chat"
	now := time.Now()
	add := func(id, source, thread, last, body string) {
		h.inbox.items[id] = &InboxItem{ID: id, Source: source, Last: last, Context: body,
			UpdatedAt: now, Connector: &InboxConnector{Thread: thread}}
	}
	h.inbox.mu.Lock()
	add("root", "larkcli", base, "om_target", "root om_real")
	add("topic", "larkcli", base+":thread:omt_topic", "om_target", "topic om_real")
	add("handoff", "larkcli", base+":handoff:om_target", "om_target", "handoff om_real")
	add("prefix-collision", "larkcli", base+"_other", "om_target", "wrong chat")
	add("wrong-last", "larkcli", base, "om_other", "wrong message")
	add("wrong-source", "lark", base, "om_target", "wrong source")
	add("bad-thread-prefix", "larkcli", base+":threadish:omt_topic", "om_target", "wrong thread prefix")
	h.inbox.mu.Unlock()
	h.in.mu.Lock()
	h.in.asked = nil
	h.in.mu.Unlock()
	args := `{"chat_id":"oc_example_chat","start":"2026-11-13T10:00:00Z","end":"2026-11-13T10:30:00Z","message_ids":["om_target"],"read_only":true}`
	raw, err := h.toolInboxReplay(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Items []InboxItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 3 || got.Items[0].ID != "handoff" || got.Items[1].ID != "root" || got.Items[2].ID != "topic" {
		t.Fatalf("filtered, sorted items = %+v", got.Items)
	}
	for _, it := range got.Items {
		if it.Context == "" || !strings.Contains(it.Context, "om_real") {
			t.Errorf("item %s lost real message context: %q", it.ID, it.Context)
		}
	}
	h.in.mu.Lock()
	defer h.in.mu.Unlock()
	if len(h.in.asked) != 0 {
		t.Fatalf("read_only made app calls: %+v", h.in.asked)
	}
}

func TestInboxReplayToolBlocksOutwardReaders(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	h.in.mu.Lock()
	h.in.asked = nil
	h.in.mu.Unlock()
	args := json.RawMessage(`{"chat_id":"oc_example_chat","start":"2026-11-13T10:00:00Z","end":"2026-11-13T10:30:00Z","message_ids":["om_target"]}`)
	if _, err := h.toolInboxReplay(withOutwardReader(ctx), args); err == nil {
		t.Fatal("outward reader replay was allowed")
	}
	h.in.mu.Lock()
	defer h.in.mu.Unlock()
	for _, call := range h.in.asked {
		if call.Method == assistantwire.InboxCollect {
			t.Fatalf("outward denial called InboxCollect: %+v", call)
		}
	}
}

func TestInboxReplayToolValidatesBoundsAndIDs(t *testing.T) {
	h := newInboxHub(t)
	if err := h.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{
		`{"chat_id":"xoc_example_chat","start":"2026-11-13T10:00:00Z","end":"2026-11-13T10:30:00Z","message_ids":["om_target"]}`,
		`{"chat_id":"oc_example_chat","start":"2026-11-13T10:00:00Z","end":"2026-11-13T12:00:00Z","message_ids":["om_target"]}`,
		`{"chat_id":"oc_example_chat","start":"2026-11-13T10:00:00Z","end":"2026-11-13T10:30:00Z","message_ids":[]}`,
		`{"chat_id":"oc_example_chat","start":"2026-11-13T10:00:00Z","end":"2026-11-13T10:30:00Z","message_ids":["om_target","om_target"]}`,
		`{"chat_id":"oc_example_chat","start":"2026-11-13T10:00:00Z","end":"2026-11-13T10:30:00Z","message_ids":["not-an-id"]}`,
		`{"chat_id":"oc_example_chat","start":"2026-11-13T10:00:00Z","end":"2026-11-13T10:30:00Z","message_ids":["om_target"],"unexpected":true}`,
	} {
		if _, err := h.toolInboxReplay(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("accepted invalid args: %s", args)
		}
	}
}
