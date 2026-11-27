package assistant

// Filing to-dos under project nodes, and taking it back (#13026, docs/assistant/todos.md "Sorting into projects"):
// every move the owner confirms, from the sort sheet or the chat (todo_sort apply), is one batch
// (db.TodoSortBatch) with each to-do's place before and after. An undo writes back each to-do still as the batch left
// it, newest first, and skips (and names) any that changed since or are gone. A batch is undone once.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Batch kinds: what the owner did.
const (
	sortKindSort = "sort" // the sort sheet, or todo_sort apply from the chat
)

// Batches kept: the newest sortBatchesKept, and all of the last sortBatchesFor.
const (
	sortBatchesKept = 200
	sortBatchesFor  = 30 * 24 * time.Hour
	sortMaxApply    = 200
)

// Undo outcomes of an item.
const (
	undoDone    = "done"
	undoChanged = "skipped: changed since"
	undoGone    = "skipped: gone"
)

var (
	errSortBatchMissing = errors.New("there's no such batch")
	errSortBatchUndone  = errors.New("that change was undone already")
)

// todoSortStore is where the batches are kept (db.DB); a Store without it (the unit tests' memStore) keeps them in
// this manager's memory only.
type todoSortStore interface {
	InsertTodoSortBatch(ctx context.Context, b db.TodoSortBatch) error
	TodoSortBatch(ctx context.Context, id string) (db.TodoSortBatch, error)
	TodoSortBatches(ctx context.Context, limit int) ([]db.TodoSortBatch, error)
	ClaimTodoSortBatchUndo(ctx context.Context, id string, at time.Time) (bool, error)
	RecordTodoSortBatchUndo(ctx context.Context, id string, outcomes []string) error
	PruneTodoSortBatches(ctx context.Context, keep int, before time.Time) error
}

var _ todoSortStore = (*db.DB)(nil)

// memSortBatches keeps the batches of a manager with no database, newest last.
type memSortBatches struct {
	mu   sync.Mutex
	list []db.TodoSortBatch
}

func (m *memSortBatches) InsertTodoSortBatch(_ context.Context, b db.TodoSortBatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.list = append(m.list, b)
	return nil
}

func (m *memSortBatches) TodoSortBatch(_ context.Context, id string) (db.TodoSortBatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.list {
		if b.ID == id {
			return b, nil
		}
	}
	return db.TodoSortBatch{}, db.ErrTodoSortBatchNotFound
}

func (m *memSortBatches) TodoSortBatches(_ context.Context, limit int) ([]db.TodoSortBatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.list)
	slices.Reverse(out)
	return out[:min(len(out), limit)], nil
}

func (m *memSortBatches) ClaimTodoSortBatchUndo(_ context.Context, id string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.list {
		if m.list[i].ID == id && m.list[i].UndoneAt == nil {
			m.list[i].UndoneAt = &at
			return true, nil
		}
	}
	return false, nil
}

func (m *memSortBatches) RecordTodoSortBatchUndo(_ context.Context, id string, outcomes []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.list {
		if m.list[i].ID == id {
			m.list[i].Outcomes = outcomes
		}
	}
	return nil
}

func (m *memSortBatches) PruneTodoSortBatches(_ context.Context, keep int, before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.list) - keep; n > 0 {
		m.list = slices.DeleteFunc(m.list, func(b db.TodoSortBatch) bool {
			if n > 0 && b.At.Before(before) {
				n--
				return true
			}
			return false
		})
	}
	return nil
}

// sortBatches is where this manager keeps its batches.
func (s *Service) sortBatches() todoSortStore {
	if st, ok := s.store().(todoSortStore); ok && st != nil {
		return st
	}
	return &s.sortMem
}

// sortMove is one to-do to file: under Project (a project's slug or name, "" none), or marked Temporary.
type sortMove struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Temporary bool   `json:"temporary"`
}

// sortItemError: one item of a batch is refused; nothing was written.
type sortItemError struct {
	Index int
	Err   error
}

func (e *sortItemError) Error() string { return fmt.Sprintf("item %d: %v", e.Index+1, e.Err) }
func (e *sortItemError) Unwrap() error { return e.Err }

// sortApplied is what a batch did: Batch its id ("" when nothing moved, so there is nothing to undo).
type sortApplied struct {
	Batch     string `json:"batch"`
	Moved     int    `json:"moved"`
	Unchanged int    `json:"unchanged"`
	Failed    int    `json:"failed,omitempty"`
}

// placeOf is where t is filed.
func placeOf(t Todo) db.TodoSortPlace {
	return db.TodoSortPlace{Project: t.Project, Temporary: t.Temporary}
}

// applySort files the moves as one batch. Every item is checked before anything is written: one refused item (a
// *sortItemError) writes nothing.
func (s *Service) applySort(ctx context.Context, moves []sortMove, by, kind string) (sortApplied, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return sortApplied{}, errTodosUnavailable
	}
	type move struct {
		id            string
		before, after db.TodoSortPlace
	}
	var plan []move
	res := sortApplied{}
	seen := map[string]bool{}
	for i, m := range moves {
		fail := func(err error) error { return &sortItemError{Index: i, Err: err} }
		id := strings.TrimSpace(m.ID)
		if seen[id] {
			return res, fail(errors.New("named twice"))
		}
		seen[id] = true
		t, ok := s.todoNow(id)
		if !ok {
			return res, fail(fmt.Errorf("no to-do %q", m.ID))
		}
		after := db.TodoSortPlace{Temporary: m.Temporary}
		if !m.Temporary {
			slug, err := s.todoProject(ctx, m.Project)
			if err != nil {
				return res, fail(err)
			}
			after.Project = slug
		}
		if before := placeOf(t); before == after {
			res.Unchanged++
		} else {
			plan = append(plan, move{id: t.ID, before: before, after: after})
		}
	}
	b := db.TodoSortBatch{ID: "tsb_" + randomHex(6), At: time.Now().UTC(), By: by, Kind: kind}
	for _, m := range plan {
		project, temporary := m.after.Project, m.after.Temporary
		if _, err := s.editTodo(ctx, m.id, db.TodoEdit{Project: &project, Temporary: &temporary}); err != nil {
			s.logf("to-do sort %s: %s not moved: %v", b.ID, m.id, err)
			res.Failed++
			continue
		}
		b.Items = append(b.Items, db.TodoSortItem{Todo: m.id, Before: m.before, After: m.after})
		res.Moved++
	}
	if len(b.Items) == 0 {
		return res, nil
	}
	b.Summary = fmt.Sprintf("moved %d", res.Moved)
	st := s.sortBatches()
	if err := st.InsertTodoSortBatch(ctx, b); err != nil {
		return res, fmt.Errorf("moved %d, but the change can't be kept for Undo: %w", res.Moved, err)
	}
	if err := st.PruneTodoSortBatches(ctx, sortBatchesKept, b.At.Add(-sortBatchesFor)); err != nil {
		s.logf("to-do sort: old batches not pruned: %v", err)
	}
	res.Batch = b.ID
	return res, nil
}

// undoOutcome is what the undo did with one to-do.
type undoOutcome struct {
	Todo    string `json:"todo"`
	Title   string `json:"title,omitempty"`
	Outcome string `json:"outcome"`
}

// sortUndone is what an undo did.
type sortUndone struct {
	Batch    string        `json:"batch"`
	Undone   int           `json:"undone"`
	Skipped  int           `json:"skipped"`
	Outcomes []undoOutcome `json:"outcomes"`
}

// undoSort puts batch id's to-dos back, newest first, each only while it is still as the batch left it.
func (s *Service) undoSort(ctx context.Context, id string) (sortUndone, error) {
	st := s.sortBatches()
	b, err := st.TodoSortBatch(ctx, strings.TrimSpace(id))
	if errors.Is(err, db.ErrTodoSortBatchNotFound) {
		return sortUndone{}, errSortBatchMissing
	}
	if err != nil {
		return sortUndone{}, err
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return sortUndone{}, errTodosUnavailable
	}
	ok, err := st.ClaimTodoSortBatchUndo(ctx, b.ID, time.Now().UTC())
	if err != nil {
		return sortUndone{}, err
	}
	if !ok {
		return sortUndone{}, errSortBatchUndone
	}
	res := sortUndone{Batch: b.ID, Outcomes: make([]undoOutcome, len(b.Items))}
	outcomes := make([]string, len(b.Items))
	for i := len(b.Items) - 1; i >= 0; i-- {
		it := b.Items[i]
		out := s.undoSortItem(ctx, it)
		outcomes[i] = out
		title := ""
		if t, ok := s.todoNow(it.Todo); ok {
			title = t.Title
		}
		res.Outcomes[i] = undoOutcome{Todo: it.Todo, Title: title, Outcome: out}
		if out == undoDone {
			res.Undone++
		} else {
			res.Skipped++
		}
	}
	if err := st.RecordTodoSortBatchUndo(ctx, b.ID, outcomes); err != nil {
		s.logf("to-do sort %s: undone, the outcomes are not kept: %v", b.ID, err)
	}
	return res, nil
}

// undoSortItem puts one to-do back; its outcome.
func (s *Service) undoSortItem(ctx context.Context, it db.TodoSortItem) string {
	t, ok := s.todoNow(it.Todo)
	if !ok {
		return undoGone
	}
	if placeOf(t) != it.After {
		return undoChanged
	}
	project, temporary := it.Before.Project, it.Before.Temporary
	if _, err := s.editTodo(ctx, it.Todo, db.TodoEdit{Project: &project, Temporary: &temporary}); err != nil {
		return "failed: " + err.Error()
	}
	return undoDone
}

// sortBatchView is a batch as the routes answer it, each to-do with its title now.
type sortBatchView struct {
	db.TodoSortBatch
	Titles map[string]string `json:"titles"`
}

func (s *Service) sortBatchViewOf(b db.TodoSortBatch) sortBatchView {
	v := sortBatchView{TodoSortBatch: b, Titles: map[string]string{}}
	if v.Items == nil {
		v.Items = []db.TodoSortItem{}
	}
	for _, it := range b.Items {
		if t, ok := s.todoNow(it.Todo); ok {
			v.Titles[it.Todo] = t.Title
		}
	}
	return v
}

// writeSortErr answers a sort's, an apply's or an undo's error.
func writeSortErr(w http.ResponseWriter, err error) {
	var item *sortItemError
	switch {
	case errors.Is(err, errTodosUnavailable), errors.Is(err, errKBNotOpen), errors.Is(err, errKBOff):
		apierr.Write(w, http.StatusServiceUnavailable, err)
	case errors.Is(err, errSortBatchMissing):
		apierr.WriteCode(w, http.StatusNotFound, "sort_batch_missing", i18n.ErrEnglish("assistant-todos.sort_batch_missing"))
	case errors.Is(err, errSortBatchUndone):
		apierr.WriteCode(w, http.StatusConflict, "sort_batch_undone", i18n.ErrEnglish("assistant-todos.sort_batch_undone"))
	case errors.As(err, &item):
		apierr.Write(w, http.StatusBadRequest, err)
	default:
		apierr.Write(w, http.StatusInternalServerError, err)
	}
}

func (s *Service) todoSortRoutes() {
	s.handle("POST /api/v1/todo-sort/suggest", s.postTodoSortSuggest) // todo_sort.go

	s.handle("POST /api/v1/todo-sort/apply", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("this device can only read"))
			return
		}
		var in struct {
			Items []sortMove `json:"items"`
		}
		if err := decodeBody(w, r, &in, 1<<20); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if len(in.Items) == 0 || len(in.Items) > sortMaxApply {
			apierr.Write(w, http.StatusBadRequest, fmt.Errorf("give 1 to %d items", sortMaxApply))
			return
		}
		res, err := s.applySort(r.Context(), in.Items, "user", sortKindSort)
		if err != nil {
			writeSortErr(w, err)
			return
		}
		apierr.WriteJSON(w, res)
	})

	s.handle("GET /api/v1/todo-sort/batches", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.sortBatches().TodoSortBatches(r.Context(), 20)
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		out := make([]sortBatchView, 0, len(list))
		for _, b := range list {
			out = append(out, s.sortBatchViewOf(b))
		}
		apierr.WriteJSON(w, map[string]any{"batches": out})
	})

	s.handle("POST /api/v1/todo-sort/batches/{id}/undo", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("this device can only read"))
			return
		}
		res, err := s.undoSort(r.Context(), r.PathValue("id"))
		if err != nil {
			writeSortErr(w, err)
			return
		}
		apierr.WriteJSON(w, res)
	})
}

// ---- the assistant's tool (prompts/mcp/todo/todo_sort) ----

func (s *Service) toolTodoSort(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string     `json:"action"`
		Items  []sortMove `json:"items"`
		Batch  string     `json:"batch"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}
	switch a.Action {
	case "suggest":
		return s.toolTodoSortSuggest(ctx)
	case "apply":
		if len(a.Items) == 0 || len(a.Items) > sortMaxApply {
			return "", fmt.Errorf("give 1 to %d items", sortMaxApply)
		}
		res, err := s.applySort(ctx, a.Items, "assistant", sortKindSort)
		return appliedText(res), err
	case "undo":
		if strings.TrimSpace(a.Batch) == "" {
			return s.sortBatchesText(ctx)
		}
		res, err := s.undoSort(ctx, a.Batch)
		if err != nil {
			return "", err
		}
		var w strings.Builder
		fmt.Fprintf(&w, "Undid batch %s: %d put back, %d skipped.", res.Batch, res.Undone, res.Skipped)
		for _, o := range res.Outcomes {
			if o.Outcome != undoDone {
				fmt.Fprintf(&w, "\n- %s %q: %s", o.Todo, o.Title, o.Outcome)
			}
		}
		return w.String(), nil
	}
	return "", fmt.Errorf("action must be suggest, apply or undo, not %q", a.Action)
}

func appliedText(r sortApplied) string {
	if r.Batch == "" {
		return fmt.Sprintf("Nothing moved: %d already there.", r.Unchanged)
	}
	text := fmt.Sprintf("Moved %d to-do(s) as batch %s (todo_sort undo with it puts them back)", r.Moved, r.Batch)
	if r.Unchanged > 0 {
		text += fmt.Sprintf("; %d already there", r.Unchanged)
	}
	if r.Failed > 0 {
		text += fmt.Sprintf("; %d could not be moved", r.Failed)
	}
	return text + "."
}

func (s *Service) sortBatchesText(ctx context.Context) (string, error) {
	list, err := s.sortBatches().TodoSortBatches(ctx, 10)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "There are no recent to-do moves to undo.", nil
	}
	var w strings.Builder
	w.WriteString("Recent to-do moves, newest first (undo one with its batch id):")
	loc := s.location(ctx)
	for _, b := range list {
		state := ""
		if b.UndoneAt != nil {
			state = " (undone)"
		}
		fmt.Fprintf(&w, "\n- %s · %s · %s, %d to-do(s)%s", b.ID, b.At.In(loc).Format("2006-01-02 15:04"), b.Kind, len(b.Items), state)
	}
	return w.String(), nil
}
