package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The owner's words each concern holds, and where an open concern stands (docs/assistant/todos.md
// "Matters"). The assistant keeps them in memory and writes each change here.

// Who filed a message under its concern.
const (
	FiledByAssistant = "assistant" // the main conversation, concern_file
	FiledByOwner     = "owner"     // the owner moved it in the app
)

// ConcernWordRow is one message of the owner's, filed under concern TodoID.
type ConcernWordRow struct {
	Conversation string
	MessageID    string
	TodoID       string
	Text         string
	SaidAt       time.Time
	FiledAt      time.Time
	FiledBy      string
}

// ConcernStateRow is where open concern TodoID stands: Now, a line of what is being done or waited for; Ask, what the
// owner must decide, and Options, the answers offered. About is what the concern is: what, why, and what counts as
// done.
type ConcernStateRow struct {
	TodoID    string
	About     string
	Now       string
	Ask       string
	Options   []string
	UpdatedAt time.Time
}

// ListConcernWords returns every filed message, oldest said first.
func (d *DB) ListConcernWords(ctx context.Context) ([]ConcernWordRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT conversation, message_id, todo_id, text, said_at, filed_at, filed_by
		FROM concern_words ORDER BY said_at, message_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConcernWordRow
	for rows.Next() {
		var r ConcernWordRow
		if err := rows.Scan(&r.Conversation, &r.MessageID, &r.TodoID, &r.Text, &r.SaidAt, &r.FiledAt, &r.FiledBy); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutConcernWord files message r under r.TodoID, moving it there when it was filed under another concern.
// ErrTodoNotFound when the concern is gone.
func (d *DB) PutConcernWord(ctx context.Context, r ConcernWordRow) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO concern_words (conversation, message_id, todo_id, text, said_at, filed_at, filed_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (conversation, message_id) DO UPDATE SET todo_id = EXCLUDED.todo_id, filed_at = EXCLUDED.filed_at,
			filed_by = EXCLUDED.filed_by`,
		r.Conversation, r.MessageID, r.TodoID, r.Text, r.SaidAt, r.FiledAt, r.FiledBy)
	return concernFK(err)
}

// ListConcernStates returns the state of every concern that has one.
func (d *DB) ListConcernStates(ctx context.Context) ([]ConcernStateRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT todo_id, about, now_line, ask, options, updated_at FROM concern_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConcernStateRow
	for rows.Next() {
		var r ConcernStateRow
		if err := rows.Scan(&r.TodoID, &r.About, &r.Now, &r.Ask, &r.Options, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutConcernState writes where concern r.TodoID stands. ErrTodoNotFound when the concern is gone.
func (d *DB) PutConcernState(ctx context.Context, r ConcernStateRow) error {
	if r.Options == nil {
		r.Options = []string{}
	}
	_, err := d.pool.Exec(ctx, `INSERT INTO concern_state (todo_id, about, now_line, ask, options, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (todo_id) DO UPDATE SET about = EXCLUDED.about, now_line = EXCLUDED.now_line, ask = EXCLUDED.ask,
			options = EXCLUDED.options, updated_at = EXCLUDED.updated_at`, r.TodoID, r.About, r.Now, r.Ask, r.Options, r.UpdatedAt)
	return concernFK(err)
}

// concernFK reads a foreign key refusal (the concern was deleted) as ErrTodoNotFound.
func concernFK(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" {
		return ErrTodoNotFound
	}
	return err
}
