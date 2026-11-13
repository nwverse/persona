package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
)

type mcpWaitDevices struct {
	*fleetDevs
	events chan devicewire.NodeEvent
}

type mcpWaitElicitExtension struct {
	ExtensionTools
	answer func(context.Context) (map[string]any, error)
}

func (e *mcpWaitElicitExtension) AnswerComputerElicit(ctx context.Context, _ string, _ json.RawMessage) (map[string]any, error) {
	return e.answer(ctx)
}

func (d *mcpWaitDevices) Notify(event devicewire.NodeEvent) { d.events <- event }

func newMCPWaitTest(t *testing.T) (*fleetTest, chan devicewire.NodeEvent) {
	t.Helper()
	f := newFakeHub()
	base := newFleetDevs()
	base.add("mac", "Computer")
	d := &mcpWaitDevices{fleetDevs: base, events: make(chan devicewire.NodeEvent, 16)}
	s := New(Options{Hub: f, Logf: testLogf(t), Devices: d})
	return &fleetTest{t: t, s: s, d: base, f: f}, d.events
}

func nextMCPWaitEvent(t *testing.T, events chan devicewire.NodeEvent, callID string, want proto.MCPWaitKind) {
	t.Helper()
	select {
	case event := <-events:
		if event.MachineID != "mac" || event.Event != proto.EventMCPWait {
			t.Fatalf("event envelope = %+v", event)
		}
		var exact map[string]json.RawMessage
		if err := json.Unmarshal(event.Params, &exact); err != nil || len(exact) != 2 || exact["call_id"] == nil || exact["kind"] == nil {
			t.Fatalf("wait params must contain exactly call_id and kind: %s (%v)", event.Params, err)
		}
		var update proto.MCPWaitEvent
		if err := json.Unmarshal(event.Params, &update); err != nil || update.CallID != callID || update.Kind != want {
			t.Fatalf("wait update = %+v, want call %q kind %q (%v)", update, callID, want, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("wait update %q did not arrive", want)
	}
}

func TestMCPWaitPermissionPublishesApprovalThenComputer(t *testing.T) {
	ft, events := newMCPWaitTest(t)
	ctx := ft.s.mcpWaitContext(context.Background(), "mac", "call-permission", true)
	nextMCPWaitEvent(t, events, "call-permission", proto.MCPWaitComputer)
	ix := proto.Interaction{ID: "ix_permission", MachineID: "mac", SessionID: "session", Kind: proto.IxPermission,
		ToolName: "example", Summary: "permission text must not be relayed", ExpiresAt: time.Now().Add(time.Hour)}
	done := make(chan error, 1)
	go func() {
		_, err := ft.s.askHub(ctx, ix)
		done <- err
	}()
	nextMCPWaitEvent(t, events, "call-permission", proto.MCPWaitApproval)
	ft.waitPending(1)
	allow := true
	if err := ft.s.resolve(ix.ID, proto.Resolution{Allow: &allow}); err != nil {
		t.Fatal(err)
	}
	nextMCPWaitEvent(t, events, "call-permission", proto.MCPWaitComputer)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected event after the permission resolved: %s", event.Params)
	default:
	}
}

func TestMCPWaitDirectPermissionPublishesApprovalThenComputer(t *testing.T) {
	ft, events := newMCPWaitTest(t)
	ctx := ft.s.mcpWaitContext(context.Background(), "mac", "call-direct-permission", true)
	nextMCPWaitEvent(t, events, "call-direct-permission", proto.MCPWaitComputer)
	ix := proto.Interaction{ID: "ix_direct_permission", MachineID: "mac", SessionID: "session", Kind: proto.IxPermission,
		ToolName: "todo_delete", Summary: "permission text must not be relayed", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	done := make(chan error, 1)
	go func() {
		_, err := ft.s.ask(ctx, ix)
		done <- err
	}()
	nextMCPWaitEvent(t, events, "call-direct-permission", proto.MCPWaitApproval)
	var answer chan proto.Resolution
	waitFor(t, "the direct permission", func() bool {
		ft.s.questions.mu.Lock()
		defer ft.s.questions.mu.Unlock()
		if q := ft.s.questions.m[ix.ID]; q != nil {
			answer = q.answer
			return true
		}
		return false
	})
	allow := true
	answer <- proto.Resolution{Allow: &allow}
	nextMCPWaitEvent(t, events, "call-direct-permission", proto.MCPWaitComputer)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMCPWaitQuestionPublishesAnswerThenComputer(t *testing.T) {
	ft, events := newMCPWaitTest(t)
	ctx := ft.s.mcpWaitContext(context.Background(), "mac", "call-question", true)
	nextMCPWaitEvent(t, events, "call-question", proto.MCPWaitComputer)
	ix := proto.Interaction{ID: "ix_question", MachineID: "mac", SessionID: "session", Kind: proto.IxQuestion,
		ToolName: "ask_user", Questions: []proto.Question{{Question: "question text must not be relayed"}},
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	done := make(chan error, 1)
	go func() {
		_, err := ft.s.ask(ctx, ix)
		done <- err
	}()
	nextMCPWaitEvent(t, events, "call-question", proto.MCPWaitAnswer)
	var answer chan proto.Resolution
	waitFor(t, "the ask_user question", func() bool {
		ft.s.questions.mu.Lock()
		defer ft.s.questions.mu.Unlock()
		if q := ft.s.questions.m[ix.ID]; q != nil {
			answer = q.answer
			return true
		}
		return false
	})
	answer <- proto.Resolution{Answers: map[string]string{ix.Questions[0].Question: "private answer"}}
	nextMCPWaitEvent(t, events, "call-question", proto.MCPWaitComputer)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("unexpected event after the question resolved: %s", event.Params)
	default:
	}
}

func TestMCPWaitNodeRequestRequiresOptIn(t *testing.T) {
	ft, events := newMCPWaitTest(t)
	params := json.RawMessage(`{"name":"unknown","args":{},"wait_updates":true,"call_id":"call-node"}`)
	if _, handled, err := ft.s.NodeRequest(context.Background(), "mac", "mcp.call", params); !handled || err != nil {
		t.Fatalf("NodeRequest handled = %v, err = %v", handled, err)
	}
	nextMCPWaitEvent(t, events, "call-node", proto.MCPWaitComputer)
	params = json.RawMessage(`{"name":"unknown","args":{},"call_id":"call-old"}`)
	if _, handled, err := ft.s.NodeRequest(context.Background(), "mac", "mcp.call", params); !handled || err != nil {
		t.Fatalf("old NodeRequest handled = %v, err = %v", handled, err)
	}
	select {
	case event := <-events:
		t.Fatalf("a call without wait_updates published %s", event.Params)
	default:
	}
}

func TestMCPWaitComputerElicitUsesSeparateRPCCorrelator(t *testing.T) {
	ft, events := newMCPWaitTest(t)
	ix := proto.Interaction{ID: "ix_lcu", MachineID: "mac", SessionID: "session", Kind: proto.IxPermission,
		ToolName: "computer_run", Summary: "permission text must not be relayed", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	ft.s.SetExtensions(&mcpWaitElicitExtension{answer: func(ctx context.Context) (map[string]any, error) {
		_, err := ft.s.askHub(ctx, ix)
		return map[string]any{"action": "accept"}, err
	}})
	type answer struct {
		handled bool
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		_, handled, err := ft.s.NodeRequest(context.Background(), "mac", "computer.elicit",
			json.RawMessage(`{"conversation":"session","params":{},"call_id":"call-lcu"}`))
		done <- answer{handled: handled, err: err}
	}()
	nextMCPWaitEvent(t, events, "call-lcu", proto.MCPWaitComputer)
	nextMCPWaitEvent(t, events, "call-lcu", proto.MCPWaitApproval)
	ft.waitPending(1)
	allow := true
	if err := ft.s.resolve(ix.ID, proto.Resolution{Allow: &allow}); err != nil {
		t.Fatal(err)
	}
	nextMCPWaitEvent(t, events, "call-lcu", proto.MCPWaitComputer)
	if got := <-done; !got.handled || got.err != nil {
		t.Fatalf("computer.elicit handled = %v, err = %v", got.handled, got.err)
	}

	ft.s.SetExtensions(&mcpWaitElicitExtension{answer: func(ctx context.Context) (map[string]any, error) {
		if mcpWaitFrom(ctx) != nil {
			t.Error("an old computer.elicit received a live reporter")
		}
		return map[string]any{"action": "cancel"}, nil
	}})
	if _, handled, err := ft.s.NodeRequest(context.Background(), "mac", "computer.elicit",
		json.RawMessage(`{"conversation":"session","params":{}}`)); !handled || err != nil {
		t.Fatalf("old computer.elicit handled = %v, err = %v", handled, err)
	}
	select {
	case event := <-events:
		t.Fatalf("an old computer.elicit published %s", event.Params)
	default:
	}
}

func TestMCPWaitNestedWaitRestoresPreviousKind(t *testing.T) {
	ft, events := newMCPWaitTest(t)
	ctx := ft.s.mcpWaitContext(context.Background(), "mac", "call-nested", true)
	nextMCPWaitEvent(t, events, "call-nested", proto.MCPWaitComputer)
	resumeApproval := pushMCPWait(ctx, proto.MCPWaitApproval)
	nextMCPWaitEvent(t, events, "call-nested", proto.MCPWaitApproval)
	resumeAnswer := pushMCPWait(ctx, proto.MCPWaitAnswer)
	nextMCPWaitEvent(t, events, "call-nested", proto.MCPWaitAnswer)
	resumeAnswer()
	nextMCPWaitEvent(t, events, "call-nested", proto.MCPWaitApproval)
	resumeApproval()
	nextMCPWaitEvent(t, events, "call-nested", proto.MCPWaitComputer)

	plain := ft.s.mcpWaitContext(context.Background(), "mac", "call-old", false)
	if mcpWaitFrom(plain) != nil {
		t.Fatal("a call without wait_updates must not publish live state")
	}
}
