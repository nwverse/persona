package node

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestCodexAssistantSpeed(t *testing.T) {
	n := codexTestNode(t)
	p, c, f := newFakeCodex(t, n, t.TempDir())
	f.results["thread/start"] = map[string]any{
		"thread": map[string]any{"id": testThread},
		"model":  "example", "serviceTier": "default",
	}
	f.results["model/list"] = map[string]any{"data": []any{map[string]any{
		"model": "example", "id": "alias",
		"serviceTiers": []proto.ServiceTier{{ID: "priority", Name: "Fast"}},
	}}}
	if err := c.start(context.Background(), "", p.cwd, false); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	n.procs[testThread] = p
	n.mu.Unlock()
	rows := n.assistantSessions()
	if len(rows) != 1 || rows[0].ServiceTier != "default" || len(rows[0].ServiceTiers) != 2 {
		t.Fatalf("assistant speed missing: %+v", rows)
	}
	_, err := n.handle(context.Background(), "session.speed", json.RawMessage(`{"session_id":"`+testThread+`","service_tier":"priority"}`))
	if err != nil {
		t.Fatal(err)
	}
	requests := f.sent("thread/settings/update")
	if len(requests) != 1 {
		t.Fatalf("speed requests: %v", requests)
	}
	var params map[string]any
	if err := json.Unmarshal(requests[0].Params, &params); err != nil {
		t.Fatal(err)
	}
	if len(params) != 2 || params["serviceTier"] != "priority" || params["threadId"] != testThread {
		t.Fatalf("speed changed other settings: %v", params)
	}
	row := proto.Session{Model: "example"}
	c.speedRow(&row)
	if row.ServiceTier != "priority" {
		t.Fatal("Fast was not acknowledged")
	}
	c.notification("thread/settings/updated", json.RawMessage(`{"threadId":"`+testThread+`","threadSettings":{"model":"example","serviceTier":null}}`))
	c.speedRow(&row)
	if row.ServiceTier != "default" {
		t.Fatal("native Standard was not synchronized")
	}
	f.mu.Lock()
	f.errors = map[string]string{"thread/settings/update": "speed unavailable"}
	f.mu.Unlock()
	if err := c.setSpeed(context.Background(), "priority"); err == nil {
		t.Fatal("native refusal became success")
	}
	c.speedRow(&row)
	if row.ServiceTier != "default" {
		t.Fatal("refused speed was advertised")
	}
}
