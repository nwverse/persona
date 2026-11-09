package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// routedFroms is who wrote each routed item of conversation conv, in order: "assistant", or "" for the user.
func routedFroms(t *testing.T, s *Service, conv string) []string {
	t.Helper()
	items, _, err := s.feed(context.Background(), conv, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		if it.Kind != feedRouted {
			continue
		}
		var b struct {
			From string `json:"from"`
		}
		if err := json.Unmarshal(it.Body, &b); err != nil {
			t.Fatalf("routed body %s: %v", it.Body, err)
		}
		out = append(out, b.From)
	}
	return out
}

// What Persona sends to a task's agent is marked as its own (#1542: the apps fold it instead of drawing the user's
// bubble): agent_send's hand-off and a rework; what the user sends to the task from its card is not.
func TestRoutedFromSaysWhoSentIt(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	ctx := context.Background()
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "把周报数据拉一下",
		"task": map[string]any{"title": "拉周报数据"}}); err != nil {
		t.Fatal(err)
	}
	todo := onlyTodo(s)
	if _, err := s.routeToTask(ctx, "conv1", todo.ID, "顺便加一列环比"); err != nil {
		t.Fatalf("the user's route: %v", err)
	}
	if _, err := s.sendToTask(ctx, todo, "（督办：验收没通过，第 1 轮返工，最多 2 轮）"); err != nil {
		t.Fatalf("rework: %v", err)
	}
	got := routedFroms(t, s, "conv1")
	if want := []string{routedFromAssistant, "", routedFromAssistant}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("from: %q, want %q (%s)", got, want, feedKinds(s, "conv1"))
	}
}
