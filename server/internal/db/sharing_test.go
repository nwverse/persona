package db

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
)

func TestShareRoutingIdentityAndAssignmentGenerations(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	issuer := "synthetic-routing-authority"
	machine := "synthetic-routing-machine"
	for _, id := range []string{"synthetic-manager-a", "synthetic-manager-b"} {
		if err := d.SaveBusIdentity(ctx, BusIdentity{ID: id, Role: "manager", Name: "Synthetic manager"}, id+"-hash"); err != nil {
			t.Fatal(err)
		}
	}
	a, err := d.ShareRouting(ctx, issuer, machine, "synthetic-manager-a")
	if err != nil {
		t.Fatal(err)
	}
	again, err := d.ShareRouting(ctx, issuer, machine, "synthetic-manager-a")
	if err != nil || !bytes.Equal(a.Handle, again.Handle) || a.Authority != again.Authority {
		t.Fatal("stable roster/assignment changed")
	}
	if err := d.SaveBusIdentity(ctx, BusIdentity{ID: "synthetic-manager-a", Role: "manager"}, "rotated-synthetic-hash"); err != nil {
		t.Fatal(err)
	}
	rotated, err := d.ShareRouting(ctx, issuer, machine, "synthetic-manager-a")
	if err != nil || !bytes.Equal(a.Handle, rotated.Handle) {
		t.Fatal("token rotation changed public routing identity")
	}
	b, err := d.ShareRouting(ctx, issuer, machine, "synthetic-manager-b")
	if err != nil || b.Authority.Generation == a.Authority.Generation {
		t.Fatal("move did not advance assignment")
	}
	if d.VerifyShareAuthority(ctx, "synthetic-manager-a", a.Authority) == nil {
		t.Fatal("old manager remained authoritative")
	}
	back, err := d.ShareRouting(ctx, issuer, machine, "synthetic-manager-a")
	if err != nil || back.Authority.Generation == a.Authority.Generation {
		t.Fatal("moving back revived assignment")
	}
	if err := d.RetireShareMachine(ctx, issuer, machine); err != nil {
		t.Fatal(err)
	}
	recreated, err := d.ShareRouting(ctx, issuer, machine, "synthetic-manager-a")
	if err != nil || recreated.Authority.Generation == back.Authority.Generation {
		t.Fatal("machine recreation revived assignment")
	}
	if _, err := d.DeleteBusIdentity(ctx, "synthetic-manager-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ShareManagerByHandle(ctx, a.Handle); err == nil {
		t.Fatal("removed manager remained routable")
	}
	if err := d.SaveBusIdentity(ctx, BusIdentity{ID: "synthetic-manager-a", Role: "manager"}, "recreated-synthetic-hash"); err != nil {
		t.Fatal(err)
	}
	newRoute, err := d.ShareRouting(ctx, issuer, machine, "synthetic-manager-a")
	if err != nil || bytes.Equal(a.Handle, newRoute.Handle) {
		t.Fatal("recreated roster revived routing handle")
	}
	if _, err := d.ShareManagerByHandle(ctx, a.Handle); err == nil {
		t.Fatal("retired capability became routable")
	}
}
func TestShareCreationReceiptRequiresDurableMatchingState(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	machine := "synthetic-creation-machine"
	caller := devicewire.Caller{Kind: "device", Issuer: "synthetic-creation-authority", OwnerSubject: "30000000-0000-4000-8000-000000000001"}
	authority := devicewire.ShareAuthority{Issuer: caller.Issuer, Machine: machine, Generation: "synthetic-generation"}
	intent, err := d.BeginSessionCreation(ctx, machine, caller, authority)
	if err != nil {
		t.Fatal(err)
	}
	if owner, err := d.SessionShareOwner(ctx, machine, "synthetic-created-session"); err != nil || owner != nil {
		t.Fatal("intent alone established ownership")
	}
	state := NodeSession{Launcher: "synthetic-launcher", Cwd: "/workspace", StartedAt: time.Now(), CreationID: intent.CreationID, Incarnation: "wrong-incarnation"}
	if err := d.PutNodeSession(ctx, machine, "synthetic-wrong-receipt", state); err != nil {
		t.Fatal(err)
	}
	if owner, err := d.SessionShareOwner(ctx, machine, "synthetic-wrong-receipt"); err != nil || owner != nil {
		t.Fatal("mismatched incarnation bound ownership")
	}
	// A distinct intent proves the successful path; a bad immutable receipt is not repaired by overwriting it.
	intent, err = d.BeginSessionCreation(ctx, machine, caller, authority)
	if err != nil {
		t.Fatal(err)
	}
	state.CreationID, state.Incarnation = intent.CreationID, intent.Incarnation
	state.StartedAt = time.Now().Add(-90 * 24 * time.Hour)
	if err := d.PutNodeSession(ctx, machine, "synthetic-created-session", state); err != nil {
		t.Fatal(err)
	}
	owner, err := d.SessionShareOwner(ctx, machine, "synthetic-created-session")
	if err != nil || owner == nil || owner.Subject != caller.OwnerSubject || owner.Incarnation != intent.Incarnation {
		t.Fatal("durable receipt did not reconcile")
	}
	loaded, err := d.NodeSessions(ctx, machine)
	if err != nil || loaded["synthetic-created-session"].Incarnation != intent.Incarnation {
		t.Fatal("creation state disappeared after 60 days")
	}
	state.CreationID, state.Incarnation = "forged-creation", "forged-incarnation"
	if err := d.PutNodeSession(ctx, machine, "synthetic-created-session", state); err != nil {
		t.Fatal(err)
	}
	loaded, err = d.NodeSessions(ctx, machine)
	if err != nil || loaded["synthetic-created-session"].Incarnation != intent.Incarnation {
		t.Fatal("creation receipt was replaced")
	}
	t.Log("synthetic durable creation: intent-only denied, wrong incarnation denied, matching persisted state binds, receipt immutable, retention >60 days PASS")
}
