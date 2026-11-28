package assistant

import (
	"context"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/assistant/todo/stagegate"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
)

type todoReadiness struct {
	prerequisites []prompts.SupervisePrerequisite
	after         []string
	active        []db.TodoBlockerRow
	missingPRs    []string
}

type todoStartBlocked struct {
	refs []string
	err  error
}

// ensureApprovedPRBlockers retries any missing writes from an approved plan. The plan records
// the owner's prerequisites across a restart or a Labs setting change.
func (s *Service) ensureApprovedPRBlockers(ctx context.Context, todoID string, refs []string) {
	if len(refs) == 0 {
		return
	}
	st := s.todoBlockerDB()
	if st == nil {
		return
	}
	changed := false
	for _, ref := range refs {
		row, err := st.EnsureTodoBlocker(ctx, db.TodoBlockerRow{
			ID: todoBlockerID(todoID, todoBlockerKindExternalPR, ref), TodoID: todoID,
			Kind: todoBlockerKindExternalPR, TargetRef: ref, State: db.TodoBlockerStateActive,
		})
		if err != nil {
			s.logf("task %s: save approved PR prerequisite: %v", todoID, err)
			break
		}
		changed = changed || row != nil
	}
	if changed {
		s.todosChanged(ctx)
	}
}

func (*todoStartBlocked) Error() string { return "task prerequisites are not ready" }

func (s *Service) checkTodoStartReadiness(ctx context.Context, task PlanTask) error {
	todo, ok := s.todoNow(task.TodoID)
	if !ok {
		todo.ID, todo.PartOf = task.TodoID, task.PartOf
	}
	readiness, err := s.taskTodoReadiness(ctx, todo, task)
	if err != nil || !readiness.ready() {
		return &todoStartBlocked{refs: readiness.refs(), err: err}
	}
	return nil
}

func (r todoReadiness) ready() bool {
	return len(r.after) == 0 && len(r.active) == 0 && len(r.missingPRs) == 0
}

func (r todoReadiness) refs() []string {
	refs := slices.Concat(r.after, r.missingPRs)
	for _, row := range r.active {
		refs = append(refs, row.TargetRef)
	}
	return cleanIDs(refs)
}

func (s *Service) taskTodoReadiness(ctx context.Context, todo Todo, task PlanTask) (todoReadiness, error) {
	if todo.ID == "" {
		todo.ID, todo.PartOf = task.TodoID, task.PartOf
	}
	var rows []db.TodoBlockerRow
	if st := s.todoBlockerDB(); st != nil && todo.ID != "" {
		var err error
		rows, err = st.ListTodoBlockersByTodo(ctx, todo.ID)
		if err != nil {
			return todoReadiness{}, err
		}
	}
	return s.evaluateTodoReadiness(ctx, todo, task, rows, s.settings(ctx).todosV2On())
}

// evaluateTodoReadiness joins the live after graph, any prerequisites kept in an approved plan,
// and persisted blockers. The caller must have successfully read the blocker rows.
func (s *Service) evaluateTodoReadiness(ctx context.Context, todo Todo, task PlanTask, rows []db.TodoBlockerRow, v2 bool) (todoReadiness, error) {
	todo.After = cleanIDs(slices.Concat(todo.After, task.After))
	result, err := s.evaluateTodoAfter(ctx, todo, task, rows, v2)
	if err != nil {
		return todoReadiness{}, err
	}
	knownPRs := make(map[string]bool, len(task.BlockedByPR))
	for _, row := range rows {
		if row.Kind == todoBlockerKindExternalPR {
			knownPRs[strings.ToLower(normalizeTodoBlockerPRRef(row.TargetRef))] = true
		}
		if row.State != db.TodoBlockerStateActive || !v2 && row.Kind != todoBlockerKindExternalPR {
			continue
		}
		// A mirror and its after edge represent the same prerequisite. The live verdict also
		// accounts for reopened prerequisites after a mirror has already been resolved.
		if isTodoAfterMirror(row, todo.After) && !slices.Contains(result.after, row.TargetRef) {
			continue
		}
		result.active = append(result.active, row)
	}
	for _, ref := range task.BlockedByPR {
		if !knownPRs[strings.ToLower(normalizeTodoBlockerPRRef(ref))] {
			result.missingPRs = append(result.missingPRs, ref)
		}
	}
	return result, nil
}

func isTodoAfterMirror(row db.TodoBlockerRow, after []string) bool {
	return (row.Kind == todoBlockerKindTodoDone || row.Kind == todoBlockerKindTodoStage) &&
		slices.Contains(after, row.TargetRef) && row.ID == todoBlockerID(row.TodoID, row.Kind, row.TargetRef)
}

func (s *Service) evaluateTodoAfter(ctx context.Context, todo Todo, task PlanTask, rows []db.TodoBlockerRow, v2 bool) (todoReadiness, error) {
	if len(todo.After) == 0 && task.RequestGate == "" {
		return todoReadiness{}, nil
	}
	if task.RequestGate == "" && todo.PartOf != "" && s.hasRequestGate(todo.PartOf) {
		task.RequestGate = todo.PartOf
	}
	if task.RequestGate == "" && v2 {
		return s.evaluateTodoAfterStageGate(ctx, todo, stageRuleForAfter(todo, task, rows))
	}
	requireAccepted := task.RequestGate != ""
	var result todoReadiness
	if requireAccepted {
		task.TodoID = todo.ID
		task.After = todo.After
		result.after = s.requestBlockers(ctx, task)
		if len(result.after) > 0 {
			return result, nil
		}
	}
	for _, id := range todo.After {
		if dep, ok := s.todoNow(id); ok {
			p, satisfied, err := s.prerequisiteSatisfied(ctx, dep, requireAccepted)
			if err != nil {
				return todoReadiness{}, err
			}
			if satisfied {
				result.prerequisites = append(result.prerequisites, p)
				continue
			}
		}
		result.after = append(result.after, id)
	}
	return result, nil
}

func stageRuleForAfter(todo Todo, task PlanTask, rows []db.TodoBlockerRow) *todoStageUnlockRule {
	stage, _ := stagegate.NormalizeStage(task.UnlockStage)
	if stage != "" || len(task.UnlockContracts) > 0 {
		rule := todoStageUnlockRule{Stage: stage, Contracts: stagegate.NormalizeContracts(task.UnlockContracts)}
		return &rule
	}
	for _, row := range rows {
		if row.Kind == todoBlockerKindTodoStage && row.State != db.TodoBlockerStateCancelled && isTodoAfterMirror(row, todo.After) {
			if rule, ok := decodeTodoStageBlockerCondition(row.ConditionJSON); ok {
				return &rule
			}
		}
	}
	return nil
}
