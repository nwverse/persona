package projection

import (
	"reflect"
	"slices"
	"testing"
)

func TestRepoGateEmptyInput(t *testing.T) {
	if got := AggregateRepoGate(nil); got != nil {
		t.Fatalf("AggregateRepoGate(nil) = %+v, want nil", got)
	}
	if got := AggregateRepoGate([]RepoGateRepo{}); got != nil {
		t.Fatalf("AggregateRepoGate(empty) = %+v, want nil", got)
	}
}

func TestRepoGateReferenceOnlyDoesNotBlock(t *testing.T) {
	repos := []RepoGateRepo{
		{
			RepoKey:   "https://github.com/org/docs-ref",
			RepoLabel: "org/docs-ref",
			IsTarget:  false,
			PRs: []RepoGatePR{
				{URL: "https://github.com/org/docs-ref/pull/1", State: "open", Checks: "failure", Conflicts: true},
			},
		},
		{
			RepoKey:   "https://github.com/org/upstream-ref",
			RepoLabel: "org/upstream-ref",
			IsTarget:  false,
		},
	}
	want := &RepoGate{
		State:          RepoGateReferenceOnly,
		TargetCount:    0,
		ReadyCount:     0,
		ReferenceCount: 2,
		Reason:         "reference_only",
	}
	if got := AggregateRepoGate(repos); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRepoGateIgnoresFailingReferenceReposWhenTargetsReady(t *testing.T) {
	repos := []RepoGateRepo{
		{
			RepoKey:   "https://github.com/org/api",
			RepoLabel: "org/api",
			IsTarget:  true,
			PRs: []RepoGatePR{
				{URL: "https://github.com/org/api/pull/10", State: "merged"},
			},
		},
		{
			RepoKey:   "https://github.com/org/web",
			RepoLabel: "org/web",
			IsTarget:  true,
			PRs: []RepoGatePR{
				{URL: "https://github.com/org/web/pull/20", State: "open", Review: "approved", Checks: "success"},
			},
		},
		{
			RepoKey:   "https://github.com/org/reference-red",
			RepoLabel: "org/reference-red",
			IsTarget:  false,
			PRs: []RepoGatePR{
				{URL: "https://github.com/org/reference-red/pull/99", State: "open", Checks: "failure", Conflicts: true, Review: "changes_requested"},
			},
		},
		{
			RepoKey:   "https://github.com/org/reference-empty",
			RepoLabel: "org/reference-empty",
			IsTarget:  false,
		},
	}
	want := &RepoGate{
		State:          RepoGateReady,
		TargetCount:    2,
		ReadyCount:     2,
		ReferenceCount: 2,
		Reason:         "all_targets_ready",
	}
	if got := AggregateRepoGate(repos); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRepoGateWeakestLinkPriorityOrder(t *testing.T) {
	repoMissing := RepoGateRepo{
		RepoKey: "k-missing", RepoLabel: "org/a-missing", IsTarget: true,
	}
	repoClosedOnly := RepoGateRepo{
		RepoKey: "k-closed", RepoLabel: "org/b-closed", IsTarget: true,
		PRs: []RepoGatePR{{URL: "https://github.com/org/b-closed/pull/1", State: "closed"}},
	}
	repoOpen := RepoGateRepo{
		RepoKey: "k-open", RepoLabel: "org/c-open", IsTarget: true,
		PRs: []RepoGatePR{{URL: "https://github.com/org/c-open/pull/2", State: "open"}},
	}
	repoChecksPending := RepoGateRepo{
		RepoKey: "k-pending", RepoLabel: "org/d-pending", IsTarget: true,
		PRs: []RepoGatePR{{URL: "https://github.com/org/d-pending/pull/3", State: "open", Review: "approved", Checks: "pending"}},
	}
	repoDraft := RepoGateRepo{
		RepoKey: "k-draft", RepoLabel: "org/e-draft", IsTarget: true,
		PRs: []RepoGatePR{{URL: "https://github.com/org/e-draft/pull/4", State: "open", Draft: true}},
	}
	repoReviewReq := RepoGateRepo{
		RepoKey: "k-review", RepoLabel: "org/f-review", IsTarget: true,
		PRs: []RepoGatePR{{URL: "https://github.com/org/f-review/pull/5", State: "open", Review: "required", Checks: "success"}},
	}
	repoChanges := RepoGateRepo{
		RepoKey: "k-changes", RepoLabel: "org/g-changes", IsTarget: true,
		PRs: []RepoGatePR{{URL: "https://github.com/org/g-changes/pull/6", State: "open", Review: "changes_requested", Checks: "success"}},
	}
	repoConflict := RepoGateRepo{
		RepoKey: "k-conflict", RepoLabel: "org/h-conflict", IsTarget: true,
		PRs: []RepoGatePR{{URL: "https://github.com/org/h-conflict/pull/7", State: "open", Conflicts: true, Review: "changes_requested"}},
	}
	repoCIFail := RepoGateRepo{
		RepoKey: "k-ci", RepoLabel: "org/i-ci", IsTarget: true,
		PRs: []RepoGatePR{{URL: "https://github.com/org/i-ci/pull/8", State: "open", Checks: "failure", Conflicts: true}},
	}
	repoReady := RepoGateRepo{
		RepoKey: "k-ready", RepoLabel: "org/j-ready", IsTarget: true,
		PRs: []RepoGatePR{{URL: "https://github.com/org/j-ready/pull/9", State: "merged"}},
	}

	cases := []struct {
		name         string
		repos        []RepoGateRepo
		wantState    string
		wantRepo     string
		wantPR       string
		wantReason   string
		wantTarget   int
		wantReady    int
		wantRefCount int
	}{
		{
			name:       "blocked_ci beats conflict and all others",
			repos:      []RepoGateRepo{repoReady, repoMissing, repoChecksPending, repoReviewReq, repoChanges, repoConflict, repoCIFail},
			wantState:  RepoGateBlockedCI,
			wantRepo:   "org/i-ci",
			wantPR:     "https://github.com/org/i-ci/pull/8",
			wantReason: "ci_failed",
			wantTarget: 7,
			wantReady:  1,
		},
		{
			name:       "blocked_conflict beats changes_requested",
			repos:      []RepoGateRepo{repoReady, repoMissing, repoChecksPending, repoReviewReq, repoChanges, repoConflict},
			wantState:  RepoGateBlockedConflict,
			wantRepo:   "org/h-conflict",
			wantPR:     "https://github.com/org/h-conflict/pull/7",
			wantReason: "merge_conflicts",
			wantTarget: 6,
			wantReady:  1,
		},
		{
			name:       "blocked_changes beats awaiting_review",
			repos:      []RepoGateRepo{repoReady, repoMissing, repoChecksPending, repoDraft, repoReviewReq, repoChanges},
			wantState:  RepoGateBlockedChanges,
			wantRepo:   "org/g-changes",
			wantPR:     "https://github.com/org/g-changes/pull/6",
			wantReason: "changes_requested",
			wantTarget: 6,
			wantReady:  1,
		},
		{
			name:       "awaiting_review draft beats checks_pending",
			repos:      []RepoGateRepo{repoReady, repoMissing, repoChecksPending, repoDraft},
			wantState:  RepoGateAwaitingReview,
			wantRepo:   "org/e-draft",
			wantPR:     "https://github.com/org/e-draft/pull/4",
			wantReason: "draft_pr",
			wantTarget: 4,
			wantReady:  1,
		},
		{
			name:       "awaiting_review required beats checks_pending",
			repos:      []RepoGateRepo{repoReady, repoMissing, repoChecksPending, repoReviewReq},
			wantState:  RepoGateAwaitingReview,
			wantRepo:   "org/f-review",
			wantPR:     "https://github.com/org/f-review/pull/5",
			wantReason: "review_required",
			wantTarget: 4,
			wantReady:  1,
		},
		{
			name:       "checks_pending beats missing_pr",
			repos:      []RepoGateRepo{repoReady, repoMissing, repoChecksPending},
			wantState:  RepoGateChecksPending,
			wantRepo:   "org/d-pending",
			wantPR:     "https://github.com/org/d-pending/pull/3",
			wantReason: "checks_pending",
			wantTarget: 3,
			wantReady:  1,
		},
		{
			name:       "open pr without approval counts as checks_pending over missing_pr",
			repos:      []RepoGateRepo{repoReady, repoMissing, repoOpen},
			wantState:  RepoGateChecksPending,
			wantRepo:   "org/c-open",
			wantPR:     "https://github.com/org/c-open/pull/2",
			wantReason: "pr_open",
			wantTarget: 3,
			wantReady:  1,
		},
		{
			name:       "missing_pr when target has no PRs",
			repos:      []RepoGateRepo{repoReady, repoMissing},
			wantState:  RepoGateMissingPR,
			wantRepo:   "org/a-missing",
			wantPR:     "",
			wantReason: "missing_pr",
			wantTarget: 2,
			wantReady:  1,
		},
		{
			name:       "closed-only PR treated as missing_pr",
			repos:      []RepoGateRepo{repoReady, repoClosedOnly},
			wantState:  RepoGateMissingPR,
			wantRepo:   "org/b-closed",
			wantPR:     "",
			wantReason: "missing_pr",
			wantTarget: 2,
			wantReady:  1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AggregateRepoGate(tc.repos)
			want := &RepoGate{
				State:          tc.wantState,
				TargetCount:    tc.wantTarget,
				ReadyCount:     tc.wantReady,
				ReferenceCount: tc.wantRefCount,
				BlockingRepo:   tc.wantRepo,
				BlockingPR:     tc.wantPR,
				Reason:         tc.wantReason,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestRepoGateDeterministicTieBreakingAcrossOrders(t *testing.T) {
	repos := []RepoGateRepo{
		{
			RepoKey:   "https://github.com/org/z-service",
			RepoLabel: "org/z-service",
			IsTarget:  true,
			PRs: []RepoGatePR{
				{URL: "https://github.com/org/z-service/pull/1", State: "open", Checks: "failure"},
			},
		},
		{
			RepoKey:   "https://github.com/org/a-service",
			RepoLabel: "org/a-service",
			IsTarget:  true,
			PRs: []RepoGatePR{
				{URL: "https://github.com/org/a-service/pull/9", State: "open", Checks: "failure"},
				{URL: "https://github.com/org/a-service/pull/2", State: "open", Checks: "failure"},
			},
		},
		{
			RepoKey:   "https://github.com/org/m-service",
			RepoLabel: "org/m-service",
			IsTarget:  true,
			PRs: []RepoGatePR{
				{URL: "https://github.com/org/m-service/pull/5", State: "merged"},
			},
		},
	}

	want := &RepoGate{
		State:          RepoGateBlockedCI,
		TargetCount:    3,
		ReadyCount:     1,
		ReferenceCount: 0,
		BlockingRepo:   "org/a-service",
		BlockingPR:     "https://github.com/org/a-service/pull/2",
		Reason:         "ci_failed",
	}

	if got := AggregateRepoGate(repos); !reflect.DeepEqual(got, want) {
		t.Fatalf("forward order: got %+v, want %+v", got, want)
	}

	reversed := slices.Clone(repos)
	slices.Reverse(reversed)
	if got := AggregateRepoGate(reversed); !reflect.DeepEqual(got, want) {
		t.Fatalf("reverse order: got %+v, want %+v", got, want)
	}
}
