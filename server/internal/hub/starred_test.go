package hub

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/extwire"
)

func TestStarredRouteIsolation(t *testing.T) {
	const path = "/api/v1/assistant/starred"
	h := newTestHub(t)
	api := h.routes()
	tokens := extTokensOf(t, h)
	token := tokens.issue("starred-check", "Starred check", []string{extwire.ScopeAgentsRead, extwire.ScopeAgentsControl})
	for _, method := range []string{"GET", "POST", "DELETE"} {
		url := path
		if method == "DELETE" {
			url += "/example"
		}
		if allowed(principal{Role: roleAnswer}, method, url) || allowed(principal{Role: roleLink}, method, url) {
			t.Fatalf("%s widened a restricted device's access", method)
		}
		if status, _ := call(t, api, method, url, token, "{}"); status != http.StatusForbidden {
			t.Fatalf("%s should refuse an extension token, got %d", method, status)
		}
		tokens.mu.Lock()
		called := len(tokens.served)
		tokens.mu.Unlock()
		if called != 0 {
			t.Fatalf("%s reached a manager despite being denied", method)
		}
	}
}

func TestStarredBroadcastKeepsManager(t *testing.T) {
	h := newTestHub(t)
	app := wsApp(t, h)
	err := h.relayBroadcastFrom("m-example", json.RawMessage(`{"type":"assistant_starred","manager":"forged"}`))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-app.out:
		var event map[string]json.RawMessage
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		if string(event["type"]) != `"assistant_starred"` || string(event["manager"]) != `"m-example"` {
			t.Fatalf("wrong event type or provenance: %s", raw)
		}
		if event["star"] != nil || event["text"] != nil {
			t.Fatal("snapshot left the manager on the event")
		}
	case <-time.After(time.Second):
		t.Fatal("the star's notification was not relayed")
	}
}
