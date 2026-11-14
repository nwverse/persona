package assistant

import (
	"context"

	"github.com/nwverse/persona/server/internal/proto"
)

// Assistant-dispatched Agents belong to main-supervised tasks (#5732). The older assistant.agent_tasks switch still
// covers manually linked user to-dos and reporting agents. agent_start from any assistant conversation makes its to-do
// at once (prepareAgentTask) independently of this switch; a session opened manually on the Agents page is not adopted.
//
//   - a todo the user made by hand gets a supervisor when an agent is linked to it (superviseLinkedTodo), and any open
//     todo's agent that reports gets one then (superviseOpenTodoOf), so report_to_supervisor has a receiver.
//
// The supervisor is the main conversation except for the explicit manual-leader and task-leader-worker topologies.
// Agents the user starts by hand in a terminal are not Persona's and are left alone.

// agentTasks: assistant.agent_tasks is on (off by default).
func (s *Service) agentTasks(ctx context.Context) bool {
	v := s.settings(ctx).AgentTasks
	return v != nil && *v
}

// agentTaskSupervisor is the conversation that supervises the task of an agent conversation started ("" for an app's
// start or the user's own todo): conversation itself when it supervises, else the main conversation. false while
// assistant.agent_tasks is off or there is no main conversation to fall back to.
func (s *Service) agentTaskSupervisor(ctx context.Context, conversation string) (string, bool) {
	if !s.agentTasks(ctx) {
		return "", false
	}
	if s.supervises(ctx, conversation) {
		return conversation, true
	}
	main := s.settings(ctx).Main
	return main, main != ""
}

// ensureSupervisor gives open todo id a supervisor when it has none (agentTaskSupervisor of the conversation that
// opened it), with no done_when: the todo with its ledger, false when it stays unsupervised. A reporting
// conversation's task is left as it is: its notices go back to that conversation, and a ledger would move them into
// the main conversation's supervision messages (#4101).
func (s *Service) ensureSupervisor(ctx context.Context, id string) (Todo, bool) {
	t, ok := s.todoNow(id)
	if !ok || t.DoneAt != nil {
		return Todo{}, false
	}
	if t.Supervision != nil && t.Supervision.Supervisor != "" {
		return t, true
	}
	supervisor, ok := s.agentTaskSupervisor(ctx, t.Origin)
	if !ok {
		return Todo{}, false
	}
	if _, reports := s.reporterOf(ctx, t.Origin); reports {
		return Todo{}, false
	}
	v, _, err := s.updateSupervision(ctx, id, func(v *Supervision) bool {
		if v.Supervisor != "" {
			return false
		}
		v.Supervisor = supervisor
		return true
	})
	if err != nil {
		s.logf("task %s: supervision: %v", id, err)
		return Todo{}, false
	}
	t.Supervision = &v
	s.logf("task %s: supervised by %s (agent tasks)", id, short(supervisor))
	return t, true
}

// superviseLinkedTodo: the user linked an agent to todo id. One they made by hand gets a supervisor (ensureSupervisor);
// a conversation's task keeps what it has.
func (s *Service) superviseLinkedTodo(ctx context.Context, id string) {
	if !s.agentTasks(ctx) {
		return
	}
	if t, ok := s.todoNow(id); ok && t.Origin == "" {
		s.ensureSupervisor(ctx, id)
	}
}

// superviseOpenTodoOf is the open todo agent machine/session works for, whoever opened it, with a supervisor
// (ensureSupervisor): where its report goes when taskOf finds no supervised task. false: it is on no open todo, or
// that todo stays unsupervised.
func (s *Service) superviseOpenTodoOf(ctx context.Context, machine, session string) (Todo, bool) {
	if !s.agentTasks(ctx) {
		return Todo{}, false
	}
	t, ok := s.openTodoOfAgent(machine, session)
	if !ok {
		return Todo{}, false
	}
	return s.ensureSupervisor(ctx, t.ID)
}

// workTools are the tools that edit files, run commands or hand work to subagents, by the names Claude Code and
// Codex write them: a turn that calls one did work (turnDidWork).
var workTools = map[string]bool{
	"Edit": true, "MultiEdit": true, "Write": true, "NotebookEdit": true, "apply_patch": true,
	"Bash": true, "shell": true, "exec_command": true, "local_shell": true, "shell_command": true,
	"Task": true, "Agent": true,
}

// turnDidWork: the turn's items have an edit, a command or a subagent (workTools). A turn that only read, searched
// and answered is a question and its answer.
func turnDidWork(items []proto.Item) bool {
	for _, it := range items {
		if it.Kind == proto.KindToolUse && workTools[it.ToolName] {
			return true
		}
	}
	return false
}
