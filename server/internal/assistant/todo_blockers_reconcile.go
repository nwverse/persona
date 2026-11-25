package assistant

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/nwverse/persona/server/internal/assistant/todo/stagegate"
	"github.com/nwverse/persona/server/internal/db"
)

// todoBlockerStore is the narrow persistence seam for persistent todos_v2 blockers (#15731).
// Production *db.DB satisfies it directly without widening the core Store interface.
type todoBlockerStore interface {
	UpsertTodoBlocker(ctx context.Context, r db.TodoBlockerRow) (*db.TodoBlockerRow, error)
	ListActiveTodoBlockers(ctx context.Context) ([]db.TodoBlockerRow, error)
	ListTodoBlockersByTodo(ctx context.Context, todoID string) ([]db.TodoBlockerRow, error)
	ListActiveTodoBlockersByTarget(ctx context.Context, targetRef string) ([]db.TodoBlockerRow, error)
	ResolveTodoBlocker(ctx context.Context, id string, at time.Time) (*db.TodoBlockerRow, error)
	CancelTodoBlocker(ctx context.Context, id string, at time.Time) (*db.TodoBlockerRow, error)
	IncrementTodoBlockerAttempts(ctx context.Context, id string, at time.Time) (*db.TodoBlockerRow, error)
}

var _ todoBlockerStore = (*db.DB)(nil)

func (s *Service) todoBlockerDB() todoBlockerStore {
	if s == nil {
		return nil
	}
	st, _ := s.store().(todoBlockerStore)
	return st
}

// todoBlockerReconcileSummary reports the persisted and notification outcomes of one
// pull-based blocker reconciliation pass under todos_v2.
type todoBlockerReconcileSummary struct {
	Evaluated        int
	ResolvedIDs      []string
	EscalatedIDs     []string
	UnblockedTodoIDs []string
	SharedGroups     []todoBlockerSharedGroup
}

type todoBlockerHooks struct {
	mu          sync.RWMutex
	registry    *blockerRegistry
	onUnblocked func(ctx context.Context, todoID string)
	fetchPR     func(ctx context.Context, targetRef string) (todoBlockerPRFact, bool)
}

// todoBlockerHookRegistry isolates optional per-Service blocker hooks without modifying Service fields
// and cleans up entries automatically when a Service is garbage-collected.
var todoBlockerHookRegistry = struct {
	mu    sync.Mutex
	bySvc map[weak.Pointer[Service]]*todoBlockerHooks
}{
	bySvc: make(map[weak.Pointer[Service]]*todoBlockerHooks),
}

func (s *Service) todoBlockerHooks(create bool) *todoBlockerHooks {
	if s == nil {
		return nil
	}
	wp := weak.Make(s)
	todoBlockerHookRegistry.mu.Lock()
	defer todoBlockerHookRegistry.mu.Unlock()
	if h, ok := todoBlockerHookRegistry.bySvc[wp]; ok {
		return h
	}
	if !create {
		return nil
	}
	h := &todoBlockerHooks{}
	todoBlockerHookRegistry.bySvc[wp] = h
	runtime.AddCleanup(s, func(key weak.Pointer[Service]) {
		todoBlockerHookRegistry.mu.Lock()
		delete(todoBlockerHookRegistry.bySvc, key)
		todoBlockerHookRegistry.mu.Unlock()
	}, wp)
	return h
}

// setTodoBlockerRegistry installs a custom blockerRegistry on s (tests / extensions).
func (s *Service) setTodoBlockerRegistry(reg *blockerRegistry) {
	if h := s.todoBlockerHooks(true); h != nil {
		h.mu.Lock()
		h.registry = reg
		h.mu.Unlock()
	}
}

// setTodoBlockerUnblockedHook installs a callback fired whenever a to-do's final active blocker resolves.
func (s *Service) setTodoBlockerUnblockedHook(fn func(ctx context.Context, todoID string)) {
	if h := s.todoBlockerHooks(true); h != nil {
		h.mu.Lock()
		h.onUnblocked = fn
		h.mu.Unlock()
	}
}

// setTodoBlockerPRFetcher installs a custom PR fact fetcher for external_pr blockers.
func (s *Service) setTodoBlockerPRFetcher(fn func(ctx context.Context, targetRef string) (todoBlockerPRFact, bool)) {
	if h := s.todoBlockerHooks(true); h != nil {
		h.mu.Lock()
		h.fetchPR = fn
		h.mu.Unlock()
	}
}

// addTodoBlocker persists a blocker edge when todos_v2 is enabled.
// When todos_v2 is disabled, it bypasses immediately with zero side effects.
func (s *Service) addTodoBlocker(ctx context.Context, row db.TodoBlockerRow) (*db.TodoBlockerRow, error) {
	if s == nil || !s.settings(ctx).todosV2On() {
		return nil, nil
	}
	st := s.todoBlockerDB()
	if st == nil {
		return nil, nil
	}
	return st.UpsertTodoBlocker(ctx, row)
}

// incrementTodoBlockerAttempts increments the persistent attempt counter for blocker id under todos_v2.
func (s *Service) incrementTodoBlockerAttempts(ctx context.Context, id string) (*db.TodoBlockerRow, error) {
	if s == nil || !s.settings(ctx).todosV2On() {
		return nil, nil
	}
	st := s.todoBlockerDB()
	if st == nil {
		return nil, nil
	}
	return st.IncrementTodoBlockerAttempts(ctx, id, time.Now().UTC())
}

// cancelTodoBlocker cancels blocker id under todos_v2.
func (s *Service) cancelTodoBlocker(ctx context.Context, id string) (*db.TodoBlockerRow, error) {
	if s == nil || !s.settings(ctx).todosV2On() {
		return nil, nil
	}
	st := s.todoBlockerDB()
	if st == nil {
		return nil, nil
	}
	return st.CancelTodoBlocker(ctx, id, time.Now().UTC())
}

// sharedActiveTodoBlockerGroups returns active blockers aggregated by shared target_ref (count >= 2)
// when todos_v2 is enabled, or nil when todos_v2 is disabled.
func (s *Service) sharedActiveTodoBlockerGroups(ctx context.Context) ([]todoBlockerSharedGroup, error) {
	if s == nil || !s.settings(ctx).todosV2On() {
		return nil, nil
	}
	st := s.todoBlockerDB()
	if st == nil {
		return nil, nil
	}
	rows, err := st.ListActiveTodoBlockers(ctx)
	if err != nil {
		return nil, err
	}
	return sharedActiveTodoBlockers(dbRowsToTodoBlockers(rows)), nil
}

// reconcilePersistedTodoBlockers performs a pull-based scan of persisted active blockers under todos_v2.
//
// When targetRef is empty, all active blockers are scanned (full fallback sweep).
// When targetRef is non-empty, only to-dos with an active blocker on targetRef are scanned,
// while still loading all active blockers for those affected to-dos so UnblockedTodoIDs only
// emits a to-do once every one of its active blockers has resolved.
//
// When todos_v2 is disabled, this method returns an empty summary with zero DB or side-effect work.
func (s *Service) reconcilePersistedTodoBlockers(ctx context.Context, targetRef string) (todoBlockerReconcileSummary, error) {
	if s == nil || !s.settings(ctx).todosV2On() {
		return todoBlockerReconcileSummary{}, nil
	}
	st := s.todoBlockerDB()
	if st == nil {
		return todoBlockerReconcileSummary{}, nil
	}

	targetRef = strings.TrimSpace(targetRef)
	var rows []db.TodoBlockerRow
	var err error
	if targetRef == "" {
		rows, err = st.ListActiveTodoBlockers(ctx)
		if err != nil {
			return todoBlockerReconcileSummary{}, err
		}
	} else {
		targeted, listErr := st.ListActiveTodoBlockersByTarget(ctx, targetRef)
		if listErr != nil {
			return todoBlockerReconcileSummary{}, listErr
		}
		if len(targeted) == 0 {
			return todoBlockerReconcileSummary{}, nil
		}
		seenTodos := make(map[string]struct{}, len(targeted))
		seenIDs := make(map[string]struct{}, len(targeted))
		for _, r := range targeted {
			if _, ok := seenTodos[r.TodoID]; ok {
				continue
			}
			seenTodos[r.TodoID] = struct{}{}
			todoRows, todoErr := st.ListTodoBlockersByTodo(ctx, r.TodoID)
			if todoErr != nil {
				return todoBlockerReconcileSummary{}, todoErr
			}
			for _, tr := range todoRows {
				if tr.State != db.TodoBlockerStateActive {
					continue
				}
				if _, dup := seenIDs[tr.ID]; dup {
					continue
				}
				seenIDs[tr.ID] = struct{}{}
				rows = append(rows, tr)
			}
		}
	}

	if len(rows) == 0 {
		return todoBlockerReconcileSummary{}, nil
	}

	active := dbRowsToTodoBlockers(rows)
	facts, err := s.buildBlockerFactSnapshot(ctx, active)
	if err != nil {
		return todoBlockerReconcileSummary{}, err
	}

	var reg *blockerRegistry
	var onUnblocked func(ctx context.Context, todoID string)
	if h := s.todoBlockerHooks(false); h != nil {
		h.mu.RLock()
		reg = h.registry
		onUnblocked = h.onUnblocked
		h.mu.RUnlock()
	}

	result := reconcileTodoBlockers(active, facts, reg)

	now := time.Now().UTC()
	resolvedSet := make(map[string]struct{}, len(result.ResolvedIDs))
	for _, id := range result.ResolvedIDs {
		if _, err := st.ResolveTodoBlocker(ctx, id, now); err != nil {
			return todoBlockerReconcileSummary{}, err
		}
		resolvedSet[id] = struct{}{}
	}

	remaining := make([]todoBlockerRecord, 0, len(active))
	for _, b := range active {
		if _, done := resolvedSet[b.ID]; !done {
			remaining = append(remaining, b)
		}
	}

	for _, todoID := range result.UnblockedTodoIDs {
		s.notifyTodoBlockerUnblocked(ctx, todoID, onUnblocked)
	}

	return todoBlockerReconcileSummary{
		Evaluated:        len(active),
		ResolvedIDs:      result.ResolvedIDs,
		EscalatedIDs:     result.EscalatedIDs,
		UnblockedTodoIDs: result.UnblockedTodoIDs,
		SharedGroups:     sharedActiveTodoBlockers(remaining),
	}, nil
}

func (s *Service) notifyTodoBlockerUnblocked(ctx context.Context, todoID string, hook func(ctx context.Context, todoID string)) {
	s.signal(superviseSignal{
		Todo:   todoID,
		Kind:   sigTodoReady,
		Key:    "blockers_unblocked:" + todoID,
		Detail: "all persistent blockers resolved",
	})
	if hook != nil {
		hook(ctx, todoID)
	}
}

func (s *Service) buildBlockerFactSnapshot(ctx context.Context, active []todoBlockerRecord) (todoBlockerFactSnapshot, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return todoBlockerFactSnapshot{}, err
	}

	asks := s.todoAsks(ctx)
	facts := todoBlockerFactSnapshot{
		Todos:       make(map[string]todoBlockerTodoFact),
		PRs:         make(map[string]todoBlockerPRFact),
		MergeTrains: make(map[string]todoBlockerMergeTrainFact),
	}

	s.todos.mu.RLock()
	for id, t := range s.todos.todos {
		cp := copyTodoLocked(t)
		cancelled := cp.Cancelled()
		accepted := cp.DoneAt != nil && cp.DoneBy == db.DoneByReview &&
			cp.Supervision != nil && cp.Supervision.state(cp) == reviewAccepted
		stage, contracts := parseTodoStageAndContracts(cp)
		if norm, ok := stagegate.NormalizeStage(cp.Stage); ok {
			stage = maxStageGateStage(stage, norm)
		} else {
			switch strings.ToLower(strings.TrimSpace(cp.Stage)) {
			case "review":
				stage = maxStageGateStage(stage, stagegate.StagePROpen)
			case "merge", "deploy", "verify":
				stage = maxStageGateStage(stage, stagegate.StageMerged)
			}
		}
		if cp.DoneAt != nil {
			stage = maxStageGateStage(stage, stagegate.StageDone)
		}
		_, hasAsk := asks[id]
		facts.Todos[id] = todoBlockerTodoFact{
			ID:              id,
			Done:            cp.DoneAt != nil,
			Accepted:        accepted,
			Cancelled:       cancelled,
			Stage:           string(stage),
			Contracts:       contracts,
			HasActiveAgent:  todoHasActiveAgent(cp),
			HasOpenDecision: hasAsk,
		}
		if cp.PRStatus != nil && strings.TrimSpace(cp.PRStatus.URL) != "" {
			u := strings.TrimRight(strings.TrimSpace(cp.PRStatus.URL), "/")
			facts.PRs[u] = todoBlockerPRFact{
				URL:    u,
				State:  cp.PRStatus.State,
				Merged: strings.EqualFold(cp.PRStatus.State, "merged"),
			}
		}
		for _, other := range cp.OtherPRs {
			if strings.TrimSpace(other.URL) == "" {
				continue
			}
			u := strings.TrimRight(strings.TrimSpace(other.URL), "/")
			facts.PRs[u] = todoBlockerPRFact{
				URL:    u,
				State:  other.State,
				Merged: strings.EqualFold(other.State, "merged"),
			}
		}
	}
	s.todos.mu.RUnlock()

	snap := s.mergeTrainSnapshot(ctx)
	for _, repo := range snap.Repos {
		mt := todoBlockerMergeTrainFact{
			RepoKey: repo.RepoKey,
		}
		if repo.Head != nil {
			mt.HeadTodoID = repo.Head.TodoID
			mt.HeadState = string(repo.Head.State)
			mt.ActiveTodoIDs = append(mt.ActiveTodoIDs, repo.Head.TodoID)
		}
		for _, q := range repo.Queue {
			mt.ActiveTodoIDs = append(mt.ActiveTodoIDs, q.TodoID)
		}
		facts.MergeTrains[repo.RepoKey] = mt
	}

	// Pull live PR truth for any external_pr blocker not already known to be terminal.
	queriedPRs := make(map[string]struct{})
	for _, b := range active {
		if b.Kind != todoBlockerKindExternalPR {
			continue
		}
		norm := strings.TrimRight(strings.TrimSpace(b.TargetRef), "/")
		if norm == "" {
			continue
		}
		if existing, ok := facts.PRs[norm]; ok {
			st := strings.ToLower(strings.TrimSpace(existing.State))
			if existing.Merged || st == "merged" || st == "closed" {
				continue
			}
		}
		if _, seen := queriedPRs[norm]; seen {
			continue
		}
		queriedPRs[norm] = struct{}{}
		if pr, ok := s.fetchBlockerPRFact(ctx, norm); ok {
			facts.PRs[norm] = pr
		}
	}

	return facts, nil
}

func (s *Service) fetchBlockerPRFact(ctx context.Context, targetRef string) (todoBlockerPRFact, bool) {
	if h := s.todoBlockerHooks(false); h != nil {
		h.mu.RLock()
		fn := h.fetchPR
		h.mu.RUnlock()
		if fn != nil {
			return fn(ctx, targetRef)
		}
	}

	f, ok := s.forge().(prStatusForge)
	if !ok {
		return todoBlockerPRFact{}, false
	}
	m := ciPRURL.FindStringSubmatch(targetRef)
	if m == nil {
		return todoBlockerPRFact{}, false
	}
	number, err := strconv.Atoi(m[2])
	if err != nil || number <= 0 {
		return todoBlockerPRFact{}, false
	}
	st, err := f.PRStatus(ctx, m[1], number)
	if err != nil {
		return todoBlockerPRFact{}, false
	}
	return todoBlockerPRFact{
		URL:    targetRef,
		State:  st.State,
		Merged: strings.EqualFold(st.State, "merged"),
	}, true
}

func dbRowsToTodoBlockers(rows []db.TodoBlockerRow) []todoBlockerRecord {
	out := make([]todoBlockerRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, todoBlockerRecord{
			ID:            r.ID,
			TodoID:        r.TodoID,
			Kind:          r.Kind,
			TargetRef:     r.TargetRef,
			ConditionJSON: r.ConditionJSON,
			State:         r.State,
			Attempts:      r.Attempts,
		})
	}
	return out
}
