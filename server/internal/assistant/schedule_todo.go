package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// ScheduleTodoAssessment describes observable evidence, not acceptance of the work.
type ScheduleTodoAssessment struct {
	TodoID     string `json:"todo_id"`
	State      string `json:"state"` // pending, progress, no_progress, unknown, inactive, not_run, superseded
	NoProgress int    `json:"no_progress,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type scheduleTodoSnapshot struct {
	Answers  map[string]string `json:"answers,omitempty"`
	Criteria map[string]string `json:"criteria,omitempty"`
	Report   string            `json:"report,omitempty"`
	Unknown  bool              `json:"unknown,omitempty"`
}

// Kept with the schedule, not just the capped, shared run history.
type scheduleTodoProgress struct {
	Run        string                 `json:"run"`
	At         time.Time              `json:"at"`
	Snapshot   scheduleTodoSnapshot   `json:"snapshot"`
	Assessment ScheduleTodoAssessment `json:"assessment"`
	NoProgress int                    `json:"no_progress"`
	Signaled   bool                   `json:"signaled,omitempty"`
	Escalated  bool                   `json:"escalated,omitempty"`
	// Kept before creating the review, so a failed final marker write can recognize its own decision.
	EscalationRun string `json:"escalation_run,omitempty"`
}

func (s *Service) validateScheduleTodo(ctx context.Context, self string, id *string) error {
	if id == nil || strings.TrimSpace(*id) == "" {
		return nil
	}
	if self == "" {
		return errors.New("a supervising conversation is required for todo_id")
	}
	*id = strings.TrimSpace(*id)
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}
	t, ok := s.todoNow(*id)
	t = s.withBrief(t)
	if !ok || !scheduledTodoOpen(t) {
		return errors.New("todo_id must name an open, supervised to-do")
	}
	if t.Origin != self && t.Supervision.Supervisor != self && !s.isMain(ctx, self) &&
		!s.backgroundSupervises(ctx, self, t) && !s.mayReviewForLeader(ctx, self, t) {
		return errors.New("this conversation does not supervise todo_id")
	}
	return nil
}

func bindScheduleTodo(t *Task, id *string) error {
	if id == nil {
		return nil
	}
	value := strings.TrimSpace(*id)
	if value != "" && t.Repeat == repeatOnce {
		return errors.New("todo_id progress tracking requires a recurring schedule")
	}
	if t.TodoID != value {
		t.TodoID, t.TodoProgress = value, nil
	}
	return nil
}

func scheduledTodoOpen(t Todo) bool {
	if t.DoneAt != nil || t.CancelledAt != nil || t.Supervision == nil {
		return false
	}
	for _, step := range t.Lineage {
		if step.TodoID == t.ID && step.SupersededBy != "" {
			return false
		}
	}
	return true
}

func evidenceHash(v any) string {
	b, _ := json.Marshal(v)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (s *Service) scheduledTodoSnapshot(ctx context.Context, task Task, run ScheduleRun) (scheduleTodoSnapshot, bool) {
	out := scheduleTodoSnapshot{Answers: map[string]string{}, Criteria: map[string]string{}}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		out.Unknown = true
		return out, true
	}
	t, ok := s.todoNow(task.TodoID)
	t = s.withBrief(t)
	if !ok || !scheduledTodoOpen(t) {
		return out, false
	}
	for _, c := range t.Supervision.Criteria {
		out.Criteria[c.Criterion] = c.State
	}
	if p := t.Supervision.Progress; p != nil && p.Kind == upReportProgress {
		out.Report = evidenceHash([]any{p.Text, p.Detail, p.Step, p.Steps})
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil ||
			a.MachineID == run.MachineID && a.SessionID == run.SessionID ||
			a.MachineID == task.MachineID && a.SessionID == task.SessionID ||
			a.MachineID == task.RunMachineID && a.SessionID == task.RunSessionID {
			continue
		}
		items, read := s.lastTurnItems(ctx, a.MachineID, a.SessionID)
		if !read {
			out.Unknown = true
			continue
		}
		_, answer := turnExchange(items)
		if answer != "" && !apiErrorText(answer) {
			out.Answers[a.MachineID+"\x00"+a.SessionID] = evidenceHash(answer)
		}
	}
	return out, true
}

func scheduledTodoAdvanced(before, after scheduleTodoSnapshot) bool {
	for agent, answer := range after.Answers {
		if answer != "" && before.Answers[agent] != answer {
			return true
		}
	}
	for criterion, state := range after.Criteria {
		if old, ok := before.Criteria[criterion]; ok && old != state {
			return true
		}
	}
	return after.Report != "" && after.Report != before.Report
}

func retainTodoEvidence(before, after scheduleTodoSnapshot) scheduleTodoSnapshot {
	out := after
	// A failed read must not replace a previously readable baseline with an unknown one.
	out.Unknown = before.Unknown && after.Unknown
	if after.Unknown && len(after.Criteria) == 0 {
		out.Criteria = maps.Clone(before.Criteria)
	}
	out.Answers = maps.Clone(before.Answers)
	if out.Answers == nil {
		out.Answers = map[string]string{}
	}
	maps.Copy(out.Answers, after.Answers)
	if out.Report == "" {
		out.Report = before.Report
	}
	return out
}

func (s *Service) finishScheduledTodo(ctx context.Context, task Task, run ScheduleRun) {
	if task.TodoID == "" || task.Repeat == repeatOnce {
		return
	}
	after, active := s.scheduledTodoSnapshot(ctx, task, run)
	reason := run.Error
	if reason == "" {
		if items, ok := s.lastTurnItems(ctx, run.MachineID, run.SessionID); ok {
			_, reason = turnExchange(items)
		}
	}
	reason = clip(oneLine(reason), 500)
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return
	}
	i := sc.indexLocked(task.ID)
	j := slices.IndexFunc(sc.runs, func(r ScheduleRun) bool { return r.ID == run.ID })
	if i < 0 || j < 0 || sc.tasks[i].TodoID != task.TodoID || sc.tasks[i].Repeat == repeatOnce {
		sc.mu.Unlock()
		return
	}
	cur := sc.tasks[i]
	if old := cur.TodoProgress; old != nil && old.Run == run.ID && old.Assessment.State != "" {
		// Recover a history write that failed after the schedule's assessment was durable.
		result := old.Assessment
		sc.runs[j].TodoProgress = &result
	}
	if sc.runs[j].TodoProgress == nil || sc.runs[j].TodoProgress.State == "pending" {
		result := ScheduleTodoAssessment{TodoID: task.TodoID, State: "no_progress", Reason: reason}
		progress := scheduleTodoProgress{Run: run.ID, At: run.At, Snapshot: after}
		before := task.todoBefore
		if old := cur.TodoProgress; old != nil {
			if old.At.After(run.At) {
				result.State = "superseded"
			} else {
				progress.NoProgress, progress.Signaled, progress.Escalated = old.NoProgress, old.Signaled, old.Escalated
				progress.EscalationRun = old.EscalationRun
				if progress.EscalationRun == "" && old.NoProgress >= 2 {
					progress.EscalationRun = old.Run // recover a partial write made before this field existed
				}
				before = &old.Snapshot
			}
		}
		switch {
		case result.State == "superseded":
		case !active || !cur.Enabled:
			result.State = "inactive"
			progress.NoProgress, progress.Signaled, progress.Escalated = 0, false, false
			progress.EscalationRun = ""
		case before == nil || before.Unknown:
			result.State = "unknown"
		case scheduledTodoAdvanced(*before, after):
			result.State = "progress"
			progress.NoProgress, progress.Signaled, progress.Escalated = 0, false, false
			progress.EscalationRun = ""
		case after.Unknown:
			result.State = "unknown"
		default:
			progress.NoProgress++
		}
		if result.State == "no_progress" && progress.NoProgress >= 2 && progress.EscalationRun == "" {
			progress.EscalationRun = run.ID
		}
		if before != nil {
			progress.Snapshot = retainTodoEvidence(*before, after)
		}
		result.NoProgress = progress.NoProgress
		if result.Reason == "" {
			result.Reason = i18n.T(s.lang(ctx), "assistant-schedule.todo_no_evidence")
		}
		if result.State == "unknown" {
			result.Reason = i18n.T(s.lang(ctx), "assistant-schedule.todo_unknown", i18n.Args{"reason": result.Reason})
		}
		progress.Assessment = result
		tasks := slices.Clone(sc.tasks)
		if result.State != "superseded" {
			tasks[i].TodoProgress = &progress
		}
		if err = s.saveLocked(sc, tasks); err != nil {
			sc.mu.Unlock()
			s.logf("task %s: save to-do progress: %v", task.ID, err)
			return
		}
		sc.runs[j].TodoProgress = &result
	}
	result := *sc.runs[j].TodoProgress
	if st := s.store(); st != nil {
		err = st.PutSetting(ctx, runsKey, sc.runs)
	}
	cur = sc.tasks[i]
	sc.mu.Unlock()
	if err != nil {
		s.logf("task %s: save run's to-do progress: %v", task.ID, err)
		return
	}
	s.broadcastSchedule()
	if result.State != "no_progress" || !cur.Enabled || cur.TodoProgress == nil || cur.TodoProgress.Run != run.ID {
		return
	}
	s.escalateScheduledTodo(ctx, cur, run, result)
}

func (s *Service) escalateScheduledTodo(ctx context.Context, task Task, run ScheduleRun, result ScheduleTodoAssessment) {
	t, ok := s.todoNow(task.TodoID)
	t = s.withBrief(t)
	if !ok || !scheduledTodoOpen(t) {
		return
	}
	progress := *task.TodoProgress
	if result.NoProgress >= 2 && progress.EscalationRun == "" {
		progress.EscalationRun = run.ID // replay a completed assessment from an older hub
	}
	lang := s.lang(ctx)
	detail := i18n.T(lang, "assistant-schedule.todo_stalled", i18n.Args{
		"name": task.labelIn(lang), "count": result.NoProgress, "reason": result.Reason,
	})
	if !progress.Signaled {
		s.signal(superviseSignal{Todo: t.ID, Kind: sigReport, Report: upReportBlocked,
			Key: run.ID, At: run.At, Machine: run.MachineID, Session: run.SessionID, Detail: detail + "\n" + run.ID})
		progress.Signaled = true
	}
	// A committed review is the outcome; its marker may still need saving after a partial write or restart.
	if result.NoProgress >= 2 && !progress.Escalated && t.Supervision.State == reviewNeedsUser {
		if n := len(t.Supervision.Reviews); n > 0 {
			review := t.Supervision.Reviews[n-1]
			progress.Escalated = review.Verdict == reviewNeedsUser && strings.HasSuffix(review.Note, "\n"+progress.EscalationRun)
		}
	}
	// Never overwrite another question already waiting for the owner.
	if result.NoProgress >= 2 && !progress.Escalated && t.Supervision.State != reviewNeedsUser {
		change := i18n.T(lang, "assistant-schedule.todo_change")
		wait := i18n.T(lang, "assistant-schedule.todo_wait")
		args, _ := json.Marshal(map[string]any{
			"todo_id": t.ID, "verdict": reviewNeedsUser, "why_owner": whyPriority,
			"note":      detail + "\n" + progress.EscalationRun,
			"reason":    i18n.T(lang, "assistant-schedule.todo_decide", i18n.Args{"reason": result.Reason}),
			"recommend": change, "cost_of_waiting": i18n.T(lang, "assistant-schedule.todo_wait_cost"),
			"options": []DecisionOption{
				{Label: change, LeadsTo: i18n.T(lang, "assistant-schedule.todo_change_result")},
				{Label: wait, LeadsTo: i18n.T(lang, "assistant-schedule.todo_wait_result")},
			},
			// Revisit the decision, never silently change the owner's approved strategy.
			"deadline": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339), "reversible": false,
		})
		supervisor := t.Supervision.Supervisor
		if supervisor == "" {
			supervisor = t.Origin
		}
		if _, err := s.toolTaskReview(ctx, supervisor, args); err != nil {
			s.logf("task %s: no-progress decision: %v", task.ID, err)
		} else {
			progress.Escalated = true
		}
	}
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return
	}
	defer sc.mu.Unlock()
	if i := sc.indexLocked(task.ID); i >= 0 && sc.tasks[i].TodoProgress != nil && sc.tasks[i].TodoProgress.Run == run.ID {
		tasks := slices.Clone(sc.tasks)
		tasks[i].TodoProgress = &progress
		if err := s.saveLocked(sc, tasks); err != nil {
			s.logf("task %s: save no-progress escalation: %v", task.ID, err)
		}
	}
}
