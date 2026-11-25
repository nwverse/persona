// Package blockers implements a pure, extensible pull-based blocker evaluation engine
// for todos_v2.
//
// It is pure: no IO, no clock, no network, and no dependencies outside the Go standard library.
// Blockers are evaluated against a caller-supplied FactSnapshot via kind-keyed Resolvers
// registered in a Registry, so upstream targets never need to push notifications and new
// blocker kinds can be added without changing the database schema or reconciliation loop.
package blockers

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Kind identifies the semantic type of a blocker edge.
type Kind string

const (
	// KindTodoDone waits for another to-do to complete (or be accepted when required).
	KindTodoDone Kind = "todo_done"
	// KindTodoStage waits for another to-do to reach a stage and/or declare required contracts.
	KindTodoStage Kind = "todo_stage"
	// KindExternalPR waits for an external pull request to be merged or closed.
	KindExternalPR Kind = "external_pr"
	// KindWriteConflict waits for a conflicting to-do to finish and release its write scope.
	KindWriteConflict Kind = "write_conflict"
	// KindMergeTrain waits for a merge-train queue head to merge or leave the queue,
	// escalating when persistent attempts reach the configured threshold (default 2).
	KindMergeTrain Kind = "merge_train"
	// KindOwnerDecision waits for a to-do's pending owner decision card to clear.
	KindOwnerDecision Kind = "owner_decision"
)

// State represents the persisted lifecycle state of a blocker edge.
type State string

const (
	StateActive    State = "active"
	StateResolved  State = "resolved"
	StateCancelled State = "cancelled"
)

const (
	// DefaultMergeTrainMaxAttempts is the default attempt threshold before a merge_train blocker escalates.
	DefaultMergeTrainMaxAttempts = 2
)

var (
	// ErrInvalidKind is returned when registering a resolver with an empty kind.
	ErrInvalidKind = errors.New("blockers: kind is required")
	// ErrNilResolver is returned when registering a nil resolver.
	ErrNilResolver = errors.New("blockers: resolver is required")
	// ErrUnknownKind is returned when no resolver is registered for a blocker's kind.
	ErrUnknownKind = errors.New("blockers: unknown blocker kind")
)

// Blocker describes one persistent wait edge attached to a to-do.
type Blocker struct {
	ID            string `json:"id"`
	TodoID        string `json:"todo_id"`
	Kind          Kind   `json:"kind"`
	TargetRef     string `json:"target_ref"`
	ConditionJSON string `json:"condition_json,omitempty"`
	State         State  `json:"state"`
	Attempts      int    `json:"attempts,omitempty"`
}

// TodoFact captures ground-truth state for a single to-do in a FactSnapshot.
type TodoFact struct {
	ID              string   `json:"id"`
	Done            bool     `json:"done,omitempty"`
	Accepted        bool     `json:"accepted,omitempty"`
	Cancelled       bool     `json:"cancelled,omitempty"`
	Stage           string   `json:"stage,omitempty"`
	Contracts       []string `json:"contracts,omitempty"`
	HasActiveAgent  bool     `json:"has_active_agent,omitempty"`
	HasOpenDecision bool     `json:"has_open_decision,omitempty"`
}

// PRFact captures ground-truth forge state for a pull/merge request URL.
type PRFact struct {
	URL    string `json:"url"`
	State  string `json:"state"`
	Merged bool   `json:"merged,omitempty"`
}

// MergeTrainFact captures ground-truth queue state for a repository merge train.
type MergeTrainFact struct {
	RepoKey       string   `json:"repo_key"`
	HeadTodoID    string   `json:"head_todo_id,omitempty"`
	HeadState     string   `json:"head_state,omitempty"`
	ActiveTodoIDs []string `json:"active_todo_ids,omitempty"`
}

// FactSnapshot holds the point-in-time ground-truth facts consulted by pull-based Resolvers.
type FactSnapshot struct {
	Todos       map[string]TodoFact       `json:"todos,omitempty"`
	PRs         map[string]PRFact         `json:"prs,omitempty"`
	MergeTrains map[string]MergeTrainFact `json:"merge_trains,omitempty"`
}

// Resolution is the deterministic outcome returned by a Resolver for one active Blocker.
type Resolution struct {
	Resolved bool   `json:"resolved"`
	Escalate bool   `json:"escalate,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Resolver evaluates whether a single blocker edge is satisfied by facts.
type Resolver interface {
	Resolve(b Blocker, facts FactSnapshot) (Resolution, error)
}

// ResolverFunc adapts a plain function to the Resolver interface.
type ResolverFunc func(b Blocker, facts FactSnapshot) (Resolution, error)

// Resolve implements Resolver.
func (f ResolverFunc) Resolve(b Blocker, facts FactSnapshot) (Resolution, error) {
	return f(b, facts)
}

// Registry maps blocker kinds to their Resolver implementations.
type Registry struct {
	mu        sync.RWMutex
	resolvers map[Kind]Resolver
}

// NewRegistry creates an empty concurrency-safe Resolver registry.
func NewRegistry() *Registry {
	return &Registry{
		resolvers: make(map[Kind]Resolver),
	}
}

// DefaultRegistry returns a new Registry pre-populated with the 6 built-in blocker resolvers.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	_ = r.Register(KindTodoDone, ResolverFunc(resolveTodoDone))
	_ = r.Register(KindTodoStage, ResolverFunc(resolveTodoStage))
	_ = r.Register(KindExternalPR, ResolverFunc(resolveExternalPR))
	_ = r.Register(KindWriteConflict, ResolverFunc(resolveWriteConflict))
	_ = r.Register(KindMergeTrain, ResolverFunc(resolveMergeTrain))
	_ = r.Register(KindOwnerDecision, ResolverFunc(resolveOwnerDecision))
	return r
}

// Register binds resolver r to kind, replacing any prior registration for the same kind.
func (r *Registry) Register(kind Kind, res Resolver) error {
	k := Kind(strings.TrimSpace(string(kind)))
	if k == "" {
		return ErrInvalidKind
	}
	if res == nil {
		return ErrNilResolver
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolvers == nil {
		r.resolvers = make(map[Kind]Resolver)
	}
	r.resolvers[k] = res
	return nil
}

// Lookup returns the Resolver registered for kind.
func (r *Registry) Lookup(kind Kind) (Resolver, bool) {
	if r == nil {
		return nil, false
	}
	k := Kind(strings.TrimSpace(string(kind)))
	r.mu.RLock()
	defer r.mu.RUnlock()
	res, ok := r.resolvers[k]
	return res, ok
}

// BlockerDecision records the evaluation result for a single blocker in a Reconcile pass.
type BlockerDecision struct {
	BlockerID  string     `json:"blocker_id"`
	TodoID     string     `json:"todo_id"`
	Kind       Kind       `json:"kind"`
	TargetRef  string     `json:"target_ref"`
	Resolution Resolution `json:"resolution"`
	Error      string     `json:"error,omitempty"`
}

// ReconcileResult summarizes one deterministic pull-based reconciliation pass.
type ReconcileResult struct {
	ResolvedIDs      []string          `json:"resolved_ids,omitempty"`
	EscalatedIDs     []string          `json:"escalated_ids,omitempty"`
	UnblockedTodoIDs []string          `json:"unblocked_todo_ids,omitempty"`
	Decisions        []BlockerDecision `json:"decisions,omitempty"`
}

// SharedGroup aggregates active blockers waiting on the same target_ref across >= 2 distinct to-dos.
type SharedGroup struct {
	TargetRef  string   `json:"target_ref"`
	Count      int      `json:"count"`
	TodoIDs    []string `json:"todo_ids"`
	BlockerIDs []string `json:"blocker_ids"`
	Kinds      []Kind   `json:"kinds"`
}

// Reconcile evaluates all active blockers against facts using reg (defaulting to DefaultRegistry).
//
// A to-do appears in UnblockedTodoIDs if and only if it had at least one active blocker in active
// and every active blocker attached to that to-do resolved in this pass.
// All output slices are deterministically ordered regardless of input order.
func Reconcile(active []Blocker, facts FactSnapshot, reg *Registry) ReconcileResult {
	if reg == nil {
		reg = DefaultRegistry()
	}

	// Copy and sort active blockers deterministically by (ID, TodoID, TargetRef) before evaluation.
	candidates := make([]Blocker, 0, len(active))
	for _, b := range active {
		if b.State != "" && b.State != StateActive {
			continue
		}
		candidates = append(candidates, b)
	}
	slices.SortFunc(candidates, func(a, b Blocker) int {
		if c := cmp.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.TodoID, b.TodoID); c != 0 {
			return c
		}
		return cmp.Compare(a.TargetRef, b.TargetRef)
	})

	var res ReconcileResult
	if len(candidates) == 0 {
		return res
	}

	totalByTodo := make(map[string]int)
	resolvedByTodo := make(map[string]int)
	seenResolved := make(map[string]struct{})
	seenEscalated := make(map[string]struct{})

	for _, b := range candidates {
		todoID := strings.TrimSpace(b.TodoID)
		if todoID != "" {
			totalByTodo[todoID]++
		}

		resolver, ok := reg.Lookup(b.Kind)
		if !ok {
			res.Decisions = append(res.Decisions, BlockerDecision{
				BlockerID: b.ID,
				TodoID:    todoID,
				Kind:      b.Kind,
				TargetRef: b.TargetRef,
				Resolution: Resolution{
					Resolved: false,
					Reason:   "unknown_kind",
				},
				Error: fmt.Sprintf("%v: %s", ErrUnknownKind, b.Kind),
			})
			continue
		}

		outcome, err := resolver.Resolve(b, facts)
		if err != nil {
			res.Decisions = append(res.Decisions, BlockerDecision{
				BlockerID: b.ID,
				TodoID:    todoID,
				Kind:      b.Kind,
				TargetRef: b.TargetRef,
				Resolution: Resolution{
					Resolved: false,
					Reason:   "resolver_error",
				},
				Error: err.Error(),
			})
			continue
		}

		res.Decisions = append(res.Decisions, BlockerDecision{
			BlockerID:  b.ID,
			TodoID:     todoID,
			Kind:       b.Kind,
			TargetRef:  b.TargetRef,
			Resolution: outcome,
		})

		if outcome.Resolved {
			if todoID != "" {
				resolvedByTodo[todoID]++
			}
			if _, dup := seenResolved[b.ID]; !dup {
				seenResolved[b.ID] = struct{}{}
				res.ResolvedIDs = append(res.ResolvedIDs, b.ID)
			}
		} else if outcome.Escalate {
			if _, dup := seenEscalated[b.ID]; !dup {
				seenEscalated[b.ID] = struct{}{}
				res.EscalatedIDs = append(res.EscalatedIDs, b.ID)
			}
		}
	}

	for todoID, total := range totalByTodo {
		if total > 0 && resolvedByTodo[todoID] == total {
			res.UnblockedTodoIDs = append(res.UnblockedTodoIDs, todoID)
		}
	}

	slices.Sort(res.ResolvedIDs)
	slices.Sort(res.EscalatedIDs)
	slices.Sort(res.UnblockedTodoIDs)
	return res
}

// SharedActiveBlockers aggregates active blockers sharing the same target_ref across
// at least 2 distinct to-dos, sorted deterministically by Count descending then TargetRef ascending.
func SharedActiveBlockers(active []Blocker) []SharedGroup {
	type acc struct {
		todos    map[string]struct{}
		blockers map[string]struct{}
		kinds    map[Kind]struct{}
	}
	byTarget := make(map[string]*acc)
	for _, b := range active {
		if b.State != "" && b.State != StateActive {
			continue
		}
		target := strings.TrimSpace(b.TargetRef)
		todoID := strings.TrimSpace(b.TodoID)
		if target == "" || todoID == "" {
			continue
		}
		g := byTarget[target]
		if g == nil {
			g = &acc{
				todos:    make(map[string]struct{}),
				blockers: make(map[string]struct{}),
				kinds:    make(map[Kind]struct{}),
			}
			byTarget[target] = g
		}
		g.todos[todoID] = struct{}{}
		if b.ID != "" {
			g.blockers[b.ID] = struct{}{}
		}
		if b.Kind != "" {
			g.kinds[b.Kind] = struct{}{}
		}
	}

	var out []SharedGroup
	for target, g := range byTarget {
		if len(g.todos) < 2 {
			continue
		}
		todoIDs := make([]string, 0, len(g.todos))
		for id := range g.todos {
			todoIDs = append(todoIDs, id)
		}
		slices.Sort(todoIDs)

		blockerIDs := make([]string, 0, len(g.blockers))
		for id := range g.blockers {
			blockerIDs = append(blockerIDs, id)
		}
		slices.Sort(blockerIDs)

		kinds := make([]Kind, 0, len(g.kinds))
		for k := range g.kinds {
			kinds = append(kinds, k)
		}
		slices.Sort(kinds)

		out = append(out, SharedGroup{
			TargetRef:  target,
			Count:      len(todoIDs),
			TodoIDs:    todoIDs,
			BlockerIDs: blockerIDs,
			Kinds:      kinds,
		})
	}

	slices.SortFunc(out, func(a, b SharedGroup) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return cmp.Compare(a.TargetRef, b.TargetRef)
	})
	return out
}

func decodeCondition(raw string, dst any) error {
	s := strings.TrimSpace(raw)
	if s == "" || s == "{}" || s == "null" {
		return nil
	}
	return json.Unmarshal([]byte(s), dst)
}

func resolveTodoDone(b Blocker, facts FactSnapshot) (Resolution, error) {
	var cond struct {
		RequireAccepted bool `json:"require_accepted"`
	}
	if err := decodeCondition(b.ConditionJSON, &cond); err != nil {
		return Resolution{}, fmt.Errorf("todo_done condition_json: %w", err)
	}
	target := strings.TrimSpace(b.TargetRef)
	todo, ok := facts.Todos[target]
	if !ok {
		return Resolution{Resolved: false, Reason: "todo_not_found"}, nil
	}
	if todo.Cancelled {
		return Resolution{Resolved: false, Escalate: true, Reason: "prerequisite_cancelled"}, nil
	}
	if cond.RequireAccepted {
		if todo.Accepted {
			return Resolution{Resolved: true, Reason: "todo_accepted"}, nil
		}
		return Resolution{Resolved: false, Reason: "todo_not_accepted"}, nil
	}
	if todo.Done {
		return Resolution{Resolved: true, Reason: "todo_done"}, nil
	}
	return Resolution{Resolved: false, Reason: "todo_not_done"}, nil
}

var stageRank = map[string]int{
	"plan":           0,
	"contract_ready": 1,
	"develop":        2,
	"pr_open":        3,
	"merged":         4,
	"done":           5,
}

func normalizeStage(raw string) (string, int, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	rank, ok := stageRank[s]
	return s, rank, ok
}

func resolveTodoStage(b Blocker, facts FactSnapshot) (Resolution, error) {
	var cond struct {
		RequiredStage     string   `json:"required_stage"`
		RequiredContracts []string `json:"required_contracts"`
	}
	if err := decodeCondition(b.ConditionJSON, &cond); err != nil {
		return Resolution{}, fmt.Errorf("todo_stage condition_json: %w", err)
	}

	reqStageRaw := strings.TrimSpace(cond.RequiredStage)
	var reqContracts []string
	for _, c := range cond.RequiredContracts {
		if norm := strings.ToLower(strings.TrimSpace(c)); norm != "" {
			reqContracts = append(reqContracts, norm)
		}
	}
	if reqStageRaw == "" && len(reqContracts) == 0 {
		return Resolution{Resolved: false, Reason: "empty_stage_condition"}, nil
	}

	target := strings.TrimSpace(b.TargetRef)
	todo, ok := facts.Todos[target]
	if !ok {
		return Resolution{Resolved: false, Reason: "todo_not_found"}, nil
	}
	if todo.Cancelled {
		return Resolution{Resolved: false, Escalate: true, Reason: "prerequisite_cancelled"}, nil
	}

	if reqStageRaw != "" {
		_, reqRank, ok := normalizeStage(reqStageRaw)
		if !ok {
			return Resolution{Resolved: false, Reason: "unknown_required_stage"}, nil
		}
		curStage := todo.Stage
		if todo.Done && curStage == "" {
			curStage = "done"
		}
		_, curRank, curOK := normalizeStage(curStage)
		if todo.Done && (!curOK || curRank < stageRank["done"]) {
			curRank = stageRank["done"]
			curOK = true
		}
		if !curOK || curRank < reqRank {
			return Resolution{Resolved: false, Reason: "stage_not_reached"}, nil
		}
	}

	if len(reqContracts) > 0 {
		have := make(map[string]struct{}, len(todo.Contracts))
		for _, c := range todo.Contracts {
			if norm := strings.ToLower(strings.TrimSpace(c)); norm != "" {
				have[norm] = struct{}{}
			}
		}
		for _, req := range reqContracts {
			if _, present := have[req]; !present {
				return Resolution{Resolved: false, Reason: "missing_contracts"}, nil
			}
		}
	}

	return Resolution{Resolved: true, Reason: "stage_satisfied"}, nil
}

func normalizePRRef(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func lookupPRFact(prs map[string]PRFact, targetRef string) (PRFact, bool) {
	if len(prs) == 0 {
		return PRFact{}, false
	}
	if pr, ok := prs[targetRef]; ok {
		return pr, true
	}
	norm := normalizePRRef(targetRef)
	if pr, ok := prs[norm]; ok {
		return pr, true
	}
	for k, pr := range prs {
		if strings.EqualFold(normalizePRRef(k), norm) || (pr.URL != "" && strings.EqualFold(normalizePRRef(pr.URL), norm)) {
			return pr, true
		}
	}
	return PRFact{}, false
}

func resolveExternalPR(b Blocker, facts FactSnapshot) (Resolution, error) {
	var cond struct {
		RequireMerged bool `json:"require_merged"`
	}
	if err := decodeCondition(b.ConditionJSON, &cond); err != nil {
		return Resolution{}, fmt.Errorf("external_pr condition_json: %w", err)
	}

	pr, ok := lookupPRFact(facts.PRs, b.TargetRef)
	if !ok {
		return Resolution{Resolved: false, Reason: "pr_not_found"}, nil
	}
	state := strings.ToLower(strings.TrimSpace(pr.State))
	if pr.Merged || state == "merged" {
		return Resolution{Resolved: true, Reason: "pr_merged"}, nil
	}
	if state == "closed" {
		if cond.RequireMerged {
			return Resolution{Resolved: false, Escalate: true, Reason: "pr_closed_without_merge"}, nil
		}
		return Resolution{Resolved: true, Reason: "pr_closed"}, nil
	}
	return Resolution{Resolved: false, Reason: "pr_open"}, nil
}

func resolveWriteConflict(b Blocker, facts FactSnapshot) (Resolution, error) {
	target := strings.TrimSpace(b.TargetRef)
	todo, ok := facts.Todos[target]
	if !ok {
		return Resolution{Resolved: false, Reason: "todo_not_found"}, nil
	}
	if todo.Done || todo.Cancelled {
		return Resolution{Resolved: true, Reason: "write_scope_released"}, nil
	}
	return Resolution{Resolved: false, Reason: "write_scope_held"}, nil
}

func resolveMergeTrain(b Blocker, facts FactSnapshot) (Resolution, error) {
	var cond struct {
		MaxAttempts int    `json:"max_attempts"`
		RepoKey     string `json:"repo_key"`
	}
	if err := decodeCondition(b.ConditionJSON, &cond); err != nil {
		return Resolution{}, fmt.Errorf("merge_train condition_json: %w", err)
	}
	maxAttempts := cond.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMergeTrainMaxAttempts
	}
	if b.Attempts >= maxAttempts {
		return Resolution{Resolved: false, Escalate: true, Reason: "merge_train_max_attempts"}, nil
	}

	target := strings.TrimSpace(b.TargetRef)
	repoKey := strings.TrimSpace(cond.RepoKey)
	if repoKey == "" {
		repoKey = target
	}

	if train, ok := facts.MergeTrains[repoKey]; ok {
		headState := strings.ToLower(strings.TrimSpace(train.HeadState))
		if headState == "merged" || headState == "cancelled" {
			return Resolution{Resolved: true, Reason: "merge_train_head_cleared"}, nil
		}
		if target != "" && target != repoKey && train.HeadTodoID != target && !slices.Contains(train.ActiveTodoIDs, target) {
			return Resolution{Resolved: true, Reason: "merge_train_head_cleared"}, nil
		}
		if train.HeadTodoID == "" && len(train.ActiveTodoIDs) == 0 {
			return Resolution{Resolved: true, Reason: "merge_train_empty"}, nil
		}
	}

	if pr, ok := lookupPRFact(facts.PRs, target); ok {
		state := strings.ToLower(strings.TrimSpace(pr.State))
		if pr.Merged || state == "merged" || state == "closed" {
			return Resolution{Resolved: true, Reason: "merge_train_pr_cleared"}, nil
		}
	}

	if todo, ok := facts.Todos[target]; ok && (todo.Done || todo.Cancelled) {
		return Resolution{Resolved: true, Reason: "merge_train_todo_cleared"}, nil
	}

	return Resolution{Resolved: false, Reason: "merge_train_waiting"}, nil
}

func resolveOwnerDecision(b Blocker, facts FactSnapshot) (Resolution, error) {
	target := strings.TrimSpace(b.TargetRef)
	if target == "" {
		target = strings.TrimSpace(b.TodoID)
	}
	todo, ok := facts.Todos[target]
	if !ok {
		return Resolution{Resolved: false, Reason: "todo_not_found"}, nil
	}
	if !todo.HasOpenDecision {
		return Resolution{Resolved: true, Reason: "owner_decision_cleared"}, nil
	}
	return Resolution{Resolved: false, Reason: "owner_decision_pending"}, nil
}
