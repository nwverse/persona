package db

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPushDeviceCrossPoolGoneKeepsNewRegistration(t *testing.T) {
	d, ctx := open(t), context.Background()
	other, err := Open(ctx, d.pool.Config().ConnString(), AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	at := time.Now().UTC().Truncate(time.Microsecond)
	old := Device{DeviceID: "phone", CredentialID: "owner", APNSToken: "old", Topic: "app.example", RegisteredAt: at, Registering: true}
	if err := d.UpsertDevice(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveLiveActivity(ctx, LiveActivity{DeviceID: "phone", PushToStartToken: "start"}); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := d.ClaimLiveActivityStart(ctx, "phone", "start", at, 10*time.Minute, 8*time.Hour); err != nil || !claimed {
		t.Fatalf("initial claim: %v %v", claimed, err)
	}
	newer := old
	newer.APNSToken, newer.RegisteredAt = "new", at.Add(time.Second)
	if err := other.UpsertDevice(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx, `DELETE FROM devices WHERE device_id='phone'`); err == nil {
		t.Fatal("legacy unconditional delete bypassed current registration fence")
	}
	if deleted, err := d.DeleteDeviceIfCurrent(ctx, old); err != nil || deleted {
		t.Fatalf("late gone deleted other pool registration: %v %v", deleted, err)
	}
	var n int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM live_activity_start_budget WHERE device_id='phone'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("late gone cleared budget: %d %v", n, err)
	}
	if deleted, err := other.DeleteDeviceIfCurrent(ctx, newer); err != nil || !deleted {
		t.Fatalf("current gone not handled: %v %v", deleted, err)
	}
	newer.APNSToken, newer.RegisteredAt = "renewed", at.Add(2*time.Second)
	if err := d.UpsertDevice(ctx, newer); err != nil {
		t.Fatalf("fresh token could not reenroll: %v", err)
	}
	t.Log("two pools: old 410 kept committed new target and budget; current gone retired it; fresh authenticated token reenrolled")
}

func TestPushDeviceLegacyOwnershipAndRetirementReasons(t *testing.T) {
	for _, reason := range []string{"ownership", "inactive", "revoked"} {
		t.Run(reason, func(t *testing.T) {
			d, ctx := open(t), context.Background()
			at := time.Now().UTC().Truncate(time.Microsecond)
			v := Device{DeviceID: "phone", CredentialID: "owner", APNSToken: "token", Topic: "app.example", RegisteredAt: at.Add(-100 * 24 * time.Hour)}
			seedPushRegistration(t, d, v)
			if reason == "revoked" {
				if err := d.CreateDeviceToken(ctx, DeviceToken{ID: "owner", Name: "fixture", Kind: "phone", Role: "full"}, "fixture-hash"); err != nil {
					t.Fatal(err)
				}
				if _, err := d.RevokeDeviceToken(ctx, "owner"); err != nil {
					t.Fatal(err)
				}
			} else if reason == "inactive" {
				if _, err := d.ReconcilePushDevices(ctx, at, 90*24*time.Hour); err != nil {
					t.Fatal(err)
				}
			}
			owner := "owner"
			if reason == "ownership" {
				owner = "different-owner"
			}
			// Exact shape of a legacy unconditional upsert, with no enrollment flag.
			_, err := d.pool.Exec(ctx, `INSERT INTO devices(device_id,name,kind,apns_token,apns_env,topic,credential,credential_id) VALUES('phone','','','legacy','','app.example',$1,$1) ON CONFLICT(device_id) DO UPDATE SET apns_token=EXCLUDED.apns_token,credential=EXCLUDED.credential,credential_id=EXCLUDED.credential_id`, owner)
			if err == nil {
				t.Fatalf("legacy writer bypassed %s fence", reason)
			}
			if reason != "ownership" {
				if err := d.SaveLiveActivity(ctx, LiveActivity{DeviceID: "phone", PushToStartToken: "late"}); err == nil {
					t.Fatalf("late activity recreated %s row", reason)
				}
				v.RegisteredAt, v.Registering = at, true
				err := d.UpsertDevice(ctx, v)
				if reason == "inactive" && err != nil {
					t.Fatalf("same-owner inactive enrollment failed: %v", err)
				}
				if reason == "revoked" && !errors.Is(err, ErrPushRegistrationConflict) {
					t.Fatalf("revoked owner reenrolled: %v", err)
				}
			}
		})
	}
}

func TestPushDeviceConcurrentRetirementAndStartClaims(t *testing.T) {
	d, base := open(t), context.Background()
	ctx, cancel := context.WithTimeout(base, 10*time.Second)
	defer cancel()
	other, err := Open(ctx, d.pool.Config().ConnString(), AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	at := time.Now().UTC().Truncate(time.Microsecond)
	for _, id := range []string{"old", "new"} {
		registered := at
		if id == "old" {
			registered = at.Add(-time.Hour)
		}
		seedPushRegistration(t, d, Device{DeviceID: id, CredentialID: "owner", PushKey: "key", Topic: "app.example", RegisteredAt: registered})
		if err := d.SaveLiveActivity(ctx, LiveActivity{DeviceID: id, PushToStartToken: id + "-start"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, claimed, err := d.ClaimLiveActivityStart(ctx, "old", "old-start", at, 10*time.Minute, 8*time.Hour); err != nil || !claimed {
		t.Fatalf("initial claim: %v %v", claimed, err)
	}
	start, results := make(chan struct{}), make(chan bool, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			<-start
			_, claimed, err := d.ClaimLiveActivityStart(ctx, "old", "old-start", at.Add(10*time.Minute), 10*time.Minute, 8*time.Hour)
			if err != nil {
				t.Error(err)
			}
			results <- claimed
		})
	}
	retired := make(chan error, 1)
	go func() {
		<-start
		_, err := other.ReconcilePushDevices(ctx, at.Add(10*time.Minute), 90*24*time.Hour)
		retired <- err
	}()
	close(start)
	wg.Wait()
	if err := <-retired; err != nil {
		t.Fatal(err)
	}
	close(results)
	claims := 0
	for claimed := range results {
		if claimed {
			claims++
		}
	}
	_, canonical, err := other.ClaimLiveActivityStart(ctx, "new", "new-start", at.Add(10*time.Minute), 10*time.Minute, 8*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if canonical {
		claims++
	}
	if claims != 1 {
		t.Fatalf("identity rollover allowed %d starts in one expiry window", claims)
	}
	t.Log("two pools, 20 old-identity claims racing retirement: exactly one old-or-canonical start, no deadlock")
}

func TestPushDeviceRepairedOwnerNeedsInstallationProof(t *testing.T) {
	d, ctx := open(t), context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	v := Device{DeviceID: "phone", CredentialID: "old-owner", APNSToken: "token", PushKey: "key", Topic: "app.example", APNSEnv: "production", RegisteredAt: at}
	seedPushRegistration(t, d, v)
	if err := d.CreateDeviceToken(ctx, DeviceToken{ID: "old-owner", Name: "fixture", Kind: "phone", Role: "full"}, "old-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RevokeDeviceToken(ctx, "old-owner"); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateDeviceToken(ctx, DeviceToken{ID: "new-owner", Name: "fixture", Kind: "phone", Role: "full"}, "new-hash"); err != nil {
		t.Fatal(err)
	}
	v.CredentialID, v.Registering, v.RegisteredAt = "new-owner", true, at.Add(time.Second)
	v.APNSEnv = "sandbox"
	if err := d.UpsertDevice(ctx, v); !errors.Is(err, ErrPushRegistrationConflict) {
		t.Fatalf("re-pair crossed delivery environment: %v", err)
	}
	v.APNSEnv = "production"
	if err := d.UpsertDevice(ctx, v); err != nil {
		t.Fatalf("same installation could not re-pair under new owner: %v", err)
	}
	v.CredentialID, v.RegisteredAt = "old-owner", at.Add(2*time.Second)
	if err := d.UpsertDevice(ctx, v); !errors.Is(err, ErrPushRegistrationConflict) {
		t.Fatalf("revoked owner replaced new binding: %v", err)
	}
}

func TestPushDeviceRetirementTransfersLongestStartBudget(t *testing.T) {
	d, ctx := open(t), context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	old := Device{DeviceID: "old", CredentialID: "owner", PushKey: "key", Topic: "app.example", RegisteredAt: at.Add(-time.Hour)}
	newer := old
	newer.DeviceID, newer.RegisteredAt = "new", at
	seedPushRegistration(t, d, old)
	seedPushRegistration(t, d, newer)
	for _, id := range []string{"old", "new"} {
		if err := d.SaveLiveActivity(ctx, LiveActivity{DeviceID: id, PushToStartToken: id + "-start"}); err != nil {
			t.Fatal(err)
		}
		claimAt := at
		if id == "old" {
			claimAt = at.Add(time.Minute)
		}
		if _, claimed, err := d.ClaimLiveActivityStart(ctx, id, id+"-start", claimAt, 10*time.Minute, 8*time.Hour); err != nil || !claimed {
			t.Fatalf("claim %s: %v %v", id, claimed, err)
		}
	}
	if _, err := d.ReconcilePushDevices(ctx, at.Add(2*time.Minute), 90*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	due, claimed, err := d.ClaimLiveActivityStart(ctx, "new", "new-start", at.Add(10*time.Minute), 10*time.Minute, 8*time.Hour)
	if err != nil || claimed || !due.Equal(at.Add(11*time.Minute)) {
		t.Fatalf("identity rollover reset longest budget: %s %v %v", due, claimed, err)
	}
	if _, claimed, err := d.ClaimLiveActivityStart(ctx, "new", "new-start", at.Add(11*time.Minute), 10*time.Minute, 8*time.Hour); err != nil || !claimed {
		t.Fatalf("expiry: %v %v", claimed, err)
	}
	if _, claimed, err := d.ClaimLiveActivityStart(ctx, "old", "old-start", at.Add(11*time.Minute), 10*time.Minute, 8*time.Hour); err != nil || claimed {
		t.Fatalf("retired identity still claims: %v %v", claimed, err)
	}
	t.Log("two existing budgets merged with maximum deadline; canonical cannot start early and retired identity cannot claim")
}
