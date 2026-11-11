package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"testing/synctest"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestReadonlyTaskReportDoesNotAskUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := readonlyTaskService(t)
		setSettings(t, s, func(v *Settings) { v.Main = "readonly-chat" })
		if _, err := s.toolTaskPlan(context.Background(), "readonly-chat", readonlyTaskArgs(t, readonlyTestTask(""))); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		todo := onlyTodo(s)
		if len(todo.Agents) != 1 || todo.Supervision == nil || todo.Supervision.Supervisor != "readonly-chat" {
			t.Fatalf("current supervised task not dispatched: %+v", todo)
		}
		agent := todo.Agents[0]
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		args := json.RawMessage(`{"kind":"done","text":"Inspection complete; no files changed."}`)
		ix := proto.Interaction{ID: "readonly-report", MachineID: agent.MachineID, SessionID: agent.SessionID,
			Kind: proto.IxPermission, ToolName: "mcp__persona__report_to_supervisor", ToolInput: args}
		done := make(chan proto.Resolution, 1)
		go func() {
			r, err := s.nodeInteraction(ctx, ix)
			if err != nil {
				t.Error(err)
			}
			done <- r
		}()
		synctest.Wait()
		select {
		case r := <-done:
			if r.Allow == nil || !*r.Allow || r.Approve != nil || r.Always || r.Mode != "" || r.Dismiss {
				t.Fatalf("report must be allowed once without a permission grant or mode change: %+v", r)
			}
		default:
			s.fleet.mu.Lock()
			pending := s.fleet.interactions[ix.ID]
			s.fleet.mu.Unlock()
			if pending != nil {
				t.Fatalf("current readonly report is waiting on a pending permission interaction: kind=%s tool=%s", pending.ix.Kind, pending.ix.ToolName)
			}
			t.Fatal("report did not return directly")
		}
		res := s.mcpCall(ctx, agent.MachineID, agent.SessionID, "report_to_supervisor", args, false)
		if res.IsError {
			t.Fatalf("completion report failed: %+v", res)
		}
		cur, _ := s.todoNow(todo.ID)
		if cur.Supervision.Progress == nil || cur.Supervision.Progress.Kind != "done" || cur.Supervision.Progress.Text != "Inspection complete; no files changed." {
			t.Fatalf("completion report not delivered to the task: %+v", cur.Supervision.Progress)
		}
		if cards := s.workCards(); len(cards) != 0 {
			t.Fatalf("pending cards: %+v", cards)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, event := range f.told {
			if event.event == assistantwire.EventFleetInteraction {
				t.Fatal("completion report created a user permission card")
			}
		}
	})
}
