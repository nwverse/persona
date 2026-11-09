package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPushDeviceFreshEnrollmentCanReplaceHistoricalCanonical(t *testing.T) {
	d, ctx := open(t), context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	old := Device{DeviceID: "older-id", CredentialID: "owner", PushKey: "key", APNSToken: "older-token", Topic: "app.example", APNSEnv: "production", RegisteredAt: at.Add(-time.Hour)}
	candidate := old
	candidate.DeviceID, candidate.APNSToken, candidate.RegisteredAt = "historical-canonical", "other-token", at
	seedPushRegistration(t, d, old)
	seedPushRegistration(t, d, candidate)
	if err := d.SetDevicePrefs(ctx, old.DeviceID, "owner", []byte(`{"input":false}`)); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveLiveActivity(ctx, LiveActivity{DeviceID: old.DeviceID, PushToStartToken: "start"}); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := d.ClaimLiveActivityStart(ctx, old.DeviceID, "start", at, 10*time.Minute, 8*time.Hour); err != nil || !claimed {
		t.Fatalf("initial start: %v %v", claimed, err)
	}
	if _, err := d.ReconcilePushDevices(ctx, at, 90*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	// Migrated updated_at is not historical enrollment proof. A genuine fresh
	// request after retirement must still work with an existing client's cached ID.
	old.Registering, old.RegisteredAt, old.APNSToken = true, time.Now().UTC().Add(time.Second), "fresh-token"
	if err := d.UpsertDevice(ctx, old); err != nil {
		t.Fatalf("fresh authenticated cached-ID enrollment failed: %v", err)
	}
	rows, err := d.Devices(ctx)
	if err != nil || len(rows) != 1 || rows[0].DeviceID != old.DeviceID {
		t.Fatalf("fresh enrollment duplicated rather than replaced canonical: %+v %v", rows, err)
	}
	if string(rows[0].Prefs) != `{"input": false}` {
		t.Fatalf("installation preferences lost: %s", rows[0].Prefs)
	}
	if err := d.SaveLiveActivity(ctx, LiveActivity{DeviceID: old.DeviceID, PushToStartToken: "fresh-start"}); err != nil {
		t.Fatal(err)
	}
	due, claimed, err := d.ClaimLiveActivityStart(ctx, old.DeviceID, "fresh-start", at.Add(time.Minute), 10*time.Minute, 8*time.Hour)
	if err != nil || claimed || !due.Equal(at.Add(10*time.Minute)) {
		t.Fatalf("canonical replacement reset start budget: %s %v %v", due, claimed, err)
	}
	candidate.Registering = true // this stale request was captured before retirement
	if err := d.UpsertDevice(ctx, candidate); !errors.Is(err, ErrPushRegistrationConflict) {
		t.Fatalf("delayed old enrollment reclaimed current ID: %v", err)
	}
	t.Log("fresh cached-ID enrollment atomically replaces historical canonical; preferences and longest budget survive; stale enrollment stays fenced")
}
