package devices

import (
	"cmp"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Only the name differs: the same entry, so the machine is kept (a rename never reconnects it).
func TestSameConfigIgnoresTheName(t *testing.T) {
	a := MachineConfig{ID: "lap", Name: "Laptop", Kind: kindLink, Icon: "laptop"}
	b := a
	b.Name = "Studio"
	if !sameConfig(a, b) {
		t.Fatal("a rename counts as another entry")
	}
	b.Icon = "desktop"
	if sameConfig(a, b) {
		t.Fatal("another icon is the same entry")
	}
}

// The hub renames a computer: the manager keeps its machine and its Persona Link, shows the new name, and tells the
// link what it is called now (endpoints).
func TestRenameKeepsTheMachineAndItsLink(t *testing.T) {
	d := newTestDevices(t)
	d.Configure(devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{linkEntry("Laptop")}})
	m := d.cfgMachine("lap")
	c, l := dialTestLink(t, d, "lap")
	if got := c.next(t, "endpoints").MachineName; got != "Laptop" {
		t.Fatalf("the link is told it is %q", got)
	}
	d.Configure(devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{linkEntry("Studio")}})
	if d.cfgMachine("lap") != m {
		t.Fatal("a rename made a new machine")
	}
	if got := c.next(t, "endpoints").MachineName; got != "Studio" {
		t.Fatalf("after the rename the link is told it is %q", got)
	}
	if l.ctx.Err() != nil || m.currentLink() != l {
		t.Fatal("a rename ended the link")
	}
	if s := m.snapshot(); s.Name != "Studio" {
		t.Fatalf("shown as %q", s.Name)
	}
	if got := d.List()[0].Name; got != "Studio" {
		t.Fatalf("the assistant sees %q", got)
	}
	// An entry that names it nothing: its id.
	bare, _ := json.Marshal(MachineConfig{ID: "lap", Kind: kindLink})
	d.Configure(devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{bare}})
	if got := c.next(t, "endpoints").MachineName; got != "lap" || m.snapshot().Name != "lap" {
		t.Fatalf("no name: told %q, shown %q", got, m.snapshot().Name)
	}
}

// Persona Link asks to rename its computer: the manager asks the hub and answers renamed with the hub's name, or its
// refusal; a hub that does not rename computers is a refusal too.
func TestLinkAsksToRenameItsComputer(t *testing.T) {
	d := newTestDevices(t)
	d.Configure(devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{linkEntry("Laptop")}})
	c, _ := dialTestLink(t, d, "lap")
	var asked devicewire.Rename
	d.host().mu.Lock()
	d.host().rename = func(p devicewire.Rename) (devicewire.RenameReply, error) {
		asked = p
		if p.Name == "bad" {
			return devicewire.RenameReply{}, &rpc.Error{Code: devicewire.RenameCodePending, Message: "confirm it first"}
		}
		return devicewire.RenameReply{Name: p.Name}, nil
	}
	d.host().mu.Unlock()
	c.send(t, link.Message{Type: "rename", ID: "r1", MachineName: "Studio"})
	if got := c.next(t, "renamed"); got.ID != "r1" || got.MachineName != "Studio" || got.Code != "" {
		t.Fatalf("renamed %+v", got)
	}
	d.host().mu.Lock()
	if asked.MachineID != "lap" || asked.Name != "Studio" {
		t.Fatalf("the hub was asked %+v", asked)
	}
	d.host().mu.Unlock()
	c.send(t, link.Message{Type: "rename", ID: "r2", MachineName: "bad"})
	if got := c.next(t, "renamed"); got.ID != "r2" || got.Code != devicewire.RenameCodePending || got.Error != "confirm it first" {
		t.Fatalf("refused: %+v", got)
	}
	d.host().mu.Lock()
	d.host().rename = nil
	d.host().mu.Unlock()
	c.send(t, link.Message{Type: "rename", ID: "r3", MachineName: "x"})
	if got := c.next(t, "renamed"); got.ID != "r3" || got.Code == "" {
		t.Fatalf("an older hub: %+v", got)
	}
}

// A member renames one of its computers: the center keeps its machine for it, under the new name.
func TestMemberRenameKeepsTheDerivedMachine(t *testing.T) {
	d := New(Options{Hub: &fakeHost{}})
	var kept *Machine
	for _, name := range []string{"Old", "New", ""} {
		cfg, _ := json.Marshal(MachineConfig{ID: "remote", Name: name})
		d.cfg.Mesh = &devicewire.Mesh{Members: []devicewire.MemberRecord{{HubID: "member", Machines: []devicewire.RecMachine{{ID: "remote", Cfg: cfg}}}}}
		d.fleetRegroup()
		list := d.List()
		if want := cmp.Or(name, "remote"); len(list) != 1 || list[0].Name != want {
			t.Fatalf("named %q: %+v", name, list)
		}
		m := d.machineByID("remote")
		if kept != nil && kept != m {
			t.Fatal("a rename made a new machine")
		}
		kept = m
	}
}
