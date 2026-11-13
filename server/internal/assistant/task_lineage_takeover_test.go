package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// Reusing an agent whose earlier card is closed creates a continuation, not an unrelated card. Both directions are
// visible in GET /api/v1/todos, with the hand-over note carried on the old card (#5732).
func TestAgentSuperviseAfterCloseLinksTheTakeoverLineage(t *testing.T) {
	s, f, old := newSupervisedService(t)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	raw, _ := json.Marshal(map[string]any{"todo_id": old.ID, "verdict": "accepted", "note": "Spec 已验收"})
	if _, err := s.runTool(context.Background(), "server", "conv1", "task_review", raw); err != nil {
		t.Fatal(err)
	}
	if got := todoNow(s, old.ID); got.DoneAt == nil {
		t.Fatal("the old card is not closed")
	}
	machine, ok := s.device(context.Background(), "box")
	if !ok {
		t.Fatal("task machine is missing")
	}
	agent, ok := s.session(context.Background(), "box", "s1")
	if !ok {
		t.Fatal("task agent is missing")
	}
	if _, made, err := s.adoptAgent(context.Background(), "conv1", machine, agent, "接手实现", "", "换 Agent 接手实现", nil, 0, true); err != nil || !made {
		t.Fatalf("adopt after close: made %v, err %v", made, err)
	}
	var current Todo
	for _, td := range s.todosOf(context.Background(), "*") {
		if td.DoneAt == nil {
			current = td
		}
	}
	if current.ID == "" || current.ID == old.ID {
		t.Fatalf("no new takeover card after %+v", old)
	}
	listed := listedTodos(t, s)
	steps := func(id string) []map[string]any {
		raw, _ := json.Marshal(listed[id]["lineage"])
		var out []map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	var oldBefore, newBefore map[string]any
	for _, step := range steps(old.ID) {
		if step["todo_id"] == old.ID {
			oldBefore = step
		}
	}
	for _, step := range steps(current.ID) {
		if step["todo_id"] == current.ID {
			newBefore = step
		}
	}
	if oldBefore["superseded_by"] != current.ID {
		t.Fatalf("old lineage = %+v", steps(old.ID))
	}
	if newBefore["continues"] != old.ID || newBefore["current"] != true {
		t.Fatalf("new lineage = %+v", steps(current.ID))
	}
	// The assistant's text snapshot exposes the same two directions and their result links, not only the API (#5732).
	shots := map[string]bool{}
	for _, task := range s.snapshotTasks(context.Background(), "https://hub.example.com") {
		shots[task.ID] = true
		switch task.ID {
		case old.ID:
			if task.SupersededBy != current.ID || task.SupersededByLink != "https://hub.example.com/todos?id="+current.ID {
				t.Fatalf("old snapshot = %+v", task)
			}
		case current.ID:
			if task.Continues != old.ID || task.ContinuesLink != "https://hub.example.com/todos?id="+old.ID {
				t.Fatalf("new snapshot = %+v", task)
			}
		}
	}
	if !shots[old.ID] || !shots[current.ID] {
		t.Fatalf("snapshot is missing a lineage card: %v", shots)
	}
	// The continuation now belongs to a replacement Agent. Re-supervising the old one must not open another continuation.
	s.todos.mu.Lock()
	delete(s.todos.activeBySession, [2]string{"box", "s1"})
	s.todos.activeBySession[[2]string{"box", "s2"}] = current.ID
	s.todos.todos[current.ID].Agents[0].SessionID = "s2"
	s.todos.mu.Unlock()
	before := todoCount(s)
	_, again, adoptErr := s.adoptAgent(context.Background(), "conv1", machine, agent, "再次接手", "", "重复接手", nil, 0, true)
	if adoptErr == nil || !strings.Contains(adoptErr.Error(), "already carried on by to-do "+current.ID) || again {
		t.Fatalf("reusing an existing continuation: made %v, err %v", again, adoptErr)
	}
	if got := todoCount(s); got != before {
		t.Fatalf("a repeated takeover made another card: before %d, after %d", before, got)
	}
}

// A newly titled agent_send task is new work, not an explicit takeover. It gets its own brief and acceptance criteria,
// leaves the closed card's result and scope alone, and creates no lineage in either direction (#5732).
func TestAgentSendAfterCloseStartsAnIndependentBrief(t *testing.T) {
	s, f, old := newSupervisedService(t)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	raw, _ := json.Marshal(map[string]any{"todo_id": old.ID, "verdict": "accepted", "note": "Spec 已验收"})
	if _, err := s.runTool(context.Background(), "server", "conv1", "task_review", raw); err != nil {
		t.Fatal(err)
	}
	agent, ok := s.session(context.Background(), "box", "s1")
	if !ok {
		t.Fatal("closed task agent is missing")
	}
	agent.SessionID = "s1000000"
	f.setSessions("box", agent)
	s.todos.mu.Lock()
	s.todos.todos[old.ID].Agents[0].SessionID = agent.SessionID
	s.todos.mu.Unlock()
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{
		"machine": "box", "session_id": "s1000000", "text": "整理一份独立周报",
		"task": map[string]any{"title": "整理周报", "done_when": []string{"周报链接贴回来"}},
	}); err != nil {
		t.Fatal(err)
	}
	var current Todo
	for _, td := range s.todosOf(context.Background(), "*") {
		if td.DoneAt == nil {
			current = td
		}
	}
	if current.ID == "" || current.ID == old.ID || current.Supervision == nil ||
		len(current.Supervision.DoneWhen) != 1 || current.Supervision.DoneWhen[0] != "周报链接贴回来" {
		t.Fatalf("independent task = %+v", current)
	}
	oldBrief, oldOK := s.briefOfTodo(old.ID)
	newBrief, newOK := s.briefOfTodo(current.ID)
	if !oldOK || !newOK || oldBrief.ID == newBrief.ID {
		t.Fatalf("the new task inherited the closed task's brief: old=%+v new=%+v", oldBrief, newBrief)
	}
	if len(oldBrief.Criteria) != 2 || oldBrief.Criteria[0] != "PR 合并到 master" || oldBrief.Criteria[1] != "浅色 / 深色截图贴进 PR" {
		t.Fatalf("the closed brief's criteria changed: %+v", oldBrief.Criteria)
	}
	listed := listedTodos(t, s)
	oldLineage, _ := json.Marshal(listed[old.ID]["lineage"])
	newLineage, _ := json.Marshal(listed[current.ID]["lineage"])
	if strings.Contains(string(oldLineage), "superseded_by") || strings.Contains(string(newLineage), "continues") {
		t.Fatalf("unrelated work formed lineage: old=%s new=%s", oldLineage, newLineage)
	}
}
