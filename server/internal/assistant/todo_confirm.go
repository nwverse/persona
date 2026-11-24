package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
)

// Confirmed matters: the owner pressed Got it on a concern the main conversation closed with concern_report. Only that
// press counts: opening a to-do marks it seen (todo_seen.go) and never confirms it; a pass made by the hub or a
// supervisor never confirms either. The mark is the to-do's confirmed_at, kept here so every device agrees. Not
// activity: updated_at stays. A concern counts as confirmed when confirmed_at is at or after its latest report
// (header_card.go), so reopening it and reporting again makes it wait again.

// confirmFinished marks todo id confirmed now, or with confirmed false takes the confirm off (Undo).
func (s *Service) confirmFinished(ctx context.Context, id string, confirmed bool) (Todo, error) {
	var at *time.Time
	if confirmed {
		now := time.Now().UTC()
		at = &now
	}
	return s.setConfirmed(ctx, id, at)
}

// setConfirmed writes todo id's confirmed_at (nil: none).
func (s *Service) setConfirmed(ctx context.Context, id string, at *time.Time) (Todo, error) {
	s.todos.writing.Lock()
	cur, ok := s.todoNow(id) // under the lock: every earlier write is in it
	if !ok {
		s.todos.writing.Unlock()
		return Todo{}, db.ErrTodoNotFound
	}
	if st := s.todoDB(); st != nil {
		stored, err := st.ConfirmTodo(ctx, id, at)
		if err != nil {
			s.todos.writing.Unlock()
			return Todo{}, err
		}
		at = stored.ConfirmedAt // as the store keeps it
	}
	// Only that column was written, so only that field changes here: nothing else of the to-do moves with a confirm.
	row := todoRowOf(cur)
	row.ConfirmedAt = at
	out, _, applied := s.setTodoRow(id, row, nil)
	s.todos.writing.Unlock()
	if !applied {
		return Todo{}, db.ErrTodoNotFound // deleted meanwhile
	}
	s.todosChanged(ctx)
	return out, nil
}

// answerAskOnConfirm passes finished todo id's open ask, the Accept / Got it a client shows, as its button would: a
// confirmed task no longer waits there either. A decision, or an ask already passed, is left.
func (s *Service) answerAskOnConfirm(ctx context.Context, id string) {
	t, ok := s.todoNow(id)
	if !ok || t.DoneAt == nil {
		return
	}
	a, err := s.acceptAskLocked(id)
	if err != nil || a == nil {
		return
	}
	s.accept.mu.Lock()
	open := !a.AskedAt.IsZero() && a.AcceptedAt.IsZero() && !decideAsk(a)
	s.accept.mu.Unlock()
	if !open {
		return
	}
	if _, err := s.acceptTryAs(ctx, id, "", false); err != nil {
		s.logf("task %s: confirm: pass its ask: %v", id, err)
	}
}

// confirmOnAccept confirms todo id after the owner passed its ask (acceptTry), when it is finished and not confirmed
// since. Only the owner's pass comes here: a pass the hub or a supervisor made (acceptTryAs) confirms nothing.
func (s *Service) confirmOnAccept(ctx context.Context, id string) {
	t, ok := s.todoNow(id)
	if !ok || t.DoneAt == nil || confirmedSinceReport(t) {
		return
	}
	now := time.Now().UTC()
	if _, err := s.setConfirmed(ctx, id, &now); err != nil {
		s.logf("task %s: accept: confirm: %v", id, err)
	}
}

func (s *Service) todoConfirmRoutes() {
	register := func(method string, confirmed bool) {
		// PUT /api/v1/todos/{id}/confirm: the owner pressed Got it; DELETE: Undo. On an open concern the body is
		// {"report": "…"}, the conclusion its card showed; without it 400.
		s.handle(method+" /api/v1/todos/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
			if !fullRole(r) {
				apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot modify todos"))
				return
			}
			if err := s.ensureTodosLoaded(r.Context()); err != nil {
				apierr.Write(w, http.StatusServiceUnavailable, errTodosUnavailable)
				return
			}
			var t Todo
			var err error
			if cur, ok := s.todoNow(r.PathValue("id")); ok && cur.isConcern() && confirmed {
				var body struct {
					Report string `json:"report"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				t, err = s.confirmMatter(r.Context(), cur.ID, body.Report)
				if errors.Is(err, errEmptyReport) {
					apierr.Write(w, http.StatusBadRequest, errors.New("an open concern is put away with the conclusion it showed: give report"))
					return
				}
			} else {
				t, err = s.confirmFinished(r.Context(), r.PathValue("id"), confirmed)
			}
			if err == nil && confirmed {
				s.answerAskOnConfirm(r.Context(), t.ID)
				if now, ok := s.todoNow(t.ID); ok {
					t = now
				}
			}
			if errors.Is(err, db.ErrTodoNotFound) {
				apierr.Write(w, http.StatusNotFound, errors.New("todo not found"))
				return
			}
			if err != nil {
				apierr.Write(w, http.StatusInternalServerError, err)
				return
			}
			apierr.WriteJSON(w, map[string]any{"todo": t})
		})
	}
	register("PUT", true)
	register("DELETE", false)
}
