package extmgr

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// botMenuEvent is a press on the bot's menu (application.bot.menu_v6) by from.
func botMenuEvent(from, key string) []byte {
	b, _ := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]any{"event_type": "application.bot.menu_v6"},
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
