package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nwverse/persona/server/internal/proto"
)

// ---- interactions ----

// interactionOccurrenceTimeSQL preserves the full RFC3339Nano occurrence time
// retained in the payload. PostgreSQL's created_at column has only microseconds,
// which is not enough to distinguish two occurrences under a reused ID. The
// epoch-second and padded-fraction tuple also avoids nanosecond integer overflow.
func interactionOccurrenceTimeSQL(value string) string {
	return `(EXTRACT(EPOCH FROM DATE_TRUNC('second', (` + value + `)::timestamptz)), COALESCE(RPAD(SUBSTRING(` + value + ` FROM '\.([0-9]+)'), 9, '0'), '000000000'))`
}

// InsertInteraction records a pending occurrence. Replaying the same timestamp
// is idempotent, except that a non-resumable occurrence cancelled by startup
// cleanup (cancelled with no owner resolution) can be listed as pending again.
// A later timestamp is a genuinely reused runtime ID, so its new pending payload
// and occurrence replace the older terminal row. An older replay cannot overwrite
// the latest occurrence, and an owner-resolved occurrence never reopens.
func (d *DB) InsertInteraction(ctx context.Context, ix proto.Interaction) error {
	payload, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(ctx, `INSERT INTO interactions (id, machine_id, session_id, kind, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			machine_id = EXCLUDED.machine_id,
			session_id = EXCLUDED.session_id,
			kind = EXCLUDED.kind,
			payload = EXCLUDED.payload,
			status = 'pending',
			resolution = NULL,
			created_at = EXCLUDED.created_at,
			resolved_at = NULL,
			user_notified_at = NULL,
			auto_authorized_at = NULL,
			authorization_id = '',
			todo_id = ''
		WHERE (`+interactionOccurrenceTimeSQL("EXCLUDED.payload->>'created_at'")+` > `+
		interactionOccurrenceTimeSQL("interactions.payload->>'created_at'")+`) OR (
			`+interactionOccurrenceTimeSQL("EXCLUDED.payload->>'created_at'")+` = `+
		interactionOccurrenceTimeSQL("interactions.payload->>'created_at'")+`
			AND interactions.status = 'cancelled' AND interactions.resolution IS NULL
			AND NOT COALESCE((EXCLUDED.payload->>'resumable')::boolean, false)
			AND interactions.machine_id = EXCLUDED.machine_id
			AND interactions.session_id = EXCLUDED.session_id
		)`,
		ix.ID, ix.MachineID, ix.SessionID, ix.Kind, payload, ix.CreatedAt)
	return err
}

// MarkInteractionResumable records that pending interaction id outlives a restart from now on (its payload's
// resumable, which ExpireStaleInteractions reads): an ask_user question that let go of its turn and is now waited on by
// no call (assistant/ask_release.go, #7392). false when id is not pending.
func (d *DB) MarkInteractionResumable(ctx context.Context, id string) (bool, error) {
	tag, err := d.pool.Exec(ctx, `UPDATE interactions SET payload = jsonb_set(payload, '{resumable}', 'true'::jsonb)
		WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// FinishInteraction records how an interaction ended; only the first outcome counts.
func (d *DB) FinishInteraction(ctx context.Context, id, status string, res *proto.Resolution) error {
	var raw []byte
	if res != nil {
		raw, _ = json.Marshal(res)
	}
	_, err := d.pool.Exec(ctx, `UPDATE interactions SET status = $2, resolution = $3, resolved_at = now()
		WHERE id = $1 AND status = 'pending'`, id, status, raw)
	return err
}

// FinishInteractionOccurrence finalizes only the specified occurrence. It first
// inserts a missing pending row (needed after an earlier best-effort insert
// failed), then conditionally finishes that exact machine/session/timestamp and,
// for a detachable command, its exact call fields. matched is false when the ID
// now belongs to another occurrence; currentTerminal reports that other row's
// state so recovery never cancels it or forgets its own journal while it is
// still pending.
func (d *DB) FinishInteractionOccurrence(ctx context.Context, ix proto.Interaction, status string, res *proto.Resolution) (matched, currentTerminal bool, err error) {
	if err := d.InsertInteraction(ctx, ix); err != nil {
		return false, false, err
	}
	var raw []byte
	if res != nil {
		raw, _ = json.Marshal(res)
	}
	var input []byte
	if ix.Detachable {
		input, err = json.Marshal(ix.ToolInput)
		if err != nil {
			return false, false, err
		}
	}
	tag, err := d.pool.Exec(ctx, `UPDATE interactions SET status = $2, resolution = $3, resolved_at = now()
		WHERE id = $1 AND status = 'pending' AND machine_id = $4 AND session_id = $5 AND created_at = $6
		AND COALESCE((payload->>'detachable')::boolean, false) = $7
		AND (NOT $7 OR (kind = $8 AND COALESCE(payload->>'tool_name', '') = $9
			AND COALESCE(payload->>'execution_cwd', '') = $10
			AND COALESCE(payload->'tool_input', 'null'::jsonb) = COALESCE($11::jsonb, 'null'::jsonb)))
		AND `+interactionOccurrenceTimeSQL("interactions.payload->>'created_at'")+` = `+
		interactionOccurrenceTimeSQL("$12::text"),
		ix.ID, status, raw, ix.MachineID, ix.SessionID, ix.CreatedAt, ix.Detachable, ix.Kind, ix.ToolName, ix.ExecutionCwd, input,
		ix.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return false, false, err
	}
	if tag.RowsAffected() == 1 {
		return true, true, nil
	}
	rec, err := d.InteractionRecord(ctx, ix.ID)
	if err != nil {
		return false, false, err
	}
	if rec == nil {
		return false, false, errors.New("interaction occurrence is missing after its insert")
	}
	if !sameInteractionOccurrence(ix, rec.Interaction) {
		return false, rec.Status != "pending", nil
	}
	if rec.Status == "pending" {
		return false, false, errors.New("interaction occurrence remained pending after its terminal write")
	}
	if rec.Status != status {
		return false, true, errors.New("interaction occurrence already has a different terminal status")
	}
	return true, true, nil
}

func sameInteractionOccurrence(a, b proto.Interaction) bool {
	if a.ID != b.ID || a.MachineID != b.MachineID || a.SessionID != b.SessionID || !a.CreatedAt.Equal(b.CreatedAt) {
		return false
	}
	if a.Detachable || b.Detachable {
		return a.Detachable && b.Detachable && a.Kind == b.Kind && a.ToolName == b.ToolName &&
			a.ExecutionCwd == b.ExecutionCwd && sameJSONDocument(a.ToolInput, b.ToolInput)
	}
	return true
}

// sameJSONDocument compares JSON values without making PostgreSQL JSONB's key
// ordering part of an occurrence's identity. Invalid documents retain the old
// exact-byte fallback and therefore fail closed.
func sameJSONDocument(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, bool) {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var value any
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, false
		}
		return value, true
	}
	av, aok := decode(a)
	bv, bok := decode(b)
	if !aok || !bok {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(av, bv)
}

// LateAnswer records on expired card id an answer that came after it expired (proto.Resolution.Late): answers (by
// question) are added to those recorded late before, and by / reason (proto.Resolution.AnsweredBy, Reason) say who
// answered. The card stays "expired"; resolved_at becomes when the answer came. A card that did not expire is left as
// it is (false).
func (d *DB) LateAnswer(ctx context.Context, id string, answers map[string]string, by, reason string) (bool, error) {
	add, err := json.Marshal(answers)
	if err != nil {
		return false, err
	}
	who := map[string]string{}
	if by != "" {
		who["answered_by"] = by
	}
	if reason != "" {
		who["reason"] = reason
	}
	extra, err := json.Marshal(who)
	if err != nil {
		return false, err
	}
	tag, err := d.pool.Exec(ctx, `UPDATE interactions SET resolved_at = now(), resolution = jsonb_build_object('late', true,
			'answers', COALESCE(CASE WHEN resolution->>'late' = 'true' THEN resolution->'answers' END, '{}'::jsonb) || $2::jsonb) || $3::jsonb
		WHERE id = $1 AND status = 'expired'`, id, add, extra)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// InteractionRecord is a past interaction with its outcome.
type InteractionRecord struct {
	Interaction proto.Interaction `json:"interaction"`
	Status      string            `json:"status"`
	Resolution  *proto.Resolution `json:"resolution,omitempty"`
	ResolvedAt  *time.Time        `json:"resolved_at,omitempty"`
}

// The statements of Interactions, one per combination of filters. The single statement they replace matched an
// empty filter with an OR ("$1 = empty OR machine_id = $1"). Once Postgres switched that cached statement to a generic
// plan (after five runs) it could not fold the empty test away, so it walked interactions_created over every row ever
// kept. TestInteractionsHistoryIndexed checks the plans of the cached ones.
const (
	interactionsSQL        = `SELECT payload, status, resolution, resolved_at FROM interactions ORDER BY created_at DESC LIMIT $1`
	interactionsMachineSQL = `SELECT payload, status, resolution, resolved_at FROM interactions
		WHERE machine_id = $1 ORDER BY created_at DESC LIMIT $2`
	interactionsSessionSQL = `SELECT payload, status, resolution, resolved_at FROM interactions
		WHERE session_id = $1 ORDER BY created_at DESC LIMIT $2`
	interactionsMachineSessionSQL = `SELECT payload, status, resolution, resolved_at FROM interactions
		WHERE machine_id = $1 AND session_id = $2 ORDER BY created_at DESC LIMIT $3`
)

// Interactions lists recent interactions, newest first, optionally for one machine and/or one session.
func (d *DB) Interactions(ctx context.Context, machine, session string, limit int) ([]InteractionRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// A machine alone or a session alone has no index that orders by created_at: the best plan walks
	// interactions_created for a busy one and reads interactions_session then sorts for a quiet one. A generic plan
	// settles on the walk, which reads every row ever kept for a quiet one, so these two go as the unnamed statement
	// of QueryExecModeExec, planned for each call with its values. TestInteractionsNotCached checks that.
	var q string
	var args []any
	switch {
	case machine != "" && session != "":
		q, args = interactionsMachineSessionSQL, []any{machine, session, limit}
	case machine != "":
		q, args = interactionsMachineSQL, []any{pgx.QueryExecModeExec, machine, limit}
	case session != "":
		q, args = interactionsSessionSQL, []any{pgx.QueryExecModeExec, session, limit}
	default:
		q, args = interactionsSQL, []any{limit}
	}
	rows, err := d.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InteractionRecord, error) {
		var rec InteractionRecord
		var payload, res []byte
		if err := r.Scan(&payload, &rec.Status, &res, &rec.ResolvedAt); err != nil {
			return rec, err
		}
		if err := json.Unmarshal(payload, &rec.Interaction); err != nil {
			return rec, err
		}
		if len(res) > 0 {
			rec.Resolution = &proto.Resolution{}
			if err := json.Unmarshal(res, rec.Resolution); err != nil {
				return rec, err
			}
		}
		return rec, nil
	})
}

// InteractionQuery pages the interaction history by time (GET /api/v1/interactions/history with since, before or
// kind): newest first, created at or after Since, before the cursor (Before, BeforeID) and of Kind, each when set.
type InteractionQuery struct {
	Machine, Session, Kind string
	Since                  time.Time
	// Before is the cursor of the page before: rows created before it, or at it with an id before BeforeID.
	Before   time.Time
	BeforeID string
	Limit    int // 1…500; another value is 100
}

// InteractionPage is a page of the history and, when it was full, the cursor of the next older one.
type InteractionPage struct {
	Records      []InteractionRecord
	NextBefore   *time.Time
	NextBeforeID string
}

// InteractionsPage is Interactions with a time range, a kind and a cursor. Its statement is built for the filters
// given and planned for each call with its values (QueryExecModeExec): the history is read rarely, and a generic plan
// over optional filters walks every row ever kept (see Interactions). The cursor's created_at bound is written twice,
// once alone, so the walk down interactions_created starts at it rather than at the newest row.
func (d *DB) InteractionsPage(ctx context.Context, q InteractionQuery) (InteractionPage, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	var where []string
	args := []any{pgx.QueryExecModeExec}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args)-1)
	}
	if q.Machine != "" {
		where = append(where, "machine_id = "+arg(q.Machine))
	}
	if q.Session != "" {
		where = append(where, "session_id = "+arg(q.Session))
	}
	if q.Kind != "" {
		where = append(where, "kind = "+arg(q.Kind))
	}
	if !q.Since.IsZero() {
		where = append(where, "created_at >= "+arg(q.Since))
	}
	if !q.Before.IsZero() {
		b := arg(q.Before)
		if q.BeforeID == "" {
			where = append(where, "created_at < "+b)
		} else {
			where = append(where, "created_at <= "+b, "(created_at, id) < ("+b+", "+arg(q.BeforeID)+")")
		}
	}
	sql := `SELECT payload, status, resolution, resolved_at, created_at, id FROM interactions`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY created_at DESC, id DESC LIMIT " + arg(q.Limit)
	rows, err := d.pool.Query(ctx, sql, args...)
	if err != nil {
		return InteractionPage{}, err
	}
	var page InteractionPage
	var lastAt time.Time
	var lastID string
	page.Records, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (InteractionRecord, error) {
		var rec InteractionRecord
		var payload, res []byte
		if err := r.Scan(&payload, &rec.Status, &res, &rec.ResolvedAt, &lastAt, &lastID); err != nil {
			return rec, err
		}
		if err := json.Unmarshal(payload, &rec.Interaction); err != nil {
			return rec, err
		}
		if len(res) > 0 {
			rec.Resolution = &proto.Resolution{}
			if err := json.Unmarshal(res, rec.Resolution); err != nil {
				return rec, err
			}
		}
		return rec, nil
	})
	if err != nil {
		return InteractionPage{}, err
	}
	if len(page.Records) == q.Limit {
		page.NextBefore, page.NextBeforeID = &lastAt, lastID
	}
	return page, nil
}

// InteractionRecord is one interaction with its outcome; nil when there is no such row.
func (d *DB) InteractionRecord(ctx context.Context, id string) (*InteractionRecord, error) {
	var rec InteractionRecord
	var payload, res []byte
	err := d.pool.QueryRow(ctx, `SELECT payload, status, resolution, resolved_at FROM interactions WHERE id = $1`, id).
		Scan(&payload, &rec.Status, &res, &rec.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(payload, &rec.Interaction); err != nil {
		return nil, err
	}
	if len(res) > 0 {
		rec.Resolution = &proto.Resolution{}
		if err := json.Unmarshal(res, rec.Resolution); err != nil {
			return nil, err
		}
	}
	return &rec, nil
}

// The startup statements of ExpireStaleInteractions. Both must stay answerable from the interactions_pending
// partial index (migration 0393); TestPendingInteractionsIndexed checks the plans of these exact constants.
const (
	resumablePendingSQL = `SELECT payload FROM interactions WHERE status = 'pending'
		AND (payload->>'resumable')::boolean IS TRUE
		AND ((payload->>'expires_at')::timestamptz > now()
			OR ((payload->>'owner_proposal')::boolean IS TRUE AND (payload->>'no_expiry')::boolean IS TRUE))`
	cancelPendingSQL = `UPDATE interactions SET status = 'cancelled', resolved_at = now()
		WHERE status = 'pending' AND NOT (id = ANY($1))
		AND id NOT IN (SELECT interaction_id FROM session_work WHERE interaction_id <> '')
		AND id NOT IN (SELECT protected.id FROM settings,
			jsonb_array_elements(CASE WHEN jsonb_typeof(value) = 'array' THEN value ELSE '[]'::jsonb END) AS item,
			LATERAL unnest(ARRAY[item->'card'->>'id', item->>'expired_id']) AS protected(id)
			WHERE key = 'agents.tool_loops' AND protected.id IS NOT NULL)
		RETURNING payload`
)

// ResumableInteractions returns the pending, unexpired cards that may be
// restored after a manager restart. Unlike ExpireStaleInteractions it never
// writes, so a delayed retry cannot cancel a current-process card.
func (d *DB) ResumableInteractions(ctx context.Context) ([]proto.Interaction, error) {
	rows, err := d.pool.Query(ctx, resumablePendingSQL)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (proto.Interaction, error) {
		var payload []byte
		var ix proto.Interaction
		if err := r.Scan(&payload); err != nil {
			return ix, err
		}
		return ix, json.Unmarshal(payload, &ix)
	})
}

// ExpireStaleInteractions marks interactions left pending by a previous hub process cancelled, and returns the
// resumable ones not yet expired instead: their hooks reconnect and claim them again. The manager's cards about a
// turn (session_work's interaction_id) and loop cards in the manager's settings stay pending too. The manager restores
// them under the same id and ends cards it no longer needs. cancelled carries the
// payloads it marked: the apps may still show them (#4568), and their occurrence
// timestamps prevent a delayed withdrawal from ending a reused interaction ID.
func (d *DB) ExpireStaleInteractions(ctx context.Context) (kept []proto.Interaction, cancelled []proto.Interaction, err error) {
	return d.expireStaleInteractions(ctx, nil)
}

func (d *DB) expireStaleInteractions(ctx context.Context, beforeCancel func()) (kept []proto.Interaction, cancelled []proto.Interaction, err error) {
	// One repeatable-read snapshot covers both statements. A current-process card
	// inserted after the resumable lookup is invisible to the cancellation lookup
	// too, instead of being cancelled merely because it missed the first query.
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, resumablePendingSQL)
	if err != nil {
		return nil, nil, err
	}
	kept, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (proto.Interaction, error) {
		var payload []byte
		var ix proto.Interaction
		if err := r.Scan(&payload); err != nil {
			return ix, err
		}
		return ix, json.Unmarshal(payload, &ix)
	})
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(kept))
	for i, ix := range kept {
		ids[i] = ix.ID
	}
	if beforeCancel != nil {
		beforeCancel()
	}
	rows, err = tx.Query(ctx, cancelPendingSQL, ids)
	if err != nil {
		return nil, nil, err
	}
	cancelled, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (proto.Interaction, error) {
		var payload []byte
		var ix proto.Interaction
		if err := r.Scan(&payload); err != nil {
			return ix, err
		}
		return ix, json.Unmarshal(payload, &ix)
	})
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return kept, cancelled, nil
}

// InteractionOutcome returns an interaction's status and resolution; an empty status when there is no such row.
func (d *DB) InteractionOutcome(ctx context.Context, id string) (string, *proto.Resolution, error) {
	var status string
	var raw []byte
	err := d.pool.QueryRow(ctx, `SELECT status, resolution FROM interactions WHERE id = $1`, id).Scan(&status, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil || len(raw) == 0 {
		return status, nil, err
	}
	var res proto.Resolution
	return status, &res, json.Unmarshal(raw, &res)
}
