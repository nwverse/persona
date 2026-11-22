package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrEnrollmentConflict = errors.New("enrollment conflict")
var ErrOwnerAdmission = errors.New("owner admission denied")

type OwnerSubject struct {
	Subject  string `json:"owner_subject"`
	Label    string `json:"label"`
	Revision int64  `json:"revision"`
}
type OwnerBinding struct {
	CredentialID string  `json:"credential_id"`
	Subject      *string `json:"owner_subject"`
	Revision     int64   `json:"binding_revision"`
	Revoked      bool    `json:"revoked"`
	Issuer       string  `json:"-"`
}

func (d *DB) OwnerSubjects(ctx context.Context, issuer string) ([]OwnerSubject, error) {
	rows, err := d.pool.Query(ctx, `SELECT subject::text, label, revision FROM owner_subjects WHERE issuer=$1 ORDER BY created_at, subject LIMIT 1000`, issuer)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (OwnerSubject, error) {
		var s OwnerSubject
		err := r.Scan(&s.Subject, &s.Label, &s.Revision)
		return s, err
	})
}
func (d *DB) CreateOwnerSubject(ctx context.Context, issuer, label, administrator, reason string) (OwnerSubject, error) {
	var s OwnerSubject
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if issuer == "" || administrator == "" || label == "" || len(label) > 120 || reason == "" || len(reason) > 1000 {
			return ErrEnrollmentConflict
		}
		if err := tx.QueryRow(ctx, `INSERT INTO owner_subjects(issuer,label) VALUES($1,$2) RETURNING subject::text,label,revision`, issuer, label).Scan(&s.Subject, &s.Label, &s.Revision); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO owner_enrollment_audit(issuer,subject,revision,action,administrator,reason) VALUES($1,$2,1,'subject_created',$3,$4)`, issuer, s.Subject, administrator, reason)
		return err
	})
	return s, err
}
func bindingIn(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, issuer, id string) (OwnerBinding, error) {
	var b OwnerBinding
	b.CredentialID = id
	err := q.QueryRow(ctx, `SELECT b.subject::text,COALESCE(b.revision,0),t.revoked_at IS NOT NULL OR b.retired_at IS NOT NULL,COALESCE(b.issuer,'')
 FROM device_tokens t LEFT JOIN owner_bindings b ON b.credential_id=t.id WHERE t.id=$1 AND (b.issuer IS NULL OR b.issuer=$2)`, id, issuer).Scan(&b.Subject, &b.Revision, &b.Revoked, &b.Issuer)
	return b, err
}
func (d *DB) OwnerBinding(ctx context.Context, issuer, id string) (OwnerBinding, error) {
	return bindingIn(ctx, d.pool, issuer, id)
}
func ordinaryOwnerCredential(role, kind string) bool {
	if role != "full" {
		return false
	}
	switch kind {
	case "link", "peer", "extension", "pair", "master", "manager":
		return false
	}
	return true
}

func (d *DB) EnrollOwner(ctx context.Context, issuer, id, subject, administrator, reason string, expected int64) (OwnerBinding, error) {
	var b OwnerBinding
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var role, kind string
		var revoked *time.Time
		if err := tx.QueryRow(ctx, `SELECT role,kind,revoked_at FROM device_tokens WHERE id=$1 FOR UPDATE`, id).Scan(&role, &kind, &revoked); err != nil {
			return err
		}
		if !ordinaryOwnerCredential(role, kind) || revoked != nil || reason == "" || len(reason) > 1000 || administrator == "" {
			return ErrEnrollmentConflict
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM owner_subjects WHERE issuer=$1 AND subject::text=$2)`, issuer, subject).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrEnrollmentConflict
		}
		var err error
		b, err = bindingIn(ctx, tx, issuer, id)
		if err != nil {
			return err
		}
		if b.Revision != expected || b.Revoked || (b.Subject != nil && *b.Subject != subject) {
			return ErrEnrollmentConflict
		}
		action := "enrollment_confirmed"
		if b.Subject == nil {
			if _, err := tx.Exec(ctx, `INSERT INTO owner_bindings(credential_id,issuer,subject) VALUES($1,$2,$3)`, id, issuer, subject); err != nil {
				return err
			}
			b.Subject = &subject
			b.Issuer = issuer
			b.Revision = 1
			action = "enrolled"
		}
		_, err = tx.Exec(ctx, `INSERT INTO owner_enrollment_audit(issuer,subject,credential_id,revision,action,administrator,reason) VALUES($1,$2,$3,$4,$5,$6,$7)`, issuer, subject, id, b.Revision, action, administrator, reason)
		return err
	})
	return b, err
}
func (d *DB) WithdrawOwner(ctx context.Context, issuer, id, administrator, reason string, expected int64) (OwnerBinding, error) {
	var b OwnerBinding
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id FROM device_tokens WHERE id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return err
		}
		var err error
		b, err = bindingIn(ctx, tx, issuer, id)
		if err != nil {
			return err
		}
		if b.Subject == nil || b.Revision != expected || b.Revoked || administrator == "" || reason == "" || len(reason) > 1000 {
			return ErrEnrollmentConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE device_tokens SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id); err != nil {
			return err
		}
		if err := dropDeviceRows(ctx, tx, id); err != nil {
			return err
		}
		// Replace the trigger's generic attribution with an additional append-only administrator event.
		b, err = bindingIn(ctx, tx, issuer, id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO owner_enrollment_audit(issuer,subject,credential_id,revision,action,administrator,reason) VALUES($1,$2,$3,$4,'credential_revoked',$5,$6)`, issuer, *b.Subject, id, b.Revision, administrator, reason)
		return err
	})
	return b, err
}

// AdmitOwner bypasses the authentication cache and holds the live credential's shared
// row lock until release. Enrollment and every SQL revocation conflict with this lock.
// Callers must use a short deadline and always release, including failed relays.
func (d *DB) AdmitOwner(ctx context.Context, issuer, hash string) (OwnerBinding, func(), error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return OwnerBinding{}, nil, err
	}
	release := func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}
	var id, role, kind string
	err = tx.QueryRow(ctx, `SELECT id,role,kind FROM device_tokens WHERE token_hash=$1 AND revoked_at IS NULL FOR SHARE`, hash).Scan(&id, &role, &kind)
	if err != nil || !ordinaryOwnerCredential(role, kind) {
		release()
		return OwnerBinding{}, nil, ErrOwnerAdmission
	}
	b, err := bindingIn(ctx, tx, issuer, id)
	if err != nil || b.Subject == nil || b.Revoked {
		release()
		return OwnerBinding{}, nil, ErrOwnerAdmission
	}
	return b, release, nil
}
