package db

import (
	"context"
	"crypto/rand"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/devicewire"
)

type SessionShareOwnership struct {
	Machine     string `json:"machine"`
	SessionID   string `json:"session_id"`
	Issuer      string `json:"issuer"`
	Subject     string `json:"owner_subject"`
	Incarnation string `json:"incarnation"`
	Generation  string `json:"assignment_generation"`
	Revision    int64  `json:"owner_revision"`
	AuditID     string `json:"audit_id"`
}
type SessionCreationIntent struct{ CreationID, Incarnation string }

func (d *DB) BeginSessionCreation(ctx context.Context, machine string, caller devicewire.Caller, authority devicewire.ShareAuthority) (SessionCreationIntent, error) {
	var intent SessionCreationIntent
	if caller.Kind != "device" || caller.OwnerSubject == "" || caller.Issuer == "" || caller.Issuer != authority.Issuer || machine != authority.Machine || authority.Generation == "" {
		return intent, ErrOwnerAdmission
	}
	intent.CreationID, intent.Incarnation = rand.Text(), rand.Text()
	_, err := d.pool.Exec(ctx, `INSERT INTO session_creation_intents(creation_id,machine_id,issuer,owner_subject,assignment_generation,incarnation) VALUES($1,$2,$3,$4,$5,$6)`, intent.CreationID, machine, caller.Issuer, caller.OwnerSubject, authority.Generation, intent.Incarnation)
	return intent, err
}

const sessionShareOwnerQuery = `SELECT machine_id,session_id,issuer,owner_subject::text,incarnation,assignment_generation,owner_revision,audit_id::text FROM session_share_ownership WHERE machine_id=$1 AND session_id=$2`

func (d *DB) SessionShareOwner(ctx context.Context, machine, session string) (*SessionShareOwnership, error) {
	return scanSessionShareOwner(d.pool.QueryRow(ctx, sessionShareOwnerQuery, machine, session))
}

// SessionShareOwner uses the existing transaction connection, including when all
// other pool connections are waiting for the sharing writer fence.
func (t *ShareTx) SessionShareOwner(machine, session string) (*SessionShareOwnership, error) {
	return scanSessionShareOwner(t.tx.QueryRow(t.ctx, sessionShareOwnerQuery, machine, session))
}

func scanSessionShareOwner(row pgx.Row) (*SessionShareOwnership, error) {
	var o SessionShareOwnership
	err := row.Scan(&o.Machine, &o.SessionID, &o.Issuer, &o.Subject, &o.Incarnation, &o.Generation, &o.Revision, &o.AuditID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}
func (d *DB) PutShareSubmission(ctx context.Context, id, machine, session, incarnation, digest, text string) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO share_submission_receipts(submission_id,machine_id,session_id,incarnation,content_digest,sanitized_text,delivery_state) VALUES($1,$2,$3,$4,$5,$6,'pending') ON CONFLICT(submission_id) DO NOTHING`, id, machine, session, incarnation, digest, text)
	return err
}
func (d *DB) SettleShareSubmission(ctx context.Context, id, machine, session, state string) error {
	_, err := d.pool.Exec(ctx, `UPDATE share_submission_receipts SET session_id=$3,delivery_state=$4 WHERE submission_id=$1 AND machine_id=$2 AND delivery_state='pending'`, id, machine, session, state)
	return err
}
