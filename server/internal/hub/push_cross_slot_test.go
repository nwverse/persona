package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

func TestPushCrossProcessLateGoneReloadsCommittedTarget(t *testing.T) {
	rows := &deviceRows{rows: map[string]db.Device{}}
	first, second := newPusher(t.TempDir(), nil, t.Logf), newPusher(t.TempDir(), nil, t.Logf)
	first.store, second.store = rows, rows
	old := Device{DeviceID: "phone", APNSToken: "old", Topic: "app.example", CredentialID: "owner"}
	if err := first.register(old); err != nil {
		t.Fatal(err)
	}
	old, _ = first.device("phone")
	newer := old
	newer.APNSToken = "new"
	if err := second.register(newer); err != nil {
		t.Fatal(err)
	}
	first.forgetIfCurrent(old)
	persisted, ok, deletes := rows.row("phone")
	if !ok || persisted.APNSToken != "new" || deletes != 0 {
		t.Fatalf("other process registration deleted: %+v %v %d", persisted, ok, deletes)
	}
	if current, ok := first.device("phone"); !ok || current.APNSToken != "new" {
		t.Fatalf("stale process did not reload canonical: %+v %v", current, ok)
	}
}

type acceptanceFailStore struct {
	*deviceRows
	fail       bool
	reconciles int
}

func (s *acceptanceFailStore) UpsertDevice(ctx context.Context, d db.Device) error {
	if s.fail {
		s.fail = false
		return errors.New("injected acceptance flush failure")
	}
	return s.deviceRows.UpsertDevice(ctx, d)
}
func (s *acceptanceFailStore) ReconcilePushDevices(context.Context, time.Time, time.Duration) ([]db.PushDeviceRetirement, error) {
	s.reconciles++
	return nil, nil
}

func TestPushCanonicalReplacementPreservesPrefs(t *testing.T) {
	p := newPusher(t.TempDir(), nil, t.Logf)
	d := Device{DeviceID: "cached-id", CredentialID: "owner", PushKey: "key", APNSToken: "old", Topic: "app.example"}
	if err := p.register(d); err != nil {
		t.Fatal(err)
	}
	if err := p.setPrefs(d.DeviceID, NotifyPrefs{Input: false, Done: DoneOff}); err != nil {
		t.Fatal(err)
	}
	d.DeviceID, d.APNSToken = "other-id", "new"
	if err := p.register(d); err != nil {
		t.Fatal(err)
	}
	if prefs := p.prefs(d.DeviceID); prefs.Input || prefs.Done != DoneOff {
		t.Fatal("canonical replacement lost preferences")
	}
	d.DeviceID, d.APNSToken = "cached-id", "fresh"
	if err := p.register(d); err != nil {
		t.Fatal(err)
	}
	if prefs := p.prefs(d.DeviceID); prefs.Input || prefs.Done != DoneOff {
		t.Fatal("cached-ID reenrollment lost preferences")
	}
	if len(p.list()) != 1 {
		t.Fatal("cached-ID reenrollment duplicated targets")
	}
}

func TestPushMaintenanceDoesNotExpireUnflushedAcceptance(t *testing.T) {
	p := newPusher(t.TempDir(), nil, t.Logf)
	d := Device{DeviceID: "phone", APNSToken: "token"}
	if err := p.register(d); err != nil {
		t.Fatal(err)
	}
	d, _ = p.device("phone")
	s := &acceptanceFailStore{deviceRows: &deviceRows{rows: map[string]db.Device{"phone": d}}, fail: true}
	p.store = s
	p.pushed(d, nil)
	if err := p.reconcileDevices(context.Background()); err == nil {
		t.Fatal("failed acceptance flush reported success")
	}
	if s.reconciles != 0 {
		t.Fatal("inactivity ran against unpersisted recent acceptance")
	}
	if _, ok := p.device("phone"); !ok {
		t.Fatal("recently accepted target removed")
	}
	if err := p.reconcileDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	persisted, _, _ := s.row("phone")
	if s.reconciles != 1 || persisted.LastPushAt.IsZero() {
		t.Fatal("retry lost liveness")
	}
}

func TestPushMaintenanceSkipsInFlightTransport(t *testing.T) {
	p := newPusher(t.TempDir(), nil, t.Logf)
	d := Device{DeviceID: "phone", APNSToken: "token"}
	if err := p.register(d); err != nil {
		t.Fatal(err)
	}
	d, _ = p.device("phone")
	s := &acceptanceFailStore{deviceRows: &deviceRows{rows: map[string]db.Device{"phone": d}}}
	p.store = s
	// Transport holds this guard from dispatch through acceptance/failure.
	p.sendMu.RLock()
	if err := p.reconcileDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.reconciles != 0 {
		t.Fatal("inactivity raced an active transport")
	}
	p.pushed(d, nil)
	p.sendMu.RUnlock()
	if err := p.reconcileDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.reconciles != 1 {
		t.Fatal("idle pass did not resume maintenance")
	}
	persisted, _, _ := s.row("phone")
	if persisted.LastPushAt.IsZero() {
		t.Fatal("completed acceptance not flushed before inactivity")
	}
}
