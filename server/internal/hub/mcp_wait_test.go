package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestMCPWaitReporterUsesCurrentManagerCapability(t *testing.T) {
	h := newTestHub(t)
	var notices bytes.Buffer
	h.bus.current = map[string]*busConn{assistantwire.AssistantID: {
		peer: rpc.New("manager", &notices), presence: busPresence{Capabilities: []string{assistantwire.CapabilityMCPWait}},
	}}
	report := h.mcpWaitReporter(assistantwire.AssistantID, "computer", "call-1")
	if report == nil {
		t.Fatal("a capable owning manager did not get a reporter")
	}
	report(proto.MCPWaitApproval)
	var wire struct {
		Event  string               `json:"event"`
		Params devicewire.NodeEvent `json:"params"`
	}
	if err := json.NewDecoder(&notices).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	if wire.Event != devicewire.EventNotify || wire.Params.MachineID != "computer" || wire.Params.Event != proto.EventMCPWait {
		t.Fatalf("relay envelope = %+v", wire)
	}
	var exact map[string]json.RawMessage
	if err := json.Unmarshal(wire.Params.Params, &exact); err != nil || len(exact) != 2 || exact["call_id"] == nil || exact["kind"] == nil {
		t.Fatalf("wait params must contain exactly call_id and kind: %s (%v)", wire.Params.Params, err)
	}
	var update proto.MCPWaitEvent
	if err := json.Unmarshal(wire.Params.Params, &update); err != nil || update.CallID != "call-1" || update.Kind != proto.MCPWaitApproval {
		t.Fatalf("wait update = %+v (%v)", update, err)
	}

	// A manager can be replaced while the phone call is pending. Its older replacement receives no unknown event.
	h.bus.current = map[string]*busConn{assistantwire.AssistantID: {peer: rpc.New("old-manager", &notices)}}
	report(proto.MCPWaitComputer)
	var extra json.RawMessage
	if err := json.NewDecoder(&notices).Decode(&extra); err == nil {
		t.Fatalf("an older manager received %s", extra)
	}
	if old := h.mcpWaitReporter(assistantwire.AssistantID, "computer", "call-old"); old != nil {
		t.Fatal("an older manager must not enable phone wait updates")
	}
}

func TestCallPhoneReportsApprovalThenComputer(t *testing.T) {
	h := newTestHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	phone := &client{hub: h, device: "credential-ios", role: roleFull, out: make(chan []byte, 2)}
	addTestClient(h, phone)
	h.onClientMessage(ctx, phone, []byte(`{"type":"register_phone","tools":[{"name":"phone_reminders_create"}]}`))
	kinds := make(chan proto.MCPWaitKind, 2)
	report := func(kind proto.MCPWaitKind) { kinds <- kind }
	done := make(chan proto.ToolResult, 1)
	go func() { done <- h.callPhoneWithWait(ctx, "phone_reminders_create", json.RawMessage(`{}`), report) }()
	var call struct {
		CallID string `json:"call_id"`
	}
	select {
	case data := <-phone.out:
		if err := json.Unmarshal(data, &call); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("phone call was not sent")
	}
	select {
	case kind := <-kinds:
		if kind != proto.MCPWaitApproval {
			t.Fatalf("first kind = %q", kind)
		}
	case <-ctx.Done():
		t.Fatal("approval was not reported")
	}
	reply, _ := json.Marshal(map[string]any{"type": "tool_result", "call_id": call.CallID,
		"content": []proto.Content{{Type: "text", Text: "saved"}}})
	h.onClientMessage(ctx, phone, reply)
	select {
	case kind := <-kinds:
		if kind != proto.MCPWaitComputer {
			t.Fatalf("resolved kind = %q", kind)
		}
	case <-ctx.Done():
		t.Fatal("computer was not reported when the phone wait ended")
	}
	select {
	case result := <-done:
		if result.IsError || textOf(result) != "saved" {
			t.Fatalf("result = %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("phone result did not return")
	}
}
