package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// presenceTimes sets how long the owner counts as present and the longest a decision waits, for a test.
func presenceTimes(t *testing.T, present, decision time.Duration) {
	t.Helper()
	oldPresent, oldDecision := ownerPresentFor, relayDecisionMax
	ownerPresentFor, relayDecisionMax = present, decision
	t.Cleanup(func() { ownerPresentFor, relayDecisionMax = oldPresent, oldDecision })
}

// stopHolds stops the timers a test's held queues left.
func stopHolds(t *testing.T, s *Service) {
	t.Helper()
	t.Cleanup(func() {
		b := &s.relays
		b.mu.Lock()
		defer b.mu.Unlock()
		for id, tm := range b.holds {
			tm.Stop()
			delete(b.holds, id)
		}
	})
}

// The ranks: the owner's late answer, what waits on a decision, an outcome to report, then the rest.
func TestRelayRanks(t *testing.T) {
	for _, tc := range []struct {
		msg      relayMsg
		rank     int
		decision bool
	}{
		{relayMsg{Tool: askAnswerTool}, relayRankAnswer, true},
		{relayMsg{Tool: superviseTool, Urgent: true}, relayRankDecision, true},
		{relayMsg{Tool: noticeTool, State: "needs"}, relayRankDecision, true},
		{relayMsg{Tool: noticeTool, State: "interrupted"}, relayRankDecision, true},
		{relayMsg{Tool: noticeTool, State: "failed"}, relayRankDecision, true},
		{relayMsg{Tool: noticeTool, State: "done"}, relayRankReport, false},
		{relayMsg{Tool: superviseTool}, relayRankOther, false},
		{relayMsg{Tool: "conversation_send"}, relayRankOther, false},
		{relayMsg{Tool: spawnNoticeTool}, relayRankOther, false},
		{relayMsg{Tool: taskEndTool}, relayRankOther, false},
	} {
		if got := tc.msg.rank(); got != tc.rank || tc.msg.decisionWaiting() != tc.decision {
			t.Errorf("%s %q urgent=%v: rank %d decision %v, want %d %v", tc.msg.Tool, tc.msg.State, tc.msg.Urgent, got,
				tc.msg.decisionWaiting(), tc.rank, tc.decision)
		}
	}
	if !anyUrgent([]superviseSignal{{Kind: sigReview}, {Kind: sigNeedsYou}}) || anyUrgent([]superviseSignal{{Kind: sigReview}, {Kind: sigStalled}}) {
		t.Fatal("a supervision message is urgent exactly when one of its signals is")
	}
}

// The queue goes out by rank, the one that queued first within a rank, whatever order the messages came in.
func TestQueuedRelaysGoOutByRank(t *testing.T) {
	s, f := newQuietService(t)
	busy := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
	f.setSessions("server", busy)
	at := time.Now().UTC()
	for _, m := range []relayMsg{
		{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "other-1"},
		{From: "t1", Tool: noticeTool, State: "done", Text: "report-1"},
		{From: superviseTool, Tool: superviseTool, Text: "supervise-plain"},
		{From: "t2", Tool: noticeTool, State: "done", Text: "report-2"},
		{From: "t3", Tool: noticeTool, State: "failed", Text: "decision-notice"},
		{From: "t4", Tool: askAnswerTool, Text: "answer-1"},
		{From: superviseTool, Tool: superviseTool, Urgent: true, Text: "never queued twice"},
	} {
		if m.Tool == superviseTool && m.Urgent {
			continue // one supervision message per conversation waits (replaceQueuedSupervise)
		}
		m.At = at
		if s.queueRelay("conv1", m) == 0 {
			t.Fatal("not queued")
		}
	}
	idle := busy
	idle.Status = proto.StatusIdle
	want := []string{"answer-1", "decision-notice", "report-1", "report-2", "other-1", "supervise-plain"}
	for i := range want {
		s.drainRelays("server", []proto.Session{idle})
		waitFor(t, "the next queued message", func() bool { return f.sentTo("conv1") == i+1 })
	}
	for i, text := range f.notices() {
		if !strings.Contains(text, want[i]) {
			t.Fatalf("message %d is %q, want %q\nall: %q", i, text, want[i], f.notices())
		}
	}
	if n := s.queuedFor("conv1"); n != 0 {
		t.Fatalf("%d left queued", n)
	}
}

// Within ownerPresentFor of the owner's message nothing from the system goes into the conversation, idle as it is;
// when the hold ends it goes in on its own, with no change of the machine's list.
func TestSystemMessagesWaitWhileTheOwnerTalks(t *testing.T) {
	presenceTimes(t, 300*time.Millisecond, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "from another chat", At: time.Now().UTC()})
	if q, sent := s.queuedFor("conv1"), f.sentTo("conv1"); q != 1 || sent != 0 {
		t.Fatalf("while the owner talks: %d queued, %d sent", q, sent)
	}
	s.drainRelays("server", []proto.Session{{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}})
	time.Sleep(50 * time.Millisecond)
	if sent := f.sentTo("conv1"); sent != 0 {
		t.Fatalf("a change of the list delivered %d held message(s)", sent)
	}
	waitFor(t, "the held message once the owner is quiet", func() bool { return f.sentTo("conv1") == 1 && s.queuedFor("conv1") == 0 })
	// The owner is quiet: the next one goes straight in.
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "again", At: time.Now().UTC()})
	if sent := f.sentTo("conv1"); sent != 2 {
		t.Fatalf("after the hold: %d sent", sent)
	}
}

// A task's message that would have gone straight into the idle main conversation waits for the owner as well: it is
// queued, shown waiting, and goes in when the hold ends.
func TestTaskMessageToAnIdleConversationWaitsForTheOwner(t *testing.T) {
	presenceTimes(t, 400*time.Millisecond, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	f.judge = `{"verdict":"done","reason":"done"}`
	startedTask(t, s, f)
	s.UserSpoke(context.Background(), SessionRef{MachineID: "server", SessionID: "conv1"})
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "finished the work", 0)
	waitFor(t, "the message to queue", func() bool { return s.queuedFor("conv1") == 1 })
	if n := f.notices(); len(n) != 0 {
		t.Fatalf("sent while the owner talks: %q", n)
	}
	waitFor(t, "the message once the owner is quiet", func() bool { return len(f.notices()) == 1 && s.queuedFor("conv1") == 0 })
	if n := f.notices()[0]; !strings.Contains(n, "finished the work") {
		t.Fatalf("message:\n%s", n)
	}
}

// A conversation the owner did not speak in is not held: only the one they talk in.
func TestPresenceHoldsOnlyTheConversationSpokenIn(t *testing.T) {
	presenceTimes(t, time.Hour, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "lead1"})
	msg := relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "hello", At: time.Now().UTC()}
	s.relayOrQueue(ctx, "conv1", msg)
	s.relayOrQueue(ctx, "lead1", msg)
	if a, b := f.sentTo("conv1"), f.sentTo("lead1"); a != 1 || b != 0 {
		t.Fatalf("sent %d to the main conversation and %d to the leader the owner talks in", a, b)
	}
}

// What waits on a decision is held relayDecisionMax at most, counted from when it queued, while the owner keeps
// talking; the rest keeps waiting.
func TestDecisionWaitingMessagesAreHeldAtMost(t *testing.T) {
	presenceTimes(t, 2*time.Minute, 5*time.Minute)
	b := &relayBook{st: relayState{Queue: map[string][]relayMsg{}}}
	now := time.Now()
	b.ownerSpokeLocked("conv1", now.Add(-30*time.Second))
	for _, tc := range []struct {
		name string
		msg  relayMsg
		want time.Duration
	}{
		{"an ordinary message waits out the owner", relayMsg{Tool: "conversation_send", At: now.Add(-10 * time.Minute)}, 90 * time.Second},
		{"a fresh decision waits as long", relayMsg{Tool: superviseTool, Urgent: true, At: now}, 90 * time.Second},
		{"a decision that waited 4 minutes has one more", relayMsg{Tool: superviseTool, Urgent: true, At: now.Add(-4 * time.Minute)}, time.Minute},
		{"a decision that waited 5 minutes goes", relayMsg{Tool: askAnswerTool, At: now.Add(-5 * time.Minute)}, 0},
		{"a decision that does not say when it queued goes", relayMsg{Tool: askAnswerTool}, 0},
	} {
		if got := b.holdLocked("conv1", tc.msg, now); got != tc.want {
			t.Errorf("%s: held %s, want %s", tc.name, got, tc.want)
		}
	}
	if got := b.holdLocked("other", relayMsg{Tool: "conversation_send", At: now}, now); got != 0 {
		t.Fatalf("a conversation the owner did not speak in is held %s", got)
	}
	// The pick skips what is held and says how long until the first may go.
	b.st.Queue["conv1"] = []relayMsg{
		{Tool: "conversation_send", Text: "other", At: now.Add(-10 * time.Minute)},
		{Tool: superviseTool, Urgent: true, Text: "soon", At: now.Add(-4 * time.Minute)},
	}
	if i, wait := b.pickLocked("conv1", now); i != -1 || wait != time.Minute {
		t.Fatalf("pick %d, wait %s", i, wait)
	}
	if i, wait := b.pickLocked("conv1", now.Add(time.Minute)); i != 1 || wait != 0 {
		t.Fatalf("after the decision's hold: pick %d, wait %s", i, wait)
	}
	b.frontLocked("conv1", 1)
	if q := b.st.Queue["conv1"]; q[0].Text != "soon" || q[1].Text != "other" {
		t.Fatalf("the picked message is not at the head: %+v", q)
	}
}

// End to end: the owner keeps talking, a decision that has waited its time goes in and the ordinary message does not.
func TestDecisionGoesInWhileTheOwnerKeepsTalking(t *testing.T) {
	presenceTimes(t, time.Hour, 150*time.Millisecond)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "ordinary", At: time.Now().UTC()})
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "t1", Tool: askAnswerTool, Text: "the late answer", At: time.Now().UTC()})
	if q, sent := s.queuedFor("conv1"), f.sentTo("conv1"); q != 2 || sent != 0 {
		t.Fatalf("at first: %d queued, %d sent", q, sent)
	}
	waitFor(t, "the decision after its longest hold", func() bool { return f.sentTo("conv1") == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := f.notices(); len(n) != 1 || !strings.Contains(n[0], "the late answer") || s.queuedFor("conv1") != 1 {
		t.Fatalf("sent %q, %d still queued", n, s.queuedFor("conv1"))
	}
}

// A supervision message or task notice that takes a queued one's place has waited since the first queued; one that
// only now waits on a decision counts from now.
func TestReplacedQueuedMessageKeepsItsWait(t *testing.T) {
	s, _ := newQuietService(t)
	first := time.Now().Add(-4 * time.Minute).UTC()
	now := time.Now().UTC()
	s.queueRelay("conv1", relayMsg{From: superviseTool, Tool: superviseTool, Urgent: true, Text: "one", At: first})
	s.queueRelay("conv1", relayMsg{From: "t1", Tool: noticeTool, State: "needs", Text: "needs", At: first})
	s.queueRelay("conv1", relayMsg{From: "t2", Tool: noticeTool, State: "done", Text: "done", At: first})
	if !s.replaceQueuedSupervise("conv1", relayMsg{From: superviseTool, Tool: superviseTool, Urgent: true, Text: "both", At: now}) {
		t.Fatal("not replaced")
	}
	if !s.replaceQueuedNotice("conv1", relayMsg{From: "t1", Tool: noticeTool, State: "failed", Text: "failed", At: now}) {
		t.Fatal("notice not replaced")
	}
	if !s.replaceQueuedNotice("conv1", relayMsg{From: "t2", Tool: noticeTool, State: "needs", Text: "now needs", At: now}) {
		t.Fatal("second notice not replaced")
	}
	q := s.relays.st.Queue["conv1"]
	if len(q) != 3 || q[0].Text != "both" || !q[0].Urgent || !q[0].At.Equal(first) || q[1].Text != "failed" || !q[1].At.Equal(first) {
		t.Fatalf("queue: %+v", q)
	}
	if q[2].Text != "now needs" || !q[2].At.Equal(now) {
		t.Fatalf("a message that only now waits on a decision counts from %s, want %s", q[2].At, now)
	}
}

// A manager that restarts with messages held for the owner has no timer for them: the supervision tick hands them on
// once the hold ended, and not before.
func TestHeldRelaysSurviveARestart(t *testing.T) {
	presenceTimes(t, time.Hour, time.Hour)
	st := &memStore{}
	s, f := newQuietServiceOn(t, st)
	stopHolds(t, s)
	ctx := context.Background()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "held", At: time.Now().UTC()})
	if s.queuedFor("conv1") != 1 {
		t.Fatal("not held")
	}
	restarted := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)}))
	setSettings(t, restarted, func(v *Settings) { v.Main = "conv1" })
	stopHolds(t, restarted)
	if restarted.queuedFor("conv1") != 1 || restarted.relays.st.Spoke["conv1"].IsZero() {
		t.Fatalf("restored: %d queued, spoke %v", restarted.queuedFor("conv1"), restarted.relays.st.Spoke)
	}
	restarted.drainHeldRelays(ctx, time.Now())
	time.Sleep(50 * time.Millisecond)
	if sent := f.sentTo("conv1"); sent != 0 {
		t.Fatalf("%d sent while the owner still counts as present", sent)
	}
	ownerPresentFor = 0 // the hold is over
	restarted.drainHeldRelays(ctx, time.Now())
	waitFor(t, "the held message after the restart", func() bool { return f.sentTo("conv1") == 1 && restarted.queuedFor("conv1") == 0 })
	restarted.relays.mu.Lock()
	_, kept := restarted.relays.st.Spoke["conv1"]
	restarted.relays.mu.Unlock()
	if kept {
		t.Fatal("the owner's last message is kept after its hold ended and its queue was handed on")
	}
}

// The stored shape: what the last version wrote reads as before, a message without the new fields writes none of
// them, and the last version (which does not know them) reads what this one writes.
func TestRelayStateReadsAcrossVersions(t *testing.T) {
	old := `{"queue":{"conv1":[{"from":"c2","from_title":"Research","tool":"conversation_send","text":"hi","hop":1,"at":"2026-11-01T00:00:00Z"}]},"hops":{"conv1":{"hop":1,"at":"2026-11-01T00:00:00Z"}}}`
	var rs relayState
	if err := json.Unmarshal([]byte(old), &rs); err != nil {
		t.Fatal(err)
	}
	if q := rs.Queue["conv1"]; len(q) != 1 || q[0].Text != "hi" || q[0].Urgent || rs.Spoke != nil {
		t.Fatalf("read: %+v", rs)
	}
	out, err := json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "urgent") || strings.Contains(string(out), "spoke") {
		t.Fatalf("new fields written for a queue that has none: %s", out)
	}
	rs.Queue["conv1"][0].Urgent = true
	rs.Spoke = map[string]time.Time{"conv1": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	out, err = json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	// The last version's shape: no urgent, no spoke; unknown fields are skipped.
	var prev struct {
		Queue map[string][]struct {
			From, Tool, Text string
			At               time.Time
		} `json:"queue"`
		Hops map[string]relayHop `json:"hops"`
	}
	if err := json.Unmarshal(out, &prev); err != nil {
		t.Fatal(err)
	}
	if q := prev.Queue["conv1"]; len(q) != 1 || q[0].Text != "hi" || q[0].Tool != "conversation_send" || prev.Hops["conv1"].Hop != 1 {
		t.Fatalf("the last version reads: %+v", prev)
	}
}
