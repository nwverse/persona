package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Settings reads several JSON settings in one statement. A caller merging a base
// configuration with a layer therefore sees one statement-consistent generation,
// rather than two reads that a concurrent writer could tear apart.
func (d *DB) Settings(ctx context.Context, keys ...string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := d.pool.Query(ctx, `SELECT key, value FROM `+d.settingsTable()+` WHERE key = ANY($1)`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var value json.RawMessage
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

var errPushGradingOwnerConflict = errors.New("push grading layer owner conflict")

// pushGradingWrite serializes every base/layer/mirror mutation, including a
// first insert or a delete where no row exists to lock. The mirror is derived
// from rows inside this transaction, never from a caller's older snapshot.
func (d *DB) pushGradingWrite(ctx context.Context, write func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "push_grading:hub_settings"); err != nil {
			return err
		}
		return write(tx)
	})
}

func (d *DB) mirrorPushGrading(ctx context.Context, tx pgx.Tx, baseKey, layerKey, mirrorKey string) error {
	rows, err := tx.Query(ctx, `SELECT key, value FROM `+d.settingsTable()+` WHERE key = ANY($1)`, []string{baseKey, layerKey})
	if err != nil {
		return err
	}
	values := map[string]json.RawMessage{}
	for rows.Next() {
		var key string
		var value json.RawMessage
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		values[key] = value
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	_, layerExists := values[layerKey]
	enabled := layerExists
	if raw, ok := values[baseKey]; ok {
		var base struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			return err
		}
		if base.Enabled != nil {
			enabled = *base.Enabled
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO `+d.settingsTable()+` (key, value, updated_at) VALUES ($1, $2::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = $2::jsonb, updated_at = now()`, mirrorKey, enabled)
	return err
}

// PutPushGradingBase stores the owner's complete base configuration and the
// effective legacy switch in one transaction, including when enabled stays the
// same while another configuration field changes.
func (d *DB) PutPushGradingBase(ctx context.Context, baseKey, layerKey, mirrorKey string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return d.pushGradingWrite(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO `+d.settingsTable()+` (key, value, updated_at) VALUES ($1, $2::jsonb, now())
			ON CONFLICT (key) DO UPDATE SET value = $2::jsonb, updated_at = now()`, baseKey, raw); err != nil {
			return err
		}
		return d.mirrorPushGrading(ctx, tx, baseKey, layerKey, mirrorKey)
	})
}

// PutPushGradingLayer claims or renews the layer and mirrors the resulting
// effective switch atomically. A different owner changes neither row.
func (d *DB) PutPushGradingLayer(ctx context.Context, baseKey, layerKey, mirrorKey, manager, source string, value any, out any) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	err = d.pushGradingWrite(ctx, func(tx pgx.Tx) error {
		var stored []byte
		err := tx.QueryRow(ctx, `INSERT INTO `+d.settingsTable()+` AS s (key, value, updated_at)
			VALUES ($1, $2::jsonb, now())
			ON CONFLICT (key) DO UPDATE SET
				value = jsonb_set($2::jsonb, '{revision}', to_jsonb(COALESCE((s.value->>'revision')::bigint, 0) + 1), false),
				updated_at = now()
			WHERE s.value->>'manager' = $3 AND s.value->>'source' = $4
			RETURNING value`, layerKey, raw, manager, source).Scan(&stored)
		if errors.Is(err, pgx.ErrNoRows) {
			return errPushGradingOwnerConflict
		}
		if err != nil {
			return err
		}
		if out != nil {
			if err := json.Unmarshal(stored, out); err != nil {
				return err
			}
		}
		return d.mirrorPushGrading(ctx, tx, baseKey, layerKey, mirrorKey)
	})
	if errors.Is(err, errPushGradingOwnerConflict) {
		return false, nil
	}
	return err == nil, err
}

// DeletePushGradingLayer deletes only the still-matching owner/revision and
// mirrors the post-delete effective switch in the same transaction.
func (d *DB) DeletePushGradingLayer(ctx context.Context, baseKey, layerKey, mirrorKey, manager, source string, revision int64) (bool, error) {
	deleted := false
	err := d.pushGradingWrite(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM `+d.settingsTable()+`
			WHERE key = $1 AND value->>'manager' = $2 AND value->>'source' = $3
			AND ($4::bigint = 0 OR COALESCE((value->>'revision')::bigint, 0) = $4)`, layerKey, manager, source, revision)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected() == 1
		if !deleted {
			return nil
		}
		return d.mirrorPushGrading(ctx, tx, baseKey, layerKey, mirrorKey)
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}
