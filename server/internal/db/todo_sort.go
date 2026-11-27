package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The batches of to-do moves the owner confirmed (#13026, docs/assistant/todos.md "Sorting into projects"): the sort
// sheet's, or a move from the chat. Each keeps every to-do's place before and after, so an undo can put back the ones
// still as the batch left them (assistant/todo_sort_batches.go).

// TodoSortPlace is where a to-do is filed: its knowledge-base project's slug ("" none), or marked temporary (a one-off).
type TodoSortPlace struct {
	Project   string `json:"project"`
	Temporary bool   `json:"temporary,omitempty"`
}

// TodoSortItem is one to-do a batch moved.
type TodoSortItem struct {
	Todo   string        `json:"todo"`
	Before TodoSortPlace `json:"before"`
	After  TodoSortPlace `json:"after"`
}

// TodoSortBatch is one batch of moves. UndoneAt is set by the undo that claimed it, Outcomes what that undo did with each
// item (in Items' order).
type TodoSortBatch struct {
	ID       string         `json:"id"`
	At       time.Time      `json:"at"`
	By       string         `json:"by"`
	Kind     string         `json:"kind"`
	Summary  string         `json:"summary"`
	Items    []TodoSortItem `json:"items"`
	UndoneAt *time.Time     `json:"undone_at,omitempty"`
	Outcomes []string       `json:"outcomes,omitempty"`
}

// ErrTodoSortBatchNotFound: no batch has that id (or it was pruned).
var ErrTodoSortBatchNotFound = errors.New("todo sort batch not found")

const todoSortColumns = `id, at, by_whom, kind, summary, items, undone_at, outcomes`

func scanTodoSortBatch(row pgx.Row) (TodoSortBatch, error) {
	var b TodoSortBatch
	var items, outcomes []byte
	if err := row.Scan(&b.ID, &b.At, &b.By, &b.Kind, &b.Summary, &items, &b.UndoneAt, &outcomes); err != nil {
		return b, err
	}
	if err := json.Unmarshal(items, &b.Items); err != nil {
		return b, err
	}
	if len(outcomes) > 0 {
		if err := json.Unmarshal(outcomes, &b.Outcomes); err != nil {
			return b, err
		}
	}
	return b, nil
}

// InsertTodoSortBatch stores a new batch.
func (d *DB) InsertTodoSortBatch(ctx context.Context, b TodoSortBatch) error {
	items, err := json.Marshal(b.Items)
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(ctx, `INSERT INTO todo_sort_batches (id, at, by_whom, kind, summary, items) VALUES ($1, $2, $3, $4, $5, $6)`,
		b.ID, b.At, b.By, b.Kind, b.Summary, items)
	return err
}

// TodoSortBatch is batch id; ErrTodoSortBatchNotFound when there is none.
func (d *DB) TodoSortBatch(ctx context.Context, id string) (TodoSortBatch, error) {
	b, err := scanTodoSortBatch(d.pool.QueryRow(ctx, `SELECT `+todoSortColumns+` FROM todo_sort_batches WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrTodoSortBatchNotFound
	}
	return b, err
}

// TodoSortBatches are the newest limit batches, newest first.
func (d *DB) TodoSortBatches(ctx context.Context, limit int) ([]TodoSortBatch, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+todoSortColumns+` FROM todo_sort_batches ORDER BY at DESC, id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TodoSortBatch
	for rows.Next() {
		b, err := scanTodoSortBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ClaimTodoSortBatchUndo marks batch id undone at at: true when this call claimed it, false when it was undone already
// (or is gone), so two undos never both run.
func (d *DB) ClaimTodoSortBatchUndo(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := d.pool.Exec(ctx, `UPDATE todo_sort_batches SET undone_at = $2 WHERE id = $1 AND undone_at IS NULL`, id, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// RecordTodoSortBatchUndo keeps what the undo of batch id did with each item.
func (d *DB) RecordTodoSortBatchUndo(ctx context.Context, id string, outcomes []string) error {
	out, err := json.Marshal(outcomes)
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(ctx, `UPDATE todo_sort_batches SET outcomes = $2 WHERE id = $1`, id, out)
	return err
}

// PruneTodoSortBatches deletes the batches beyond the newest keep that are also older than before.
func (d *DB) PruneTodoSortBatches(ctx context.Context, keep int, before time.Time) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM todo_sort_batches WHERE at < $2 AND id NOT IN
		(SELECT id FROM todo_sort_batches ORDER BY at DESC, id LIMIT $1)`, keep, before)
	return err
}
