package extmgr

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// botMenuEvent is a press on the bot's menu (application.bot.menu_v6) by from.
func botMenuEvent(from, key string) []byte { return botMenuEventID(from, key, "ev_menu_"+from+"_"+key) }

// botMenuEventID is botMenuEvent with the event's id (a press Feishu delivers again keeps it).
func botMenuEventID(from, key, id string) []byte {
	b, _ := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]any{"event_id": id, "event_type": "application.bot.menu_v6"},
		"event": map[string]any{"operator": map[string]any{"operator_id": map[string]any{"open_id": from}}, "event_key": key,
			"timestamp": 1795138200}})
	return b
}

// The bot's menu: the owner's press goes to the assistant as its key (feishu.menu), anyone else's nowhere, and an
// empty key is nothing.
func TestBotMenuIsTheOwnersAlone(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	w.a.on(assistantwire.MethodFeishuMenu, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	got := func() []string {
		var keys []string
		for _, raw := range w.a.got(assistantwire.MethodFeishuMenu) {
			var m assistantwire.FeishuMenu
			_ = json.Unmarshal(raw, &m)
			keys = append(keys, m.Key)
		}
		return keys
	}
	w.send(t, botMenuEvent("ou_stranger", "home"))
	w.send(t, botMenuEvent("ou_me", ""))
	w.send(t, botMenuEvent("ou_me", "tasks"))
	eventually(t, 5*time.Second, "the owner's press handed", func() bool { return len(got()) > 0 })
	time.Sleep(100 * time.Millisecond)
	if keys := got(); strings.Join(keys, ",") != "tasks" {
		t.Fatalf("handed %q, want only the owner's tasks", keys)
	}
}

// A press Feishu delivers again (its acknowledgement lost) is the same event: it goes to the assistant once, with its id,
// which the assistant claims too (a handover cut off and made again).
func TestBotMenuRedeliveryRunsOnce(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	w.a.on(assistantwire.MethodFeishuMenu, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	w.send(t, botMenuEventID("ou_me", "today", "ev_1"))
	w.send(t, botMenuEventID("ou_me", "today", "ev_1"))
	w.send(t, botMenuEventID("ou_me", "today", "ev_2"))
	eventually(t, 5*time.Second, "the presses handed", func() bool { return len(w.a.got(assistantwire.MethodFeishuMenu)) >= 2 })
	time.Sleep(100 * time.Millisecond)
	var ids []string
	for _, raw := range w.a.got(assistantwire.MethodFeishuMenu) {
		var m assistantwire.FeishuMenu
		_ = json.Unmarshal(raw, &m)
		ids = append(ids, m.EventID)
	}
	if strings.Join(ids, ",") != "ev_1,ev_2" {
		t.Fatalf("handed %q, want each press once with its id", ids)
	}
}

// A press takes its place in the chat's line as it comes, as a message does: the owner's message right after it is
// handed over after it, so a /new pressed and a message sent at once never reach the assistant the other way round.
func TestBotMenuIsHandedInLineWithMessages(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	var mu sync.Mutex
	var order []string
	note := func(s string) {
		mu.Lock()
		order = append(order, s)
		mu.Unlock()
	}
	w.a.on(assistantwire.MethodFeishuMenu, func(json.RawMessage) (any, error) {
		time.Sleep(300 * time.Millisecond) // a slow handover: the message after it waits
		note("menu")
		return map[string]any{}, nil
	})
	w.a.on(assistantwire.MethodFeishuMessage, func(json.RawMessage) (any, error) {
		note("message")
		return map[string]any{}, nil
	})
	w.send(t, botMenuEventID("ou_me", "new", "ev_new"))
	w.send(t, botMessageEvent("om_after_menu", "ou_me", "text", textContent("帮我看看日志"), nil))
	eventually(t, 5*time.Second, "both handed", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 2
	})
	if strings.Join(order, ",") != "menu,message" {
		t.Fatalf("handed %v, want the press first", order)
	}
}
