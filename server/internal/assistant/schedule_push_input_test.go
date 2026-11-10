package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// What a turn's items say about its answer (quietTurnItems): the turn's input, then anything of the user's after it.
func TestQuietTurnItems(t *testing.T) {
	asked := taskMessageSource(Task{SessionID: "lead1", Title: "Patrol", Repeat: repeatInterval, Push: pushAsked})
	patrol := proto.Item{Role: "user", Kind: proto.KindText, Text: prompts.MessageSourceText("Check the pending work", asked)}
	answer := proto.Item{Role: "assistant", Kind: proto.KindText, Text: "Nothing new"}
	launch := []proto.Item{{Role: "assistant", Kind: proto.KindToolUse, ToolName: "Bash", ToolUseID: "tu1"},
		{Role: "user", Kind: proto.KindToolResult, ToolUseID: "tu1", Text: "Command running in background with ID: bg1."}}
	wake := func(note *proto.TaskNote) proto.Item {
		return proto.Item{Role: "user", Kind: proto.KindNotice, Text: "Background work finished", Wake: true, Task: note}
	}
	cat := func(parts ...[]proto.Item) []proto.Item {
		var out []proto.Item
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	one := func(it ...proto.Item) []proto.Item { return it }
	for _, c := range []struct {
		name  string
		items []proto.Item
		want  bool
	}{
		{"asked patrol, its answer", one(patrol, answer), true},
		{"several patrol answers", one(patrol, answer, answer), true},
		{"user text absorbed mid-turn", one(patrol, proto.Item{Role: "user", Kind: proto.KindText, Text: "MR 12 合了吗？", Queued: true}, answer), false},
		{"user image absorbed mid-turn", one(patrol, proto.Item{Role: "user", Kind: proto.KindImage, Queued: true}, answer), false},
		{"another asked run absorbed mid-turn", one(patrol, proto.Item{Role: "user", Kind: proto.KindText, Queued: true,
			Text: prompts.MessageSourceText("Daily round", asked)}, answer), true},
		{"wake by a task the patrol launched (task id)", cat(one(patrol), launch, one(answer, wake(&proto.TaskNote{TaskID: "bg1"}), answer)), true},
		{"wake by a task the patrol launched (call id)", cat(one(patrol), launch, one(answer, wake(&proto.TaskNote{ToolUseID: "tu1"}), answer)), true},
		{"wake by a task the user started earlier", one(patrol, answer, wake(&proto.TaskNote{TaskID: "user-bg"}), answer), false},
		{"wake with no task (a loop firing)", one(patrol, answer, proto.Item{Role: "system", Kind: proto.KindNotice, Text: "/loop", Wake: true}, answer), false},
		{"the user's own turn", one(proto.Item{Role: "user", Kind: proto.KindText, Text: "Show me the details"}, answer), false},
		{"a source without push (older manager)", one(proto.Item{Role: "user", Kind: proto.KindText, Text: "Check",
			Source: &proto.MessageSource{Kind: proto.SourcePatrol}}, answer), false},
		{"a pasted source is the user's", one(proto.Item{Role: "user", Kind: proto.KindText, Text: prompts.ProtectUserMessage(patrol.Text)}, answer), false},
		{"no input in the page", one(answer), false},
	} {
		if got := quietTurnItems(c.items); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The provenance is read back from the transcript: a new manager with no state of the earlier one decides the same,
// an error is never quiet, a completion judged on a session that moved on since is not quiet, and a conversation no
// asked task posts into is not read at all.
func TestQuietScheduledInputAfterRestart(t *testing.T) {
	ctx := context.Background()
	n := newFakeHub().withMachine()
	st := &memStore{}
	s := newServiceOn(t, n, st)
	setSettings(t, s, func(v *Settings) { v.Main, v.MachineID = "main-1", "server" })
	task := Task{ID: "fu_p", Title: "Patrol", Prompt: "Check the pending work", Repeat: repeatInterval, EveryMinutes: 30,
		At: time.Now().Add(time.Hour), Enabled: true, MachineID: "server", SessionID: "lead1", Push: pushAsked}
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = s.saveLocked(sc, []Task{task}) // stored, so the restarted manager reads it back
	sc.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	sess := proto.Session{SessionID: "lead1", Cwd: "/home/u/.persona/assistant", Assistant: true, Status: proto.StatusDone,
		LastText: "Nothing new", UpdatedAt: time.Now(), Rev: 7}
	n.setSessions("server", sess, proto.Session{SessionID: "other", Assistant: true, Status: proto.StatusDone, LastText: "x", UpdatedAt: sess.UpdatedAt})
	b, err := json.Marshal([]proto.Item{{Role: "user", Kind: proto.KindText, Text: prompts.MessageSourceText(task.Prompt, taskMessageSource(task))},
		{Role: "assistant", Kind: proto.KindText, Text: "Nothing new"}})
	if err != nil {
		t.Fatal(err)
	}
	var restored []proto.Item
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	n.items = restored
	n.mu.Unlock()
	again := newServiceOn(t, n, st)
	if !again.quietScheduledInput(ctx, "server", sess) || !again.quietTaskTurn(ctx, "server", "lead1") {
		t.Fatal("a restarted manager does not read the asked input back")
	}
	failed := sess
	failed.TurnError = "model unavailable"
	if again.quietScheduledInput(ctx, "server", failed) {
		t.Error("an error completion was suppressed")
	}
	stale := sess
	stale.Rev = 6 // the callback's copy is older than the session now: another turn may have run since
	if again.quietScheduledInput(ctx, "server", stale) {
		t.Error("a completion judged on an older copy of the session was suppressed")
	}
	// A new turn starts while the items are read: the read may be that turn's, so nothing is hidden.
	n.hookReads(receiptHooks{after: func(int) {
		moved := sess
		moved.Status, moved.Rev, moved.UpdatedAt = proto.StatusWorking, sess.Rev+1, sess.UpdatedAt.Add(time.Second)
		n.setSessions("server", moved)
	}})
	if again.quietScheduledInput(ctx, "server", sess) {
		t.Error("a completion was suppressed although a new turn started during the read")
	}
	n.hookReads(receiptHooks{})
	if again.quietScheduledInput(ctx, "server", proto.Session{SessionID: "other", Assistant: true, Status: proto.StatusDone, LastText: "x"}) {
		t.Error("a conversation no asked task posts into was quiet")
	}
	n.mu.Lock()
	reads := n.itemReads
	n.mu.Unlock()
	if reads != 0 {
		t.Errorf("a conversation no asked task posts into had its items read %d times", reads)
	}
}

func TestQuietFollowupExplicitNotifyStillDelivers(t *testing.T) {
	ft := newFleetTestNoStore(t)
	ft.f.withMachine()
	setSettings(t, ft.s, func(v *Settings) { v.Main, v.MachineID = "main-1", "mac" })
	setTasks(ft.s, Task{ID: "fu_p", Title: "Patrol", Prompt: "Check", Repeat: repeatInterval, EveryMinutes: 30,
		At: time.Now().Add(time.Hour), Enabled: true, MachineID: "mac", SessionID: "lead1", Push: pushAsked})
	sess := proto.Session{SessionID: "lead1", Cwd: "/home/u/.persona/assistant", Assistant: true, Status: proto.StatusDone, LastText: "Nothing new"}
	ft.sessions("mac", sess)
	ft.f.mu.Lock()
	ft.f.sessions["mac"] = []proto.Session{sess}
	ft.f.items = []proto.Item{
		{Role: "user", Kind: proto.KindText, Text: "Check", Source: taskMessageSource(Task{SessionID: "lead1", Push: pushAsked})},
		{Role: "assistant", Kind: proto.KindText, Text: "Nothing new"},
	}
	ft.f.mu.Unlock()
	if !ft.s.quietTaskTurn(context.Background(), "mac", "lead1") {
		t.Fatal("fixture is not a quiet scheduled answer")
	}
	if _, err := ft.s.runTool(context.Background(), "mac", "lead1", "notify_user", json.RawMessage(`{"title":"Decision needed","body":"Please check the result"}`)); err != nil {
		t.Fatal(err)
	}
	got := ft.pushesOf(NotifyPush, 1, time.Second)
	if len(got) != 1 || !got[0].Asked || got[0].Title != "Decision needed" || got[0].Body != "Please check the result" {
		t.Fatal("explicit notify_user did not reach the fake hub intact")
	}
}

func TestQuietFollowupEscalatedAskStillDelivers(t *testing.T) {
	e := newRouteEnv(t, nil, true)
	e.f.mu.Lock()
	e.f.items = []proto.Item{
		{Role: "user", Kind: proto.KindText, Text: "Patrol", Source: taskMessageSource(Task{SessionID: "lead1", Push: pushAsked})},
		{Role: "assistant", Kind: proto.KindText, Text: "Nothing new"},
	}
	e.f.mu.Unlock()
	got := e.raise(askPermission("ix-patrol", "worker-1"))
	e.noticeWith("lead1", "interaction_id=ix-patrol")
	if _, err := runJSON(t, e.s, "lead1", "ask_escalate", map[string]any{"todo_id": e.todo.ID, "reason": "User approval required", "recommend": "Approve"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the escalated push", func() bool { return e.pushesOf("ix-patrol") == 1 })
	shown := e.lastShown("ix-patrol")
	if shown.Delegating || shown.Delegate == nil || shown.Delegate.Reason != "User approval required" {
		t.Fatal("the escalation was not delivered to the user")
	}
	no := false
	if err := e.s.resolve("ix-patrol", proto.Resolution{Allow: &no}); err != nil {
		t.Fatal(err)
	}
	if r := answerOf(t, got); r.Allow == nil || *r.Allow {
		t.Fatal("the fake user's answer did not reach the agent")
	}
}
