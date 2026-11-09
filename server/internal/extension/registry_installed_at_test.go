package extension

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// What a recall relies on (connector/proc Config.RecallNotBefore): an installed extension's InstalledAt is when it was
// first installed, and nothing but uninstalling it and installing it again moves it. Each test runs on a clock that moves
// a second at every look, so that whatever stamped it again would show.

// installedOnAMovingClock installs the example extension (version 1.2.0) and says when.
func installedOnAMovingClock(t *testing.T) (r *Registry, store *memStore, pkg string, at time.Time) {
	t.Helper()
	r, store, _ = newTestRegistry(t)
	clock := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	r.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	pkg = t.TempDir()
	writePackage(t, pkg, nil)
	pv, err := r.Preview(context.Background(), pkg, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Install(context.Background(), pv.ID, pv.Digest, "cli")
	if err != nil {
		t.Fatal(err)
	}
	at = p.Record.InstalledAt
	if at.IsZero() {
		t.Fatal("installed with no InstalledAt")
	}
	return r, store, pkg, at
}

// keptAs says the extension's InstalledAt, in memory and in the row the store has, is at.
func keptAs(t *testing.T, what string, r *Registry, store *memStore, at time.Time) {
	t.Helper()
	if got := r.Get("example-notes").Record.InstalledAt; !got.Equal(at) {
		t.Errorf("%s: in memory %v, installed at %v", what, got, at)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if got := store.rows["example-notes"].InstalledAt; !got.Equal(at) {
		t.Errorf("%s: in the store %v, installed at %v", what, got, at)
	}
}

func TestInstalledAtIsKeptByAnUpgrade(t *testing.T) {
	ctx := context.Background()
	r, store, _, at := installedOnAMovingClock(t)
	pkg2 := t.TempDir()
	writePackage(t, pkg2, func(m map[string]any) { m["version"] = "1.3.0" })
	pv, err := r.Preview(ctx, pkg2, "example-notes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Upgrade(ctx, "example-notes", pv.ID, "", "cli"); err != nil {
		t.Fatal(err)
	}
	if r.Get("example-notes").Record.Version != "1.3.0" {
		t.Fatal("not upgraded")
	}
	keptAs(t, "after an upgrade", r, store, at)
}

func TestInstalledAtIsKeptByAConfigChange(t *testing.T) {
	ctx := context.Background()
	r, store, _, at := installedOnAMovingClock(t)
	if err := r.SetConfig(ctx, "example-notes", json.RawMessage(`{"folders": ["a"]}`), "device:x"); err != nil {
		t.Fatal(err)
	}
	keptAs(t, "after a config change", r, store, at)
}

func TestInstalledAtIsKeptByDisablingAndEnabling(t *testing.T) {
	ctx := context.Background()
	r, store, _, at := installedOnAMovingClock(t)
	if err := r.SetEnabled(ctx, "example-notes", false, "cli"); err != nil {
		t.Fatal(err)
	}
	keptAs(t, "disabled", r, store, at)
	if err := r.SetEnabled(ctx, "example-notes", true, "cli"); err != nil {
		t.Fatal(err)
	}
	keptAs(t, "enabled again", r, store, at)
}

func TestInstalledAtIsKeptByARollback(t *testing.T) {
	ctx := context.Background()
	r, store, _, at := installedOnAMovingClock(t)
	pkg2 := t.TempDir()
	writePackage(t, pkg2, func(m map[string]any) { m["version"] = "1.3.0" })
	pv, _ := r.Preview(ctx, pkg2, "example-notes")
	if _, err := r.Upgrade(ctx, "example-notes", pv.ID, "", "cli"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Rollback(ctx, "example-notes", "cli"); err != nil {
		t.Fatal(err)
	}
	if r.Get("example-notes").Record.Version != "1.2.0" {
		t.Fatal("not rolled back")
	}
	keptAs(t, "after a rollback", r, store, at)
}

// A restart reads the row back: the registry loads what the store has, and changes made after that still keep it.
func TestInstalledAtIsKeptByARestart(t *testing.T) {
	ctx := context.Background()
	r, store, _, at := installedOnAMovingClock(t)
	r2 := NewRegistry(r.dir, store, rot13{}, 202611100000)
	clock := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	r2.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	if err := r2.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r2.Get("example-notes").Record.InstalledAt; !got.Equal(at) {
		t.Fatalf("after a restart %v, installed at %v", got, at)
	}
	if err := r2.SetConfig(ctx, "example-notes", json.RawMessage(`{"folders": ["a"]}`), "device:x"); err != nil {
		t.Fatal(err)
	}
	if got := r2.Get("example-notes").Record.InstalledAt; !got.Equal(at) {
		t.Errorf("after a restart and a config change %v, installed at %v", got, at)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if got := store.rows["example-notes"].InstalledAt; !got.Equal(at) {
		t.Errorf("in the store %v, installed at %v", got, at)
	}
}

// The one thing that moves it: uninstalling and installing again is a new install, and what the ledger wrote before
// belongs to the one before.
func TestInstalledAtIsNewAfterUninstallingAndInstallingAgain(t *testing.T) {
	ctx := context.Background()
	r, _, pkg, at := installedOnAMovingClock(t)
	if err := r.Uninstall(ctx, "example-notes", false, "cli"); err != nil {
		t.Fatal(err)
	}
	pv, err := r.Preview(ctx, pkg, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Install(ctx, pv.ID, pv.Digest, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Record.InstalledAt.After(at) {
		t.Errorf("installed again at %v, first at %v", p.Record.InstalledAt, at)
	}
}
