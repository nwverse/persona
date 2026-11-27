package assistant

import (
	"strings"
	"testing"
	"time"
)

func TestCommentedReviewFailureReportsAreActionable(t *testing.T) {
	for _, body := range []string{
		"CI failed",
		"All CI checks are failing.",
		"Tests: ❌ failed",
		"Build is red!",
		"Lint: failure",
		"Coverage failed",
		"Coverage report: failure",
		"Verification completed.\nCI failed",
	} {
		if commentedReviewLooksInformational(body) {
			t.Errorf("failure report was treated as informational: %q", body)
		}
	}
	for _, body := range []string{"CI passed", "All tests are green", "Coverage: 98.5%", "LGTM"} {
		if !commentedReviewLooksInformational(body) {
			t.Errorf("routine status was treated as actionable: %q", body)
		}
	}
}

func TestPRFeedbackFailureReportsAndExplicitOptOut(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s, hub, task := newBoundPRTask(t, &memStore{}, feedbackTestAgent(now, false))
	turnPREventsRouteOn(t, s)
	feedbackTestSendReceipt(hub, agentSendReply{OK: true})
	source, forge := newFeedbackTestForge(t)
	s.deploy.forge = forge
	s.sweepPRStatuses(t.Context(), now)

	const optedOut = "Please rerun this check. <!-- \n\t PeRsOnA : NO-WAKE \t-->"
	observation := now.Add(deployRecheck)
	source.mu.Lock()
	source.comments = []map[string]any{
		feedbackTestComment(41, observation, "CI failed"),
		feedbackTestComment(42, observation, optedOut),
	}
	source.reviews = []githubPRReview{
		feedbackTestReview(41, observation, "Coverage failed"),
		feedbackTestReview(42, observation, optedOut),
		feedbackTestReview(43, observation, ""),
	}
	source.inline = map[int64][]githubPRInlineComment{
		43: {{ID: 101, Body: optedOut}},
	}
	source.mu.Unlock()
	before := len(sentToAgent(hub))
	s.sweepPRStatuses(t.Context(), observation)
	got := strings.Join(sentToAgent(hub)[before:], "\n")
	for _, want := range []string{"CI failed", "Coverage failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("first observation did not deliver %q: %s", want, got)
		}
	}
	if strings.Contains(got, "Please rerun") {
		t.Fatalf("opt-out woke the Agent: %s", got)
	}
	// A repeated observation is still silent even without the marker in an edited body.
	source.mu.Lock()
	source.comments[1]["body"] = "Please rerun this check."
	source.mu.Unlock()
	n := len(sentToAgent(hub))
	s.sweepPRStatuses(t.Context(), observation.Add(7*time.Hour))
	if got := sentToAgent(hub)[n:]; len(got) != 0 {
		t.Fatalf("opt-out was replayed: %q", got)
	}
	if got := feedbackTestCursors(t, s.store(), task)[prFeedbackReviews].IDsAt; len(got) != 3 {
		t.Fatalf("opt-outs did not advance the cursor: %v", got)
	}
}
