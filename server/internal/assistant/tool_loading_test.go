package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The tools that load up front, pinned: adding or dropping one is a reviewed change (#7317). Each is still a tool here
// under that name (a rename would silently defer it), but for the Lark extension's lark_*, which are not in this repository.
func TestCoreToolsPinned(t *testing.T) {
	want := []string{
		"agent_archive", "agent_interrupt", "agent_read", "agent_send", "agent_start", "agent_supervise", "agents_list",
		"ask_user", "cancel_followup", "conversation_read", "conversation_send", "conversations_list", "interaction_answer",
		"kb_get", "kb_list", "kb_search", "kb_update", "lark_chats", "lark_messages", "lark_search", "larkcli_chats",
		"larkcli_messages", "larkcli_search", "list_followups", "machines_list", "model_profiles_list", "notify_user",
		"persona_settings", "schedule_followup", "schedule_routine", "supervision_finish", "task_plan", "task_review",
		"task_scope", "todo_get", "todo_list", "todo_update",
	}
	if !slices.Equal(coreTools, want) {
		t.Fatalf("core tools changed:\n got %q\nwant %q", coreTools, want)
	}
	for _, name := range coreTools {
		if strings.HasPrefix(name, "lark_") {
			continue
		}
		func() {
			defer func() {
				if recover() != nil {
					t.Errorf("core tool %s is no MCP tool here: renamed or removed?", name)
				}
			}()
			prompts.Tool(name)
		}()
	}
}

// An assistant conversation's tool list has every tool but the core ones deferred; a guest's (no ToolSearch), an
// agent's and every one while assistant.load_all_tools is on have none deferred.
func TestToolLoadingMarksAssistantTools(t *testing.T) {
	ft := newFleetTest(t)
	ft.f.hello.Assistant.MachineID = "mac"
	s := ft.s
	s.SetExtensions(&guestExt{}) // lark_chats (core), lark_agent_reply and computer_run (not)
	ctx := context.Background()
	if err := s.guestSet(ctx, "mac", "g1", []string{"lark_agent_reply", "lark_chats"}); err != nil {
		t.Fatal(err)
	}
	// list is the tool list as the node gets it (JSON), by name: whether each is deferred.
	list := func(session string, assistant, guest bool) map[string]bool {
		raw, _ := json.Marshal(map[string]any{"assistant": assistant, "session_id": session, "guest": guest})
		out, handled, err := s.NodeRequest(ctx, "mac", "mcp.tools", raw)
		if err != nil || !handled {
			t.Fatal(err, handled)
		}
		b, _ := json.Marshal(out)
		var res struct {
			Tools []proto.Tool `json:"tools"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, tool := range res.Tools {
			got[tool.Name] = tool.DeferLoading
		}
		return got
	}
	got := list("o1", true, false)
	for name, deferred := range map[string]bool{"agent_read": false, "ask_user": false, "lark_chats": false, "task_plan": false,
		"inbox_move": true, "view_show": true, "task_plan_withdraw": true, "lark_agent_reply": true, "computer_run": true} {
		if d, ok := got[name]; !ok || d != deferred {
			t.Errorf("%s: listed %v, deferred %v; want deferred %v", name, ok, d, deferred)
		}
	}
	for name, deferred := range got {
		if deferred == slices.Contains(coreTools, name) {
			t.Errorf("%s: deferred %v", name, deferred)
		}
	}
	none := func(what string, got map[string]bool) {
		t.Helper()
		if len(got) == 0 {
			t.Fatalf("%s: no tools", what)
		}
		for name, deferred := range got {
			if deferred {
				t.Errorf("%s: %s deferred", what, name)
			}
		}
	}
	none("a guest", list("g1", true, true))
	none("an agent", list("a1", false, false))
	setSettings(t, s, func(v *Settings) { on := true; v.LoadAllTools = &on })
	none("load_all_tools on", list("o1", true, false))
}

// assistant.load_all_tools through persona_settings: off by default, a change asks on the phone with its own card, and
// the approved value is what the tool list reads.
func TestLoadAllToolsSetting(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, newFakeHub().withMachine())
	tool := func(args string) (string, bool) {
		r := s.Tool(ctx, ToolCall{Name: "persona_settings", Arguments: json.RawMessage(args), MachineID: "server", SessionID: "c1"})
		return r.Content[0].Text, r.IsError
	}
	get := func() string { out, _ := tool(`{"action":"get","key":"assistant.load_all_tools"}`); return out }
	deferred := func() bool {
		return s.withToolLoading(ctx, []proto.Tool{{Name: "inbox_move"}})[0].DeferLoading
	}
	if get() != "false" || !deferred() {
		t.Fatalf("default: %s, deferred %v", get(), deferred())
	}
	if out, isErr := tool(`{"action":"set","key":"assistant.load_all_tools","value":false}`); isErr || !strings.Contains(out, "is unchanged") {
		t.Fatalf("set to what it is: %s", out)
	}
	word := map[bool]string{true: i18n.T(i18n.Default, "assistant-settings_tool.on"), false: i18n.T(i18n.Default, "assistant-settings_tool.off")}
	card := i18n.T(i18n.Default, "assistant-settings_tool.change_load_all_tools", i18n.Args{"before": word[false], "after": word[true]})
	go func() {
		q, ok := awaitCard(s)
		if !ok || q.Summary != card {
			t.Errorf("card %q (%v), want %q", q.Summary, ok, card)
		}
		allow := true
		raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
		if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: q.ID, Answer: raw}); err != nil {
			t.Error(err)
		}
	}()
	if out, _ := tool(`{"action":"set","key":"assistant.load_all_tools","value":true}`); !strings.HasPrefix(out, "assistant.load_all_tools is true") {
		t.Fatalf("approved: %s", out)
	}
	if get() != fmt.Sprint(true) || deferred() {
		t.Fatalf("after the change: %s, deferred %v", get(), deferred())
	}
}
