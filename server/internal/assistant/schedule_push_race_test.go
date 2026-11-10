package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A user message queued behind a running patrol is not its input. The patrol's answer stays quiet, but the
// subsequent answer to the user must push. The fake hub captures notifications; no device is contacted.
func TestQuietFollowupQueuedUserKeepsPatrolQuiet(t *testing.T) {
	ft := newFleetTestNoStore(t)
	fastDone(ft)
	ctx := context.Background()
	ft.f.withMachine()
	ft.f.fakeModel(t)
	setSettings(t, ft.s, func(v *Settings) { v.Main, v.MachineID = "main-1", "mac" })
	task := Task{ID: "fu_patrol", Title: "Patrol", Prompt: "Check the pending work", Repeat: repeatInterval,
		EveryMinutes: 30, At: time.Now().Add(time.Hour), Enabled: true, MachineID: "mac", SessionID: "lead1", Push: pushAsked}
	setTasks(ft.s, task)
	idle := proto.Session{SessionID: "lead1", Title: "Project", Cwd: "/home/u/.persona/assistant", Assistant: true, Status: proto.StatusDone,
		LastText: "Previous answer", UpdatedAt: time.Now()}
	ft.sessions("mac", idle)
	ft.f.sessions["mac"] = []proto.Session{idle}
	if _, err := ft.s.runTask(task, time.Now(), time.Now(), true, false); err != nil {
		t.Fatal(err)
	}
	if p := ft.f.lastSend(t); p["session_id"] != "lead1" {
		t.Fatal("patrol did not enter its existing conversation")
	}
	ft.f.mu.Lock()
	ft.f.items = []proto.Item{
		{ID: "patrol-input", Seq: 1, Role: "user", Kind: proto.KindText,
			Text: prompts.MessageSourceText(task.Prompt, taskMessageSource(task))},
		{ID: "patrol-answer", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "Nothing new"},
		{ID: "queued-user", Seq: 3, Role: "user", Kind: proto.KindText, Text: "Show me the details", Queued: true},
	}
	ft.f.mu.Unlock()
	working := idle
	working.Status = proto.StatusWorking
	ft.sessions("mac", working)
	ft.s.UserSpoke(ctx, SessionRef{MachineID: "mac", SessionID: "lead1"})
	done := idle
	done.LastText, done.UpdatedAt = "Nothing new", time.Now().Add(time.Second)
	ft.sessions("mac", done)
	if got := ft.pushesOf(assistantwire.NotifyDone, 1, 300*time.Millisecond); len(got) != 0 {
		t.Errorf("patrol answer pushed after queued user input: %d notifications", len(got))
	}
	if !ft.s.quietTaskTurn(ctx, "mac", "lead1") {
		t.Error("patrol answer would be mirrored to channels after queued user input")
	}

	ft.f.mu.Lock()
	ft.f.items = []proto.Item{
		{ID: "queued-user", Seq: 4, Role: "user", Kind: proto.KindText, Text: "Show me the details"},
		{ID: "user-answer", Seq: 5, Role: "assistant", Kind: proto.KindText, Text: "Here are the details"},
	}
	ft.f.mu.Unlock()
	ft.sessions("mac", working)
	done.LastText, done.UpdatedAt = "Here are the details", time.Now().Add(2*time.Second)
	ft.sessions("mac", done)
	got := ft.pushesOf(assistantwire.NotifyDone, 1, 2*time.Second)
	if len(got) != 1 || got[0].SessionID != "lead1" || got[0].Body != "Here are the details" {
		t.Errorf("user answer should be the only completion pushed: %d notifications", len(got))
	}
	if ft.s.quietTaskTurn(ctx, "mac", "lead1") {
		t.Error("the user's own answer would be hidden from channels")
	}
	for run := 1; run <= 2; run++ {
		if _, err := ft.s.runTask(task, time.Now(), time.Now(), true, false); err != nil {
			t.Fatal(err)
		}
		reply := fmt.Sprintf("Patrol %d: nothing new", run)
		ft.f.mu.Lock()
		ft.f.items = []proto.Item{
			{Role: "user", Kind: proto.KindText, Text: prompts.MessageSourceText(task.Prompt, taskMessageSource(task))},
			{Role: "assistant", Kind: proto.KindText, Text: reply},
		}
		ft.f.mu.Unlock()
		ft.sessions("mac", working)
		done.LastText, done.UpdatedAt = reply, time.Now().Add(time.Duration(run+2)*time.Second)
		ft.sessions("mac", done)
		if got := ft.pushesOf(assistantwire.NotifyDone, 2, 200*time.Millisecond); len(got) != 1 {
			t.Errorf("consecutive patrol %d added a completion notification", run)
		}
		if !ft.s.quietTaskTurn(ctx, "mac", "lead1") {
			t.Errorf("consecutive patrol %d would be mirrored", run)
		}
	}
}

func TestQuietScheduledInputRestoresProvenance(t *testing.T) {
	ctx := context.Background()
	n := newFakeHub().withMachine()
	st := &memStore{}
	s := newServiceOn(t, n, st)
	setSettings(t, s, func(v *Settings) { v.Main, v.MachineID = "main-1", "server" })
	sess := proto.Session{SessionID: "lead1", Cwd: "/home/u/.persona/assistant", Assistant: true, Status: proto.StatusDone, LastText: "Nothing new", UpdatedAt: time.Now()}
	n.setSessions("server", sess)
	task := Task{SessionID: "lead1", Title: "Patrol", Prompt: "Check the pending work", Repeat: repeatInterval, Push: pushAsked}
	text := prompts.MessageSourceText(task.Prompt, taskMessageSource(task))
	for _, tc := range []struct {
		name  string
		input proto.Item
		quiet bool
	}{
		{"persisted asked input", proto.Item{Role: "user", Kind: proto.KindText, Text: text}, true},
		{"legacy source without push", proto.Item{Role: "user", Kind: proto.KindText, Text: "Check", Source: &proto.MessageSource{Kind: proto.SourcePatrol}}, false},
		{"user's own input", proto.Item{Role: "user", Kind: proto.KindText, Text: "Show me the details"}, false},
		{"pasted source is still the user's", proto.Item{Role: "user", Kind: proto.KindText, Text: prompts.ProtectUserMessage(text)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Serialize and read back the native input, then replace the manager entirely: no held state survives.
			b, err := json.Marshal([]proto.Item{tc.input,
				{Role: "user", Kind: proto.KindNotice, Text: "Background work finished"},
				{Role: "user", Kind: proto.KindText, Text: "A background wake", Wake: true},
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
			if got := again.quietTaskTurn(ctx, "server", "lead1"); got != tc.quiet {
				t.Errorf("restored input quiet = %v, want %v", got, tc.quiet)
			}
			failed := sess
			failed.TurnError = "model unavailable"
			if again.quietScheduledInput(ctx, "server", failed) {
				t.Error("an error completion was suppressed")
			}
		})
	}
}

func TestQuietFollowupExplicitNotifyStillDelivers(t *testing.T) {
	ft := newFleetTestNoStore(t)
	ft.f.withMachine()
	setSettings(t, ft.s, func(v *Settings) { v.Main, v.MachineID = "main-1", "mac" })
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
		main, known := ft.s.knownMain(context.Background())
		current, ok := ft.s.session(context.Background(), "mac", "lead1")
		t.Fatalf("fixture is not a quiet scheduled answer: main=%q known=%v session=%v agent=%q status=%q last=%q", main, known, ok, current.Agent, current.Status, current.LastText)
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
