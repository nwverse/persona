package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/todo/mergetrain"
)

func setMergeTrainTodosV2Flag(t *testing.T, s *Service, want, expected bool) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT todos_v2=%v: code=%d body=%s", want, w.Code, w.Body.String())
	}
}

func newMergeTrainTask(t *testing.T, s *Service, title, prURL string) Todo {
	t.Helper()
	ctx := context.Background()
	task, err := s.createTodo(ctx, title, "Track merge train PR status.", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, task.ID, "conv1", []string{"The PR is merged."}, 0)
	s.setTaskStage(ctx, task.ID, "review", prURL)
	return todoNow(s, task.ID)
}

func findRepoSnapshot(snap mergetrain.Snapshot, repoKey string) (mergetrain.RepoSnapshot, bool) {
	for _, r := range snap.Repos {
		if r.RepoKey == repoKey {
			return r, true
		}
	}
	return mergetrain.RepoSnapshot{}, false
}

func TestTodosV2MergeTrainBypassWhenFlagOff(t *testing.T) {
	s, _, task := newPRConflictTask(t, nil)
	ctx := context.Background()
	if s.settings(ctx).todosV2On() {
		t.Fatal("expected todos_v2 default to be false")
	}

	now := time.Now().UTC()
	for i, head := range []string{"head-1", "head-2"} {
		err := s.applyPRStatus(ctx, task, TodoPRStatus{
			URL:       task.PRURL,
			State:     "open",
			HeadOID:   head,
			Review:    "required",
			Checks:    "success",
			Conflicts: true,
			CheckedAt: now.Add(time.Duration(i+1) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if snap := s.mergeTrainSnapshot(ctx); len(snap.Repos) != 0 {
		t.Fatalf("todos_v2=false populated merge train: %+v", snap)
	}
	g, ok := pendingPRConflict(s, task.ID)
	if !ok {
		t.Fatal("expected existing sigPRConflict behavior to remain intact when todos_v2=false")
	}
	if strings.Contains(g.Detail, "circuit breaker") {
		t.Fatalf("todos_v2=false leaked merge train escalation detail: %q", g.Detail)
	}
}

func TestTodosV2MergeTrainSameRepoSerialAndCrossRepoParallel(t *testing.T) {
	s, _ := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	setMergeTrainTodosV2Flag(t, s, true, false)

	ctx := context.Background()
	now := time.Now().UTC()

	taskA1 := newMergeTrainTask(t, s, "Repo A first PR", "https://github.com/example/repo-a/pull/10")
	taskA2 := newMergeTrainTask(t, s, "Repo A second PR", "https://github.com/example/repo-a/pull/11")
	taskB1 := newMergeTrainTask(t, s, "Repo B first PR", "https://github.com/example/repo-b/pull/20")

	if err := s.applyPRStatus(ctx, taskA1, TodoPRStatus{
		URL: taskA1.PRURL, State: "open", HeadOID: "a1-sha", Review: "approved", Checks: "pending", CheckedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.applyPRStatus(ctx, taskA2, TodoPRStatus{
		URL: taskA2.PRURL, State: "open", HeadOID: "a2-sha", Review: "approved", Checks: "success", CheckedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.applyPRStatus(ctx, taskB1, TodoPRStatus{
		URL: taskB1.PRURL, State: "open", HeadOID: "b1-sha", Review: "approved", Checks: "success", CheckedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	snap := s.mergeTrainSnapshot(ctx)
	repoA, ok := findRepoSnapshot(snap, "github.com/example/repo-a")
	if !ok {
		t.Fatalf("missing repo-a in snapshot: %+v", snap)
	}
	if repoA.Head == nil || repoA.Head.TodoID != taskA1.ID || repoA.Head.State != mergetrain.StateActive {
		t.Fatalf("repo-a head = %+v, want %s active", repoA.Head, taskA1.ID)
	}
	if len(repoA.Queue) != 1 || repoA.Queue[0].TodoID != taskA2.ID || repoA.Queue[0].State != mergetrain.StateQueued {
		t.Fatalf("repo-a queue = %+v, want [%s queued]", repoA.Queue, taskA2.ID)
	}

	repoB, ok := findRepoSnapshot(snap, "github.com/example/repo-b")
	if !ok {
		t.Fatalf("missing repo-b in snapshot: %+v", snap)
	}
	if repoB.Head == nil || repoB.Head.TodoID != taskB1.ID || repoB.Head.State != mergetrain.StateActive {
		t.Fatalf("repo-b head = %+v, want %s active independently", repoB.Head, taskB1.ID)
	}

	// Merging taskA1 automatically promotes taskA2 to active head in repo-a.
	if err := s.applyPRStatus(ctx, taskA1, TodoPRStatus{
		URL: taskA1.PRURL, State: "merged", HeadOID: "a1-sha", CheckedAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	snap = s.mergeTrainSnapshot(ctx)
	repoA, ok = findRepoSnapshot(snap, "github.com/example/repo-a")
	if !ok {
		t.Fatalf("missing repo-a after merge: %+v", snap)
	}
	if repoA.Head == nil || repoA.Head.TodoID != taskA2.ID || repoA.Head.State != mergetrain.StateActive {
		t.Fatalf("repo-a head after merge = %+v, want %s promoted to active", repoA.Head, taskA2.ID)
	}
	if len(repoA.Queue) != 0 {
		t.Fatalf("repo-a queue after promotion = %+v, want empty", repoA.Queue)
	}
	if len(repoA.Terminal) != 1 || repoA.Terminal[0].TodoID != taskA1.ID || repoA.Terminal[0].State != mergetrain.StateMerged {
		t.Fatalf("repo-a terminal = %+v, want [%s merged]", repoA.Terminal, taskA1.ID)
	}
}

func TestTodosV2MergeTrainHumanReviewGuardNeverAutoMerges(t *testing.T) {
	s, _ := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	setMergeTrainTodosV2Flag(t, s, true, false)

	ctx := context.Background()
	now := time.Now().UTC()
	task := newMergeTrainTask(t, s, "Human review gated PR", "https://github.com/example/guarded/pull/7")

	// Even when CI checks are green, PolicyHumanReviewRequired must pause at StateAwaitingApproval.
	if err := s.applyPRStatus(ctx, task, TodoPRStatus{
		URL:       task.PRURL,
		State:     "open",
		HeadOID:   "sha-1",
		Review:    "required",
		Checks:    "success",
		Conflicts: false,
		CheckedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	snap := s.mergeTrainSnapshot(ctx)
	repo, ok := findRepoSnapshot(snap, "github.com/example/guarded")
	if !ok || repo.Head == nil {
		t.Fatalf("missing guarded repo head: %+v", snap)
	}
	if repo.Head.Policy != mergetrain.PolicyHumanReviewRequired {
		t.Fatalf("policy = %q, want %q", repo.Head.Policy, mergetrain.PolicyHumanReviewRequired)
	}
	if repo.Head.Approved {
		t.Fatal("entry was unexpectedly approved without human review")
	}
	if repo.Head.State != mergetrain.StateAwaitingApproval {
		t.Fatalf("head state = %q, want %q", repo.Head.State, mergetrain.StateAwaitingApproval)
	}

	// Re-checking with changes_requested also stays blocked and unapproved.
	out := s.onPRStatusMergeTrain(ctx, task, TodoPRStatus{
		URL:       task.PRURL,
		State:     "open",
		HeadOID:   "sha-1",
		Review:    "changes_requested",
		Checks:    "success",
		CheckedAt: now.Add(time.Second),
	})
	if out.Decision.Action == mergetrain.ActionRebaseAndMerge {
		t.Fatalf("PolicyHumanReviewRequired unexpectedly allowed auto-merge: %+v", out.Decision)
	}
	if out.Decision.Action != mergetrain.ActionAwaitHumanReview || out.Decision.State != mergetrain.StateAwaitingApproval {
		t.Fatalf("decision = %+v, want await_human_review / awaiting_approval", out.Decision)
	}
}

func TestTodosV2MergeTrainTwoStrikeConflictEscalationDedupAndReset(t *testing.T) {
	s, _ := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	setMergeTrainTodosV2Flag(t, s, true, false)

	ctx := context.Background()
	now := time.Now().UTC()
	task := newMergeTrainTask(t, s, "Conflicting PR", "https://github.com/example/conflict-repo/pull/42")

	// Strike 1 on head-a.
	if err := s.applyPRStatus(ctx, task, TodoPRStatus{
		URL: task.PRURL, State: "open", HeadOID: "head-a", Review: "approved", Checks: "failure", Conflicts: true, CheckedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	repo, ok := findRepoSnapshot(s.mergeTrainSnapshot(ctx), "github.com/example/conflict-repo")
	if !ok || repo.Head == nil || repo.Head.RebaseFailures != 1 || repo.Head.State != mergetrain.StateActive {
		t.Fatalf("after strike 1: %+v", repo.Head)
	}

	// Silent re-poll of the same head-a must NOT increment rebase failures.
	if err := s.applyPRStatus(ctx, task, TodoPRStatus{
		URL: task.PRURL, State: "open", HeadOID: "head-a", Review: "approved", Checks: "failure", Conflicts: true, CheckedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	repo, _ = findRepoSnapshot(s.mergeTrainSnapshot(ctx), "github.com/example/conflict-repo")
	if repo.Head.RebaseFailures != 1 || repo.Head.State != mergetrain.StateActive {
		t.Fatalf("silent re-poll incremented failures: %+v", repo.Head)
	}

	// Conflict clears before strike 2 -> failure streak resets to 0.
	if err := s.applyPRStatus(ctx, task, TodoPRStatus{
		URL: task.PRURL, State: "open", HeadOID: "head-b", Review: "approved", Checks: "pending", Conflicts: false, CheckedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	repo, _ = findRepoSnapshot(s.mergeTrainSnapshot(ctx), "github.com/example/conflict-repo")
	if repo.Head.RebaseFailures != 0 || repo.Head.State != mergetrain.StateActive {
		t.Fatalf("conflict resolution did not reset failure count: %+v", repo.Head)
	}

	// Now trigger two consecutive conflict rounds (head-c then head-d) -> trips StateEscalated.
	if err := s.applyPRStatus(ctx, task, TodoPRStatus{
		URL: task.PRURL, State: "open", HeadOID: "head-c", Review: "approved", Checks: "failure", Conflicts: true, CheckedAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	repo, _ = findRepoSnapshot(s.mergeTrainSnapshot(ctx), "github.com/example/conflict-repo")
	if repo.Head.RebaseFailures != 1 || repo.Head.State != mergetrain.StateActive {
		t.Fatalf("after fresh strike 1: %+v", repo.Head)
	}

	if err := s.applyPRStatus(ctx, task, TodoPRStatus{
		URL: task.PRURL, State: "open", HeadOID: "head-d", Review: "approved", Checks: "failure", Conflicts: true, CheckedAt: now.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	repo, _ = findRepoSnapshot(s.mergeTrainSnapshot(ctx), "github.com/example/conflict-repo")
	if repo.Head.RebaseFailures != 2 || repo.Head.State != mergetrain.StateEscalated {
		t.Fatalf("after strike 2: head=%+v, want RebaseFailures=2 StateEscalated", repo.Head)
	}
	sig, hasSig := pendingPRConflict(s, task.ID)
	if !hasSig || !strings.Contains(sig.Detail, "circuit breaker") || !strings.Contains(sig.Detail, "needs_user") {
		t.Fatalf("expected escalated supervision signal detail, got (%v, %+v)", hasSig, sig)
	}

	// Repeated poll at the same escalated head-d must be deduplicated (Escalate == false).
	dup := s.onPRStatusMergeTrain(ctx, task, TodoPRStatus{
		URL: task.PRURL, State: "open", HeadOID: "head-d", Review: "approved", Checks: "failure", Conflicts: true, CheckedAt: now.Add(5 * time.Second),
	})
	if dup.Escalate {
		t.Fatalf("duplicate escalation emitted for same (todo, pr, head): %+v", dup)
	}
	if dup.Decision.Action != mergetrain.ActionEscalateNeedsUser || dup.Decision.State != mergetrain.StateEscalated {
		t.Fatalf("expected escalated decision on repeat poll, got %+v", dup.Decision)
	}
}

func TestTodosV2MergeTrainRollbackClearsStateAndConcurrentIsolation(t *testing.T) {
	s1, _ := newQuietServiceOn(t, nil)
	s2, _ := newQuietServiceOn(t, nil)
	setMergeTrainTodosV2Flag(t, s1, true, false)
	setMergeTrainTodosV2Flag(t, s2, true, false)

	ctx := context.Background()
	now := time.Now().UTC()
	task1 := newMergeTrainTask(t, s1, "Service 1 PR", "https://github.com/example/shared/pull/1")
	task2 := newMergeTrainTask(t, s2, "Service 2 PR", "https://github.com/example/shared/pull/2")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 5 {
			_ = s1.applyPRStatus(ctx, task1, TodoPRStatus{
				URL: task1.PRURL, State: "open", HeadOID: "s1-head", Review: "required", Checks: "success", CheckedAt: now.Add(time.Duration(i) * time.Second),
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 5 {
			_ = s2.applyPRStatus(ctx, task2, TodoPRStatus{
				URL: task2.PRURL, State: "open", HeadOID: "s2-head", Review: "approved", Checks: "success", CheckedAt: now.Add(time.Duration(i) * time.Second),
			})
		}
	}()
	wg.Wait()

	// Both Service instances must remain completely isolated.
	r1, ok1 := findRepoSnapshot(s1.mergeTrainSnapshot(ctx), "github.com/example/shared")
	r2, ok2 := findRepoSnapshot(s2.mergeTrainSnapshot(ctx), "github.com/example/shared")
	if !ok1 || r1.Head == nil || r1.Head.TodoID != task1.ID || len(r1.Queue) != 0 {
		t.Fatalf("s1 snapshot polluted: %+v", r1)
	}
	if !ok2 || r2.Head == nil || r2.Head.TodoID != task2.ID || len(r2.Queue) != 0 {
		t.Fatalf("s2 snapshot polluted: %+v", r2)
	}

	// Disabling todos_v2 at runtime immediately clears and bypasses s1's merge train without affecting s2.
	setMergeTrainTodosV2Flag(t, s1, false, true)
	if snap := s1.mergeTrainSnapshot(ctx); len(snap.Repos) != 0 {
		t.Fatalf("disabling todos_v2 did not clear merge train: %+v", snap)
	}
	if err := s1.applyPRStatus(ctx, task1, TodoPRStatus{
		URL: task1.PRURL, State: "open", HeadOID: "s1-head-2", Review: "required", Checks: "success", CheckedAt: now.Add(10 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if snap := s1.mergeTrainSnapshot(ctx); len(snap.Repos) != 0 {
		t.Fatalf("merge train remained active after todos_v2=false: %+v", snap)
	}
	if r2After, ok := findRepoSnapshot(s2.mergeTrainSnapshot(ctx), "github.com/example/shared"); !ok || r2After.Head == nil || r2After.Head.TodoID != task2.ID {
		t.Fatalf("s2 was unexpectedly affected by s1 rollback: %+v", r2After)
	}
}
