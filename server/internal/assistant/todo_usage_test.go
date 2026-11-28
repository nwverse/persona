package assistant

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// A to-do's usage sums every linked session, its subagents included, prices each model and keeps the numbers of a
// computer that stopped answering (#12936).
func TestTodoUsageSumsLinkedSessions(t *testing.T) {
	e := newTodoEnv(t, nil)
	e.f.setSessions("box")
	todo := e.create("Project board")
	for _, sid := range []string{"s1", "s2"} {
		if err := e.s.linkTodoAgent(t.Context(), todo.ID, "box", sid, "Session "+sid, "/code", "claude"); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	fail := false
	e.f.answerNode(proto.MethodSessionUsage, func(machine string, raw json.RawMessage) (any, error) {
		calls++
		if fail {
			return nil, errors.New("offline")
		}
		var p struct {
			SessionIDs []string `json:"session_ids"`
		}
		_ = json.Unmarshal(raw, &p)
		if machine != "box" || len(p.SessionIDs) != 2 {
			t.Errorf("call: %s %v", machine, p.SessionIDs)
		}
		return proto.SessionUsage{
			Sessions: []proto.SessionUsageFound{{SessionID: "s1", Found: true, Agent: "claude"}, {SessionID: "s2", Found: true, Agent: "claude"}},
			Rows: []proto.SessionUsageRow{
				{SessionID: "s1", Agent: "claude", Model: "claude-opus-5-5", Day: "2026-11-22", Input: 1_000_000, CacheWrite: 1_000_000, CacheRead: 10_000_000, Output: 1_000_000, Requests: 10},
				{SessionID: "s1", Subagent: "a1", SubagentType: "Explore", SubagentTitle: "Find it", Agent: "claude", Model: "claude-opus-5-5", Day: "2026-11-22", Output: 500_000, Requests: 2},
				{SessionID: "s2", Agent: "claude", Model: "some-unpriced-model", Day: "2026-11-21", Input: 2_000_000, Requests: 1},
			},
		}, nil
	})
	var got TodoUsage
	if err := json.Unmarshal(e.must("GET", "/api/v1/todos/"+todo.ID+"/usage", "").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// Opus 5.5 from the catalog: 4 + 5 + 10*0.2 + 1.5*20 = 41; the unpriced model at the default rate: 2*4 = 8.
	if got.Tokens.Total != 15_500_000 || math.Abs(got.USD-49) > 1e-9 || !got.Estimate || got.Sessions != 2 || got.Found != 2 || got.Stale {
		t.Fatalf("total: %+v", got)
	}
	if len(got.Parts) != 2 || got.Parts[0].SessionID != "s1" || len(got.Parts[0].Subagents) != 1 ||
		got.Parts[0].Subagents[0].Kind != "Explore" || math.Abs(got.Parts[0].Subagents[0].USD-10) > 1e-9 {
		t.Fatalf("parts: %+v", got.Parts)
	}
	if len(got.Models) != 2 || got.Models[0].PriceFrom != "catalog" || got.Models[1].PriceFrom != "default" {
		t.Fatalf("models: %+v", got.Models)
	}

	// The card read uses the kept numbers while they are fresh: no second call.
	var list struct {
		Todos map[string]TodoUsageSummary `json:"todos"`
	}
	_ = json.Unmarshal(e.must("GET", "/api/v1/todos/usage?ids="+todo.ID+",nope", "").Body.Bytes(), &list)
	if calls != 1 || len(list.Todos) != 1 || list.Todos[todo.ID].Tokens.Total != 15_500_000 {
		t.Fatalf("list: calls %d, %+v", calls, list.Todos)
	}

	// The computer goes offline after the numbers age: the last ones stay.
	e.s.todoUsage.mu.Lock()
	for k, v := range e.s.todoUsage.sessions {
		v.At = v.At.Add(-10 * todoUsageFresh)
		e.s.todoUsage.sessions[k] = v
	}
	e.s.todoUsage.mu.Unlock()
	fail = true
	_ = json.Unmarshal(e.must("GET", "/api/v1/todos/usage?ids="+todo.ID, "").Body.Bytes(), &list)
	if s := list.Todos[todo.ID]; calls != 2 || s.Tokens.Total != 15_500_000 || !s.Stale {
		t.Fatalf("offline: calls %d, %+v", calls, s)
	}
}

// The price table: an override wins over the catalog; bad prices are refused.
func TestTodoUsagePrices(t *testing.T) {
	p := UsagePrices{Default: defaultUsagePrice, Models: map[string]UsagePrice{"claude-opus-5-5": {Input: 1}}}
	if got, from := p.priceOf("claude-opus-5-5"); from != "override" || got.Input != 1 {
		t.Fatalf("override: %v %s", got, from)
	}
	if got, from := (UsagePrices{Default: defaultUsagePrice}).priceOf("claude-opus-5-5"); from != "catalog" || got != defaultUsagePrice {
		t.Fatalf("catalog: %v %s", got, from)
	}
	if got, from := (UsagePrices{Default: defaultUsagePrice}).priceOf("unknown"); from != "default" || got != defaultUsagePrice {
		t.Fatalf("default: %v %s", got, from)
	}
	e := newTodoEnv(t, nil)
	if w := e.do("PUT", "/api/v1/todos/usage/prices", `{"default":{"input":-1}}`); w.Code != 400 {
		t.Fatalf("negative price: %d", w.Code)
	}
	var got struct{ Prices UsagePrices }
	_ = json.Unmarshal(e.must("GET", "/api/v1/todos/usage/prices", "").Body.Bytes(), &got)
	if got.Prices.Default != defaultUsagePrice {
		t.Fatalf("default table: %+v", got)
	}
}
