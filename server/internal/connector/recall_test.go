package connector

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// recallWriter is a writer that can also take back what it wrote (Recaller).
type recallWriter struct{ writer }

func (recallWriter) CanRecall(action string, age time.Duration) bool {
	return action == "example_comment" && age < time.Hour
}

func (recallWriter) Recall(context.Context, SentMessage) error { return nil }

// A connector is a Recaller by implementing it, and only then: one without it, and no connector at all, have nothing
// to recall.
func TestRecallerOf(t *testing.T) {
	f := &fake{id: "example", status: Status{State: StateOK}}
	if r, ok := RecallerOf(recallWriter{writer{f}}); !ok || !r.CanRecall("example_comment", time.Minute) || r.CanRecall("example_note", time.Minute) {
		t.Fatalf("a connector with Recall: %v %v", r, ok)
	}
	if _, ok := RecallerOf(writer{f}); ok {
		t.Error("a connector without Recall has it")
	}
	if _, ok := RecallerOf(plain{}); ok {
		t.Error("a connector with no actions has Recall")
	}
	if _, ok := RecallerOf(nil); ok {
		t.Error("no connector has Recall")
	}
}

// Recall is not a capability: a connector that has it registers as it did, declares nothing more, and its writes go
// through the gate and run as before.
func TestRecallerIsNoCapability(t *testing.T) {
	f := &fake{id: "example", status: Status{State: StateOK}}
	r := NewRegistry([]Rule{{Connector: "*", Action: "*", Decision: Allow}})
	c := recallWriter{writer{f}}
	if err := r.Add(c); err != nil {
		t.Fatalf("a Recaller does not register: %v", err)
	}
	if got, plainCaps := Capabilities(c), Capabilities(writer{f}); !slices.Equal(got, plainCaps) {
		t.Errorf("capabilities %v, without Recall %v", got, plainCaps)
	}
	if res := r.Call(context.Background(), "example_comment", json.RawMessage(`{"text":"hi"}`), nil); res.IsError || len(f.did) != 1 {
		t.Fatalf("a write of a Recaller: %+v, ran %v", res, f.did)
	}
}

// proxyRecaller is a Recaller that answers for a program: a proxy implements every interface and has only what it
// declares.
type proxyRecaller struct {
	recallWriter
	caps []Capability
}

func (p proxyRecaller) Manifest() Manifest { return Manifest{Capabilities: p.caps, Proxy: true} }

// A program's connector implements every interface, so what its manifest declares decides whether the hub finds it as a
// Recaller: without recall it is offered nothing, with it the hub finds it. The declaration registers like any other and
// is not one of the capabilities GET /api/v1/connectors lists.
func TestRecallerOfAProgramGoesByItsDeclaration(t *testing.T) {
	f := &fake{id: "example", status: Status{State: StateOK}}
	base := recallWriter{writer{f}}
	undeclared := proxyRecaller{base, []Capability{CapStatus, CapActions}}
	declared := proxyRecaller{base, []Capability{CapStatus, CapActions, CapRecall}}
	if _, ok := RecallerOf(undeclared); ok {
		t.Error("a program that does not declare recall is found as a Recaller")
	}
	if r, ok := RecallerOf(declared); !ok || !r.CanRecall("example_comment", time.Minute) {
		t.Errorf("a program that declares recall: %v %v", r, ok)
	}
	reg := NewRegistry(nil)
	if err := reg.Add(declared); err != nil {
		t.Fatalf("a program declaring recall does not register: %v", err)
	}
	if got, without := Capabilities(declared), Capabilities(undeclared); !slices.Equal(got, without) {
		t.Errorf("capabilities %v, without recall %v", got, without)
	}
}
