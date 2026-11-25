package projection

import "strings"

// Weakest-link delivery gate states across a to-do's bound Target Repos under todos_v2.
const (
	RepoGateBlockedCI       = "blocked_ci"
	RepoGateBlockedConflict = "blocked_conflict"
	RepoGateBlockedChanges  = "blocked_changes"
	RepoGateAwaitingReview  = "awaiting_review"
	RepoGateChecksPending   = "checks_pending"
	RepoGateMissingPR       = "missing_pr"
	RepoGateReady           = "ready"
	RepoGateReferenceOnly   = "reference_only"
)

// RepoGatePR is a single delivery pull request observation belonging to a bound repository.
type RepoGatePR struct {
	URL       string
	State     string // "open", "merged", "closed", or "" (unobserved)
	Draft     bool
	Review    string // "approved", "changes_requested", "required", "pending", or ""
	Checks    string // "success", "failure", "pending", "none", or ""
	Conflicts bool
}

// RepoGateRepo is one bound repository's delivery gate input for AggregateRepoGate.
type RepoGateRepo struct {
	RepoKey   string       // deterministic canonical identifier (e.g. canonical URL or repo ID)
	RepoLabel string       // human-readable repository name (e.g. "owner/repo")
	IsTarget  bool         // true when the repo has delivery/default links or no links; false for reference-only repos
	PRs       []RepoGatePR // delivery PRs belonging to this repository
}

// RepoGate is the weakest-link ("barrel rule") delivery gate summary across a to-do's bound Target Repos.
type RepoGate struct {
	State          string `json:"state"`
	TargetCount    int    `json:"target_count"`
	ReadyCount     int    `json:"ready_count"`
	ReferenceCount int    `json:"reference_count"`
	BlockingRepo   string `json:"blocking_repo,omitempty"`
	BlockingPR     string `json:"blocking_pr,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

type repoGateEval struct {
	severity int
	state    string
	repo     string
	pr       string
	reason   string
}

const (
	gateSeverityReady           = 0
	gateSeverityMissingPR       = 1
	gateSeverityChecksPending   = 2
	gateSeverityAwaitingReview  = 3
	gateSeverityBlockedChanges  = 4
	gateSeverityBlockedConflict = 5
	gateSeverityBlockedCI       = 6
)

// AggregateRepoGate computes a deterministic weakest-link ("barrel rule") delivery gate summary
// across a to-do's bound repositories. Reference-only repositories (IsTarget == false) are counted
// in ReferenceCount and strictly ignored for blocking and readiness calculations.
// Returns nil when repos is empty.
func AggregateRepoGate(repos []RepoGateRepo) *RepoGate {
	if len(repos) == 0 {
		return nil
	}

	var targetCount, readyCount, refCount int
	var worst *repoGateEval

	for _, repo := range repos {
		if !repo.IsTarget {
			refCount++
			continue
		}
		targetCount++
		ev := evaluateTargetRepo(repo)
		if ev.severity == gateSeverityReady {
			readyCount++
			continue
		}
		if worst == nil || isWeakerEval(ev, *worst) {
			cp := ev
			worst = &cp
		}
	}

	if targetCount == 0 {
		return &RepoGate{
			State:          RepoGateReferenceOnly,
			TargetCount:    0,
			ReadyCount:     0,
			ReferenceCount: refCount,
			Reason:         "reference_only",
		}
	}

	if worst == nil {
		return &RepoGate{
			State:          RepoGateReady,
			TargetCount:    targetCount,
			ReadyCount:     readyCount,
			ReferenceCount: refCount,
			Reason:         "all_targets_ready",
		}
	}

	return &RepoGate{
		State:          worst.state,
		TargetCount:    targetCount,
		ReadyCount:     readyCount,
		ReferenceCount: refCount,
		BlockingRepo:   worst.repo,
		BlockingPR:     worst.pr,
		Reason:         worst.reason,
	}
}

func evaluateTargetRepo(repo RepoGateRepo) repoGateEval {
	name := strings.TrimSpace(repo.RepoLabel)
	if name == "" {
		name = strings.TrimSpace(repo.RepoKey)
	}

	var worst *repoGateEval
	activeOrMerged := 0

	for _, pr := range repo.PRs {
		state := strings.ToLower(strings.TrimSpace(pr.State))
		if state == "closed" {
			continue
		}
		activeOrMerged++
		ev := evaluateTargetPR(name, pr)
		if worst == nil || isWeakerEval(ev, *worst) {
			cp := ev
			worst = &cp
		}
	}

	if activeOrMerged == 0 {
		return repoGateEval{
			severity: gateSeverityMissingPR,
			state:    RepoGateMissingPR,
			repo:     name,
			reason:   "missing_pr",
		}
	}
	return *worst
}

func evaluateTargetPR(repoName string, pr RepoGatePR) repoGateEval {
	url := strings.TrimSpace(pr.URL)
	state := strings.ToLower(strings.TrimSpace(pr.State))
	review := strings.ToLower(strings.TrimSpace(pr.Review))
	checks := strings.ToLower(strings.TrimSpace(pr.Checks))

	if state == "merged" || state == "ready" {
		return repoGateEval{
			severity: gateSeverityReady,
			state:    RepoGateReady,
			repo:     repoName,
			pr:       url,
		}
	}

	if isRepoGateCheckFailure(checks) {
		return repoGateEval{
			severity: gateSeverityBlockedCI,
			state:    RepoGateBlockedCI,
			repo:     repoName,
			pr:       url,
			reason:   "ci_failed",
		}
	}
	if pr.Conflicts {
		return repoGateEval{
			severity: gateSeverityBlockedConflict,
			state:    RepoGateBlockedConflict,
			repo:     repoName,
			pr:       url,
			reason:   "merge_conflicts",
		}
	}
	if review == "changes_requested" {
		return repoGateEval{
			severity: gateSeverityBlockedChanges,
			state:    RepoGateBlockedChanges,
			repo:     repoName,
			pr:       url,
			reason:   "changes_requested",
		}
	}
	if pr.Draft {
		return repoGateEval{
			severity: gateSeverityAwaitingReview,
			state:    RepoGateAwaitingReview,
			repo:     repoName,
			pr:       url,
			reason:   "draft_pr",
		}
	}
	if review == "required" || review == "review_required" || review == "pending" {
		return repoGateEval{
			severity: gateSeverityAwaitingReview,
			state:    RepoGateAwaitingReview,
			repo:     repoName,
			pr:       url,
			reason:   "review_required",
		}
	}
	if checks == "pending" {
		return repoGateEval{
			severity: gateSeverityChecksPending,
			state:    RepoGateChecksPending,
			repo:     repoName,
			pr:       url,
			reason:   "checks_pending",
		}
	}
	if review == "approved" && (checks == "success" || checks == "none") {
		return repoGateEval{
			severity: gateSeverityReady,
			state:    RepoGateReady,
			repo:     repoName,
			pr:       url,
		}
	}
	return repoGateEval{
		severity: gateSeverityChecksPending,
		state:    RepoGateChecksPending,
		repo:     repoName,
		pr:       url,
		reason:   "pr_open",
	}
}

func isRepoGateCheckFailure(checks string) bool {
	switch checks {
	case "failure", "error", "cancelled", "timed_out", "action_required", "startup_failure", "stale":
		return true
	default:
		return false
	}
}

// isWeakerEval reports whether candidate should beat current as the weakest link:
// strictly higher severity wins; ties break deterministically by lexicographically smallest
// repository label, then PR URL, then reason.
func isWeakerEval(candidate, current repoGateEval) bool {
	if candidate.severity != current.severity {
		return candidate.severity > current.severity
	}
	if candidate.repo != current.repo {
		return candidate.repo < current.repo
	}
	if candidate.pr != current.pr {
		return candidate.pr < current.pr
	}
	return candidate.reason < current.reason
}
