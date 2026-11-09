package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func seedPushRegistration(t *testing.T, d *DB, v Device) {
	t.Helper()
	// Model pre-upgrade registrations, before registration-time reconciliation.
	if err := pgx.BeginFunc(context.Background(), d.pool, func(tx pgx.Tx) error { return upsertPushDevice(context.Background(), tx, v) }); err != nil {
		t.Fatal(err)
	}
}

func TestSamePushInstallationBoundaries(t *testing.T) {
	base := Device{DeviceID: "a", CredentialID: "owner", PushKey: "key", APNSToken: "token", Topic: "app.example", APNSEnv: "production"}
	for _, field := range []string{"same-key", "same-token", "different-key", "different-owner", "different-topic", "different-env", "empty-owner", "empty-target"} {
		t.Run(field, func(t *testing.T) {
			other := base
			other.DeviceID = "b"
			switch field {
			case "same-key":
				other.APNSToken = "rotated"
			case "same-token":
				other.PushKey = "rotated"
			case "different-key":
				other.PushKey, other.APNSToken = "other", "other"
			case "different-owner":
				other.CredentialID = "other"
			case "different-topic":
				other.Topic = "other.example"
			case "different-env":
				other.APNSEnv = "sandbox"
			case "empty-owner":
				other.CredentialID = ""
			case "empty-target":
				other.PushKey, other.APNSToken = "", ""
			}
			want := field == "same-key" || field == "same-token"
			if SamePushInstallation(base, other) != want {
				t.Fatalf("installation match for %s", field)
			}
		})
	}
}

func TestPushDeviceRetirementAndFence(t *testing.T) {
	d, ctx := open(t), context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	old := Device{DeviceID: "old", CredentialID: "owner", Topic: "app.example", APNSEnv: "production", PushKey: "key", APNSToken: "old-token", RegisteredAt: at.Add(-time.Hour)}
	newer := old
	newer.DeviceID, newer.APNSToken, newer.RegisteredAt = "new", "new-token", at
	seedPushRegistration(t, d, old)
	seedPushRegistration(t, d, newer)
	if err := d.SaveLiveActivity(ctx, LiveActivity{DeviceID: "old", PushToStartToken: "old-start"}); err != nil {
		t.Fatal(err)
	}
	before, err := d.Devices(ctx)
	if err != nil || len(before) != 2 {
		t.Fatalf("before: %d %v", len(before), err)
	}
	retired, err := d.ReconcilePushDevices(ctx, at, 90*24*time.Hour)
	if err != nil || len(retired) != 1 || retired[0].DeviceID != "old" || retired[0].CanonicalID != "new" {
		t.Fatalf("retirement: %+v %v", retired, err)
	}
	after, err := d.Devices(ctx)
	if err != nil || len(after) != 1 || after[0].DeviceID != "new" {
		t.Fatalf("after: %+v %v", after, err)
	}
	activities, err := d.LiveActivities(ctx)
	if err != nil || len(activities) != 0 {
		t.Fatalf("retired activity remains: %+v %v", activities, err)
	}
	for _, registering := range []bool{false, true} {
		old.Registering = registering
		old.RegisteredAt = at.Add(-time.Hour) // a delayed enrollment captured before retirement
		if err := d.UpsertDevice(ctx, old); !errors.Is(err, ErrPushRegistrationConflict) {
			t.Fatalf("old identity revived (enrollment=%v): %v", registering, err)
		}
	}
	// The SQL trigger also rejects the old binary's writer, which lacks new fields.
	if _, err := d.pool.Exec(ctx, `INSERT INTO devices(device_id,name,kind,apns_token,apns_env,topic) VALUES('old','','','','','')`); err == nil {
		t.Fatal("legacy insert revived retired identity")
	}
	if err := d.SaveLiveActivity(ctx, LiveActivity{DeviceID: "old", PushToStartToken: "late"}); err == nil {
		t.Fatal("late activity save revived retired row")
	}
	stale := newer
	stale.DeviceID, stale.Registering, stale.RegisteredAt = "late-new-id", true, at.Add(-time.Second)
	if err := d.UpsertDevice(ctx, stale); !errors.Is(err, ErrPushRegistrationConflict) {
		t.Fatalf("older enrollment displaced canonical: %v", err)
	}
	stolen := newer
	stolen.CredentialID, stolen.Registering, stolen.RegisteredAt = "different-owner", true, at.Add(time.Minute)
	if err := d.UpsertDevice(ctx, stolen); !errors.Is(err, ErrPushRegistrationConflict) {
		t.Fatalf("ownership overwritten: %v", err)
	}
	// A new process still sees exactly the same persisted canonical registration.
	other, err := Open(ctx, d.pool.Config().ConnString(), AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if got, err := other.Devices(ctx); err != nil || len(got) != 1 {
		t.Fatalf("restart lost retirement: %d %v", len(got), err)
	}
	t.Log("before two same-installation targets; after one; stale enrollment, late saves and a second pool cannot resurrect old identity")
}

func TestPushDeviceInactivityProtectsActiveRegistrations(t *testing.T) {
	d, ctx := open(t), context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	for _, id := range []string{"inactive", "accepted", "seen", "recent", "revoked"} {
		v := Device{DeviceID: id, CredentialID: id, Topic: "app.example", PushKey: id, RegisteredAt: at.Add(-100 * 24 * time.Hour)}
		if id == "accepted" {
			v.LastPushAt = at.Add(-time.Hour)
		}
		if id == "recent" || id == "revoked" {
			v.RegisteredAt = at.Add(-time.Hour)
		}
		seedPushRegistration(t, d, v)
		if err := d.CreateDeviceToken(ctx, DeviceToken{ID: id, Name: "fixture", Kind: "phone", Role: "full"}, id+"-hash"); err != nil {
			t.Fatal(err)
		}
		seen := at.Add(-100 * 24 * time.Hour)
		if id == "seen" {
			seen = at.Add(-time.Minute)
		}
		if _, err := d.pool.Exec(ctx, `UPDATE device_tokens SET last_seen=$2 WHERE id=$1`, id, seen); err != nil {
			t.Fatal(err)
		}
		if id == "revoked" {
			if _, err := d.pool.Exec(ctx, `UPDATE device_tokens SET revoked_at=$2 WHERE id=$1`, id, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	retired, err := d.ReconcilePushDevices(ctx, at, 90*24*time.Hour)
	if err != nil || len(retired) != 2 {
		t.Fatalf("inactivity: %+v %v", retired, err)
	}
	rows, err := d.Devices(ctx)
	if err != nil || len(rows) != 3 {
		t.Fatalf("active registrations lost: %+v %v", rows, err)
	}
	for _, v := range rows {
		if v.DeviceID == "inactive" || v.DeviceID == "revoked" {
			t.Fatalf("retained %s", v.DeviceID)
		}
	}
	// Only authenticated enrollment, not a background save, may recreate an idle row.
	v := Device{DeviceID: "inactive", CredentialID: "inactive", RegisteredAt: at.Add(time.Minute)}
	if err := d.UpsertDevice(ctx, v); !errors.Is(err, ErrPushRegistrationConflict) {
		t.Fatal("stale save recreated inactive row")
	}
	v.Registering = true
	if err := d.UpsertDevice(ctx, v); err != nil {
		t.Fatalf("returning installation could not enroll: %v", err)
	}
}

func TestPushDeviceRetirementRollsBackOnFailure(t *testing.T) {
	d, ctx := open(t), context.Background()
	at := time.Now().UTC()
	old := Device{DeviceID: "old", CredentialID: "owner", PushKey: "key", Topic: "app.example", RegisteredAt: at.Add(-time.Hour)}
	seedPushRegistration(t, d, old)
	if err := d.SaveLiveActivity(ctx, LiveActivity{DeviceID: "old", PushToStartToken: "start"}); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := d.ClaimLiveActivityStart(ctx, "old", "start", at, 10*time.Minute, 8*time.Hour); err != nil || !claimed {
		t.Fatalf("initial budget: %v %v", claimed, err)
	}
	if _, err := d.pool.Exec(ctx, `CREATE FUNCTION reject_test_retirement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected retirement failure'; END; $$; CREATE TRIGGER reject_test_retirement BEFORE DELETE ON devices FOR EACH ROW EXECUTE FUNCTION reject_test_retirement()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.pool.Exec(ctx, `DROP TRIGGER IF EXISTS reject_test_retirement ON devices; DROP FUNCTION IF EXISTS reject_test_retirement()`)
	})
	newer := old
	newer.DeviceID, newer.Registering, newer.RegisteredAt = "new", true, at
	if err := d.UpsertDevice(ctx, newer); err == nil {
		t.Fatal("failed retirement reported registration success")
	}
	rows, err := d.Devices(ctx)
	if err != nil || len(rows) != 1 || rows[0].DeviceID != "old" {
		t.Fatalf("failed transaction changed targets: %+v %v", rows, err)
	}
	var n int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM live_activity_start_budget WHERE device_id='old' AND next_start_at=$1`, at.Add(10*time.Minute)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("budget transfer did not roll back: %d %v", n, err)
	}
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM push_device_retirements`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tombstone did not roll back: %d %v", n, err)
	}
}
