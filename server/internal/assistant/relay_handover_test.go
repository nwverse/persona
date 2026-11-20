package assistant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// handoverList is the hand-overs s keeps, oldest first.
func handoverList(s *Service) []handover {
	b := &s.handovers
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.loadLocked(context.Background(), s) {
		return nil
	}
	out := make([]handover, 0, len(b.m))
	for _, e := range b.m {
		out = append(out, e)
	}
	slices.SortFunc(out, func(x, y handover) int { return x.At.Compare(y.At) })
	return out
}

// handoverState is hand-over id's state ("" when s keeps none by that id).
func handoverState(s *Service, id string) string {
	for _, e := range handoverList(s) {
		if e.ID == id {
			return e.State
		}
	}
	return ""
}

// onlyHandover is the one hand-over s keeps.
func onlyHandover(t *testing.T, s *Service) handover {
	t.Helper()
	list := handoverList(s)
	if len(list) != 1 {
		t.Fatalf("%d hand-overs, want one: %+v", len(list), list)
	}
	return list[0]
}

// pendingHandovers counts the hand-overs of sends not accepted yet.
func pendingHandovers(s *Service) int {
	s.handovers.mu.Lock()
	defer s.handovers.mu.Unlock()
	return len(s.handovers.pending)
}

// setStatus shows conversation id with status in the harness's list, every other one idle.
func (d *dispatchPolicyHarness) setStatus(id, status string) {
	var list []proto.Session
	for _, c := range []string{d.mainID, d.leaderID, d.bgID, d.patrolID, d.sideID} {
		st := proto.StatusIdle
		if c == id {
			st = status
		}
		list = append(list, proto.Session{MachineID: "server", SessionID: c, Assistant: true, Status: st})
	}
	d.f.setSessions("server", list...)
}

// idle is conversation id as the harness lists it when it is idle.
func (d *dispatchPolicyHarness) idle(id string) proto.Session {
	return proto.Session{MachineID: "server", SessionID: id, Assistant: true, Status: proto.StatusIdle}
}

// send is conversation_send from → to with args (JSON members after id), which must be accepted; its result.
func (d *dispatchPolicyHarness) send(t *testing.T, from, to, args string) string {
	t.Helper()
	res := d.tool(t, from, "conversation_send", `{"id":"`+to+`",`+args+`}`)
	if res.IsError {
		t.Fatalf("conversation_send %s → %s: %s", from, to, res.Content[0].Text)
	}
	return res.Content[0].Text
}

// handoverLogs are the log lines about hand-over id.
func (d *dispatchPolicyHarness) handoverLogs(id string) []string {
	var out []string
	for _, line := range strings.Split(d.log(), "\n") {
		if strings.HasPrefix(line, "hand-over "+id+" ") {
			out = append(out, line)
		}
	}
	return out
}

// A quoted conversation_send with a direction's lead on either end is one hand-over: from the main conversation to the
// lead, and from the lead to the main conversation. A send without a quote, a quoted one between two conversations
// neither of which is a lead, and one refused at once are none.
func TestHandoverEntriesAreMadeForQuotedSendsWithALead(t *testing.T) {
	d := newDispatchPolicyHarness(t)
	d.f.setItems(ownerItem("m-owner", quotedWords))
	d.send(t, d.mainID, d.leaderID, `"text":"take this one: the export fix","quote":"latest"`)
	e := onlyHandover(t, d.s)
	if e.ID == "" || e.From != d.mainID || e.To != d.leaderID || e.ToTitle == "" || e.Conversation != d.mainID ||
		e.MessageID != "m-owner" || e.State != handoverDelivered || e.Code != "" || e.At.IsZero() || e.ChangedAt.Before(e.At) {
		t.Fatalf("main → lead: %+v", e)
	}
	if text, _ := d.f.lastSend(t)["text"].(string); strings.Contains(text, e.ID) {
		t.Fatalf("the receiver sees the hand-over's id:\n%s", text)
	}

	d.f.setItems(received(t, d.f, d.mainID)) // the lead passes the owner's words back with what it found
	d.send(t, d.leaderID, d.mainID, `"text":"done: the export keeps the old format","quote":"latest"`)
	list := handoverList(d.s)
	if len(list) != 2 {
		t.Fatalf("lead → main: %d hand-overs, want two: %+v", len(list), list)
	}
	back := list[1]
	if back.From != d.leaderID || back.To != d.mainID || back.Conversation != d.mainID || back.MessageID != "m-owner" || back.State != handoverDelivered {
		t.Fatalf("lead → main: %+v", back)
	}

	d.f.setItems(ownerItem("m-owner", quotedWords))
	d.send(t, d.mainID, d.leaderID, `"text":"and the docs, please"`)                // no quote
	d.send(t, d.mainID, d.sideID, `"text":"look into the export","quote":"latest"`) // no lead on either end
	refuseSendsTo(d.f, d.leaderID, &rpc.Error{Code: "unheard_of", Message: "this conversation will not take it"})
	if res := d.tool(t, d.mainID, "conversation_send", `{"id":"`+d.leaderID+`","text":"one more","quote":"latest"}`); !res.IsError {
		t.Fatalf("a refused send went: %s", res.Content[0].Text)
	}
	if n := len(handoverList(d.s)); n != 2 || pendingHandovers(d.s) != 0 {
		t.Fatalf("%d hand-overs and %d pending after sends that are none", n, pendingHandovers(d.s))
	}
}

// A hand-over goes queued → delivered → answered as its message waits, goes in, and the lead's first message back goes
// into the asking conversation: sent at once (never queued), from the queue, and a try in doubt that is found in the
// transcript later (queued until then). Each change is one log line.
func TestHandoverGoesQueuedDeliveredAnswered(t *testing.T) {
	for _, way := range []string{"sent at once", "from the queue", "in doubt, found later"} {
		t.Run(way, func(t *testing.T) {
			d := newDispatchPolicyHarness(t)
			stopHolds(t, d.s)
			d.f.setItems(ownerItem("m-owner", quotedWords))
			var tried string // the text of the try in doubt
			switch way {
			case "from the queue":
				d.setStatus(d.leaderID, proto.StatusWorking)
			case "in doubt, found later":
				d.f.mu.Lock()
				d.f.onAssistantSend = func(f *fakeHub, p map[string]any) {
					if p["session_id"] == d.leaderID {
						tried, _ = p["text"].(string)
					}
				}
				d.f.mu.Unlock()
				refuseSendsTo(d.f, d.leaderID, context.DeadlineExceeded) // written, then no answer in time
			}
			out := d.send(t, d.mainID, d.leaderID, `"text":"take this one: the export fix","quote":"latest"`)
			e := onlyHandover(t, d.s)
			want := []string{"delivered"}
			switch way {
			case "sent at once":
				if e.State != handoverDelivered {
					t.Fatalf("sent at once: %s (%s)", e.State, out)
				}
			case "from the queue":
				if e.State != handoverQueued || d.s.queuedFor(d.leaderID) != 1 {
					t.Fatalf("while the lead works: %s, %d queued (%s)", e.State, d.s.queuedFor(d.leaderID), out)
				}
				d.setStatus("", "")
				drainRelayTest(t, d.s, d.idle(d.leaderID))
				want = []string{"queued", "delivered"}
			case "in doubt, found later":
				if e.State != handoverQueued || !strings.Contains(out, "queued") {
					t.Fatalf("a try in doubt: %s (%s)", e.State, out)
				}
				drainRelayTest(t, d.s, d.idle(d.leaderID)) // not in the transcript yet: still in doubt
				if s := handoverState(d.s, e.ID); s != handoverQueued || d.s.queuedFor(d.leaderID) != 1 {
					t.Fatalf("a try in doubt not found: %s, %d queued", s, d.s.queuedFor(d.leaderID))
				}
				d.f.mu.Lock()
				d.f.items = append(d.f.items, proto.Item{Seq: len(d.f.items) + 1, Role: "user", Kind: proto.KindText, Text: tried, TS: time.Now()})
				d.f.mu.Unlock()
				advanceRelayLook(t, d.s, d.leaderID, 1)
				drainRelayTest(t, d.s, d.idle(d.leaderID))
				want = []string{"queued", "delivered"}
			}
			if s := handoverState(d.s, e.ID); s != handoverDelivered || d.s.queuedFor(d.leaderID) != 0 {
				t.Fatalf("gone in: %s, %d queued", s, d.s.queuedFor(d.leaderID))
			}

			d.send(t, d.leaderID, d.mainID, `"text":"on it; the fix lands today"`)
			if s := handoverState(d.s, e.ID); s != handoverAnswered {
				t.Fatalf("after the lead's answer went in: %s", s)
			}
			want = append(want, "answered")
			logs := d.handoverLogs(e.ID)
			if len(logs) != len(want) {
				t.Fatalf("log lines %q, want one per change %q", logs, want)
			}
			for i, w := range want {
				if !strings.HasSuffix(logs[i], ": "+w) {
					t.Errorf("log line %d is %q, want the change to %s", i, logs[i], w)
				}
			}
		})
	}
}

// The lead's answer is matched on the entries, not on the relay's hop record: it marks its hand-over answered more than
// an hour later, after the owner spoke in the lead's conversation, after a third conversation's request reached the
// lead, and when the answer itself carries the owner's words.
func TestHandoverAnswerIsMatchedOnTheEntries(t *testing.T) {
	for _, after := range []string{"more than an hour", "the owner spoke to the lead", "a third conversation's request", "a quote in the answer"} {
		t.Run(after, func(t *testing.T) {
			d := newDispatchPolicyHarness(t)
			stopHolds(t, d.s)
			d.f.setItems(ownerItem("m-owner", quotedWords))
			d.send(t, d.mainID, d.leaderID, `"text":"take this one: the export fix","quote":"latest"`)
			e := onlyHandover(t, d.s)
			args := `"text":"on it; the fix lands today"`
			switch after {
			case "more than an hour":
				b := &d.s.relays
				b.mu.Lock()
				hp := b.st.Hops[d.leaderID]
				hp.At = hp.At.Add(-relayHopMemory - time.Minute)
				b.st.Hops[d.leaderID] = hp
				b.mu.Unlock()
			case "the owner spoke to the lead":
				d.s.UserSpoke(context.Background(), SessionRef{MachineID: "server", SessionID: d.leaderID})
			case "a third conversation's request":
				d.send(t, d.sideID, d.leaderID, `"text":"how far is the export fix?"`)
			case "a quote in the answer":
				d.f.setItems(received(t, d.f, d.mainID))
				args += `,"quote":"latest"`
			}
			d.send(t, d.leaderID, d.mainID, args)
			if s := handoverState(d.s, e.ID); s != handoverAnswered {
				t.Fatalf("after %s: %s", after, s)
			}
		})
	}
}

// Two hand-overs to one lead are answered oldest first, each by one answer. An answer that waits in the asking
// conversation's queue answers nothing until it goes in, and a later answer meanwhile takes the next hand-over.
func TestTwoHandoversAreAnsweredOldestFirst(t *testing.T) {
	d := newDispatchPolicyHarness(t)
	stopHolds(t, d.s)
	d.f.setItems(ownerItem("m-owner", quotedWords), ownerItem("m-owner-2", "and the import as well"))
	d.send(t, d.mainID, d.leaderID, `"text":"take this one: the export fix","quote":"m-owner"`)
	d.send(t, d.mainID, d.leaderID, `"text":"and the import","quote":"m-owner-2"`)
	list := handoverList(d.s)
	if len(list) != 2 || list[0].MessageID != "m-owner" || list[1].MessageID != "m-owner-2" {
		t.Fatalf("hand-overs %+v", list)
	}
	d.setStatus(d.mainID, proto.StatusWorking)
	d.send(t, d.leaderID, d.mainID, `"text":"the export fix is in"`)
	d.send(t, d.leaderID, d.mainID, `"text":"the import fix is in"`)
	q := queuedRelays(d.s, d.mainID)
	if len(q) != 2 || q[0].Answers != list[0].ID || q[1].Answers != list[1].ID {
		t.Fatalf("queued answers %+v, want the first for %s and the second for %s", q, list[0].ID, list[1].ID)
	}
	for _, e := range list {
		if s := handoverState(d.s, e.ID); s != handoverDelivered {
			t.Fatalf("answered before the answer went in: %s %s", e.ID, s)
		}
	}
	d.setStatus("", "")
	drainRelayTest(t, d.s, d.idle(d.mainID))
	if a, b := handoverState(d.s, list[0].ID), handoverState(d.s, list[1].ID); a != handoverAnswered || b != handoverDelivered {
		t.Fatalf("after the first answer went in: %s, %s", a, b)
	}
	drainRelayTest(t, d.s, d.idle(d.mainID))
	if b := handoverState(d.s, list[1].ID); b != handoverAnswered {
		t.Fatalf("after the second answer went in: %s", b)
	}
}

// An answer that is refused, at once or for good from the queue, leaves its hand-over delivered; the next answer that
// goes in answers it.
func TestARefusedAnswerLeavesItsHandoverDelivered(t *testing.T) {
	for _, way := range []string{"at once", "from the queue"} {
		t.Run(way, func(t *testing.T) {
			d := newDispatchPolicyHarness(t)
			stopHolds(t, d.s)
			d.f.setItems(ownerItem("m-owner", quotedWords))
			d.send(t, d.mainID, d.leaderID, `"text":"take this one: the export fix","quote":"latest"`)
			e := onlyHandover(t, d.s)
			refusal := &rpc.Error{Code: "unheard_of", Message: "this conversation will not take it"}
			if way == "at once" {
				refuseSendsTo(d.f, d.mainID, refusal)
				if res := d.tool(t, d.leaderID, "conversation_send", `{"id":"`+d.mainID+`","text":"on it"}`); !res.IsError {
					t.Fatalf("a refused answer went: %s", res.Content[0].Text)
				}
			} else {
				d.setStatus(d.mainID, proto.StatusWorking)
				d.send(t, d.leaderID, d.mainID, `"text":"on it"`)
				if q := queuedRelays(d.s, d.mainID); len(q) != 1 || q[0].Answers != e.ID {
					t.Fatalf("queued %+v", q)
				}
				d.setStatus("", "")
				refuseSendsTo(d.f, d.mainID, refusal)
				drainSettled(t, d.s, d.idle(d.mainID))
				if n := d.s.queuedFor(d.mainID); n != 0 {
					t.Fatalf("a refused answer is still queued (%d)", n)
				}
			}
			if s := handoverState(d.s, e.ID); s != handoverDelivered {
				t.Fatalf("after a refused answer: %s", s)
			}
			refuseSendsTo(d.f, d.mainID, nil)
			d.send(t, d.leaderID, d.mainID, `"text":"on it, second try"`)
			if s := handoverState(d.s, e.ID); s != handoverAnswered {
				t.Fatalf("after the next answer went in: %s", s)
			}
		})
	}
}

// A queued hand-over that is refused for good is not delivered, with the refusal's code, and its sender still gets the
// line saying so; one whose lead is gone is not delivered, with "gone".
func TestQueuedHandoverThatWillNotGoIsNotDelivered(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"refused for good", &rpc.Error{Code: "unheard_of", Message: "this conversation will not take it"}, "unheard_of"},
		{"gone", errors.New("conversation leader-dp-1 is gone"), handoverGone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDispatchPolicyHarness(t)
			stopHolds(t, d.s)
			d.f.setItems(ownerItem("m-owner", quotedWords))
			d.setStatus(d.leaderID, proto.StatusWorking)
			d.send(t, d.mainID, d.leaderID, `"text":"take this one: the export fix","quote":"latest"`)
			e := onlyHandover(t, d.s)
			if e.State != handoverQueued {
				t.Fatalf("queued: %+v", e)
			}
			d.setStatus("", "")
			refuseSendsTo(d.f, d.leaderID, tc.err)
			drainSettled(t, d.s, d.idle(d.leaderID))
			got := onlyHandover(t, d.s)
			if got.State != handoverNotDelivered || got.Code != tc.code || d.s.queuedFor(d.leaderID) != 0 {
				t.Fatalf("%s: %+v, %d queued", tc.name, got, d.s.queuedFor(d.leaderID))
			}
			if tc.name == "refused for good" {
				waitFor(t, "the line to the sender", func() bool { return d.f.sentTo(d.mainID) == 1 })
				if line := d.f.sentTexts(d.mainID)[0]; !strings.Contains(line, "unheard_of") || strings.Contains(line, e.ID) {
					t.Fatalf("the sender's line:\n%s", line)
				}
			}
			drainSettled(t, d.s, d.idle(d.leaderID))
			if logs := d.handoverLogs(e.ID); len(logs) != 2 || !strings.HasSuffix(logs[1], ": not_delivered ("+tc.code+")") {
				t.Fatalf("log lines %q", logs)
			}
		})
	}
}

// The hand-overs survive a manager restart. They are kept 14 days and at most 64, the oldest dropped first.
func TestHandoversAreKeptLikeTheQuotes(t *testing.T) {
	ctx := context.Background()
	store := &memStore{}
	s, _ := newQuietServiceOn(t, store)
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	quote := &BriefRequest{Conversation: "conv1", MessageID: "m1", Text: quotedWords}
	if id, done := s.handoverFor(ctx, "conv1", "conv2", "Research", quote); id != "" {
		done()
		t.Fatalf("a hand-over between two conversations neither of which is a lead: %s", id)
	}
	id, done := s.handoverFor(ctx, "conv1", "lead1", "Lead", quote)
	if !s.handoverSet(id, handoverQueued, "", nil) {
		t.Fatal("not queued")
	}
	done()
	if s.handoverSet(id, handoverAnswered, "", nil) || s.handoverSet(id, handoverQueued, "", nil) {
		t.Fatal("a queued hand-over was answered, or queued again")
	}

	restarted, _ := newQuietServiceOn(t, store)
	if e := onlyHandover(t, restarted); e.ID != id || e.State != handoverQueued || e.To != "lead1" || e.ToTitle != "Lead" || e.MessageID != "m1" {
		t.Fatalf("after a restart: %+v", e)
	}
	if !restarted.handoverSet(id, handoverDelivered, "", nil) {
		t.Fatal("not delivered after a restart")
	}

	// What is kept once one more hand-over is made, from the hand-overs ages (each so long ago) a restart reads.
	keptAfterOneMore := func(ages []time.Duration) []string {
		t.Helper()
		now := time.Now().UTC()
		m := map[string]handover{}
		for i, age := range ages {
			hid := fmt.Sprintf("handover_%02d", i)
			m[hid] = handover{ID: hid, From: "conv1", To: "lead1", State: handoverDelivered, At: now.Add(-age), ChangedAt: now.Add(-age)}
		}
		if err := store.PutSetting(ctx, handoversKey, m); err != nil {
			t.Fatal(err)
		}
		again, _ := newQuietServiceOn(t, store)
		setSettings(t, again, func(v *Settings) { v.Leaders = []string{"lead1"} })
		newest, done := again.handoverFor(ctx, "conv1", "lead1", "Lead", quote)
		again.handoverSet(newest, handoverQueued, "", nil)
		done()
		var ids []string
		for _, e := range handoverList(again) {
			if e.ID == newest {
				e.ID = "newest"
			}
			ids = append(ids, e.ID)
		}
		var saved map[string]handover
		if ok, err := store.Setting(ctx, handoversKey, &saved); !ok || err != nil || len(saved) != len(ids) {
			t.Fatalf("saved %d of %d (%v, %v)", len(saved), len(ids), ok, err)
		}
		return ids
	}
	if ids := keptAfterOneMore([]time.Duration{handoverMemory + time.Hour, handoverMemory - time.Hour}); !slices.Equal(ids, []string{"handover_01", "newest"}) {
		t.Fatalf("kept %v: want the one past %s dropped", ids, handoverMemory)
	}
	ages := make([]time.Duration, handoversKept)
	for i := range ages {
		ages[i] = time.Duration(handoversKept-i) * time.Hour
	}
	ids := keptAfterOneMore(ages)
	if len(ids) != handoversKept || ids[0] != "handover_01" || ids[len(ids)-1] != "newest" {
		t.Fatalf("kept %d from %s to %s: want %d, the oldest dropped", len(ids), ids[0], ids[len(ids)-1], handoversKept)
	}
}
