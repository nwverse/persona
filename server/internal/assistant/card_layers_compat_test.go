package assistant

import (
	"context"
	"errors"
	"testing"
)

// The stable deployment needs the real held-notification outcome, not an always-zero field.
func TestCardLedgerHeldOutcomeSurvivesStableBackport(t *testing.T) {
	st := newFleetMem()
	s, f := newTaskServiceOn(t, st)
	ctx := context.Background()
	setSettings(t, s, func(v *Settings) { v.Main = ""; v.Leaders = nil })
	f.mu.Lock()
	f.notifyResult = NotifyResult{OK: true, Held: true, Reason: "ln_p1_held"}
	f.mu.Unlock()

	if _, err := s.pushResult(withCard(ctx, cardAskHeld), Notification{Title: "held reminder", SessionID: "conv1"}); err != nil {
		t.Fatal(err)
	}
	if c := cardsToday(t, s)[cardAskHeld]; c.N != 0 || c.Held != 1 {
		t.Fatalf("held outcome = %+v, want N=0 Held=1", c)
	}
	s.deliverCard(cardAgentDone, Notification{Title: "held completion", SessionID: "sess1"})
	waitFor(t, "asynchronous held outcome recorded", func() bool {
		c := cardsToday(t, s)[cardAgentDone]
		return c.N == 0 && c.Held == 1
	})
	n := Notification{ID: "ntf_held_recording", Title: "held recording", SessionID: "conv1"}
	h := recordingHost{s}
	if err := h.Notify(ctx, n); err != nil {
		t.Fatal(err)
	}
	h.NoteCard(ctx, cardMinutesReady, n)
	if c := cardsToday(t, s)[cardMinutesReady]; c.N != 0 || c.Held != 1 {
		t.Fatalf("recording host outcome = %+v, want N=0 Held=1", c)
	}

	again, _ := newTaskServiceOn(t, st)
	if c := cardsToday(t, again)[cardAskHeld]; c.N != 0 || c.Held != 1 {
		t.Fatalf("restored held outcome = %+v, want N=0 Held=1", c)
	}
	f.mu.Lock()
	f.refuseNotify = errors.New("synthetic notification refusal")
	f.mu.Unlock()
	if _, err := s.pushResult(withCard(ctx, cardAskHeld), Notification{Title: "refused reminder"}); err == nil {
		t.Fatal("notification refusal was lost")
	}
	if c := cardsToday(t, s)[cardAskHeld]; c.N != 0 || c.Held != 1 {
		t.Fatalf("failed notification changed counts: %+v", c)
	}
}

func TestInternalOwnerReasonSourcesKeepTheStableSixCategories(t *testing.T) {
	for _, tc := range []struct {
		why, source string
		category    int
	}{
		{whyPriority, cardDecisionDirection, cardCatDirection},
		{whyAcceptance, cardDecisionDirection, cardCatDirection},
		{whyOnlyOwnerKnow, cardDecisionOwnerKnows, cardCatOwnerKnows},
		{whyVisible, cardDecisionOutward, cardCatOutward},
		{whyIrreversible, cardDecisionIrreversible, cardCatIrreversible},
	} {
		if got := decisionCardSource(tc.why); got != tc.source {
			t.Fatalf("source for %q = %q, want %q", tc.why, got, tc.source)
		}
		if got := cardCategoryOf(tc.source); got != tc.category {
			t.Fatalf("category for %q = %d, want %d", tc.source, got, tc.category)
		}
	}
}
