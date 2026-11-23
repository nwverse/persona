package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Partner hubs and their codes: a code pairs once and only in its time, a hub paired already is never replaced (the
// code stays good, nothing changes), pairing again after an unpair revokes a credential left over (a mesh peer's of
// the same hub_id stays) and is a pairing of its own (created_at), the dial setting and what a live partner says are
// kept.
func TestPartnerHubsAndCodes(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	// Not in openTables (other lines of work edit it), as partner_messages_test.go's openPartners: a run before left them.
	if _, err := d.pool.Exec(ctx, `TRUNCATE partner_hubs, partner_codes`); err != nil {
		t.Fatal(err)
	}
	if err := d.CreatePartnerCode(ctx, "h1", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.PartnerCodeValid(ctx, "h1"); err != nil || !ok {
		t.Fatalf("fresh code valid = %v, %v", ok, err)
	}
	// A mesh credential of the same hub_id is not the partner's to revoke.
	peerCred := DeviceToken{ID: "dev_mesh", Name: "mesh", Kind: "peer", Role: "peer", MachineID: "hub-b"}
	if err := d.CreateDeviceToken(ctx, peerCred, "meshhash"); err != nil {
		t.Fatal(err)
	}
	paired := time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Add(123456789) // finer than Postgres keeps
	p := PartnerHub{HubID: "hub-b", Name: "B", FP: "", Endpoints: nil, Token: "sealed", CredentialID: "dev_b", Dial: "never", CreatedAt: paired}
	cred := DeviceToken{ID: "dev_b", Name: "Partner hub: B", Kind: "partner", Role: "partner", MachineID: "hub-b"}
	if ok, err := d.RedeemPartnerCode(ctx, "h1", p, cred, "credhash1"); err != nil || !ok {
		t.Fatalf("redeem = %v, %v", ok, err)
	}
	// Kept as given, to the microsecond: the hub's own copy says the same.
	if list, _ := d.PartnerHubs(ctx); len(list) != 1 || !list[0].CreatedAt.Equal(paired.Truncate(time.Microsecond)) {
		t.Fatalf("created_at: %+v", list)
	}
	cred2 := DeviceToken{ID: "dev_b2", Name: "Partner hub: B", Kind: "partner", Role: "partner", MachineID: "hub-b"}
	if ok, err := d.RedeemPartnerCode(ctx, "h1", p, cred2, "credhash2"); err != nil || ok {
		t.Fatalf("second redeem = %v, %v", ok, err)
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "credhash2"); tok != nil {
		t.Fatal("a used code still made a credential")
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "credhash1"); tok == nil || tok.Role != "partner" || tok.MachineID != "hub-b" {
		t.Fatalf("credential %+v", tok)
	}
	list, err := d.PartnerHubs(ctx)
	if err != nil || len(list) != 1 || list[0].Dial != "never" || list[0].Endpoints == nil || len(list[0].Endpoints) != 0 || list[0].SeenAt != nil {
		t.Fatalf("partners %+v, %v", list, err)
	}
	// A new code for a hub paired already (whoever holds it claims that hub_id): refused, nothing changed, the code
	// still good.
	if err := d.CreatePartnerCode(ctx, "h2", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	taker := PartnerHub{HubID: "hub-b", Name: "Taker", FP: "fp-taker", Token: "sealed-taker", CredentialID: cred2.ID, Dial: "auto"}
	if ok, err := d.RedeemPartnerCode(ctx, "h2", taker, cred2, "credhash2"); !errors.Is(err, ErrPartnerPaired) || ok {
		t.Fatalf("redeem for a hub paired already = %v, %v", ok, err)
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "credhash1"); tok == nil {
		t.Fatal("a refused pairing revoked the partner's credential")
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "credhash2"); tok != nil {
		t.Fatal("a refused pairing made a credential")
	}
	if list, _ := d.PartnerHubs(ctx); len(list) != 1 || list[0].Name != "B" || list[0].CredentialID != "dev_b" || list[0].Token != "sealed" {
		t.Fatalf("a refused pairing changed the partner: %+v", list)
	}
	if ok, _ := d.PartnerCodeValid(ctx, "h2"); !ok {
		t.Fatal("a refused pairing used the code up")
	}
	if err := d.SavePartnerHub(ctx, taker); !errors.Is(err, ErrPartnerPaired) {
		t.Fatalf("save over a partner: %v", err)
	}
	// Unpaired (here without revoking, as a credential left over), then paired again: the leftover revoked, not the
	// mesh one, and a pairing of its own.
	if ok, err := d.DeletePartnerHub(ctx, "hub-b"); err != nil || !ok {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	p.Dial, p.CredentialID, p.CreatedAt = "auto", cred2.ID, time.Time{}
	if ok, err := d.RedeemPartnerCode(ctx, "h2", p, cred2, "credhash2"); err != nil || !ok {
		t.Fatalf("re-pair = %v, %v", ok, err)
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "credhash1"); tok != nil {
		t.Fatal("the old partner credential still works")
	}
	if tok, _ := d.DeviceTokenByHash(ctx, "meshhash"); tok == nil {
		t.Fatal("pairing a partner revoked a mesh credential")
	}
	if list, _ := d.PartnerHubs(ctx); len(list) != 1 || !list[0].CreatedAt.After(paired) {
		t.Fatalf("paired again with the old created_at: %+v", list)
	}
	// Seen for an older pairing (another credential) changes nothing; for the current one it is kept.
	if err := d.UpdatePartnerSeen(ctx, PartnerHub{HubID: "hub-b", Name: "old", FP: "fp-old", CredentialID: cred.ID}); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.PartnerHubs(ctx); list[0].Name != "B" || list[0].FP != "" || list[0].SeenAt != nil {
		t.Fatalf("seen for an older pairing: %+v", list[0])
	}
	if err := d.UpdatePartnerSeen(ctx, PartnerHub{HubID: "hub-b", Name: "B2", FP: "fp-b", Endpoints: []string{"https://[2001:db8::1]:18662"},
		CredentialID: cred2.ID}); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.SetPartnerDial(ctx, "hub-b", "never"); err != nil || !ok {
		t.Fatalf("set dial = %v, %v", ok, err)
	}
	if ok, _ := d.SetPartnerDial(ctx, "hub-x", "never"); ok {
		t.Fatal("set the dial of a hub that is not paired")
	}
	list, _ = d.PartnerHubs(ctx)
	if got := list[0]; got.Name != "B2" || got.FP != "fp-b" || got.Endpoints[0] != "https://[2001:db8::1]:18662" || got.Dial != "never" ||
		got.SeenAt == nil || got.CredentialID != "dev_b2" {
		t.Fatalf("after seen: %+v", got)
	}
	// SavePartnerHub adds (the joining side's save), never over a partner.
	p2 := PartnerHub{HubID: "hub-c", Name: "C", FP: "fp-c", Endpoints: []string{"https://hub.example.com:18662"}, Token: "sealed-c", CredentialID: "dev_c"}
	if err := d.SavePartnerHub(ctx, p2); err != nil {
		t.Fatal(err)
	}
	p2.Name = "C2"
	if err := d.SavePartnerHub(ctx, p2); !errors.Is(err, ErrPartnerPaired) {
		t.Fatalf("saved twice: %v", err)
	}
	list, _ = d.PartnerHubs(ctx)
	if len(list) != 2 || list[1].Name != "C" || list[1].Dial != "auto" || list[1].CreatedAt.IsZero() {
		t.Fatalf("saved: %+v", list)
	}
	// An expired code is not valid, and pruning drops it.
	if err := d.CreatePartnerCode(ctx, "h3", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.PartnerCodeValid(ctx, "h3"); ok {
		t.Fatal("expired code valid")
	}
	if ok, _ := d.RedeemPartnerCode(ctx, "h3", p, DeviceToken{ID: "dev_b3", Kind: "partner", Role: "partner", MachineID: "hub-b"}, "credhash3"); ok {
		t.Fatal("an expired code paired")
	}
	if err := d.PrunePartnerCodes(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM partner_codes WHERE code_hash = 'h3'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("pruned: %d left, %v", left, err)
	}
	if ok, err := d.DeletePartnerHub(ctx, "hub-b"); err != nil || !ok {
		t.Fatalf("delete = %v, %v", ok, err)
	}
	if ok, _ := d.DeletePartnerHub(ctx, "hub-b"); ok {
		t.Fatal("deleted twice")
	}
}
