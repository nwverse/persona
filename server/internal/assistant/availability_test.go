package assistant

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// availEvents are the details of the availability events of kind the service recorded, oldest first.
func availEvents(s *Service, kind string) []map[string]any {
	var out []map[string]any
	for _, e := range testEvents(s) {
		if e.Kind == kind {
			out = append(out, e.Detail)
		}
	}
	return out
}

// The owner's message to an idle conversation: the wait runs to the first change of the conversation's last text,
// and the round it started is the owner's. A supervision round after it is a background round; a message that comes
// during it is counted as having come on a background round, and is answered by the text that follows once nothing
// of the owner's is queued any more.
func TestAvailabilityReplyWaitAndRounds(t *testing.T) {
	ft := newFleetTest(t)
	ft.f.sessions["mac"], ft.f.known["conv"] = nil, true // the fake hub answers the sends to it
	s := ft.s
	m, _ := s.fleetMachine("mac")
	conv := proto.Session{SessionID: "conv", Agent: "claude", Cwd: m.assistantCwd(), Status: proto.StatusIdle, LastText: "earlier"}
	ref := SessionRef{MachineID: "mac", SessionID: "conv"}
	list := func(status, text string, queued ...string) {
		c := conv
		c.Status, c.LastText, c.Queued = status, text, queued
		ft.sessions("mac", c)
	}
	list(proto.StatusIdle, "earlier")

	s.UserSpoke(t.Context(), ref)
	list(proto.StatusWorking, "earlier")
	if got := availEvents(s, replyWaitEvent); len(got) != 0 {
		t.Fatalf("a reply before any text: %v", got)
	}
	list(proto.StatusWorking, "Looking.")
	waits := availEvents(s, replyWaitEvent)
	if len(waits) != 1 || waits[0]["state"] != waitIdle || waits[0]["unanswered"] != nil {
		t.Fatalf("reply waits %v", waits)
	}
	list(proto.StatusWorking, "Done.") // more text of the same reply: not another wait
	list(proto.StatusDone, "Done.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 1 })
	if r := availEvents(s, roundEvent)[0]; r["source"] != roundOwner {
		t.Fatalf("round %v", r)
	}
	if got := availEvents(s, replyWaitEvent); len(got) != 1 {
		t.Fatalf("the round's end counted the answered message again: %v", got)
	}

	// A supervision message starts the next round; the owner writes while it runs.
	if err := s.deliverRelay(t.Context(), "mac", "conv", relayMsg{Tool: superviseTool, Text: "Synthetic supervision."}); err != nil {
		t.Fatal(err)
	}
	list(proto.StatusWorking, "Done.")
	waitUntil(t, func() bool {
		s.avail.mu.Lock()
		defer s.avail.mu.Unlock()
		return s.avail.rounds[[2]string{"mac", "conv"}].source == proto.SourceSupervision
	})
	s.UserSpoke(t.Context(), ref)
	list(proto.StatusWorking, "Checking the PR.", "my question") // the round's own text: the owner's message still waits
	if got := availEvents(s, replyWaitEvent); len(got) != 1 {
		t.Fatalf("the background round's text counted as the reply: %v", got)
	}
	list(proto.StatusWorking, "Here is the answer.")
	waits = availEvents(s, replyWaitEvent)
	if len(waits) != 2 || waits[1]["state"] != waitBackground {
		t.Fatalf("reply waits %v", waits)
	}
	list(proto.StatusDone, "Here is the answer.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 2 })
	if r := availEvents(s, roundEvent)[1]; r["source"] != proto.SourceSupervision {
		t.Fatalf("round %v", r)
	}
}

// A reply whose text the session list never showed changing (the same words as before) is counted when its round
// ends; a message nobody answers is recorded unanswered at the bound instead of waiting for ever.
func TestAvailabilityReplyWaitFallbacks(t *testing.T) {
	ft := newFleetTest(t)
	s := ft.s
	m, _ := s.fleetMachine("mac")
	conv := proto.Session{SessionID: "conv", Agent: "claude", Cwd: m.assistantCwd(), Status: proto.StatusIdle, LastText: "OK"}
	ref := SessionRef{MachineID: "mac", SessionID: "conv"}
	ft.sessions("mac", conv)
	s.UserSpoke(t.Context(), ref)
	conv.Status = proto.StatusWorking
	ft.sessions("mac", conv)
	conv.Status = proto.StatusDone
	ft.sessions("mac", conv)
	waitUntil(t, func() bool { return len(availEvents(s, replyWaitEvent)) == 1 })
	if w := availEvents(s, replyWaitEvent)[0]; w["by"] != "turn_end" || w["state"] != waitIdle {
		t.Fatalf("reply wait %v", w)
	}

	old := replyWaitMax
	replyWaitMax = 0
	t.Cleanup(func() { replyWaitMax = old })
	s.UserSpoke(t.Context(), ref)
	time.Sleep(time.Millisecond)
	ft.sessions("mac", conv) // never started: the conversation's node did not take it
	waits := availEvents(s, replyWaitEvent)
	if len(waits) != 2 || waits[1]["unanswered"] != true {
		t.Fatalf("reply waits %v", waits)
	}
	if n := s.avail.waiting.Load(); n != 0 {
		t.Fatalf("%d waits left", n)
	}
}

// A queued message records, once delivered, how long it waited and how long the queue was when it was added; one
// that takes a queued message's place has waited since the first.
func TestAvailabilityQueueWait(t *testing.T) {
	f := newFakeHub().withMachine()
	f.known["receiver"] = true
	s := newTestService(t, f)
	first := relayMsg{From: "c1", FromTitle: "Research", Tool: "conversation_send", Text: "Read the result.", Hop: 1, At: time.Now().UTC()}
	if n := s.queueRelay("receiver", first); n != 1 {
		t.Fatalf("queued %d", n)
	}
	if n := s.queueRelay("receiver", relayMsg{Tool: superviseTool, Text: "Synthetic supervision."}); n != 2 {
		t.Fatalf("queued %d", n)
	}
	queued := s.relays.st.Queue["receiver"][1]
	if queued.Depth != 2 || queued.QueuedAt.IsZero() {
		t.Fatalf("queued %+v", queued)
	}
	if !s.replaceQueuedSupervise("receiver", relayMsg{Tool: superviseTool, Text: "A later one."}) {
		t.Fatal("not replaced")
	}
	if got := s.relays.st.Queue["receiver"][1]; got.Text != "A later one." || !got.QueuedAt.Equal(queued.QueuedAt) || got.Depth != 2 {
		t.Fatalf("the replacement lost when the first was queued: %+v", got)
	}
	now := s.availabilityQueued(t.Context(), time.Now())
	if len(now) != 1 || now[0].Conversation != "receiver" || now[0].Depth != 2 {
		t.Fatalf("queued now %+v", now)
	}

	idle := []proto.Session{{SessionID: "receiver", Status: proto.StatusIdle}}
	s.drainRelays("server", idle)
	waitUntil(t, func() bool { return len(availEvents(s, queueWaitEvent)) == 1 })
	if e := availEvents(s, queueWaitEvent)[0]; e["kind"] != proto.SourceConversation || e["depth"] != 1 {
		t.Fatalf("queue wait %v", e)
	}
	s.drainRelays("server", idle)
	waitUntil(t, func() bool { return len(availEvents(s, queueWaitEvent)) == 2 })
	if e := availEvents(s, queueWaitEvent)[1]; e["kind"] != proto.SourceSupervision || e["depth"] != 2 {
		t.Fatalf("queue wait %v", e)
	}
}

// A card's end records who ended it; the read tells the assistant's answers apart by the events that already say
// whose they were, and counts a card whose answer was changed later once.
func TestAvailabilityAskOutcome(t *testing.T) {
	s := newStorelessService(t, newFakeHub().withMachine())
	asked := time.Now().Add(-time.Minute)
	ix := func(id, kind, tool string) proto.Interaction {
		return proto.Interaction{ID: id, MachineID: "mac", SessionID: "s1", Kind: kind, ToolName: tool, CreatedAt: asked}
	}
	now := time.Now()
	s.availAskEnded(InteractionResolved{Interaction: ix("q1", proto.IxQuestion, ""), Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("q1", proto.IxQuestion, ""), Status: "resolved"}, now) // re-answered
	by := proto.Resolution{AnsweredBy: proto.AnsweredByAssistant}
	s.availAskEnded(InteractionResolved{Interaction: ix("q2", proto.IxQuestion, ""), Resolution: by, Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("q3", proto.IxQuestion, ""), Resolution: by, Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("p1", proto.IxPermission, ""), Resolution: by, Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("p2", proto.IxPermission, ""), Resolution: proto.Resolution{Remembered: true}, Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("a1", proto.IxQuestion, "ask_user"), Status: "dismissed"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("f1", proto.IxQuestion, ""), Status: "failed"}, now) // still open
	s.delegateMark(ix("q2", proto.IxQuestion, ""), "delegate_start")
	s.workEvent("mac", "s1", "standing_authorized", map[string]any{"interaction": "p1"})

	asks := []taskAsk{{kind: "decision", who: askWhoDefault, after: 86400}, {kind: "decision", who: askByOwner, after: 60}}
	got := summarizeAvailability(1, testEvents(s), time.UTC, asks, nil).AskOutcome
	want := map[string]map[string]int{
		proto.IxQuestion:   {askByOwner: 1, askWhoDelegate: 1, askWhoSupervisor: 1},
		proto.IxPermission: {askWhoStanding: 1, askByRemembered: 1},
		"ask_user":         {askWhoDismissed: 1},
		"decision":         {askWhoDefault: 1, askByOwner: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("ask outcome %+v", got)
	}
	for kind, who := range want {
		if len(got[kind]) != len(who) {
			t.Fatalf("%s: %+v", kind, got[kind])
		}
		for w, n := range who {
			if got[kind][w].N != n {
				t.Fatalf("%s by %s: %+v, want %d", kind, w, got[kind][w], n)
			}
		}
	}
	if st := got[proto.IxQuestion][askByOwner]; st.Median < 59 || st.Median > 61 {
		t.Fatalf("answered after %+v, want about a minute", st)
	}
}

// The read: waits by what the conversation was doing, the main conversation's per day, round time by what started
// the round with the share the owner did not start, and queue waits by kind.
func TestSummarizeAvailability(t *testing.T) {
	day := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	ev := func(at time.Time, session, kind string, detail map[string]any) db.WorkEvent {
		b, _ := json.Marshal(detail) // as the store returns them
		var d map[string]any
		_ = json.Unmarshal(b, &d)
		return db.WorkEvent{At: at, MachineID: "server", SessionID: session, Kind: kind, Detail: d}
	}
	events := []db.WorkEvent{
		ev(day, "main", replyWaitEvent, map[string]any{"state": waitIdle, "wait": 2.0, "main": true}),
		ev(day, "main", replyWaitEvent, map[string]any{"state": waitIdle, "wait": 4.0, "main": true}),
		ev(day, "main", replyWaitEvent, map[string]any{"state": waitBackground, "wait": 90.0, "main": true}),
		ev(day.Add(24*time.Hour), "main", replyWaitEvent, map[string]any{"state": waitIdle, "wait": 1800.0, "main": true, "unanswered": true}),
		ev(day, "side", replyWaitEvent, map[string]any{"state": waitOwnerRound, "wait": 7.0, "main": false}),
		ev(day, "main", roundEvent, map[string]any{"source": roundOwner, "seconds": 30.0, "main": true}),
		ev(day, "main", roundEvent, map[string]any{"source": proto.SourceSupervision, "seconds": 60.0, "main": true}),
		ev(day, "main", roundEvent, map[string]any{"source": proto.SourceSupervision, "seconds": 30.0, "main": true}),
		ev(day, "side", roundEvent, map[string]any{"source": roundOwner, "seconds": 5.0, "main": false}),
		ev(day, "main", queueWaitEvent, map[string]any{"kind": proto.SourceSupervision, "wait": 600.0, "depth": 3, "main": true}),
		ev(day, "main", queueWaitEvent, map[string]any{"kind": roundNotice, "wait": 20.0, "depth": 1, "main": true}),
	}
	got := summarizeAvailability(48, events, time.UTC, nil, []queuedNow{{Conversation: "main", Main: true, Depth: 1, OldestSeconds: 5}})
	if w := got.ReplyWait.Main[waitIdle]; w.N != 3 || w.Median != 4 || w.P90 != 1800 || w.Unanswered != 1 {
		t.Fatalf("main idle %+v", w)
	}
	if w := got.ReplyWait.Main[waitBackground]; w.N != 1 || w.P90 != 90 {
		t.Fatalf("main background %+v", w)
	}
	if _, ok := got.ReplyWait.Main[waitOwnerRound]; ok || got.ReplyWait.All[waitOwnerRound].N != 1 {
		t.Fatalf("another conversation's wait: %+v", got.ReplyWait)
	}
	if d := got.ReplyWait.MainDays; len(d) != 2 || d[0].Day != "2026-03-03" || d[0].Unanswered != 1 || d[1].N != 3 || d[1].P90 != 90 {
		t.Fatalf("main days %+v", d)
	}
	if len(got.BackgroundShare) != 2 {
		t.Fatalf("background share %+v", got.BackgroundShare)
	}
	main, side := got.BackgroundShare[0], got.BackgroundShare[1]
	if main.Conversation != "main" || !main.Main || main.Seconds[proto.SourceSupervision] != 90 || main.Rounds[proto.SourceSupervision] != 2 ||
		main.Rounds[roundOwner] != 1 || main.BackgroundShare != 0.75 {
		t.Fatalf("main rounds %+v", main)
	}
	if side.Conversation != "side" || side.Main || side.BackgroundShare != 0 {
		t.Fatalf("side rounds %+v", side)
	}
	if q := got.QueueWait.ByKind[proto.SourceSupervision]; q.N != 1 || q.P90 != 600 || q.MaxDepth != 3 {
		t.Fatalf("queue wait %+v", q)
	}
	if len(got.QueueWait.QueuedNow) != 1 || got.QueueWait.ByKind[roundNotice].MaxDepth != 1 {
		t.Fatalf("queue %+v", got.QueueWait)
	}
	empty, err := json.Marshal(summarizeAvailability(24, nil, time.UTC, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"hours":24,"reply_wait":{"all":{},"main":{},"main_days":[]},"background_share":[],"queue_wait":{"by_kind":{},"queued_now":[]},"ask_outcome":{}}`; string(empty) != want {
		t.Fatalf("no events:\n%s\nwant\n%s", empty, want)
	}
}

// The route is a full device's, reads a week unless asked otherwise, and says so when there is no database.
func TestAvailabilityRoute(t *testing.T) {
	f := newFakeHub().withMachine()
	st := &workMemStore{}
	s := newServiceOn(t, f, st)
	s.workEvent("server", "main", roundEvent, map[string]any{"source": roundOwner, "seconds": 12.5, "main": true})
	s.waitWorkEvents()
	if w := serveAs(s, roleAnswer, "GET", "/api/v1/assistant/availability", ""); w.Code != http.StatusForbidden {
		t.Fatalf("answer-only device: %d %s", w.Code, w.Body)
	}
	w := serve(s, "GET", "/api/v1/assistant/availability", "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var got availabilityStats
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Hours != 24*7 || len(got.BackgroundShare) != 1 || got.BackgroundShare[0].Seconds[roundOwner] != 12.5 {
		t.Fatalf("read %+v", got)
	}
	if w := serve(s, "GET", "/api/v1/assistant/availability?hours=24", ""); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	none := newTestService(t, newFakeHub().withMachine())
	if w := serve(none, "GET", "/api/v1/assistant/availability", ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no database: %d %s", w.Code, w.Body)
	}
}
