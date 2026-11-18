package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOwnerEnrollmentAndRevocationFence(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	subject, err := d.CreateOwnerSubject(ctx, "synthetic-authority", "Synthetic owner", "synthetic-admin", "Synthetic acceptance")
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.CreateOwnerSubject(ctx, "synthetic-authority", "Other owner", "synthetic-admin", "Synthetic acceptance")
	if err != nil {
		t.Fatal(err)
	}
	id := "synthetic-owner-credential"
	if err := d.CreateDeviceToken(ctx, DeviceToken{ID: id, Name: "Synthetic device", Role: "full", Kind: "web"}, "synthetic-owner-hash"); err != nil {
		t.Fatal(err)
	}
	bound, err := d.EnrollOwner(ctx, "synthetic-authority", id, subject.Subject, "synthetic-admin", "Synthetic enrollment", 0)
	if err != nil || bound.Revision != 1 {
		t.Fatalf("enroll: %+v %v", bound, err)
	}
	if _, err := d.EnrollOwner(ctx, "synthetic-authority", id, other.Subject, "synthetic-admin", "Reassignment denied", 1); !errors.Is(err, ErrEnrollmentConflict) {
		t.Fatalf("reassigned: %v", err)
	}
	admission, release, err := d.AdmitOwner(ctx, "synthetic-authority", "synthetic-owner-hash")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if admission.Subject == nil || *admission.Subject != subject.Subject {
		t.Fatal("wrong subject")
	}
	revokeCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = d.RevokeDeviceToken(revokeCtx, id)
	cancel()
	if err == nil {
		t.Fatal("revocation bypassed admission lock")
	}
	release()
	if ok, err := d.RevokeDeviceToken(ctx, id); !ok || err != nil {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	retired, err := d.OwnerBinding(ctx, "synthetic-authority", id)
	if err != nil || !retired.Revoked || retired.Revision != 2 {
		t.Fatalf("retirement: %+v %v", retired, err)
	}
	if _, _, err := d.AdmitOwner(ctx, "synthetic-authority", "synthetic-owner-hash"); !errors.Is(err, ErrOwnerAdmission) {
		t.Fatal("revoked admission succeeded")
	}
	var count int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM owner_enrollment_audit WHERE credential_id=$1 AND action='credential_revoked'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit: %d %v", count, err)
	}
}
func TestOwnerSubjectsAreAuthorityScoped(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	s, err := d.CreateOwnerSubject(ctx, "issuer-a", "Synthetic owner", "synthetic-admin", "Synthetic acceptance")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"web", "watch", "link"} {
		id := "synthetic-authority-" + kind
		role := "full"
		if kind == "watch" {
			role = "answer"
		}
		if kind == "link" {
			role = "link"
		}
		if err := d.CreateDeviceToken(ctx, DeviceToken{ID: id, Kind: kind, Role: role}, id+"-hash"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.EnrollOwner(ctx, "issuer-b", id, s.Subject, "synthetic-admin", "Wrong authority", 0); !errors.Is(err, ErrEnrollmentConflict) {
			t.Fatalf("cross authority: %v", err)
		}
		_, err = d.EnrollOwner(ctx, "issuer-a", id, s.Subject, "synthetic-admin", "Synthetic enrollment", 0)
		if kind == "web" && err != nil {
			t.Fatal(err)
		}
		if kind != "web" && !errors.Is(err, ErrEnrollmentConflict) {
			t.Fatalf("enrolled %s", kind)
		}
	}
}
func TestOwnerWithdrawalAndSameSubjectDevices(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	s, err := d.CreateOwnerSubject(ctx, "withdrawal-authority", "Synthetic owner", "synthetic-admin", "Synthetic acceptance")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"synthetic-device-a", "synthetic-device-b"} {
		if err := d.CreateDeviceToken(ctx, DeviceToken{ID: id, Kind: "ios", Role: "full"}, id+"-hash"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.EnrollOwner(ctx, "withdrawal-authority", id, s.Subject, "synthetic-admin", "Synthetic enrollment", 0); err != nil {
			t.Fatal(err)
		}
	}
	b, err := d.WithdrawOwner(ctx, "withdrawal-authority", "synthetic-device-a", "synthetic-admin", "Synthetic withdrawal", 1)
	if err != nil || !b.Revoked || b.Revision != 2 {
		t.Fatalf("withdrawal: %+v %v", b, err)
	}
	if _, err := d.EnrollOwner(ctx, "withdrawal-authority", "synthetic-device-a", s.Subject, "synthetic-admin", "No revival", 2); !errors.Is(err, ErrEnrollmentConflict) {
		t.Fatal("revived binding")
	}
	_, release, err := d.AdmitOwner(ctx, "withdrawal-authority", "synthetic-device-b-hash")
	if err != nil {
		t.Fatal(err)
	}
	release()
}
