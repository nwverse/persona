package db

import (
	"context"
	"sync"
	"testing"
	"time"
)

// openPartners is open with the partner tables emptied: they are not in openTables, which other lines of work edit.
func openPartners(t *testing.T) *DB {
	t.Helper()
	d := open(t)
	if _, err := d.pool.Exec(context.Background(), `TRUNCATE partner_messages, partner_prefs`); err != nil {
		t.Fatal(err)
	}
	return d
}

// An envelope is kept once per direction, partner and message_id: a resend finds the row it made, the same message_id
// from the other direction or from another partner is a row of its own.
func TestPartnerMessagesInsertOnce(t *testing.T) {
	d, ctx := openPartners(t), context.Background()
	m := PartnerMessage{ID: "pr_1", Direction: "in", PeerHub: "hub-b", PeerName: "B", MessageID: "pm_1", Kind: "message", Hop: 1,
		Text: "hello", State: "received"}
	got, created, err := d.InsertPartnerMessage(ctx, m)
	if err != nil || !created || got.ID != "pr_1" || got.Revision != 1 || got.CreatedAt.IsZero() || got.NextTry != nil ||
		!got.PairedAt.IsZero() {
		t.Fatalf("insert = %+v %v %v", got, created, err)
	}
	// The pairing a row was written for is kept as the hub said it (microseconds, UTC); none is NULL, read as zero.
	paired := time.Date(2026, 11, 20, 9, 30, 0, 123456789, time.FixedZone("UTC+8", 8*3600))
	if got, _, err := d.InsertPartnerMessage(ctx, PartnerMessage{ID: "pr_p", Direction: "out", PeerHub: "hub-b", MessageID: "pm_p",
		Kind: "message", State: "queued", PairedAt: paired}); err != nil || !got.PairedAt.Equal(paired.Truncate(time.Microsecond)) ||
		got.PairedAt.Location() != time.UTC {
		t.Fatalf("paired_at = %v %v", got.PairedAt, err)
	}
	if got, err := d.PartnerMessageOf(ctx, "out", "hub-b", "pm_p"); err != nil || got == nil || !got.PairedAt.Equal(paired.Truncate(time.Microsecond)) {
		t.Fatalf("paired_at read = %+v %v", got, err)
	}
	again := m
	again.ID, again.Text = "pr_2", "changed"
	got, created, err = d.InsertPartnerMessage(ctx, again)
	if err != nil || created || got.ID != "pr_1" || got.Text != "hello" {
		t.Fatalf("resend = %+v %v %v", got, created, err)
	}
	for i, other := range []PartnerMessage{{ID: "pr_3", Direction: "out", PeerHub: "hub-b", MessageID: "pm_1", Kind: "message", State: "queued"},
		{ID: "pr_4", Direction: "in", PeerHub: "hub-c", MessageID: "pm_1", Kind: "message", State: "received"}} {
		if _, created, err := d.InsertPartnerMessage(ctx, other); err != nil || !created {
			t.Fatalf("other %d: %v %v", i, created, err)
		}
	}
	if m, err := d.PartnerMessageOf(ctx, "in", "hub-b", "pm_9"); m != nil || err != nil {
		t.Fatalf("missing = %+v %v", m, err)
	}
	// Concurrent resends of one envelope: one row, one creator.
	var wg sync.WaitGroup
	made := make(chan bool, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, created, err := d.InsertPartnerMessage(ctx, PartnerMessage{ID: "pr_c" + string(rune('a'+i)), Direction: "in", PeerHub: "hub-b",
				MessageID: "pm_same", Kind: "message", State: "received"})
			if err != nil {
				t.Error(err)
			}
			made <- created
		}()
	}
	wg.Wait()
	close(made)
	n := 0
	for c := range made {
		if c {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d concurrent inserts created the row", n)
	}
}

// A change is written only from the revision read; a writer that read an older one learns it lost.
func TestPartnerMessagesCompareAndSet(t *testing.T) {
	d, ctx := openPartners(t), context.Background()
	m, _, err := d.InsertPartnerMessage(ctx, PartnerMessage{ID: "pr_1", Direction: "out", PeerHub: "hub-b", MessageID: "pm_1",
		Kind: "message", Text: "hi", State: "confirming", MachineID: "server", SessionID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	stale := m
	now := time.Now()
	m.State, m.NextTry, m.CardID = "queued", &now, "ix_1"
	if ok, err := d.UpdatePartnerMessage(ctx, m); !ok || err != nil {
		t.Fatalf("update = %v %v", ok, err)
	}
	stale.State = "declined"
	if ok, err := d.UpdatePartnerMessage(ctx, stale); ok || err != nil {
		t.Fatalf("stale update = %v %v", ok, err)
	}
	got, err := d.PartnerMessageOf(ctx, "out", "hub-b", "pm_1")
	if err != nil || got.State != "queued" || got.CardID != "ix_1" || got.Revision != 2 || got.NextTry == nil || got.SessionID != "main" {
		t.Fatalf("row = %+v %v", got, err)
	}
	if list, err := d.PartnerMessagesInState(ctx, "out", "queued"); err != nil || len(list) != 1 || list[0].ID != "pr_1" {
		t.Fatalf("in state = %+v %v", list, err)
	}
	if list, err := d.PartnerMessagesInState(ctx, "in", "queued"); err != nil || len(list) != 0 {
		t.Fatalf("other direction = %+v %v", list, err)
	}
}

// The loop claims what is due, once: a claimed row's next try moves past the lease, so a second claim (another
// manager) gets nothing, and its revision moved, so a writer that read it before the claim loses.
func TestPartnerMessagesClaim(t *testing.T) {
	d, ctx := openPartners(t), context.Background()
	past, future := time.Now().Add(-time.Second), time.Now().Add(time.Hour)
	for _, m := range []PartnerMessage{
		{ID: "pr_due", Direction: "out", PeerHub: "hub-b", MessageID: "pm_1", Kind: "message", State: "queued", NextTry: &past},
		{ID: "pr_later", Direction: "out", PeerHub: "hub-b", MessageID: "pm_2", Kind: "message", State: "queued", NextTry: &future},
		{ID: "pr_idle", Direction: "out", PeerHub: "hub-b", MessageID: "pm_3", Kind: "message", State: "sent"},
	} {
		if _, _, err := d.InsertPartnerMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := d.PartnerMessageOf(ctx, "out", "hub-b", "pm_1")
	got, err := d.ClaimPartnerMessages(ctx, time.Minute, 10)
	if err != nil || len(got) != 1 || got[0].ID != "pr_due" || got[0].Revision != before.Revision+1 ||
		got[0].NextTry == nil || time.Until(*got[0].NextTry) < 50*time.Second {
		t.Fatalf("claim = %+v %v", got, err)
	}
	if again, err := d.ClaimPartnerMessages(ctx, time.Minute, 10); err != nil || len(again) != 0 {
		t.Fatalf("second claim = %+v %v", again, err)
	}
	before.State = "failed"
	if ok, _ := d.UpdatePartnerMessage(ctx, *before); ok {
		t.Fatal("a write from before the claim won")
	}
	claimed := got[0]
	claimed.State, claimed.NextTry = "sent", nil
	if ok, err := d.UpdatePartnerMessage(ctx, claimed); !ok || err != nil {
		t.Fatalf("the claimer's write = %v %v", ok, err)
	}
}

// Recent lists messages and replies newest first, without statuses; the counts are per direction, partner and kind,
// since a time; the prefs keep the last word per hub.
func TestPartnerMessagesListsCountsAndPrefs(t *testing.T) {
	d, ctx := openPartners(t), context.Background()
	base := time.Now().Add(-time.Hour)
	rows := []PartnerMessage{
		{ID: "pr_1", Direction: "out", PeerHub: "hub-b", MessageID: "pm_1", Kind: "message", State: "sent", CreatedAt: base},
		{ID: "pr_2", Direction: "in", PeerHub: "hub-b", MessageID: "pm_2", Kind: "reply", State: "delivered", CreatedAt: base.Add(time.Minute)},
		{ID: "pr_3", Direction: "out", PeerHub: "hub-b", MessageID: "pm_3", Kind: "status", Text: "delivered", State: "sent", CreatedAt: base.Add(2 * time.Minute)},
		{ID: "pr_4", Direction: "out", PeerHub: "hub-c", MessageID: "pm_4", Kind: "message", State: "queued", CreatedAt: base.Add(3 * time.Minute)},
	}
	for _, m := range rows {
		if _, _, err := d.InsertPartnerMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	all, err := d.RecentPartnerMessages(ctx, "", 10)
	if err != nil || len(all) != 3 || all[0].ID != "pr_4" || all[2].ID != "pr_1" {
		t.Fatalf("recent = %+v %v", all, err)
	}
	if b, err := d.RecentPartnerMessages(ctx, "hub-b", 1); err != nil || len(b) != 1 || b[0].ID != "pr_2" {
		t.Fatalf("recent hub-b = %+v %v", b, err)
	}
	for _, c := range []struct {
		dir, peer string
		status    bool
		since     time.Time
		want      int
	}{{"out", "hub-b", false, base, 1}, {"out", "hub-b", true, base, 1}, {"in", "hub-b", false, base, 1},
		{"out", "hub-b", false, base.Add(time.Second), 0}, {"out", "hub-c", false, base, 1}} {
		if n, err := d.CountPartnerMessages(ctx, c.dir, c.peer, c.status, c.since); err != nil || n != c.want {
			t.Errorf("count %+v = %d %v", c, n, err)
		}
	}
	if prefs, err := d.PartnerPrefs(ctx); err != nil || len(prefs) != 0 {
		t.Fatalf("no prefs = %+v %v", prefs, err)
	}
	for _, v := range []bool{false, true, false} {
		if err := d.SetPartnerConfirm(ctx, "hub-b", v, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	if prefs, err := d.PartnerPrefs(ctx); err != nil || len(prefs) != 1 || prefs[0].HubID != "hub-b" || prefs[0].Confirm ||
		!prefs[0].PairedAt.IsZero() {
		t.Fatalf("prefs = %+v %v", prefs, err)
	}
	// A setting keeps the pairing it was made for; one for a later pairing replaces it.
	paired := time.Date(2026, 11, 20, 9, 30, 0, 123456000, time.UTC)
	if err := d.SetPartnerConfirm(ctx, "hub-b", true, paired); err != nil {
		t.Fatal(err)
	}
	if prefs, err := d.PartnerPrefs(ctx); err != nil || len(prefs) != 1 || !prefs[0].Confirm || !prefs[0].PairedAt.Equal(paired) {
		t.Fatalf("prefs of a pairing = %+v %v", prefs, err)
	}
}
