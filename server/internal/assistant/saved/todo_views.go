package saved

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

const (
	TodoViewsKey     = "assistant.saved_todo_views"
	todoViewVersion  = 1
	todoViewsMax     = 50
	todoViewNameMax  = 64
	todoViewTextMax  = 1024
	todoViewIDPrefix = "view_"
)

// A saved view holds display criteria only. Applying one is a client action
// and does not change tasks, scheduling, or the manager's status counts.
type todoViewCriteria struct {
	Text        string `json:"text"`
	DueWindow   string `json:"due_window"`
	SectionSort string `json:"section_sort"`
}

type savedTodoView struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Criteria  todoViewCriteria `json:"criteria"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type todoViewsDocument struct {
	SchemaVersion int             `json:"schema_version"`
	Revision      int64           `json:"revision"`
	Views         []savedTodoView `json:"views"`
}

type todoViewsResponse struct {
	SchemaVersion int             `json:"schema_version"`
	Revision      string          `json:"revision"`
	Views         []savedTodoView `json:"views"`
	CanWrite      bool            `json:"can_write"`
}

type todoViewMutation struct {
	revision int64
	op, id   string
	name     string
	criteria todoViewCriteria
}

type todoViewBook struct{ h Host }

func validTodoViewID(id string) bool {
	digits, prefixed := strings.CutPrefix(id, todoViewIDPrefix)
	return prefixed && len(digits) == 24 && strings.Trim(digits, "0123456789abcdef") == ""
}

func normalizeTodoView(name string, criteria todoViewCriteria) (string, todoViewCriteria, error) {
	name, criteria.Text = trimSavedInput(name), trimSavedInput(criteria.Text)
	if name == "" || utf8.RuneCountInString(name) > todoViewNameMax || !savedSearchText(name) ||
		len(criteria.Text) > todoViewTextMax || !savedSearchText(criteria.Text) {
		return name, criteria, savedSearchInvalid
	}
	switch criteria.DueWindow {
	case "all", "no_due", "today", "week", "month":
	default:
		return name, criteria, savedSearchInvalid
	}
	switch criteria.SectionSort {
	case "default", "due", "priority":
	default:
		return name, criteria, savedSearchInvalid
	}
	return name, criteria, nil
}

func decodeTodoViewCriteria(raw json.RawMessage) (todoViewCriteria, error) {
	var criteria todoViewCriteria
	fields, err := savedSearchObject(raw, "text", "due_window", "section_sort")
	if err != nil {
		return criteria, err
	}
	for _, f := range []struct {
		name  string
		value *string
	}{{"text", &criteria.Text}, {"due_window", &criteria.DueWindow}, {"section_sort", &criteria.SectionSort}} {
		*f.value, err = savedSearchString(fields, f.name, true)
		if err != nil {
			return criteria, err
		}
	}
	return criteria, nil
}

func parseTodoViewMutation(raw []byte) (todoViewMutation, error) {
	var m todoViewMutation
	fields, err := savedSearchObject(raw, "revision", "op", "id", "name", "criteria")
	if err != nil {
		return m, err
	}
	m.revision, err = savedRevision(fields)
	if err != nil {
		return m, err
	}
	m.op, err = savedSearchString(fields, "op", true)
	if err != nil {
		return m, err
	}
	switch m.op {
	case "add":
		if len(fields) != 4 || fields["id"] != nil {
			return m, savedSearchInvalid
		}
	case "update", "delete":
		want := 5
		if m.op == "delete" {
			want = 3
		}
		m.id, err = savedSearchString(fields, "id", true)
		if len(fields) != want || err != nil || !validTodoViewID(m.id) {
			return m, savedSearchInvalid
		}
		if m.op == "delete" {
			return m, nil
		}
	default:
		return m, savedSearchInvalid
	}
	m.name, err = savedSearchString(fields, "name", true)
	if err != nil {
		return m, err
	}
	m.criteria, err = decodeTodoViewCriteria(fields["criteria"])
	if err != nil {
		return m, err
	}
	m.name, m.criteria, err = normalizeTodoView(m.name, m.criteria)
	return m, err
}

func readTodoViews(ctx context.Context, st revisionedSettings) (todoViewsDocument, error) {
	doc := todoViewsDocument{SchemaVersion: todoViewVersion, Views: []savedTodoView{}}
	var raw json.RawMessage
	exists, err := st.Setting(ctx, TodoViewsKey, &raw)
	if err != nil {
		return doc, savedSearchStoreUnavailable
	}
	if !exists {
		return doc, nil
	}
	fields, err := savedSearchObject(raw, "schema_version", "revision", "views")
	if err != nil || len(fields) != 3 || string(fields["schema_version"]) == "null" ||
		string(fields["revision"]) == "null" || json.Unmarshal(raw, &doc) != nil ||
		doc.SchemaVersion != todoViewVersion || doc.Revision < 0 || doc.Views == nil || len(doc.Views) > todoViewsMax {
		return doc, savedSearchStoreUnavailable
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(fields["views"], &rows); err != nil {
		return doc, savedSearchStoreUnavailable
	}
	for i, item := range doc.Views {
		viewFields, err := savedSearchObject(rows[i], "id", "name", "criteria", "created_at", "updated_at")
		if err != nil || len(viewFields) != 5 {
			return doc, savedSearchStoreUnavailable
		}
		criteria, err := decodeTodoViewCriteria(viewFields["criteria"])
		if err != nil {
			return doc, savedSearchStoreUnavailable
		}
		name, criteria, err := normalizeTodoView(item.Name, criteria)
		if err != nil || name != item.Name || criteria != item.Criteria || !validTodoViewID(item.ID) ||
			item.CreatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
			return doc, savedSearchStoreUnavailable
		}
		if slices.ContainsFunc(doc.Views[:i], func(prev savedTodoView) bool {
			return prev.ID == item.ID || strings.EqualFold(prev.Name, item.Name)
		}) {
			return doc, savedSearchStoreUnavailable
		}
	}
	return doc, nil
}

func changeTodoViews(before todoViewsDocument, mutation todoViewMutation, now time.Time) (todoViewsDocument, error) {
	if before.Revision != mutation.revision {
		return before, savedSearchConflict
	}
	if before.Revision == math.MaxInt64 {
		return before, savedSearchInvalid
	}
	i := slices.IndexFunc(before.Views, func(view savedTodoView) bool { return view.ID == mutation.id })
	if mutation.op != "add" && i < 0 {
		return before, savedSearchMissing
	}
	if mutation.op != "delete" && slices.ContainsFunc(before.Views, func(view savedTodoView) bool {
		return view.ID != mutation.id && strings.EqualFold(view.Name, mutation.name)
	}) {
		return before, savedSearchDuplicate
	}
	next := before
	next.Views = slices.Clone(before.Views)
	switch mutation.op {
	case "add":
		if len(next.Views) >= todoViewsMax {
			return before, savedSearchLimit
		}
		id := todoViewIDPrefix + randomHex(12)
		for slices.ContainsFunc(next.Views, func(view savedTodoView) bool { return view.ID == id }) {
			id = todoViewIDPrefix + randomHex(12)
		}
		next.Views = append(next.Views, savedTodoView{
			ID: id, Name: mutation.name, Criteria: mutation.criteria, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
		})
	case "update":
		view := &next.Views[i]
		if view.Name == mutation.name && view.Criteria == mutation.criteria {
			return before, nil
		}
		view.Name, view.Criteria = mutation.name, mutation.criteria
		if now.After(view.UpdatedAt) {
			view.UpdatedAt = now.UTC()
		}
	case "delete":
		next.Views = slices.Delete(next.Views, i, i+1)
	default:
		return before, savedSearchInvalid
	}
	next.Revision++
	return next, nil
}

func (b *todoViewBook) write(ctx context.Context, st revisionedSettings, m todoViewMutation) (todoViewsDocument, error) {
	before, err := readTodoViews(ctx, st)
	if err != nil {
		return before, err
	}
	next, err := changeTodoViews(before, m, time.Now())
	if err != nil || next.Revision == before.Revision {
		return before, err
	}
	ok, err := st.CompareSetting(ctx, TodoViewsKey, before.Revision, next)
	switch {
	case err != nil:
		return before, savedSearchStoreUnavailable
	case !ok:
		return before, savedSearchConflict
	}
	b.h.Event(ctx, map[string]any{"type": "saved_todo_views", "revision": strconv.FormatInt(next.Revision, 10)})
	return next, nil
}

func writeTodoViewError(w http.ResponseWriter, err error) {
	status, code, key := http.StatusServiceUnavailable, "storage_unavailable", "storage"
	switch err {
	case savedSearchUnsupported:
		code, key = "not_supported", "unavailable"
	case savedSearchInvalid:
		status, code, key = http.StatusBadRequest, "invalid_request", "invalid"
	case savedSearchConflict:
		status, code, key = http.StatusConflict, "conflict", "conflict"
	case savedSearchDuplicate:
		status, code, key = http.StatusConflict, "duplicate_name", "duplicate"
	case savedSearchLimit:
		status, code, key = http.StatusBadRequest, "limit_reached", "limit"
	case savedSearchMissing:
		status, code, key = http.StatusNotFound, "view_not_found", "missing"
	}
	apierr.WriteCode(w, status, code, i18n.Err("assistant-saved-todo-views."+key))
}

func (b *todoViewBook) serve(w http.ResponseWriter, r *http.Request) {
	if !b.h.Full(r) {
		apierr.WriteCode(w, http.StatusForbidden, "forbidden", i18n.Err("assistant-saved-todo-views.owner_only"))
		return
	}
	st, ok := b.h.Store().(revisionedSettings)
	if !ok || st == nil {
		writeTodoViewError(w, savedSearchUnsupported)
		return
	}
	var doc todoViewsDocument
	var err error
	if r.Method == http.MethodPost {
		raw, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if readErr != nil {
			b.h.BodyErr(w, readErr, i18n.Err("assistant-saved-todo-views.invalid"))
			return
		}
		mutation, parseErr := parseTodoViewMutation(raw)
		if parseErr != nil {
			writeTodoViewError(w, parseErr)
			return
		}
		doc, err = b.write(r.Context(), st, mutation)
	} else {
		doc, err = readTodoViews(r.Context(), st)
	}
	if err != nil {
		writeTodoViewError(w, err)
		return
	}
	apierr.WriteJSON(w, todoViewsResponse{
		SchemaVersion: todoViewVersion, Revision: strconv.FormatInt(doc.Revision, 10), Views: doc.Views, CanWrite: true,
	})
}

func (b *todoViewBook) routes() {
	b.h.Handle("GET "+assistantwire.SavedTodoViewsPath, b.serve)
	b.h.Handle("POST "+assistantwire.SavedTodoViewsPath, b.serve)
}
