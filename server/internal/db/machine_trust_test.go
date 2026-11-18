package db

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestMachineTrust(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	if list, err := d.PendingMachines(ctx); err != nil || len(list) != 0 {
		t.Fatalf("no rows: every computer is confirmed: %v %v", list, err)
	}
	if err := d.SetMachinePending(ctx, "laptop", "dev-1"); err != nil {
		t.Fatal(err)
	}
	list, err := d.PendingMachines(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "laptop" || list[0].AddedBy != "dev-1" || list[0].CreatedAt.IsZero() {
		t.Fatalf("pending %+v %v", list, err)
	}
	if ok, err := d.ConfirmMachine(ctx, "other"); err != nil || ok {
		t.Fatalf("confirming one that is not pending: %v %v", ok, err)
	}
	if ok, err := d.ConfirmMachine(ctx, "laptop"); err != nil || !ok {
		t.Fatalf("confirm %v %v", ok, err)
	}
	if ok, _ := d.ConfirmMachine(ctx, "laptop"); ok {
		t.Fatal("confirmed twice")
	}
	if list, _ := d.PendingMachines(ctx); len(list) != 0 {
		t.Fatalf("confirmed is not pending: %+v", list)
	}
	if err := d.SetMachinePending(ctx, "laptop", "dev-2"); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.PendingMachines(ctx); len(list) != 1 || list[0].AddedBy != "dev-2" {
		t.Fatalf("pending again %+v", list)
	}
	if err := d.ForgetMachineTrust(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.PendingMachines(ctx); len(list) != 0 {
		t.Fatalf("forgotten %+v", list)
	}
}

func TestRevokeMachineCredentials(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	for _, tok := range []DeviceToken{
		{ID: "link-1", Name: "laptop", Kind: "link", Role: "link", MachineID: "laptop"},
		{ID: "link-2", Name: "desk", Kind: "link", Role: "link", MachineID: "desk"},
		{ID: "phone-1", Name: "phone", Kind: "phone", Role: "full"},
		{ID: "peer-1", Name: "hub", Kind: "peer", Role: "peer", MachineID: "laptop"}, // a peer hub whose id is the same
	} {
		if err := d.CreateDeviceToken(ctx, tok, "hash-"+tok.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.CreatePairToken(ctx, "pair-laptop", time.Now().Add(10*time.Minute), "laptop"); err != nil {
		t.Fatal(err)
	}
	ids, err := d.RevokeMachineCredentials(ctx, "laptop")
	if err != nil || len(ids) != 1 || ids[0] != "link-1" {
		t.Fatalf("revoked %v %v", ids, err)
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "hash-link-1"); tok != nil {
		t.Fatal("the laptop's credential still works")
	}
	for _, h := range []string{"hash-link-2", "hash-phone-1", "hash-peer-1"} {
		if tok, _ := d.DeviceTokenByHash(ctx, h); tok == nil {
			t.Fatalf("%s was revoked too", h)
		}
	}
	if p, _ := d.PairTokenByHash(ctx, "pair-laptop"); p == nil || p.ExpiresAt.After(time.Now()) {
		t.Fatalf("the laptop's pairing code still works: %+v", p)
	}
}

// Whether a computer has a Persona Link already (a switch from ssh refuses one that has): a live link credential or
// a pairing code for it that is still open counts; a revoked credential, a used or expired code, another computer's,
// and a peer hub's credential that binds the same id do not.
func TestMachineLinkInUse(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	inUse := func(machine string) bool {
		t.Helper()
		got, err := d.MachineLinkInUse(ctx, machine)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if inUse("laptop") {
		t.Fatal("nothing yet")
	}
	if err := d.CreateDeviceToken(ctx, DeviceToken{ID: "peer-1", Name: "hub", Kind: "peer", Role: "peer", MachineID: "laptop"}, "hash-peer"); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateDeviceToken(ctx, DeviceToken{ID: "link-desk", Name: "desk", Kind: "link", Role: "link", MachineID: "desk"}, "hash-desk"); err != nil {
		t.Fatal(err)
	}
	if err := d.CreatePairToken(ctx, "pair-desk", time.Now().Add(time.Hour), "desk"); err != nil {
		t.Fatal(err)
	}
	if err := d.CreatePairToken(ctx, "pair-over", time.Now().Add(-time.Minute), "laptop"); err != nil {
		t.Fatal(err)
	}
	if err := d.CreatePairToken(ctx, "pair-app", time.Now().Add(time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	if inUse("laptop") {
		t.Fatal("a peer's credential, another computer's, an expired code or an app's code counts")
	}
	if err := d.CreatePairToken(ctx, "pair-open", time.Now().Add(time.Hour), "laptop"); err != nil {
		t.Fatal(err)
	}
	if !inUse("laptop") {
		t.Fatal("an open pairing code does not count")
	}
	link := DeviceToken{ID: "link-1", Name: "laptop", Kind: "link", Role: "link", MachineID: "laptop"}
	if ok, err := d.RedeemPairToken(ctx, "pair-open", link, "hash-link-1"); err != nil || !ok {
		t.Fatalf("redeem: %v %v", ok, err)
	}
	if !inUse("laptop") {
		t.Fatal("a live credential does not count")
	}
	if ok, err := d.RevokeDeviceToken(ctx, "link-1"); err != nil || !ok {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if inUse("laptop") {
		t.Fatal("a revoked credential or a used code counts")
	}
	if !inUse("desk") {
		t.Fatal("desk's credential and code do not count")
	}
	if _, err := d.MachineLinkInUse(ctx, ""); err == nil {
		t.Fatal("no machine accepted")
	}
}

// Whether a switch's begin took effect, by its audit row: that operation's begun step on that computer only. The
// audit is not cleared between tests: the computers are this test's, the operations this run's.
func TestLinkSwitchBegun(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	run := fmt.Sprintf("%x", time.Now().UnixNano())
	op := func(s string) string { return "ls_" + s + run }
	for _, a := range []struct{ machine, action, detail string }{
		{"begun-laptop", "link_switch", `{"operation":"` + op("a") + `","step":"begun","by":"device"}`},
		{"begun-laptop", "link_switch", `{"operation":"` + op("b") + `","step":"begin_failed"}`},
		{"begun-desk", "link_switch", `{"operation":"` + op("c") + `","step":"begun"}`},
		{"begun-laptop", "code", `{"operation":"` + op("d") + `","step":"begun"}`},
	} {
		if err := d.AddMachineAudit(ctx, a.machine, a.action, "manager:assistant", json.RawMessage(a.detail)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		machine, operation string
		want               bool
	}{
		{"begun-laptop", op("a"), true}, {"begun-laptop", op("b"), false}, {"begun-laptop", op("c"), false},
		{"begun-desk", op("c"), true}, {"begun-laptop", op("d"), false}, {"begun-laptop", op("none"), false},
	} {
		if got, err := d.LinkSwitchBegun(ctx, c.machine, c.operation); err != nil || got != c.want {
			t.Errorf("%s %s: %v %v, want %v", c.machine, c.operation, got, err, c.want)
		}
	}
	if _, err := d.LinkSwitchBegun(ctx, "begun-laptop", ""); err == nil {
		t.Fatal("no operation accepted")
	}
}

func TestMachineAudit(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	if err := d.AddMachineAudit(ctx, "laptop", "confirm", "dev-1", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMachineAudit(ctx, "laptop", "reject", "dev-1", []byte(`{"why":"x"}`)); err != nil {
		t.Fatal(err)
	}
	list, err := d.MachineAudits(ctx, "laptop")
	if err != nil || len(list) != 2 || list[0].Action != "confirm" || list[1].Action != "reject" || string(list[1].Detail) != `{"why": "x"}` {
		t.Fatalf("audit %+v %v", list, err)
	}
}
