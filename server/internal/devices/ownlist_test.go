package devices

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/managerconf"
	"github.com/nwverse/persona/server/internal/rpc"
)

func linkEntryOf(id string) json.RawMessage {
	b, _ := json.Marshal(MachineConfig{ID: id, Name: "Name of " + id, Kind: kindLink})
	return b
}

// ownListDevices is a manager's devices with file as its manager.json, run by Run until the test ends.
func ownListDevices(t *testing.T, file string) *Devices {
	t.Helper()
	d := New(Options{Hub: &fakeHost{}, Store: newMemStore(), Logf: func(string, ...any) {}, ConfigFile: file})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return d
}

// A manager whose hub does not answer connects the machines of its own list (manager.json) after ownListWait, as the
// hub's last hand-over had them; the hub's hand-over, when it comes, is the list, and is kept there. A hub that answers
// in time leaves nothing to connect before it; a list another hub handed over, or one from a mesh group, waits for the hub.
func TestTheOwnListStartsWithoutTheHub(t *testing.T) {
	was := ownListWait
	ownListWait = 100 * time.Millisecond
	t.Cleanup(func() { ownListWait = was })
	file := filepath.Join(t.TempDir(), managerconf.File)
	if _, _, _, err := managerconf.KeepMachines(file, []json.RawMessage{linkEntryOf("one"), linkEntryOf("two")},
		managerconf.MachinesFromHub{HubID: "hub-test"}); err != nil {
		t.Fatal(err)
	}

	d := ownListDevices(t, file)
	eventually(t, 3*time.Second, "the own list started", func() bool { return d.cfgMachine("one") != nil && d.cfgMachine("two") != nil })
	if _, err := d.PendingMachineIDs(context.Background()); err == nil {
		t.Fatal("the hub's word on pending computers taken as said")
	}
	one := d.cfgMachine("one")
	d.Configure(devicewire.Config{HubID: "hub-test", Machines: []json.RawMessage{linkEntryOf("one"), linkEntryOf("three")},
		Mesh: &devicewire.Mesh{}})
	if d.cfgMachine("one") != one || d.cfgMachine("two") != nil || d.cfgMachine("three") == nil {
		t.Fatal("the hub's hand-over is not the list")
	}
	list, from, ok, err := managerconf.OwnMachines(file)
	if err != nil || !ok || len(list) != 2 || !from.Mesh || from.HubID != "hub-test" {
		t.Fatalf("kept: %s %+v %v %v", list, from, ok, err)
	}

	// The hub was in a mesh group at that hand-over: the next start waits for it.
	next := ownListDevices(t, file)
	time.Sleep(4 * ownListWait)
	if next.cfgMachine("one") != nil {
		t.Fatal("a mesh group's computers connected without the hub")
	}

	// A hub that answers in time: nothing from the own list before it.
	if _, _, _, err := managerconf.KeepMachines(file, list, managerconf.MachinesFromHub{HubID: "hub-test"}); err != nil {
		t.Fatal(err)
	}
	ownListWait = time.Hour
	quick := ownListDevices(t, file)
	time.Sleep(200 * time.Millisecond)
	if quick.cfgMachine("one") != nil {
		t.Fatal("connected before the hub had the time to answer")
	}
	quick.Configure(devicewire.Config{HubID: "hub-test", Machines: []json.RawMessage{linkEntryOf("three")}})
	eventually(t, 3*time.Second, "the hub's list started", func() bool { return quick.cfgMachine("three") != nil })
	if quick.cfgMachine("one") != nil {
		t.Fatal("the own list was used with the hub there")
	}

	// The manager joined another hub since: that list is not read.
	ownListWait = 100 * time.Millisecond
	_ = managerconf.Edit(file, func(o *managerconf.Object) error { return o.Set("hub_id", "hub-other") })
	other := ownListDevices(t, file)
	time.Sleep(4 * ownListWait)
	if other.cfgMachine("three") != nil {
		t.Fatal("another hub's list connected")
	}
}

// A manager the hub hands MachinesOwn keeps its own list: the first such hand-over is taken over as its own (over a
// list taken over before), later ones' machines are not the list; the hub's edits (put, remove) change it and its
// machines at once, get gives an entry back. Without MachinesOwn the edits are refused; a hub that hands the list again
// (an older build) has it run, but the own list is kept for when it hands MachinesOwn again; an own list that does not
// read is never written over.
func TestTheHubEditsTheOwnList(t *testing.T) {
	file := filepath.Join(t.TempDir(), managerconf.File)
	d := newTestDevices(t)
	d.opts.ConfigFile = file
	ctx := context.Background()
	if err := d.putMachine(ctx, linkEntryOf("x")); !isCode(err, devicewire.CodeNotOwnList) {
		t.Fatalf("put without MachinesOwn: %v", err)
	}
	// A list kept before the hub handed MachinesOwn (phase 2's copy) is not the list: the hand-over is taken over.
	d.Configure(devicewire.Config{HubID: "hub-test", Machines: []json.RawMessage{linkEntryOf("stale")}})
	d.Configure(devicewire.Config{HubID: "hub-test", MachinesOwn: true, Machines: []json.RawMessage{linkEntryOf("one"), linkEntryOf("two")}})
	if d.cfgMachine("one") == nil || d.cfgMachine("two") == nil || d.cfgMachine("stale") != nil {
		t.Fatal("the first MachinesOwn hand-over was not taken over")
	}
	d.Configure(devicewire.Config{HubID: "hub-test", MachinesOwn: true, Machines: []json.RawMessage{}})
	if d.cfgMachine("one") == nil || d.cfgMachine("two") == nil {
		t.Fatal("a later hand-over's machines were taken as the list")
	}
	if err := d.putMachine(ctx, linkEntryOf("three")); err != nil || d.cfgMachine("three") == nil {
		t.Fatalf("put: %v", err)
	}
	if removed, err := d.removeMachine(ctx, "one"); !removed || err != nil || d.cfgMachine("one") != nil {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if e, err := d.machineEntry("two"); err != nil || !strings.Contains(string(e), `"Name of two"`) {
		t.Fatalf("get: %s %v", e, err)
	}
	if _, err := d.machineEntry("one"); !isCode(err, devicewire.CodeNoDevice) {
		t.Fatalf("get of one it does not have: %v", err)
	}

	// An older hub switched back to hands the list again: it runs, the own list is kept.
	d.Configure(devicewire.Config{HubID: "hub-test", Machines: []json.RawMessage{linkEntryOf("one")}})
	if d.cfgMachine("one") == nil || d.cfgMachine("two") != nil {
		t.Fatal("an older hub's hand-over is not what runs")
	}
	if err := d.putMachine(ctx, linkEntryOf("four")); !isCode(err, devicewire.CodeNotOwnList) {
		t.Fatalf("put while the hub hands the list: %v", err)
	}
	if list, from, _, _ := managerconf.OwnMachines(file); len(list) != 2 || !from.Own {
		t.Fatalf("the own list was written over: %s %+v", list, from)
	}
	d.Configure(devicewire.Config{HubID: "hub-test", MachinesOwn: true, Machines: []json.RawMessage{}})
	if d.cfgMachine("two") == nil || d.cfgMachine("three") == nil || d.cfgMachine("one") != nil {
		t.Fatal("the own list is not the list again")
	}

	// A list edited into something that does not read: the machines stay, the file is not written over.
	_ = managerconf.Edit(file, func(o *managerconf.Object) error {
		o.SetRaw(managerconf.MachinesKey, json.RawMessage(`"broken"`))
		return nil
	})
	d.Configure(devicewire.Config{HubID: "hub-test", MachinesOwn: true, Machines: []json.RawMessage{}})
	if d.cfgMachine("two") == nil || d.cfgMachine("three") == nil {
		t.Fatal("the machines went with a list that does not read")
	}
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), `"broken"`) {
		t.Fatal("a list that does not read was written over")
	}

	// A hand-over with MachinesOwn and no machines at all is taken over as an empty list of its own.
	fresh := newTestDevices(t)
	fresh.opts.ConfigFile = filepath.Join(t.TempDir(), managerconf.File)
	fresh.Configure(devicewire.Config{HubID: "hub-test", MachinesOwn: true})
	if err := fresh.putMachine(ctx, linkEntryOf("one")); err != nil || fresh.cfgMachine("one") == nil {
		t.Fatalf("put after an empty hand-over: %v", err)
	}
}

// The edge cases of a manager keeping its own list: no manager.json (nothing to keep: refused), another hub's own
// list (the hand-over is taken over), a list that does not read at the manager's start (the hand-over runs, the file
// stays), an entry with no lowercase id (refused as the hub's mistake).
func TestTheOwnListAtItsEdges(t *testing.T) {
	ctx := context.Background()
	none := newTestDevices(t)
	none.Configure(devicewire.Config{HubID: "hub-test", MachinesOwn: true, Machines: []json.RawMessage{linkEntryOf("one")}})
	if err := none.putMachine(ctx, linkEntryOf("two")); !isCode(err, devicewire.CodeNotOwnList) {
		t.Fatalf("put without a manager.json: %v", err)
	}

	file := filepath.Join(t.TempDir(), managerconf.File)
	if _, _, _, err := managerconf.KeepMachines(file, []json.RawMessage{linkEntryOf("theirs")}, managerconf.MachinesFromHub{HubID: "hub-a", Own: true}); err != nil {
		t.Fatal(err)
	}
	_ = managerconf.Edit(file, func(o *managerconf.Object) error { return o.Set("hub_id", "hub-b") })
	d := newTestDevices(t)
	d.opts.ConfigFile = file
	d.Configure(devicewire.Config{HubID: "hub-b", MachinesOwn: true, Machines: []json.RawMessage{linkEntryOf("mine")}})
	if d.cfgMachine("mine") == nil || d.cfgMachine("theirs") != nil {
		t.Fatal("another hub's own list was taken as this one's")
	}
	if copies, _ := filepath.Glob(file + ".machines-*"); len(copies) != 1 {
		t.Fatalf("another hub's own list not kept aside: %v", copies)
	} else if b, _ := os.ReadFile(copies[0]); !strings.Contains(string(b), "theirs") {
		t.Fatalf("the copy: %s", b)
	}
	for _, bad := range []string{`{"ID":"caps","kind":"link"}`, `{"id":"typed","kind":"link","botmux":"yes"}`} {
		if err := d.putMachine(ctx, json.RawMessage(bad)); !isCode(err, "invalid_params") {
			t.Fatalf("put %s: %v", bad, err)
		}
	}

	broken := filepath.Join(t.TempDir(), managerconf.File)
	if err := os.WriteFile(broken, []byte(`{"machines":"broken","machines_from_hub":{"at":"2026-01-01T00:00:00Z","own":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := New(Options{Hub: &fakeHost{}, Store: newMemStore(), Logf: func(string, ...any) {}, ConfigFile: broken})
	fctx, cancel := context.WithCancel(ctx)
	fresh.mu.Lock()
	fresh.ctx = fctx
	fresh.mu.Unlock()
	t.Cleanup(func() { cancel(); fresh.workers.Wait() })
	fresh.Configure(devicewire.Config{HubID: "hub-test", MachinesOwn: true, Machines: []json.RawMessage{linkEntryOf("handed")}})
	if fresh.cfgMachine("handed") == nil {
		t.Fatal("at the start, with a list that does not read, the hand-over does not run")
	}
	if b, _ := os.ReadFile(broken); !strings.Contains(string(b), `"broken"`) {
		t.Fatal("a list that does not read was written over")
	}
	// An older hub's hand-over over an own list that does not read does not write over it either.
	fresh.Configure(devicewire.Config{HubID: "hub-test", Machines: []json.RawMessage{linkEntryOf("older")}})
	if b, _ := os.ReadFile(broken); !strings.Contains(string(b), `"broken"`) || !strings.Contains(strings.ReplaceAll(string(b), " ", ""), `"own":true`) {
		t.Fatalf("an older hub's hand-over wrote over a list that does not read:\n%s", b)
	}
}

func isCode(err error, code string) bool {
	var re *rpc.Error
	return errors.As(err, &re) && re.Code == code
}
