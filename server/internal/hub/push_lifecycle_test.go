package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

func (s *meshStore) DeleteDeviceIfCurrent(_ context.Context, expected db.Device) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.devices[expected.DeviceID]
	if !ok || current.APNSToken != expected.APNSToken || current.RelayURL != expected.RelayURL {
		return false, nil
	}
	delete(s.devices, expected.DeviceID)
	return true, nil
}
func (s *forgetStore) DeleteDeviceIfCurrent(ctx context.Context, expected db.Device) (bool, error) {
	err := s.DeleteDevice(ctx, expected.DeviceID)
	return err == nil, err
}
func (s *deviceRows) DeleteDeviceIfCurrent(_ context.Context, expected db.Device) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.rows[expected.DeviceID]
	if !ok || current.APNSToken != expected.APNSToken || current.RelayURL != expected.RelayURL {
		return false, nil
	}
	delete(s.rows, expected.DeviceID)
	s.deletes++
	return true, nil
}

// Unrelated fixtures do not model retirement; the lifecycle and SQL tests do.
func (s *meshStore) ReconcilePushDevices(context.Context, time.Time, time.Duration) ([]db.PushDeviceRetirement, error) {
	return nil, nil
}
func (s *forgetStore) ReconcilePushDevices(context.Context, time.Time, time.Duration) ([]db.PushDeviceRetirement, error) {
	return nil, nil
}
func (s *stuckPrefsStore) ReconcilePushDevices(context.Context, time.Time, time.Duration) ([]db.PushDeviceRetirement, error) {
	return nil, nil
}
func (s *deviceRows) Devices(context.Context) ([]db.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []db.Device
	for _, d := range s.rows {
		rows = append(rows, d)
	}
	return rows, nil
}
func (s *deviceRows) ReconcilePushDevices(context.Context, time.Time, time.Duration) ([]db.PushDeviceRetirement, error) {
	return nil, nil
}

func TestPushRegistrationCommitsBeforePublication(t *testing.T) {
	p := newPusher(t.TempDir(), nil, t.Logf)
	old := Device{DeviceID: "phone", APNSToken: "old"}
	if err := p.register(old); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	failure := errors.New("injected registration failure")
	p.store = &registrationErrorStore{device: func(context.Context, db.Device) error {
		close(entered)
		<-release
		return failure
	}}
	result := make(chan error, 1)
	go func() { result <- p.register(Device{DeviceID: "phone", APNSToken: "new"}) }()
	<-entered
	if current, _ := p.device("phone"); current.APNSToken != "old" {
		t.Fatal("uncommitted registration became sendable")
	}
	close(release)
	if err := <-result; !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if current, _ := p.device("phone"); current.APNSToken != "old" {
		t.Fatal("failed registration changed target")
	}
}

func TestPushRegistrationDeduplicatesOnlySameInstallation(t *testing.T) {
	p := newPusher(t.TempDir(), nil, t.Logf)
	base := Device{DeviceID: "old", CredentialID: "owner", APNSToken: "old-token", PushKey: "key", Topic: "app.example", APNSEnv: "production"}
	if err := p.register(base); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"other-key", "other-owner", "other-env", "other-topic"} {
		d := base
		d.DeviceID, d.APNSToken = id, id+"-token"
		switch id {
		case "other-key":
			d.PushKey = "different"
		case "other-owner":
			d.CredentialID = "different"
		case "other-env":
			d.APNSEnv = "sandbox"
		case "other-topic":
			d.Topic = "different.example"
		}
		if err := p.register(d); err != nil {
			t.Fatal(err)
		}
	}
	base.DeviceID, base.APNSToken = "new", "new-token"
	if err := p.register(base); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.device("old"); ok {
		t.Fatal("old same-installation target retained")
	}
	if got := len(p.list()); got != 5 {
		t.Fatalf("got %d registrations, want canonical plus four distinct targets", got)
	}
	t.Log("same installation: two registrations -> one; four ownership/environment/key/topic boundaries retained")
}

func TestPushAcceptanceCoalescesAndRejectsLateTarget(t *testing.T) {
	p := newPusher(t.TempDir(), nil, t.Logf)
	d := Device{DeviceID: "phone", APNSToken: "current"}
	if err := p.register(d); err != nil {
		t.Fatal(err)
	}
	rows := &deviceRows{rows: map[string]db.Device{}}
	p.store = rows
	for range 100 {
		p.pushed(d, nil)
	}
	if err := p.reconcileDevices(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, ok, _ := rows.row("phone")
	if !ok || stored.LastPushAt.IsZero() {
		t.Fatal("acceptance not persisted")
	}
	before := stored.LastPushAt
	p.pushed(Device{DeviceID: "phone", APNSToken: "old"}, nil)
	p.pushed(d, errors.New("APNs refused"))
	if current, _ := p.device("phone"); !current.LastPushAt.Equal(before) {
		t.Fatal("stale or failed push extended liveness")
	}
	p.mu.Lock()
	dirty := len(p.acceptedDirty)
	p.mu.Unlock()
	if dirty != 0 {
		t.Fatalf("stale/failed push left %d dirty registrations", dirty)
	}
}
