package assistant

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestA2AQueuedRequestDeliversOnNextIdleAfterRestart(t *testing.T) {
	s, h, st, cfg, parent, child := a2aFixture(t)
	setStatus(h.fakeHub, child.Machine, child.Session, proto.StatusWorking)
	task, err := s.a2aStart(t.Context(), st, cfg, parent, child, "busy-request", "", "Queued work.", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	task = a2aProcessOne(t, s, st, cfg, task.ID)
	if len(h.sentCalls()) != 0 || task.State != a2aSubmitted || task.Events[0].Delivery != a2aQueued || task.Events[0].WaitingSince.IsZero() {
		t.Fatalf("busy recipient did not retain queued input: %+v", task)
	}
	// Even an old request gets its next idle opportunity before timing out.
	task.Events[0].WaitingSince = time.Now().Add(-a2aConfirmFor - time.Minute)
	if err := s.a2aSave(t.Context(), st, task); err != nil {
		t.Fatal(err)
	}
	restarted := New(Options{Hub: h, Store: func() Store { return st }})
	setStatus(h.fakeHub, child.Machine, child.Session, proto.StatusDone)
	task = a2aProcessOne(t, restarted, st, cfg, task.ID)
	a2aProcessOne(t, restarted, st, cfg, task.ID)
	calls := h.sentCalls()
	if len(calls) != 1 || task.State != a2aWorking || task.Events[0].Delivery != a2aDelivered ||
		!strings.Contains(string(calls[0].Params), a2aDeliveryID(task.ID, 1)) {
		t.Fatalf("queued request did not deliver exactly once on idle: %+v, %+v", task, calls)
	}
}

func TestA2ARecipientClearsUndeliveredRequests(t *testing.T) {
	for _, state := range []string{a2aFailed, a2aComplete} {
		for _, queued := range []bool{false, true} {
			t.Run(state+"/queued="+map[bool]string{false: "false", true: "true"}[queued], func(t *testing.T) {
				s, h, st, cfg, parent, child := a2aFixture(t)
				ctx := t.Context()
				first, err := s.a2aStart(ctx, st, cfg, parent, child, "first", "", "First work.", true, nil)
				if err != nil {
					t.Fatal(err)
				}
				second, err := s.a2aStart(ctx, st, cfg, a2aCaller{Owner: "device:second-sender"}, child, "second", "", "Second work.", false, nil)
				if err != nil || second.State != a2aQueued {
					t.Fatalf("another sender was refused: %+v %v", second, err)
				}
				target := first
				if queued {
					target = second
				}
				args, _ := json.Marshal(a2aToolArgs{Task: target.ID, ID: "clear", State: "progress", Text: "Cannot receive this request."})
				result := s.a2aTool(ctx, child, "a2a_report", args)
				if !result.IsError || !strings.Contains(resultText(result), "report failed or completed to clear it") {
					t.Fatalf("queued progress lacks actionable error: %s", resultText(result))
				}
				args, _ = json.Marshal(a2aToolArgs{Task: target.ID, ID: "clear", State: state, Text: "Cleared by the recipient."})
				for range 2 {
					result = s.a2aTool(ctx, child, "a2a_report", args)
					if result.IsError {
						t.Fatalf("recipient cannot clear queued work idempotently: %s", resultText(result))
					}
				}
				got := a2aProcessOne(t, s, st, cfg, target.ID)
				if got.State != state || got.Events[0].Delivery != a2aDiscarded || len(h.sentCalls()) != 0 {
					t.Fatalf("cleared request was delivered or falsely receipted: %+v", got)
				}
				if queued {
					first, err = decodeA2A(st.A2AGet(ctx, first.ID))
					if err != nil || first.State != a2aSubmitted {
						t.Fatalf("clearing FIFO tail disturbed the head: %+v %v", first, err)
					}
				} else {
					second = a2aProcessOne(t, s, st, cfg, second.ID)
					if second.State != a2aWorking || len(h.sentCalls()) != 1 {
						t.Fatalf("recipient cleanup did not drain another sender: %+v", second)
					}
				}
			})
		}
	}
}

func TestA2ARepeatedBusyRequeuesFailAndReleaseNextSender(t *testing.T) {
	s, h, st, cfg, parent, child := a2aFixture(t)
	ctx := t.Context()
	first, err := s.a2aStart(ctx, st, cfg, parent, child, "stuck", "", "Stuck work.", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.sendErr = &rpc.Error{Code: "a2a_session_busy", Message: "private node output must not be persisted"}
	first = a2aProcessOne(t, s, st, cfg, first.ID)
	if first.Events[0].Delivery != a2aQueued || first.Events[0].DeliveryReason == "" {
		t.Fatalf("definite refusal lost its reason: %+v", first)
	}
	since := first.Events[0].WaitingSince
	first = a2aProcessOne(t, s, st, cfg, first.ID)
	if !first.Events[0].WaitingSince.Equal(since) {
		t.Fatal("busy retry reset the delivery deadline")
	}
	first.Events[0].WaitingSince = time.Now().Add(-a2aConfirmFor - time.Minute)
	first.Events[0].At = first.Events[0].WaitingSince
	if err := s.a2aSave(ctx, st, first); err != nil {
		t.Fatal(err)
	}
	view := first.view()
	meta := first.wire(nil, false).Metadata
	if view.WaitingSeconds < 600 || view.WaitingReason == "" || meta["personaWaitingSeconds"] != view.WaitingSeconds || meta["personaWaitingReason"] != view.WaitingReason {
		t.Fatalf("sender lacks waiting age/reason: %+v, %+v", view, meta)
	}
	second, err := s.a2aStart(ctx, st, cfg, a2aCaller{Owner: "device:another-sender"}, child, "next", "", "Next work.", false, nil)
	if err != nil || second.State != a2aQueued {
		t.Fatalf("another sender was refused by the stuck task: %+v %v", second, err)
	}
	restarted := New(Options{Hub: h, Store: func() Store { return st }})
	first = a2aProcessOne(t, restarted, st, cfg, first.ID)
	last := first.Events[len(first.Events)-1]
	if first.State != a2aFailed || first.Events[0].Delivery != a2aDiscarded || last.Delivery != a2aQueued ||
		!strings.Contains(last.Text, "ten minutes") || !strings.Contains(last.Text, "node could not accept") || strings.Contains(last.Text, "private") {
		t.Fatalf("busy requeue did not fail with a safe, durable reason: %+v", first)
	}
	h.sendErr = nil
	second = a2aProcessOne(t, restarted, st, cfg, second.ID)
	if second.State != a2aWorking || second.Events[0].Delivery != a2aDelivered {
		t.Fatalf("failed head did not release next sender: %+v", second)
	}
	setStatus(h.fakeHub, parent.Ref.Machine, parent.Ref.Session, proto.StatusDone)
	first = a2aProcessOne(t, restarted, st, cfg, first.ID)
	if first.Events[len(first.Events)-1].Delivery != a2aDelivered {
		t.Fatal("failure reason did not reach the sending session")
	}
}

func TestA2AQueuedDeliveryTimesOutWhileRecipientRemainsBusy(t *testing.T) {
	s, h, st, cfg, parent, child := a2aFixture(t)
	setStatus(h.fakeHub, child.Machine, child.Session, proto.StatusWorking)
	task, err := s.a2aStart(t.Context(), st, cfg, parent, child, "busy", "", "Work.", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	task = a2aProcessOne(t, s, st, cfg, task.ID)
	task.Events[0].WaitingSince = time.Now().Add(-a2aConfirmFor - time.Minute)
	if err := s.a2aSave(t.Context(), st, task); err != nil {
		t.Fatal(err)
	}
	task = a2aProcessOne(t, s, st, cfg, task.ID)
	if task.State != a2aFailed || len(h.sentCalls()) != 0 || !strings.Contains(task.Events[len(task.Events)-1].Text, "live and idle") {
		t.Fatalf("busy recipient left a queued task stuck forever: %+v", task)
	}
}
