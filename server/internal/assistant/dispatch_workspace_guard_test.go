package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

func TestDispatchRefusesAssistantWorkspace(t *testing.T) {
	for _, tool := range []string{"task_plan", "agent_start"} {
		for _, cwd := range []string{"~/.persona/assistant", "~/.jarvis/assistant", "/home/me/.persona/assistant/.", "/home/me/.jarvis/assistant/../assistant", "/home/me/.persona/assistant-m-abc", "/state/assistant", "/state/assistant-m-abc"} {
			t.Run(tool+cwd, func(t *testing.T) {
				s, f := newTaskService(t)
				f.devices = []Device{{ID: "box", Home: "/home/me", DataDir: "/state", Online: true, Reachable: true}}
				args := map[string]any{"title": "Inspect project", "prompt": "Inspect the source", "machine": "box", "cwd": cwd, "worktree": map[string]string{"branch": "fix/example"}}
				var input any = args
				if tool == "task_plan" {
					input = map[string]any{"tasks": []any{args}}
				}
				raw, _ := json.Marshal(input)
				_, err := s.runTool(context.Background(), "server", "conv9", tool, raw)
				var message *i18n.Message
				if !errors.As(err, &message) || message.Key != "rpc-errors.agent_assistant_workspace" {
					t.Fatalf("expected readable workspace refusal, got %v", err)
				}
				if len(f.starts) != 0 || len(f.worktrees) != 0 {
					t.Fatalf("dispatch had side effects: starts=%v worktrees=%v", f.starts, f.worktrees)
				}
			})
		}
	}
}
