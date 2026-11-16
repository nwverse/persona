package assistant

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestParkWaitsOnRecordedSource(t *testing.T) {
	const source = "https://github.com/example/app/pull/12"
	for _, tc := range []struct {
		name, kind, waiting, url string
		ready, want              bool
	}{
		{"recorded source", "merge_review", parkSomeoneElse, source, true, true},
		{"canonical object", "merge_review", parkSomeoneElse, "https://GitHub.com/Example/App/pull/12/?tab=checks#discussion", true, true},
		{"different repository", "merge_review", parkSomeoneElse, "https://github.com/example/other/pull/12", true, false},
		{"different number", "merge_review", parkSomeoneElse, "https://github.com/example/app/pull/120", true, false},
		{"different provider", "merge_review", parkSomeoneElse, "https://git.example.org/example/app/-/merge_requests/12", true, false},
		{"missing source", "merge_review", parkSomeoneElse, "", true, false},
		{"path is not the object", "merge_review", parkSomeoneElse, source + "/files", true, false},
		{"source embedded in another URL", "merge_review", parkSomeoneElse, "https://example.org/redirect/" + source, true, false},
		{"not ready", "merge_review", parkSomeoneElse, source, false, false},
		{"person reply", "person_reply", parkSomeoneElse, "", false, false},
		{"external event", "external_event", parkExternalEvent, source, false, false},
		{"date", "date", parkDate, "", false, false},
		{"inconsistent kind", "merge_review", parkExternalEvent, source, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &TaskReview{
				WaitingOn: tc.waiting, Reason: "PR #12: " + source,
				Evidence: &ParkEvidence{Kind: tc.kind, URL: tc.url, Ready: tc.ready},
			}
			if got := parkWaitsOnPR(r, source); got != tc.want {
				t.Fatalf("recorded source match = %v, want %v", got, tc.want)
			}
		})
	}
	if !parkWaitsOnPR(&TaskReview{WaitingOn: parkSomeoneElse, Reason: "Waiting for PR #12"}, source) {
		t.Fatal("a legacy park lost its reason-based compatibility")
	}
}

func TestPRStatusRecordedWaitUsesEvidence(t *testing.T) {
	const source = "https://github.com/example/app/pull/12"
	for _, tc := range []struct {
		name, terminal, kind, bound, reason string
		wake                                bool
	}{
		{"merge without prose link", "merged", "merge_review", source, "Waiting for the named review", true},
		{"close without prose link", "closed", "merge_review", source, "Waiting for the named review", true},
		{"different source despite prose", "merged", "merge_review", "https://github.com/example/other/pull/12", "Waiting for " + source, false},
		{"person despite prose", "merged", "person_reply", "", "Waiting for a reply about " + source, false},
		{"external event despite prose", "merged", "external_event", source, "Waiting for deployment after " + source, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, task := newSupervisedService(t)
			holdDigest(s)
			ctx := context.Background()
			s.setTaskStage(ctx, task.ID, "review", source)
			a := mergePark(source)
			a["reason"] = tc.reason
			if tc.kind != "merge_review" {
				a["waiting_on"] = parkSomeoneElse
				if tc.kind == "external_event" {
					a["waiting_on"] = parkExternalEvent
				}
			}
			a["evidence"] = testParkEvidence(tc.kind, "external reviewer", tc.bound, "The recorded dependency changes")
			if _, err := reviewErr(s, task.ID, a); err != nil {
				t.Fatal(err)
			}
			before := todoNow(s, task.ID).Supervision.parking().At
			s.deploy.forge = &statusForge{status: TodoPRStatus{State: tc.terminal}}
			s.sweepPRStatuses(ctx, time.Now())
			got := todoNow(s, task.ID)
			if (got.Supervision.State == reviewPending) != tc.wake || pendingReviewSignal(s, task.ID) != tc.wake {
				t.Fatalf("wake = %q, queued = %v, want %v", got.Supervision.State, pendingReviewSignal(s, task.ID), tc.wake)
			}
			if got.DoneAt != nil || len(got.Supervision.Reviews) != 1 || !got.Supervision.Reviews[0].At.Equal(before) {
				t.Fatal("observing a terminal PR completed the task or replaced its wait history")
			}
		})
	}
}

type parkedResponseForge struct {
	fakeForge
	beforeResponse func()
}

func (f *parkedResponseForge) PRStatus(context.Context, string, int) (TodoPRStatus, error) {
	if f.beforeResponse != nil {
		f.beforeResponse()
		f.beforeResponse = nil
	}
	return TodoPRStatus{State: "merged"}, nil
}

func TestPRStatusRecordedWaitRejectsInFlightReplacement(t *testing.T) {
	const source = "https://github.com/example/app/pull/12"
	for _, repark := range []bool{false, true} {
		name := "first park while request is in flight"
		if repark {
			name = "same source parked again while request is in flight"
		}
		t.Run(name, func(t *testing.T) {
			s, _, task := newSupervisedService(t)
			holdDigest(s)
			ctx := context.Background()
			s.setTaskStage(ctx, task.ID, "review", source)
			park := func() {
				t.Helper()
				if _, err := reviewErr(s, task.ID, mergePark(source)); err != nil {
					t.Fatal(err)
				}
			}
			if repark {
				park()
			}
			f := &parkedResponseForge{beforeResponse: park}
			s.deploy.forge = f
			now := time.Now()
			s.sweepPRStatuses(ctx, now)
			got := todoNow(s, task.ID)
			if got.Supervision.State != reviewParked || pendingReviewSignal(s, task.ID) {
				t.Fatalf("an old in-flight response resolved the new wait: %+v", got.Supervision)
			}
			at := got.Supervision.parking().At
			// A new observation of that same terminal state reconciles the new wait; no new transition is needed.
			s.sweepPRStatuses(ctx, now.Add(deployRecheck))
			got = todoNow(s, task.ID)
			if got.Supervision.State != reviewPending || !pendingReviewSignal(s, task.ID) || !got.Supervision.Reviews[len(got.Supervision.Reviews)-1].At.Equal(at) {
				t.Fatalf("a post-park observation did not resolve the recorded wait: %+v", got.Supervision)
			}
		})
	}
}

func TestPRStatusRecordedWaitRejectsOlderFacts(t *testing.T) {
	s, _, task := newSupervisedService(t)
	holdDigest(s)
	ctx := context.Background()
	const source = "https://github.com/example/app/pull/12"
	s.setTaskStage(ctx, task.ID, "review", source)
	if _, err := reviewErr(s, task.ID, mergePark(source)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.applyPRStatus(ctx, todoNow(s, task.ID), TodoPRStatus{URL: source, State: "open", HeadOID: "new-head", CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.applyPRStatus(ctx, todoNow(s, task.ID), TodoPRStatus{URL: source, State: "merged", HeadOID: "old-head", CheckedAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got := todoNow(s, task.ID)
	if got.PRStatus.State != "open" || got.PRStatus.HeadOID != "new-head" || got.Supervision.State != reviewParked || pendingReviewSignal(s, task.ID) {
		t.Fatalf("older facts replaced the newer open observation or woke its wait: %+v", got)
	}
}

func TestPRStatusRecordedEarlierSourceWaitsForAllPRs(t *testing.T) {
	s, _, task := newSupervisedService(t)
	holdDigest(s)
	ctx, now := context.Background(), time.Now()
	const first, last = "https://github.com/example/app/pull/11", "https://github.com/example/app/pull/12"
	s.setTaskStage(ctx, task.ID, "review", first)
	s.setTaskStage(ctx, task.ID, "review", last)
	a := mergePark(first)
	a["reason"] = "Waiting for the recorded review"
	if _, err := reviewErr(s, task.ID, a); err != nil {
		t.Fatal(err)
	}
	f := &prsForge{byNumber: map[int]TodoPRStatus{11: {State: "merged"}, 12: {State: "open"}}}
	s.deploy.forge = f
	s.sweepPRStatuses(ctx, now)
	if got := todoNow(s, task.ID); got.Supervision.State != reviewParked || pendingReviewSignal(s, task.ID) {
		t.Fatalf("a bound PR merging bypassed another open PR: %+v", got.Supervision)
	}
	f.byNumber[12] = TodoPRStatus{State: "merged"}
	s.sweepPRStatuses(ctx, now.Add(deployRecheck))
	if got := todoNow(s, task.ID); got.Supervision.State != reviewPending || !pendingReviewSignal(s, task.ID) || f.calls[11] != 1 {
		t.Fatalf("the last PR did not reconcile the already terminal recorded source: %+v, calls %v", got.Supervision, f.calls)
	}
}

func TestPRStatusRecordedWaitRecoversAfterLedgerFailure(t *testing.T) {
	database := &ledgerDown{DB: taskRecoveryDatabase(t)}
	s, _, task := newSupervisedServiceOn(t, database)
	holdDigest(s)
	ctx := context.Background()
	const source = "https://github.com/example/app/pull/12"
	s.setTaskStage(ctx, task.ID, "review", source)
	if _, err := reviewErr(s, task.ID, mergePark(source)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.applyPRStatus(ctx, todoNow(s, task.ID), TodoPRStatus{URL: source, State: "open", CheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	database.down.Store(true)
	if err := s.applyPRStatus(ctx, todoNow(s, task.ID), TodoPRStatus{URL: source, State: "merged", CheckedAt: now.Add(time.Minute)}); err == nil {
		t.Fatal("expected the supervision write to fail")
	}
	before := todoNow(s, task.ID)
	if before.PRStatus.State != "merged" || before.Supervision.State != reviewParked || pendingReviewSignal(s, task.ID) {
		t.Fatalf("failed ledger write lost the saved observation or published a wake: %+v", before)
	}
	database.down.Store(false)
	again, _ := restartedOn(t, database)
	holdDigest(again)
	if err := again.ensureTodosLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	again.deploy.forge = &statusForge{status: TodoPRStatus{State: "merged"}}
	again.sweepPRStatuses(ctx, now.Add(2*deployRecheck))
	got := todoNow(again, task.ID)
	if got.Supervision.State != reviewPending || got.DoneAt != nil || !slices.Equal(pendingKinds(again, task.ID), []string{sigReview}) {
		t.Fatalf("saved terminal facts were not reconciled after restart: %+v, signals %v", got.Supervision, pendingKinds(again, task.ID))
	}
	if len(got.Supervision.Reviews) != 1 || !got.Supervision.Reviews[0].At.Equal(before.Supervision.parking().At) || got.Supervision.Reviews[0].Evidence.URL != source {
		t.Fatal("recovery replaced the recorded wait or its evidence")
	}
	again.sweepPRStatuses(ctx, now.Add(3*deployRecheck))
	if !slices.Equal(pendingKinds(again, task.ID), []string{sigReview}) {
		t.Fatalf("a repeated terminal observation queued another logical wake: %v", pendingKinds(again, task.ID))
	}
	rows, err := database.ListTodoSupervision(ctx)
	if err != nil || len(rows) != 1 || rows[0].State != reviewPending {
		t.Fatalf("recovered review was not persisted: %+v, %v", rows, err)
	}
}
