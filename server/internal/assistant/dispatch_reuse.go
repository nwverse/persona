package assistant

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
)

// checkDispatchReuse handles explicit task identity before planning or starting.
// Directory, project and title similarity remain advice given by planDuplicates.
func (s *Service) checkDispatchReuse(ctx context.Context, self string, task PlanTask, request *BriefRequest) error {
	if task.ReuseTodo != "" || strings.TrimSpace(task.Continues) != "" || self == "" {
		return nil
	}
	if request == nil && task.InboxItem == "" {
		return nil
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return fmt.Errorf("cannot check the existing task: %w; nothing was dispatched", err)
	}

	var tokens []string
	if request != nil {
		tokens = strings.FieldsFunc(request.Text, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
		})
	}
	s.todos.mu.RLock()
	var eligible []Todo
	for _, todo := range s.todos.todos {
		if todo.Origin == self || todo.Supervision != nil && todo.Supervision.Supervisor == self {
			eligible = append(eligible, copyTodoLocked(todo))
		}
	}
	s.todos.mu.RUnlock()

	var exact, bound []Todo
	for _, todo := range eligible {
		explicit := false
		for _, token := range tokens {
			if token == todo.ID {
				explicit = true
				break
			}
			for _, agent := range todo.Agents {
				if agent.DismissedAt == nil && agent.SessionID != "" &&
					(token == agent.SessionID || sessionIDPrefix(token, agent.SessionID)) {
					explicit = true
					break
				}
			}
		}
		if explicit {
			exact = append(exact, todo)
		}
		if todo.DoneAt != nil {
			continue
		}
		sameRequest := false
		if request != nil && request.MessageID != "" && request.Conversation != "" {
			if brief, ok := s.briefOfTodo(todo.ID); ok && brief.Request != nil {
				sameRequest = brief.Request.Conversation == request.Conversation && brief.Request.MessageID == request.MessageID
			}
		}
		if sameRequest || task.InboxItem != "" && todo.InboxItem == task.InboxItem {
			bound = append(bound, todo)
		}
	}
	if len(exact) == 0 {
		exact = bound
	}
	if len(exact) != 1 {
		return nil
	}
	todo := exact[0]
	for _, reason := range task.DistinctFrom {
		if strings.TrimSpace(reason.TodoID) == todo.ID && strings.TrimSpace(reason.Reason) != "" {
			return nil
		}
	}
	target := "its Agent"
	var active []TodoAgent
	for _, agent := range todo.Agents {
		if agent.active() && agent.SessionID != "" {
			active = append(active, agent)
		}
	}
	if len(active) == 1 {
		target = fmt.Sprintf("machine=%s session_id=%s", active[0].MachineID, active[0].SessionID)
	}
	return fmt.Errorf("existing to-do %s (%q) matches this request. Use agent_send to %s if available, "+
		"use task_plan with continues and continues_why to replace it on the original to-do, "+
		"or give distinct_from with this todo_id and the reason for separate work. Nothing was dispatched", todo.ID, todo.Title, target)
}

func sessionIDPrefix(token, session string) bool {
	if len(token) != 8 || !strings.HasPrefix(session, token) {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}
