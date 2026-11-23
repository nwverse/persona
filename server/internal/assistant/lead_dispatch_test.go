package assistant

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A direction lead driving its own Agents is not asked on the phone about what its words say: the owner confirmed the
// work once, when it reached the lead. Its other hand-offs (a2a_send) still are, and a lead in a track whose rules
// confirm every hand-off (health) still is.
func TestLeadDrivesItsOwnAgentsWithoutAPhoneCard(t *testing.T) {
	ctx := context.Background()
	s, f, _ := infoUseService(t)
	setSettings(t, s, func(v *Settings) { v.Main, v.Leaders = "qa-main-chat", []string{"qa-main", "qa-health-1"} })
	f.mu.Lock()
	f.infoTag = `{"categories":["legal_id"]}`
	f.mu.Unlock()
	for _, tool := range []string{"agent_start", "agent_send", "agent_interrupt"} {
		done := make(chan bool, 1)
		go func() {
			ok, err := s.confirmOutbound(ctx, "qa-machine", "qa-main", tool, "qa: a placeholder passport number", "summary", nil)
			done <- ok && err == nil
		}()
		select {
		case ok := <-done:
			if !ok {
				t.Fatalf("%s: the lead's hand-off to its own Agent was refused", tool)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: the lead's hand-off to its own Agent waits on a phone card", tool)
		}
		if qs := s.pendingQuestions(); len(qs) != 0 {
			t.Fatalf("%s: a phone card was raised: %+v", tool, qs)
		}
	}
	answerOutboundIn(t, s, "qa-main", "a2a_send", "summary", "", false)
	if ok, err := s.confirmOutbound(ctx, "qa-machine", "qa-main", "a2a_send", "qa: a placeholder passport number", "summary", nil); err != nil || ok {
		t.Fatalf("the lead's a2a_send was not asked about: %v %v", ok, err)
	}
	answerOutboundIn(t, s, "qa-health-1", "agent_send", "summary", "", false)
	if ok, err := s.confirmOutbound(ctx, "qa-machine", "qa-health-1", "agent_send", "qa: a step count", "summary", nil); err != nil || ok {
		t.Fatalf("a lead in the health track was not asked about: %v %v", ok, err)
	}
}

// A direction lead's plan starts at once, writable work in a folder the owner never let start by itself included: no
// card, no push. An ordinary conversation's same plan still waits on its card.
func TestLeadPlanStartsAtOnce(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	f.known["lead1"] = true
	res, err := s.toolTaskPlan(ctx, "lead1", planArgs(false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res, "card") {
		t.Fatalf("the lead's plan waits on a card: %s", res)
	}
	waitFor(t, "the lead's task to start", func() bool { return len(onlyTodo(s).Agents) == 1 })
	if got := f.pushed(); len(got) != 0 {
		t.Fatalf("the lead's plan was pushed: %+v", got)
	}

	s2, f2 := newQuietService(t)
	if res, err := s2.toolTaskPlan(ctx, "side1", planArgs(false)); err != nil || !strings.Contains(res, "card") {
		t.Fatalf("an ordinary conversation's writable plan did not wait on its card: %s %v", res, err)
	}
	if got := f2.pushed(); len(got) != 1 {
		t.Fatalf("an ordinary conversation's plan card, with the owner away, pushes once: %+v", got)
	}
}
