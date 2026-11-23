package assistant

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/repository"
)

// newLinkedDeliveryTask is a supervised task whose stage names no PR: its delivery PRs are only repository links.
func newLinkedDeliveryTask(t *testing.T, links ...repository.TaskLink) (*Service, *fakeHub, *todoReadyRepoStore, Todo) {
	t.Helper()
	st := &todoReadyRepoStore{memStore: &memStore{}}
	s, f := newQuietServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	task, err := s.createTodo(ctx, "Fix the approval reminder", "Ship the reminder fix.", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, task.ID, "conv1", []string{"The fix is merged and running."}, 0)
	st.setRepo(task.ID, repository.TaskSet{TodoID: task.ID, Repositories: []repository.TaskRepository{{Links: links}}})
	return s, f, st, todoNow(s, task.ID)
}

// A delivery PR that is only a repository link (the stage named a branch, not the PR) is polled like a stage PR; a
// reference link is not (#14049).
func TestLinkedDeliveryPRsArePolled(t *testing.T) {
	const delivery, unset, ref = "https://github.com/example/app/pull/21", "https://github.com/example/app/pull/22", "https://github.com/example/app/pull/99"
	s, _, _, task := newLinkedDeliveryTask(t,
		repository.TaskLink{Kind: "pull_request", CanonicalURL: delivery, Role: repository.LinkRoleDelivery},
		repository.TaskLink{Kind: "pull_request", CanonicalURL: unset},
		repository.TaskLink{Kind: "pull_request", CanonicalURL: ref, Role: repository.LinkRoleReference},
		repository.TaskLink{Kind: "issue", CanonicalURL: "https://github.com/example/app/issues/20"})
	if task.PRURL != "" || len(task.PRs) != 0 {
		t.Fatalf("premise: the stage names a PR: %q %v", task.PRURL, task.PRs)
	}
	forge := &prsForge{byNumber: map[int]TodoPRStatus{21: {State: "open", HeadOID: "a"}, 22: {State: "open", HeadOID: "b"}, 99: {State: "open"}}}
	s.deploy.forge = forge
	s.sweepPRStatuses(context.Background(), time.Now())
	if forge.calls[21] != 1 || forge.calls[22] != 1 || forge.calls[99] != 0 {
		t.Fatalf("polled = %v; want the two delivery links once each and never the reference", forge.calls)
	}
	got := todoNow(s, task.ID)
	if !slices.Contains(got.PRs, delivery) || !slices.Contains(got.PRs, unset) || slices.Contains(got.PRs, ref) {
		t.Fatalf("followed PRs = %v", got.PRs)
	}
	if f, ok := todoObservedPRState(got, delivery); !ok || f.State != "open" {
		t.Fatalf("no facts kept for the linked PR: %+v %v", f, ok)
	}
}

// A task parked on something else (here: the design team) whose delivery PRs all finish while it waits, one merged
// and the last closed, wakes its supervisor once; the park stays, and the message names what it waits for (#14049).
func TestParkedTaskIsWokenWhenItsDeliveryPRsFinish(t *testing.T) {
	const first, second = "https://github.com/example/app/pull/21", "https://github.com/example/app/pull/22"
	s, f, _, task := newLinkedDeliveryTask(t,
		repository.TaskLink{Kind: "pull_request", CanonicalURL: first, Role: repository.LinkRoleDelivery},
		repository.TaskLink{Kind: "pull_request", CanonicalURL: second, Role: repository.LinkRoleDelivery})
	ctx, now := context.Background(), time.Now()
	if _, err := reviewErr(s, task.ID, waitingPark()); err != nil {
		t.Fatal(err)
	}
	forge := &prsForge{byNumber: map[int]TodoPRStatus{21: {State: "open", HeadOID: "a"}, 22: {State: "open", HeadOID: "b"}}}
	s.deploy.forge = forge
	s.sweepPRStatuses(ctx, now)
	forge.byNumber[21] = TodoPRStatus{State: "merged", HeadOID: "a"}
	s.sweepPRStatuses(ctx, now.Add(deployRecheck))
	if _, ok := pendingPRMerged(s, task.ID, first); ok {
		t.Fatal("woken while the other delivery PR was still open")
	}
	forge.byNumber[22] = TodoPRStatus{State: "closed", HeadOID: "b"}
	s.sweepPRStatuses(ctx, now.Add(2*deployRecheck))
	if _, ok := pendingPRMerged(s, task.ID, first); !ok {
		t.Fatalf("not woken once every delivery PR finished: %v", pendingKinds(s, task.ID))
	}
	if got := todoNow(s, task.ID).Supervision.State; got != reviewParked {
		t.Fatalf("the unrelated park was ended: %q", got)
	}
	sendPendingPRConflicts(s)
	notices := f.notices()
	if len(notices) != 1 {
		t.Fatalf("%d notices, want 1: %q", len(notices), notices)
	}
	for _, want := range []string{"signal=" + sigPRMerged, first, "The design team has to pick the icon set first"} {
		if !strings.Contains(notices[0], want) {
			t.Errorf("the wake lacks %q:\n%s", want, notices[0])
		}
	}
	// Once only: later sweeps, and a restart that forgets what was sent, do not wake it again.
	forgetSignals(t, s)
	for i := 3; i < 6; i++ {
		s.sweepPRStatuses(ctx, now.Add(time.Duration(i)*deployRecheck))
	}
	if _, ok := pendingPRMerged(s, task.ID, first); ok {
		t.Fatal("woken a second time")
	}
}

// A PR closed unmerged that ends a park names the task's other linked PRs, so its supervisor can tell a continuation
// (a maintainer's follow-up PR) from abandoned work (#14049).
func TestClosedPRWakeNamesLinkedSuccessors(t *testing.T) {
	st := &todoReadyRepoStore{memStore: &memStore{}}
	s, _, task := newPRConflictTask(t, st)
	const next = "https://github.com/example/app/pull/31"
	st.setRepo(task.ID, repository.TaskSet{TodoID: task.ID, Repositories: []repository.TaskRepository{{Links: []repository.TaskLink{
		{Kind: "pull_request", CanonicalURL: task.PRURL, Role: repository.LinkRoleDelivery},
		{Kind: "pull_request", CanonicalURL: next, Role: repository.LinkRoleReference},
	}}}})
	ctx, now := context.Background(), time.Now()
	if _, err := reviewErr(s, task.ID, mergePark(task.PRURL)); err != nil {
		t.Fatal(err)
	}
	forge := &prsForge{byNumber: map[int]TodoPRStatus{12: {State: "open", HeadOID: "h"}}}
	s.deploy.forge = forge
	s.sweepPRStatuses(ctx, now)
	forge.byNumber[12] = TodoPRStatus{State: "closed", HeadOID: "h"}
	s.sweepPRStatuses(ctx, now.Add(2*deployRecheck)) // the first read is dated at the park, after now
	b := &s.supervise
	b.mu.Lock()
	g, ok := b.pending[(superviseSignal{Todo: task.ID, Kind: sigReview}).id()]
	b.mu.Unlock()
	if !ok {
		t.Fatalf("the closed PR did not wake the park: %v", pendingKinds(s, task.ID))
	}
	if !strings.Contains(g.Detail, "PR closed: "+task.PRURL) || !strings.Contains(g.Detail, "may continue it: "+next) {
		t.Fatalf("detail = %q", g.Detail)
	}
}
