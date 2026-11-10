package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
)

func todoRuleTestConfig() TodoRuleConfig {
	on := true
	return TodoRuleConfig{Rules: []TodoRule{{ID: "review", Name: "Review requests", Enabled: &on, Sources: []string{"example"}, Kinds: []string{"review_requested"}, Prompt: "Someone asks me to review, fix, or reply to something."}}}
}

func todoRuleItem(id, last, title, contextText string) *InboxItem {
	return &InboxItem{ID: id, Source: "example", Kind: "review_requested", Title: title, Sender: "A sender", Preview: contextText,
		Context: contextText, Last: last, TriagedAt: last, UpdatedAt: time.Now().UTC(), Connector: &InboxConnector{Thread: "thread-1"}}
}

func setupTodoRuleTest(t *testing.T, reply string) (*inboxHub, func() []capturedCall) {
	t.Helper()
	h := newInboxHub(t)
	model, calls := captureModel(t, reply)
	h.in.models[purposeTriage] = model
	if err := h.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.todoRules.mu.Lock()
	h.todoRules.state.Config = todoRuleTestConfig()
	h.todoRules.mu.Unlock()
	return h, calls
}

func TestTodoRulesCreateAppendAndDeduplicate(t *testing.T) {
	h, calls := setupTodoRuleTest(t, `{"is_todo":true,"title":"Review the change","summary":"Review the requested change and reply with the result.","reason":"The sender asks for a review."}`)
	first := todoRuleItem("item-1", "v1", "Review request", "Please review this change and reply with your findings.")
	h.inbox.mu.Lock()
	h.inbox.items[first.ID] = first
	h.inbox.mu.Unlock()
	h.processTodoRules(context.Background())
	if len(calls()) != 1 {
		t.Fatalf("model calls = %d, want 1", len(calls()))
	}
	todos := h.todos.todos
	if len(todos) != 1 {
		t.Fatalf("to-dos = %d, want 1", len(todos))
	}
	var todoID string
	for id, todo := range todos {
		todoID = id
		if todo.Title != "Review the change" || !strings.Contains(todo.Notes, "Review the requested change") {
			t.Fatalf("unexpected to-do: %+v", todo)
		}
	}
	h.todoRules.mu.Lock()
	if got := h.todoRules.state.Decisions; len(got) != 1 || got[0].Verdict != "created" || got[0].RuleName != "Review requests" || got[0].TodoID != todoID || got[0].Reason == "" {
		h.todoRules.mu.Unlock()
		t.Fatalf("create decision log = %+v", got)
	}
	h.todoRules.mu.Unlock()
	h.inbox.mu.Lock()
	parent := first.Parent
	h.inbox.mu.Unlock()
	if parent == nil || parent.Kind != ParentTodo || parent.ID != todoID || parent.By != ParentByRule {
		t.Fatalf("parent = %+v, want rule parent to %s", parent, todoID)
	}

	second := todoRuleItem("item-2", "v2", "Follow-up", "Here is a follow-up in the same thread; please reply with the review result.")
	h.inbox.mu.Lock()
	h.inbox.items[second.ID] = second
	h.inbox.mu.Unlock()
	h.processTodoRules(context.Background())
	if len(calls()) != 2 {
		t.Fatalf("model calls after follow-up = %d, want 2", len(calls()))
	}
	if len(h.todos.todos) != 1 || strings.Count(h.todos.todos[todoID].Notes, "Review the requested change") != 2 {
		t.Fatalf("follow-up did not append to %s: %+v", todoID, h.todos.todos)
	}
	h.todoRules.mu.Lock()
	if got := h.todoRules.state.Decisions; len(got) != 2 || got[1].Verdict != "appended" || got[1].TodoID != todoID || got[1].RuleName != "Review requests" {
		h.todoRules.mu.Unlock()
		t.Fatalf("append decision log = %+v", got)
	}
	h.todoRules.mu.Unlock()
	h.processTodoRules(context.Background())
	if len(calls()) != 2 || len(h.todos.todos) != 1 {
		t.Fatalf("same version was processed again: calls=%d todos=%d", len(calls()), len(h.todos.todos))
	}
}

func TestTodoRulesChitChatAndDryRun(t *testing.T) {
	h, calls := setupTodoRuleTest(t, `{"is_todo":false,"title":"","summary":"","reason":"This is ordinary conversation."}`)
	it := todoRuleItem("chat", "v1", "Chat", "Nice work, thanks for sharing this.")
	h.inbox.mu.Lock()
	h.inbox.items[it.ID] = it
	h.inbox.mu.Unlock()
	h.processTodoRules(context.Background())
	if len(calls()) != 1 || len(h.todos.todos) != 0 || it.Parent != nil {
		t.Fatalf("chit-chat result: calls=%d todos=%d parent=%+v", len(calls()), len(h.todos.todos), it.Parent)
	}

	h2, calls2 := setupTodoRuleTest(t, `{"is_todo":true,"title":"Review","summary":"Review it.","reason":"Action requested."}`)
	h2.todoRules.mu.Lock()
	cfg := h2.todoRules.state.Config
	cfg.DryRun = true
	h2.todoRules.state.Config = cfg
	h2.todoRules.mu.Unlock()
	dry := todoRuleItem("dry", "v1", "Review", "Please review this.")
	h2.inbox.mu.Lock()
	h2.inbox.items[dry.ID] = dry
	h2.inbox.mu.Unlock()
	h2.processTodoRules(context.Background())
	if len(calls2()) != 1 || len(h2.todos.todos) != 0 || dry.Parent != nil {
		t.Fatalf("dry run created state: calls=%d todos=%d parent=%+v", len(calls2()), len(h2.todos.todos), dry.Parent)
	}
}

func TestTodoRulesSkipWithoutModelCall(t *testing.T) {
	tests := []struct {
		name string
		make func(*inboxHub) *InboxItem
	}{
		{"done", func(_ *inboxHub) *InboxItem {
			now := time.Now()
			it := todoRuleItem("done", "v1", "Done", "Review this")
			it.DoneAt = &now
			return it
		}},
		{"muted", func(h *inboxHub) *InboxItem {
			it := todoRuleItem("muted", "v1", "Muted", "Review this")
			it.Connector.Tags = []connector.Tag{{Key: connector.TagChat, Value: "thread-1"}}
			h.inbox.mutes = []InboxMute{{Source: "example", Tag: connector.TagChat, Value: "thread-1"}}
			return it
		}},
		{"parented", func(_ *inboxHub) *InboxItem {
			it := todoRuleItem("parented", "v1", "Parented", "Review this")
			it.Parent = &InboxParent{Kind: ParentTodo, ID: "todo_existing", By: ParentByUser}
			return it
		}},
		{"disabled", func(h *inboxHub) *InboxItem {
			off := false
			h.todoRules.mu.Lock()
			cfg := h.todoRules.state.Config
			cfg.Rules[0].Enabled = &off
			h.todoRules.state.Config = cfg
			h.todoRules.mu.Unlock()
			return todoRuleItem("disabled", "v1", "Disabled", "Review this")
		}},
		{"unmatched", func(_ *inboxHub) *InboxItem {
			it := todoRuleItem("unmatched", "v1", "Other", "Review this")
			it.Source = "other"
			return it
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, calls := setupTodoRuleTest(t, `{"is_todo":true,"title":"Review","summary":"Review","reason":"action"}`)
			it := tc.make(h)
			h.inbox.mu.Lock()
			h.inbox.items[it.ID] = it
			h.inbox.mu.Unlock()
			h.processTodoRules(context.Background())
			if len(calls()) != 0 || len(h.todos.todos) != 0 {
				t.Fatalf("skip made a model call or created a to-do: calls=%d todos=%d", len(calls()), len(h.todos.todos))
			}
		})
	}
}

func TestTodoRuleSettingsAndDecisionLog(t *testing.T) {
	h := newInboxHub(t)
	if code, _ := inboxCall(t, h.routes(), http.MethodPut, "/api/v1/todo-rules", `{"dry_run":true,"rules":[{"id":"r1","name":"Review","enabled":true,"prompt":"review requests"}]}`); code != http.StatusOK {
		t.Fatalf("PUT settings status = %d", code)
	}
	code, out := inboxCall(t, h.routes(), http.MethodGet, "/api/v1/todo-rules", "")
	if code != http.StatusOK || out["dry_run"] != true {
		t.Fatalf("GET settings: code=%d out=%v", code, out)
	}
	h.todoRules.mu.Lock()
	h.todoRules.state.Decisions = []TodoRuleDecision{{At: time.Now().UTC(), ItemID: "item-1", RuleName: "Review", Verdict: "not_todo", Reason: "No action requested."}}
	h.todoRules.mu.Unlock()
	code, out = inboxCall(t, h.routes(), http.MethodGet, "/api/v1/todo-rules/decisions", "")
	if code != http.StatusOK {
		t.Fatalf("GET decisions status = %d", code)
	}
	var decisions []TodoRuleDecision
	raw, _ := json.Marshal(out["decisions"])
	if err := json.Unmarshal(raw, &decisions); err != nil || len(decisions) != 1 || decisions[0].ItemID != "item-1" || decisions[0].Reason == "" {
		t.Fatalf("decision log = %s", raw)
	}
}

func TestGoldenTodoRulePrompt(t *testing.T) {
	golden(t, "todo_rule.system", prompts.RenderTodoRule(prompts.TodoRule{RulePrompt: "Someone asks me to review or reply.", Source: "example", Kind: "review_requested", Sender: "Example sender", Title: "Review request", Context: "Please review this change and reply."}))
}
