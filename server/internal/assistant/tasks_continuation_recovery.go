package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// PlanContinuationRecovery is delivery evidence, not a successful replacement start. The original Error stays.
// A sending record survives a lost response or manager restart and cannot be replayed as a fresh send.
type PlanContinuationRecovery struct {
	TodoID           string    `json:"todo_id"`
	MachineID        string    `json:"machine_id"`
	SessionID        string    `json:"session_id"`
	SourceInstanceID string    `json:"source_instance_id,omitempty"`
	ScopeRevision    string    `json:"scope_revision"`
	State            string    `json:"state"` // available, sending, sent, queued, unknown
	InputID          string    `json:"input_id,omitempty"`
	SentAt           time.Time `json:"sent_at,omitzero"`
	SendError        string    `json:"send_error,omitempty"`
}

type planContinuationRequest struct {
	PlanID    string `json:"plan_id"`
	TaskIndex int    `json:"task_index"`
}

// A loaded app-server thread takes messages through its own supported send entry. It is not a terminal process
// Persona may retire. A history-only/offline/archived row never authorizes this path, regardless of resume=true.
func continuationSession(c proto.Session) bool {
	return c.SourceKind == "codex_app_server" && c.Agent == "codex" && !c.Assistant && !c.Archived &&
		(c.Status == proto.StatusDone || c.Status == proto.StatusIdle || c.Status == proto.StatusWorking) &&
		c.Capabilities != nil && c.Capabilities.Send && !c.Capabilities.Terminal && !stoppedAgent(c) &&
		(c.Capabilities.StopCause == proto.StopUnknown || c.Capabilities.StopCause.AllowsSupervisorResume())
}

// Only offer the same placement/executor. This action never changes a launcher, model or permission mode.
func continuationPlacement(t PlanTask, c proto.Session) bool {
	return filepath.Clean(t.Cwd) == filepath.Clean(c.Cwd) &&
		(t.Launcher == "" || t.Launcher == c.Launcher || t.Launcher == "codex" && c.Launcher == "") &&
		(t.Model == "" || t.Model == c.Model) &&
		(t.Permission == "" || t.Permission == c.PermissionMode || t.Permission == "default" && c.PermissionMode == "") &&
		(t.Workspace == nil || t.Workspace.Mode == "none")
}

// The original failure must name this exact stop. Other failures (scope, admission, storage, a different Agent)
// have no send remedy. Legacy cards without this evidence stay historical failures; there is no title matching.
func (s *Service) continuationOffer(ctx context.Context, conversation string, task PlanTask) *PlanContinuationRecovery {
	if task.Continues == "" || task.ReuseTodo != task.Continues || task.TodoID != "" || task.Brief == nil {
		return nil
	}
	todo, err := s.taskTodoForConversation(conversation, task.Continues)
	if err != nil || todo.DoneAt != nil || todo.Cancelled() || s.checkContinuationScope(&task) != nil {
		return nil
	}
	var candidate *TodoAgent
	for _, a := range todo.Agents {
		if a.active() {
			if candidate != nil {
				return nil // a multi-writer task needs an explicit handoff review
			}
			cp := a
			candidate = &cp
		}
	}
	if candidate == nil || todo.AgentAborted(candidate.MachineID, candidate.SessionID) {
		return nil
	}
	a := *candidate
	prefix := fmt.Sprintf("continues: cannot stop agent %s/%s on to-do %s:", a.MachineID, a.SessionID, todo.ID)
	if !strings.HasPrefix(task.Error, prefix) || !strings.Contains(task.Error, "this session belongs to another tool") {
		return nil
	}
	m, ok := s.machineByName(ctx, task.Machine)
	if !ok || m.ID != a.MachineID || !m.Reachable {
		return nil
	}
	c, ok := s.session(ctx, a.MachineID, a.SessionID)
	if !ok || !continuationSession(c) || !continuationPlacement(task, c) {
		return nil
	}
	if current, ok := s.openTodoOfAgent(a.MachineID, a.SessionID); !ok || current.ID != todo.ID {
		return nil
	}
	return &PlanContinuationRecovery{TodoID: todo.ID, MachineID: a.MachineID, SessionID: a.SessionID,
		SourceInstanceID: c.SourceInstanceID, ScopeRevision: task.Brief.scopeRevision().Format(time.RFC3339Nano), State: "available"}
}

func (s *Service) checkPlanContinuation(ctx context.Context, self string, task PlanTask, r *PlanContinuationRecovery, mid string, c proto.Session) error {
	if r == nil || task.Error == "" || task.TodoID != "" || task.Continues != r.TodoID || task.ReuseTodo != r.TodoID ||
		r.MachineID != mid || r.SessionID != c.SessionID || r.SourceInstanceID != c.SourceInstanceID ||
		c.SourceKind != "codex_app_server" || c.Agent != "codex" || !continuationPlacement(task, c) {
		return fmt.Errorf("continuation no longer matches the original session")
	}
	todo, err := s.taskTodoForConversation(self, r.TodoID)
	if err != nil {
		return err
	}
	s.todos.mu.RLock()
	current := s.todos.activeBySession[[2]string{mid, c.SessionID}]
	s.todos.mu.RUnlock()
	linked := false
	for _, a := range todo.Agents {
		linked = linked || a.MachineID == mid && a.SessionID == c.SessionID
	}
	if !linked || current != "" && current != r.TodoID {
		return fmt.Errorf("continuation Agent no longer belongs to this task")
	}
	if err := s.checkContinuationScope(&task); err != nil {
		return err
	}
	if task.Brief == nil || r.ScopeRevision != task.Brief.scopeRevision().Format(time.RFC3339Nano) {
		return fmt.Errorf("continuation scope changed")
	}
	return nil
}

// Send eligibility is checked only before delivery. A confirmed input may immediately put the original session
// into Needs you or stop; that does not undo its receipt or authorize a second send.
func (s *Service) checkPlanContinuationSend(ctx context.Context, self string, task PlanTask, r *PlanContinuationRecovery, mid string, c proto.Session) error {
	if !continuationSession(c) {
		return fmt.Errorf("the original session no longer accepts continuation messages; nothing was sent")
	}
	if err := s.checkPlanContinuation(ctx, self, task, r, mid, c); err != nil {
		return err
	}
	todo, linked := s.openTodoOfAgent(mid, c.SessionID)
	if !linked || todo.ID != r.TodoID || todo.Cancelled() || todo.AgentAborted(mid, c.SessionID) {
		return fmt.Errorf("continuation Agent no longer belongs to this open task; nothing was sent")
	}
	for _, a := range todo.Agents {
		if a.active() && (a.MachineID != mid || a.SessionID != c.SessionID) {
			return fmt.Errorf("another Agent joined this task; review its handoff before continuing; nothing was sent")
		}
	}
	return nil
}

// Membership writes take todoBook.writing before mu. A reservation excludes new members and moving this session
// to another task while the input is in flight. Stops, completion and unlink remain the owner's controls.
func (b *todoBook) continuationLinkBlocked(todoID, mid, sid string) bool {
	key := [2]string{mid, sid}
	for id, original := range b.continuationSends {
		if id == todoID && original != key || id != todoID && original == key {
			return true
		}
	}
	return false
}

func (s *Service) continuationLinkBlocked(todoID, mid, sid string) bool {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	return s.todos.continuationLinkBlocked(todoID, mid, sid)
}

// sendPlanContinuation annotates one failed dispatch through the existing agent_send contract. It never starts,
// retires or relinks an Agent. The task's start claim excludes replacement/retry while this send is in flight;
// the persisted compare-and-set excludes duplicate sends, including across managers and after a restart.
func (s *Service) sendPlanContinuation(ctx context.Context, self, mid, sid string, req planContinuationRequest, params map[string]any, sent *agentSendReply) error {
	p, err := s.loadPlan(ctx, req.PlanID)
	if err != nil {
		return err
	}
	if p.State != planStarted || req.TaskIndex < 0 || req.TaskIndex >= len(p.Tasks) {
		return fmt.Errorf("continuation requires a task in a started plan")
	}
	task := p.Tasks[req.TaskIndex]
	r := task.ContinuationRecovery
	c, ok := s.session(ctx, mid, sid)
	if !ok {
		return fmt.Errorf("continuation session is unavailable; nothing was sent")
	}
	if err := s.checkPlanContinuationSend(ctx, self, task, r, mid, c); err != nil {
		return err
	}
	claim, ok := s.claimTaskStart(r.TodoID)
	if !ok {
		return fmt.Errorf("a continuation or replacement is already in progress; nothing was sent")
	}
	defer s.endTaskStart(r.TodoID, claim, proto.Session{})
	s.todos.writing.Lock()
	err = s.checkPlanContinuationSend(ctx, self, task, r, mid, c)
	if err == nil {
		s.todos.mu.Lock()
		if s.todos.continuationSends == nil {
			s.todos.continuationSends = make(map[string][2]string)
		}
		s.todos.continuationSends[r.TodoID] = [2]string{mid, sid}
		s.todos.mu.Unlock()
	}
	s.todos.writing.Unlock()
	if err != nil {
		return err
	}
	defer func() {
		s.todos.writing.Lock()
		s.todos.mu.Lock()
		delete(s.todos.continuationSends, r.TodoID)
		s.todos.mu.Unlock()
		s.todos.writing.Unlock()
	}()
	inputID := "continuation_" + randomHex(16)
	claimed := false
	err = s.patchPlanTask(ctx, req.PlanID, req.TaskIndex, func(t *PlanTask) {
		cur := t.ContinuationRecovery
		if cur == nil || cur.State != "available" || *cur != *r || t.Error != task.Error || t.TodoID != "" {
			return
		}
		next := *cur
		next.State, next.InputID, next.SendError = "sending", inputID, ""
		t.ContinuationRecovery = &next
		claimed = true
	})
	if err != nil {
		return err // no send without the durable claim
	}
	if !claimed {
		return fmt.Errorf("continuation was already sent or its delivery is unconfirmed; use agent_read, do not resend")
	}
	params["client_msg_id"] = inputID
	// Recheck after taking the claim, since ownership/scope may have changed while it was being persisted.
	if c, ok = s.session(ctx, mid, sid); !ok {
		err = fmt.Errorf("continuation session is unavailable; nothing was sent")
	} else {
		err = s.checkPlanContinuationSend(ctx, self, task, r, mid, c)
	}
	state := "available"
	if err == nil {
		err = s.call(ctx, mid, DeviceSessionSend, params, sent)
		switch {
		case err != nil && !sendUnknown(err):
			state = "available" // a definite refusal may be remedied explicitly
		case err != nil || !sent.OK:
			state = "unknown"
			if err == nil {
				err = fmt.Errorf("the node did not confirm continuation delivery; use agent_read before any further send")
			}
		default:
			state = "sent"
			if sent.Queued || sent.CompactAfterTurn {
				state = "queued"
			}
		}
	}
	// Never attribute an accepted send to a task/scope it ceased to belong to during delivery.
	if state == "sent" || state == "queued" {
		if fresh, found := s.session(ctx, mid, sid); !found {
			state, err = "unknown", fmt.Errorf("message accepted, but the original session could not be verified")
		} else if check := s.checkPlanContinuation(ctx, self, task, r, mid, fresh); check != nil {
			state, err = "unknown", fmt.Errorf("message accepted, but continuation ownership or scope changed: %w", check)
		}
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	saved := false
	saveErr := s.patchPlanTask(saveCtx, req.PlanID, req.TaskIndex, func(t *PlanTask) {
		cur := t.ContinuationRecovery
		if cur == nil || cur.State != "sending" || cur.InputID != inputID {
			return
		}
		next := *cur
		next.State = state
		if state == "sent" || state == "queued" {
			next.SentAt = time.Now().UTC()
		}
		if err != nil {
			next.SendError = err.Error()
		}
		t.ContinuationRecovery = &next
		saved = true
	})
	if saveErr != nil {
		return fmt.Errorf("continuation delivery may have succeeded but its receipt could not be saved; do not resend: %w", saveErr)
	}
	if !saved {
		return fmt.Errorf("continuation receipt changed before it could be saved; delivery is unconfirmed, do not resend")
	}
	return err
}

func continuationAction(planID string, index int, t PlanTask) string {
	r := t.ContinuationRecovery
	if r == nil || r.State != "available" {
		return ""
	}
	raw, _ := json.Marshal(map[string]any{"tool": "agent_send", "arguments": map[string]any{
		"machine": r.MachineID, "session_id": r.SessionID, "text": t.Prompt,
		"continuation": planContinuationRequest{PlanID: planID, TaskIndex: index}}})
	return "\nThe original session can receive this follow-up on the same task. No replacement was started. " +
		"Use this explicit action only to continue this scope; its receipt records delivery, not execution:\n" + string(raw) + "\n"
}
