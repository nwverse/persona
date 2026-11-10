package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// A meeting turn's mark: sent, started, ended, then gone when the next turn starts; the user speaking during it makes
// the rest of the turn theirs; an end before its start is an earlier turn's; it survives a restart, and a batch whose
// turn never started runs out.
func TestMeetingTurnLifecycle(t *testing.T) {
	st := &memStore{}
	f := newFakeHub()
	s := newServiceOn(t, f, st)
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	s.attend.turns.now = func() time.Time { return now }
	running := func(s *Service) bool {
		t.Helper()
		on, err := s.meetingTurnRunning(context.Background(), "server", "c1")
		if err != nil {
			t.Fatal(err)
		}
		return on
	}
	if _, err := s.meetingTurnSent("server", "c1", "rec"); err != nil {
		t.Fatal(err)
	}
	if !running(s) || s.meetingTurnNow("server", "c1") {
		t.Fatal("a batch sent: its turn may run (tools), but the hooks of the turn before are not its")
	}
	if s.meetingTurnEnded("server", "c1") {
		t.Fatal("an end before the batch's turn started is the turn before's")
	}
	if !s.meetingTurnStarted("server", "c1", false) || !s.meetingTurnNow("server", "c1") || !running(s) {
		t.Fatal("the batch's turn started")
	}
	if !s.meetingTurnStarted("server", "c1", false) {
		t.Fatal("the same turn seen again (a reconnect) is still the meeting's")
	}
	// A restart: the mark is read back.
	again := newServiceOn(t, f, st)
	again.attend.turns.now = s.attend.turns.now
	if !again.meetingTurnNow("server", "c1") || !running(again) {
		t.Fatal("the mark did not survive a restart")
	}
	if !s.meetingTurnEnded("server", "c1") || running(s) || !s.meetingTurnNow("server", "c1") {
		t.Fatal("ended: no tools are its any more, the hooks of its end still find it")
	}
	if !s.meetingTurnEnded("server", "c1") {
		t.Fatal("the same end, told twice, says the same")
	}
	s.meetingTurnUserSpoke("server", "c1") // the user's message after it: the hooks of its end still find it
	if !s.meetingTurnNow("server", "c1") {
		t.Fatal("the user's next message dropped an ended mark before its turn started")
	}
	if s.meetingTurnStarted("server", "c1", true) || s.meetingTurnNow("server", "c1") {
		t.Fatal("the turn after a meeting turn is not one")
	}

	// The user speaks while the batch's turn runs: from there on it is theirs.
	_, _ = s.meetingTurnSent("server", "c1", "rec")
	s.meetingTurnStarted("server", "c1", true)
	s.meetingTurnUserSpoke("server", "c1")
	if running(s) || s.meetingTurnNow("server", "c1") {
		t.Fatal("the user's own message did not end the meeting turn")
	}

	// A send that was refused takes its mark back; one whose turn never starts runs out.
	at, _ := s.meetingTurnSent("server", "c1", "rec")
	s.meetingTurnUnsent("server", "c1", at)
	if running(s) {
		t.Fatal("a refused batch left its mark")
	}
	_, _ = s.meetingTurnSent("server", "c1", "rec")
	now = now.Add(meetingTurnStartWithin + time.Second)
	if running(s) || s.meetingTurnStarted("server", "c1", true) {
		t.Fatal("a batch whose turn never started still marks the next one")
	}
}

// During a meeting turn of the calling session only the reading tools work; every other hub tool says it needs the
// user's own message. Another session, or the same once the turn ended, is not affected.
func TestMeetingTurnToolsOnlyRead(t *testing.T) {
	f := newFakeHub()
	f.hello.Locale = "en"
	s := newTestService(t, f)
	ctx := context.Background()
	_, _ = s.meetingTurnSent("server", "c1", "rec")
	for _, name := range meetingTurnTools {
		if _, refused := s.meetingRefuses(ctx, "server", "c1", name); refused {
			t.Errorf("%s refused during a meeting turn", name)
		}
	}
	for _, name := range []string{"todo_add", "agent_send", "conversation_send", "notify_user", "phone_notify", "mail_send", "kb_update", "task_plan"} {
		res, refused := s.meetingRefuses(ctx, "server", "c1", name)
		if !refused || !res.IsError || !strings.Contains(res.Content[0].Text, name) || !strings.Contains(res.Content[0].Text, "user's own message") {
			t.Errorf("%s: %v %+v", name, refused, res)
		}
	}
	if res := s.mcpCall(ctx, "server", "c1", "notify_user", json.RawMessage(`{}`), true); !res.IsError || !strings.Contains(res.Content[0].Text, "user's own message") {
		t.Errorf("mcp.call during a meeting turn: %+v", res)
	}
	if res := s.Tool(ctx, ToolCall{Name: "todo_add", MachineID: "server", SessionID: "c1", Arguments: json.RawMessage(`{}`)}); !res.IsError ||
		!strings.Contains(res.Content[0].Text, "user's own message") {
		t.Errorf("assistant.tool during a meeting turn: %+v", res)
	}
	if _, refused := s.meetingRefuses(ctx, "server", "c2", "todo_add"); refused {
		t.Error("another session's tools refused")
	}
	s.meetingTurnStarted("server", "c1", false)
	s.meetingTurnEnded("server", "c1")
	if _, refused := s.meetingRefuses(ctx, "server", "c1", "todo_add"); refused {
		t.Error("tools refused after the meeting turn ended")
	}
}

// A meeting's batch is no one speaking in the conversation: a supervision hold stays (its turns stay unpushed), as it
// does not for another send; and the batch carries its source in the text.
func TestMeetingSendKeepsSupervisionHold(t *testing.T) {
	f := newFakeHub().withMachine()
	f.known["c1"] = true
	s := newTestService(t, f)
	ctx := context.Background()
	s.holdPushes(ctx, "c1")
	src := &proto.MessageSource{Kind: proto.SourceMeeting, Title: "Design review"}
	if err := s.call(ctx, "server", DeviceAssistantSend, map[string]any{"session_id": "c1", "text": "lines", "message_source": src, "resume_only": true}, nil); err != nil {
		t.Fatal(err)
	}
	if !s.supervising("c1") {
		t.Fatal("a meeting's batch released the supervision hold")
	}
	f.mu.Lock()
	sent := f.sends[len(f.sends)-1]
	f.mu.Unlock()
	if text, _ := sent["text"].(string); !strings.Contains(text, `kind="message-source"`) || !strings.Contains(text, `"kind":"meeting"`) || sent["client_msg_id"] != nil {
		t.Fatalf("sent %+v", sent)
	}
	if err := s.call(ctx, "server", DeviceAssistantSend, map[string]any{"session_id": "c1", "text": "hi", "resume_only": true}, nil); err != nil {
		t.Fatal(err)
	}
	if s.supervising("c1") {
		t.Fatal("another send kept the hold")
	}
}

// A meeting turn in the fleet: no finished push and nothing of it left in the turn tracker, so the next turn of the
// user's is timed and pushed as ever; no triage, no channel mirror, never the task's.
func TestMeetingTurnIsSideExchange(t *testing.T) {
	ft := newFleetTestNoStore(t)
	fastDone(ft)
	s := ft.s
	work := proto.Session{SessionID: "s1", Title: "Fix login", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking, PaneID: "p1",
		UpdatedAt: time.Now()}
	done := work
	done.Status, done.LastText = proto.StatusDone, "Notes: Ada keeps the cap."
	ft.sessions("mac", done)
	if _, err := s.meetingTurnSent("mac", "s1", "rec"); err != nil {
		t.Fatal(err)
	}
	ft.sessions("mac", work)
	if !s.meetingTurnNow("mac", "s1") || !s.quietTaskTurn(context.Background(), "mac", "s1") {
		t.Fatal("the batch's turn is not a meeting turn")
	}
	ft.sessions("mac", done)
	if got := ft.pushesOf(assistantwire.NotifyDone, 1, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("a meeting turn was pushed: %+v", got)
	}
	if !s.turnsBook().startOf("mac/s1").IsZero() {
		t.Fatal("the meeting turn is in the turn tracker")
	}
	s.triageTurn(context.Background(), s.newWorkView(context.Background()), "mac", done)
	s.work.mu.Lock()
	_, tried := s.work.tried[[2]string{"mac", "s1"}]
	s.work.mu.Unlock()
	if tried {
		t.Fatal("the meeting turn was triaged")
	}
	// The user's own turn after it is pushed as ever.
	user := work
	user.UpdatedAt = time.Now()
	ft.sessions("mac", user)
	if s.meetingTurnNow("mac", "s1") {
		t.Fatal("the turn after the meeting turn is one")
	}
	answer := user
	answer.Status, answer.LastText = proto.StatusDone, "Fixed."
	ft.sessions("mac", answer)
	if got := ft.pushesOf(assistantwire.NotifyDone, 1, 2*time.Second); len(got) != 1 || got[0].Body != "Fixed." {
		t.Fatalf("the user's turn: %+v", got)
	}
}

// A turn started by a meeting's batch is a side exchange to the task's completion: it never hands the task over.
func TestTaskTurnSourceMeeting(t *testing.T) {
	text := "<persona-context kind=\"message-source\" title=\"\">\n{\"kind\":\"meeting\",\"title\":\"Design review\"}\n</persona-context>\nNew lines"
	_, source := taskTurnSource(Todo{}, []proto.Item{{Role: "user", Kind: proto.KindText, Text: text}})
	if source != "meeting" {
		t.Fatalf("source %q", source)
	}
}
