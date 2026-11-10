package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ShareTx is a bounded manager transaction over opaque prepared data. Policy and
// projection decisions remain in devices; this layer only persists them.
type ShareTx struct {
	ctx context.Context
	tx  pgx.Tx
}
type ShareRow struct {
	ID, Issuer, Subject, Machine, Session, Hash string
	Expires                                     time.Time
	Live                                        bool
	Body                                        json.RawMessage
}

func (d *DB) ShareTransaction(ctx context.Context, fn func(*ShareTx) error) error {
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		// One database-backed writer fence covers create quotas, idempotency and jobs.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(35713604)`); err != nil {
			return err
		}
		return fn(&ShareTx{ctx, tx})
	})
}
func (t *ShareTx) Object(id, kind, actor string, out any) error {
	var raw []byte
	if err := t.tx.QueryRow(t.ctx, `SELECT body FROM agent_share_objects WHERE id=$1 AND kind=$2 AND actor=$3 AND expires_at>now()`, id, kind, actor).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func (t *ShareTx) PutObject(id, kind, actor string, expires time.Time, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(raw) > 16<<20 {
		return errors.New("sharing object limit")
	}
	var bytes int64
	if err := t.tx.QueryRow(t.ctx, `SELECT COALESCE(sum(octet_length(body::text)),0) FROM agent_share_objects WHERE actor=$1 AND id<>$2`, actor, id).Scan(&bytes); err != nil {
		return err
	}
	if bytes+int64(len(raw)) > 64<<20 {
		return errors.New("sharing object limit")
	}
	if _, err = t.tx.Exec(t.ctx, `DELETE FROM agent_share_objects WHERE expires_at<=now()`); err != nil {
		return err
	}
	var count int
	if err = t.tx.QueryRow(t.ctx, `SELECT count(*) FROM agent_share_objects WHERE actor=$1 AND kind=$2 AND id<>$3`, actor, kind, id).Scan(&count); err != nil {
		return err
	}
	if count >= 256 {
		return errors.New("sharing object limit")
	}
	_, err = t.tx.Exec(t.ctx, `INSERT INTO agent_share_objects(id,kind,actor,expires_at,body) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET expires_at=EXCLUDED.expires_at,body=EXCLUDED.body`, id, kind, actor, expires, raw)
	return err
}
func scanShare(row pgx.Row) (ShareRow, error) {
	var r ShareRow
	err := row.Scan(&r.ID, &r.Issuer, &r.Subject, &r.Machine, &r.Session, &r.Hash, &r.Expires, &r.Live, &r.Body)
	return r, err
}

const shareColumns = `id,issuer,subject::text,machine,session_id,token_hash,expires_at,live,body`

func (t *ShareTx) Share(id string) (ShareRow, error) {
	return scanShare(t.tx.QueryRow(t.ctx, `SELECT `+shareColumns+` FROM agent_shares WHERE id=$1 FOR UPDATE`, id))
}
func (t *ShareTx) ByHash(hash string) (ShareRow, error) {
	return scanShare(t.tx.QueryRow(t.ctx, `SELECT `+shareColumns+` FROM agent_shares WHERE token_hash=$1 FOR UPDATE`, hash))
}
func (t *ShareTx) SaveShare(r ShareRow) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO agent_shares(id,issuer,subject,machine,session_id,token_hash,expires_at,live,body) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(id) DO UPDATE SET expires_at=EXCLUDED.expires_at,live=EXCLUDED.live,body=EXCLUDED.body`, r.ID, r.Issuer, r.Subject, r.Machine, r.Session, r.Hash, r.Expires, r.Live, r.Body)
	return err
}
func (t *ShareTx) Shares(issuer, subject string) ([]ShareRow, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT `+shareColumns+` FROM agent_shares WHERE issuer=$1 AND subject::text=$2 ORDER BY expires_at DESC LIMIT 1000`, issuer, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShareRow
	for rows.Next() {
		r, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (t *ShareTx) Creation(actor, id string) (digest, share string, err error) {
	err = t.tx.QueryRow(t.ctx, `SELECT digest,share_id FROM agent_share_creations WHERE actor=$1 AND request_id::text=$2`, actor, id).Scan(&digest, &share)
	return
}
func (t *ShareTx) PutCreation(actor, id, digest, share string) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO agent_share_creations(actor,request_id,digest,share_id) VALUES($1,$2,$3,$4)`, actor, id, digest, share)
	return err
}
func (t *ShareTx) MarkIngested(id string) error {
	_, err := t.tx.Exec(t.ctx, `UPDATE agent_shares SET last_ingested_at=now() WHERE id=$1`, id)
	return err
}

func (d *DB) LiveShareIDs(ctx context.Context) ([]string, error) {
	rows, err := d.pool.Query(ctx, `SELECT id FROM agent_shares WHERE live AND expires_at>now() ORDER BY last_ingested_at NULLS FIRST, id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

type ShareSubmissionReceipt struct {
	ID, Text, State string
	Sequence        int64
	Created         time.Time
}

func (d *DB) ShareSubmissions(ctx context.Context, machine, session, incarnation string) ([]ShareSubmissionReceipt, error) {
	rows, err := d.pool.Query(ctx, `SELECT submission_id,sanitized_text,delivery_state,sequence,created_at FROM share_submission_receipts WHERE machine_id=$1 AND (session_id=$2 OR session_id='') AND incarnation=$3 ORDER BY sequence LIMIT 10001`, machine, session, incarnation)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ShareSubmissionReceipt, error) {
		var s ShareSubmissionReceipt
		err := r.Scan(&s.ID, &s.Text, &s.State, &s.Sequence, &s.Created)
		return s, err
	})
}
func (d *DB) AttributeSession(ctx context.Context, o SessionShareOwnership, admin, reason, checkpoint string) (SessionShareOwnership, error) {
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, o.Machine+"/"+o.SessionID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM session_share_ownership WHERE machine_id=$1 AND session_id=$2)`, o.Machine, o.SessionID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrEnrollmentConflict
		}
		if err := tx.QueryRow(ctx, `INSERT INTO session_attribution_audit(machine,session_id,issuer,subject,administrator,reason,checkpoint_digest) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, o.Machine, o.SessionID, o.Issuer, o.Subject, admin, reason, checkpoint).Scan(&o.AuditID); err != nil {
			return err
		}
		o.Revision = 1
		_, err := tx.Exec(ctx, `INSERT INTO session_share_ownership(machine_id,session_id,issuer,owner_subject,incarnation,assignment_generation,audit_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, o.Machine, o.SessionID, o.Issuer, o.Subject, o.Incarnation, o.Generation, o.AuditID)
		return err
	})
	return o, err
}
