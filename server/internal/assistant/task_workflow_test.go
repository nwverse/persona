package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestCodexWorkflowPlanReceipt(t *testing.T) {
	const oldCLI = "Codex CLI 0.144.0 lacks stable multi-agent v2."
	for _, tc := range []struct {
		name   string
		old    bool
		active bool
		calls  int
	}{
		{name: "confirmed", active: true, calls: 1},
		{name: "older executable", old: true, calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := newTaskService(t)
			f.mu.Lock()
			f.zshrc = map[string][]proto.Launcher{"box": {{Name: "cx", Agent: "codex", Source: "zshrc"}}}
			if tc.old {
				f.refuseUltra = &rpc.Error{Code: "ultracode_unsupported", Message: oldCLI}
			}
			f.startReply = func(a map[string]any, sess *proto.Session) *proto.StartInstead {
				sess.Agent = "codex"
				sess.CodexMultiAgentV2 = a["ultracode"] == true
				return nil
			}
			f.mu.Unlock()
			ctx := context.Background()
			args := json.RawMessage(`{"tasks":[{"title":"Document a module","role":"worker","prompt":"Explain the public interfaces","machine":"box","cwd":"/code/app","launcher":"cx","ultracode":true,"workflow_kind":"claude_ultracode","workflow_active":true,"workflow_note":"forged"}]}`)
			if _, err := s.toolTaskPlan(ctx, "conv1", args); err != nil {
				t.Fatal(err)
			}
			items, plans, _ := s.feed(ctx, "conv1", time.Time{})
			var id string
			for _, it := range items {
				if it.Kind == feedPlan {
					id = it.Ref
				}
			}
			if id == "" {
				t.Fatal("missing plan")
			}
			planned := plans[id].Tasks[0]
			if planned.WorkflowKind != workflowCodex || planned.WorkflowActive || planned.WorkflowNote != "" {
				t.Fatalf("unverified or incorrect card: %+v", planned)
			}
			if _, err := s.startPlan(ctx, id, nil, false); err != nil {
				t.Fatal(err)
			}
			var receipt PlanTask
			waitFor(t, "the confirmed or fallback receipt", func() bool {
				_, snapshots, _ := s.feed(ctx, "conv1", time.Time{})
				receipt = snapshots[id].Tasks[0]
				return receipt.WorkflowActive || receipt.WorkflowNote != ""
			})
			if receipt.WorkflowActive != tc.active || strings.Contains(receipt.WorkflowNote, oldCLI) != tc.old {
				t.Fatalf("wrong workflow result: active=%t note=%q", receipt.WorkflowActive, receipt.WorkflowNote)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.starts) != tc.calls || f.agents != 1 || f.starts[0]["ultracode"] != true {
				t.Fatalf("wrong start sequence: %v (%d agents)", f.starts, f.agents)
			}
			if tc.old && f.starts[1]["ultracode"] != nil {
				t.Fatalf("fallback kept workflow: %v", f.starts[1])
			}
		})
	}
}

func TestTaskWorkflowClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		fit  startAgent
		want taskWorkflowKind
	}{
		{name: "verified local Codex", fit: startAgent{snapshot: "codex", source: "zshrc"}, want: workflowCodex},
		{name: "wrapped verified Claude", fit: startAgent{snapshot: "claude", source: "path"}, want: workflowClaude},
		{name: "runtime provider", fit: startAgent{snapshot: "codex", source: "paseo"}, want: workflowUnsupported},
		{name: "compatibility default cannot prove Claude", fit: startAgent{agent: "claude"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fit.taskWorkflow(); got != tc.want {
				t.Fatalf("workflow %q, want %q", got, tc.want)
			}
		})
	}
	if !workflowCodex.changedTo(workflowClaude) || !workflowCodex.changedTo("") || workflowCodex.changedTo(workflowCodex) {
		t.Fatal("a Codex request must stay tied to its resolved kind")
	}
}
