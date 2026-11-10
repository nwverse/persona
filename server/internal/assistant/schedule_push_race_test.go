package assistant

import (
	"context"
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
	idle := proto.Session{SessionID: "lead1", Title: "Project", Assistant: true, Status: proto.StatusDone,
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
		{ID: "user-input", Seq: 4, Role: "user", Kind: proto.KindText, Text: "Show me the details"},
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
}
