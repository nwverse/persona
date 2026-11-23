package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// taskSpawn keeps a separate coding session on its caller's task. Unlike a leader's task_plan, it creates no task,
// role or supervision level. The node supplies the caller; none of those identities come from tool arguments.
func (s *Service) taskSpawn(ctx context.Context, machine, session string, args json.RawMessage) (string, error) {
	var a struct {
		Title  string `json:"title"`
		Prompt string `json:"prompt"`
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return "", fmt.Errorf("task_spawn arguments: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", errors.New("task_spawn expects one JSON object")
	}
	a.Title, a.Prompt = strings.TrimSpace(a.Title), strings.TrimSpace(a.Prompt)
	if a.Title == "" || a.Prompt == "" {
		return "", errors.New("task_spawn requires title and prompt; nothing was started")
	}
	machine = s.canonical(machine)
	t, decision := s.spawnAuthorization(ctx, machine, session)
	if decision != spawnAllowed {
		return "", taskSpawnAuthorizationError(decision)
	}
	// Todo.Notes stands in only when no started plan binds the to-do. A failed read, a stored plan that does not
	// decode, or two plans binding it leave the current assignment unknown, so nothing starts rather than comparing
	// against historical notes. This one strict read also supplies the assignment's permission and model below.
	plan, index, planErr := s.strictPlanAssignment(ctx, t)
	var parentAssignment string
	switch {
	case planErr == nil:
		parentAssignment = plan.Tasks[index].Prompt
	case errors.Is(planErr, errTaskPlanAmbiguous):
		return "", errors.New("task_spawn cannot tell this task's current assignment: more than one plan holds it; nothing was started; ask your supervisor, do not retry")
	case errors.Is(planErr, errTaskNotInPlan):
		parentAssignment = t.Notes
	default:
		return "", errors.New("task_spawn cannot read this task's current assignment; nothing was started; ask your supervisor, do not retry")
	}
	if strings.TrimSpace(parentAssignment) == a.Prompt {
		return "", errors.New("task_spawn requires a different bounded subtask: the prompt must narrow the scope, say what not to do, and name the evidence to return rather than copy the parent assignment; ask your supervisor; do not retry")
	}
	parent, ok := s.session(ctx, machine, session)
	if !ok || parent.Assistant || parent.Cwd == "" || parent.Launcher == "" {
		return "", errors.New("Persona cannot verify this Agent's directory and launcher; ask your supervisor, nothing was started")
	}
	// Unknown permission is read-only, never the owner's writable default. Codex does not expose PermissionMode, so
	// the persisted assignment supplies it when this is that assignment's original Agent.
	mode := parent.PermissionMode
	assigned := false
	for _, agent := range t.Agents {
		if agent.MachineID == machine && agent.SessionID == session && agent.active() &&
			(agent.Source == "manual" || agent.Source == sourceLeader || agent.Source == sourceWorker) {
			assigned = true
		}
	}
	var assignment *PlanTask
	if assigned && planErr == nil && plan.Tasks[index].Machine == machine {
		assignment = &plan.Tasks[index]
	}
	if mode == "" {
		mode = "plan"
		if assignment != nil {
			// Prefer durable evidence of the assignment's actual start. Falling back to its caller-owned mode is
			// conservative; inferring Allow all from the launcher could broaden a real plan-mode parent.
			mode = firstNonEmpty(assignment.StartPermission, assignment.Permission, mode)
		}
	}
	project := t.Project
	if project == "" {
		if _, d, ok := s.directionForTodo(ctx, t); ok {
			project = d.Project
		}
	}
	assignmentLauncherDefault := assignment != nil && assignment.LauncherDefaultModel && s.startLauncher(ctx, assignment.Launcher) == parent.Launcher
	child := PlanTask{TodoID: t.ID, Project: project, Supervisor: t.Supervision.Supervisor,
		Title: a.Title, Prompt: a.Prompt, Machine: machine, Cwd: parent.Cwd,
		Launcher: parent.Launcher, Model: s.taskSpawnModel(ctx, machine, parent, assignment), Effort: parent.Effort,
		Permission: mode, Readonly: mode == "plan", preservePermission: true, spawnExactOnly: true,
		LauncherDefaultModel: assignmentLauncherDefault || parent.RequestedModel != nil && *parent.RequestedModel == ""}
	// A caller disconnecting must not abandon a start before its ownership is recorded.
	startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	noteSpawnBeginTestHook()
	// Recheck the caller and register this start in the same membership/window boundary. If its parent finished or a
	// workspace link became suspect after the assignment read, this start is refused before anything reaches the node.
	inFlight, decision := s.beginSpawnStartForCaller(machine, session, t.ID)
	if decision != spawnAllowed {
		return "", taskSpawnAuthorizationError(decision)
	}
	defer func() {
		if inFlight != nil { // the child was not linked: whatever was linked meanwhile stays suspect
			s.endSpawnStart(inFlight, false)
		}
	}()
	mid, sid, err := s.startTaskAgent(startCtx, &child, false)
	inFlight.identity = child.spawnIdentity
	if sid != "" {
		inFlight.unconfirmed = [2]string{mid, sid} // even a failed stop must not let this exact child return as first layer
	}
	// This path is not wrapped by runPlanTaskStart, so release the gate's claim itself once the child is linked or the
	// start has failed for good (by then the ledger carries the linked agent, or nothing runs).
	if child.admissionRelease != nil {
		defer child.admissionRelease()
	}
	if err != nil {
		return "", fmt.Errorf("task_spawn: %w; do not retry an uncertain start, ask your supervisor", err)
	}
	// The final caller check, child link and successful close are one transaction. A refusal leaves the returned child
	// unconfirmed, takes the stop path, and the deferred failed-end cleanup suspects both window links and mid/sid.
	currentDecision, linkErr := s.completeSpawnStartAuthorized(startCtx, inFlight, machine, session, t.ID,
		a.Title, child.Cwd, child.startedAgent, machine+"/"+session, mid, sid)
	if currentDecision != spawnAllowed {
		err = fmt.Errorf("the caller's authorization changed during the start: %w", taskSpawnAuthorizationError(currentDecision))
	} else {
		err = linkErr
	}
	if err != nil {
		stopCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer stop()
		stopErr := s.call(stopCtx, mid, deviceSessionStop, map[string]any{"session_id": sid}, nil)
		return "", fmt.Errorf("child %s/%s was not linked: %w; stop result: %v; tell your supervisor", mid, sid, err, stopErr)
	}
	inFlight = nil
	t, ok = s.ensureSupervisor(startCtx, t.ID)
	if !ok {
		return "", fmt.Errorf("child %s/%s was linked, but the task closed or lost its supervisor; tell your supervisor", mid, sid)
	}
	out, _ := json.Marshal(map[string]string{"machine_id": mid, "session_id": sid, "todo_id": t.ID,
		"supervisor": t.Supervision.Supervisor, "parent": machine + "/" + session})
	return string(out), nil
}

// An assignment identifies its own Agent's model. Other managed sessions report the exact launch argument,
// including an empty value for the launcher's default. Only older or observed sessions need a catalog lookup.
func (s *Service) taskSpawnModel(ctx context.Context, machine string, parent proto.Session, assignment *PlanTask) string {
	if assignment != nil && s.startLauncher(ctx, assignment.Launcher) == parent.Launcher {
		if assignment.LauncherDefaultModel {
			return ""
		}
		if assignment.Model != "" {
			return assignment.Model
		}
	}
	if parent.RequestedModel != nil {
		return *parent.RequestedModel
	}
	if !proto.ValidModelID(parent.Model) {
		return parent.Model
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if parent.Host != "" {
		ctx = context.WithValue(ctx, launcherModelHostKey{}, parent.Host)
	}
	catalog, err := s.launcherModelRows(ctx, machine, parent.Launcher, false)
	if err != nil {
		return parent.Model
	}
	// Use the launcher's original rows: the picker folds rows that run the same model. An exact ID always
	// wins, and separate routes or context windows with the same reported resolution are ambiguous.
	match := ""
	for _, row := range catalog.Models {
		if row.ID == parent.Model || row.ResolvedOffered && row.Resolved == parent.Model || slices.Contains(row.Aliases, parent.Model) {
			return parent.Model
		}
		if row.Resolved != parent.Model || !proto.ValidModelID(row.ID) || row.ID == "default" || slices.Contains(unfoldedAliases, strings.TrimSuffix(strings.ToLower(row.ID), "[1m]")) {
			continue
		}
		if match != "" && match != row.ID {
			return parent.Model
		}
		match = row.ID
	}
	if match == "" || s.declaredSpawnModel(ctx, machine, parent) {
		return parent.Model
	}
	return match
}

func taskSpawnAuthorizationError(decision spawnDecision) error {
	switch decision {
	case spawnCallerIsChild:
		return errors.New("task_spawn allows one supervised handoff layer: this session is already a spawned child; finish the current bounded subtask and report to your supervisor, or ask your supervisor for help; do not retry")
	case spawnAncestryUnknown:
		return errors.New("task_spawn allows one supervised handoff layer: this session may be a child that another task_spawn on this computer is still starting, so it cannot spawn; nothing was started; finish the current bounded subtask and report to your supervisor, or ask your supervisor for help; do not retry")
	default:
		return errors.New("task_spawn requires an active Agent on an open supervised task; ask your supervisor to assign one")
	}
}

func activeTodoAgent(t Todo, machine, session string) (TodoAgent, bool) {
	for _, agent := range t.Agents {
		if agent.MachineID == machine && agent.SessionID == session && agent.active() {
			return agent, true
		}
	}
	return TodoAgent{}, false
}

func (s *Service) spawnTask(ctx context.Context, machine, session string) (Todo, bool) {
	t, ok := s.taskOf(ctx, machine, session)
	return t, session != "" && ok && !t.Cancelled() && t.Supervision != nil && t.Supervision.Supervisor != ""
}

func (s *Service) spawnTools(ctx context.Context, machine, session string) []proto.Tool {
	if _, decision := s.spawnAuthorization(ctx, s.canonical(machine), session); decision != spawnAllowed {
		return nil
	}
	return toolsNamed([]string{"task_spawn"})
}
