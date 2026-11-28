package assistant

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

func TestInboxRefreshReplayRequest(t *testing.T) {
	h := newInboxHub(t)
	h.addConnector(&inboxConnector{things: []connector.InboxThing{thing("item-1", time.Now())}}, false)
	post := func(body string) (int, map[string]any) {
		return serveJSON(t, h.Service, "POST", "/api/v1/inbox/refresh?sources=all", "en", body)
	}
	collectCalls := func() []AppCall {
		h.in.mu.Lock()
		defer h.in.mu.Unlock()
		var calls []AppCall
		for _, call := range h.in.asked {
			if call.Method == assistantwire.InboxCollect {
				calls = append(calls, call)
			}
		}
		return calls
	}
	status, _ := post("")
	calls := collectCalls()
	if status != http.StatusOK || len(calls) == 0 {
		t.Fatalf("empty refresh: status=%d calls=%d", status, len(calls))
	}
	fullCall := len(calls) - 1
	var full assistantwire.InboxCollectRequest
	if err := json.Unmarshal(calls[fullCall].Params, &full); err != nil {
		t.Fatal(err)
	}
	if full.Round != assistantwire.RoundFull || full.LarkReplay != nil {
		t.Fatalf("empty body request = %+v", full)
	}

	body := `{"lark_replay":{"chat_id":"chat-1","start":"2026-11-13T10:00:00Z","end":"2026-11-13T10:30:00Z","message_ids":["m1","m2"]}}`
	status, _ = post(body)
	calls = collectCalls()
	if status != http.StatusOK || len(calls) != fullCall+2 {
		t.Fatalf("replay refresh: status=%d calls=%d", status, len(calls))
	}
	var replay assistantwire.InboxCollectRequest
	if err := json.Unmarshal(calls[fullCall+1].Params, &replay); err != nil {
		t.Fatal(err)
	}
	if replay.Round != "" || replay.LarkReplay == nil || replay.LarkReplay.ChatID != "chat-1" || replay.LarkReplay.Start != "2026-11-13T10:00:00Z" || replay.LarkReplay.End != "2026-11-13T10:30:00Z" || len(replay.LarkReplay.MessageIDs) != 2 || replay.LarkReplay.MessageIDs[1] != "m2" {
		t.Fatalf("forwarded replay = %+v", replay)
	}

	for _, bad := range []string{
		`{`,
		`{"unexpected":true}`,
		`{"round":"full"}`,
		`{"lark_replay":null}`,
		`{} {}`,
	} {
		status, _ = post(bad)
		calls = collectCalls()
		if status != http.StatusBadRequest || len(calls) != fullCall+2 {
			t.Fatalf("invalid body %q: status=%d calls=%d", bad, status, len(calls))
		}
	}
}
