package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

const (
	maxBatchDecisionItems = 50
	maxBatchDecisionBody  = 512 << 10
)

// batchDecisionItem is one item in POST /api/v1/todos/decisions/batch (#15698).
// Each item must explicitly specify either an option or a custom reply; the server never auto-picks a default option.
type batchDecisionItem struct {
	TodoID string `json:"todo_id"`
	Option string `json:"option,omitempty"`
	Reply  string `json:"reply,omitempty"`
}

// batchDecisionRequest is the body of POST /api/v1/todos/decisions/batch (#15698).
type batchDecisionRequest struct {
	Items []batchDecisionItem `json:"items"`
}

// batchDecisionResult is the per-item outcome of POST /api/v1/todos/decisions/batch (#15698).
type batchDecisionResult struct {
	TodoID string `json:"todo_id"`
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// batchDecisionResponse is the answer of POST /api/v1/todos/decisions/batch (#15698).
type batchDecisionResponse struct {
	Results   []batchDecisionResult `json:"results"`
	Succeeded int                   `json:"succeeded"`
	Failed    int                   `json:"failed"`
}

// todoDecisionsBatchRoute registers POST /api/v1/todos/decisions/batch (#15698).
func (s *Service) todoDecisionsBatchRoute() {
	s.handle("POST /api/v1/todos/decisions/batch", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot modify todos"))
			return
		}
		if err := s.ensureTodosLoaded(r.Context()); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, errTodosUnavailable)
			return
		}
		var in batchDecisionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBatchDecisionBody)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		items, err := validateBatchDecisionItems(in.Items)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		res := s.resolveBatchDecisions(r.Context(), items)
		apierr.WriteJSON(w, res)
	})
}

func validateBatchDecisionItems(raw []batchDecisionItem) ([]batchDecisionItem, error) {
	if len(raw) == 0 || len(raw) > maxBatchDecisionItems {
		return nil, fmt.Errorf("items must contain between 1 and %d decision items", maxBatchDecisionItems)
	}
	seen := make(map[string]bool, len(raw))
	out := make([]batchDecisionItem, 0, len(raw))
	for i, it := range raw {
		todoID := strings.TrimSpace(it.TodoID)
		if todoID == "" {
			return nil, fmt.Errorf("items[%d]: todo_id cannot be empty", i)
		}
		if seen[todoID] {
			return nil, fmt.Errorf("items[%d]: duplicate todo_id %q", i, todoID)
		}
		seen[todoID] = true
		option := strings.TrimSpace(it.Option)
		reply := strings.TrimSpace(it.Reply)
		if option == "" && reply == "" {
			return nil, fmt.Errorf("items[%d]: option or reply must be specified", i)
		}
		if option != "" && reply != "" {
			return nil, fmt.Errorf("items[%d]: specify either option or reply, not both", i)
		}
		if utf8.RuneCountInString(reply) > decisionTextMax {
			return nil, fmt.Errorf("items[%d]: reply is longer than %d characters", i, decisionTextMax)
		}
		out = append(out, batchDecisionItem{
			TodoID: todoID,
			Option: option,
			Reply:  reply,
		})
	}
	return out, nil
}

func (s *Service) resolveBatchDecisions(ctx context.Context, items []batchDecisionItem) batchDecisionResponse {
	results := make([]batchDecisionResult, 0, len(items))
	succeeded, failed := 0, 0
	for _, item := range items {
		res := s.resolveBatchDecisionItem(ctx, item)
		results = append(results, res)
		if res.OK {
			succeeded++
		} else {
			failed++
		}
	}
	if succeeded > 0 {
		s.todosChanged(ctx)
	}
	return batchDecisionResponse{
		Results:   results,
		Succeeded: succeeded,
		Failed:    failed,
	}
}

func (s *Service) resolveBatchDecisionItem(ctx context.Context, item batchDecisionItem) batchDecisionResult {
	t, ok := s.todoNow(item.TodoID)
	if !ok {
		return batchDecisionResult{
			TodoID: item.TodoID,
			OK:     false,
			Status: http.StatusNotFound,
			Error:  "todo not found",
		}
	}
	if t.DoneAt != nil {
		return batchDecisionResult{
			TodoID: item.TodoID,
			OK:     false,
			Status: http.StatusConflict,
			Error:  db.ErrTodoCompleted.Error(),
		}
	}
	a, err := s.askRound(item.TodoID, "")
	if err != nil {
		return batchDecisionResult{
			TodoID: item.TodoID,
			OK:     false,
			Status: batchDecisionErrStatus(err),
			Error:  s.batchDecisionErrText(ctx, err),
		}
	}
	if a == nil {
		return batchDecisionResult{
			TodoID: item.TodoID,
			OK:     false,
			Status: http.StatusConflict,
			Error:  s.batchDecisionErrText(ctx, errAskStale),
		}
	}
	round := a.AskedAt.UTC().Format(timeKeyLayout)
	var result acceptResult
	if item.Option != "" {
		result, err = s.chooseOption(ctx, item.TodoID, round, item.Option)
	} else {
		result, err = s.answerDecisionText(ctx, item.TodoID, round, item.Reply)
	}
	if errors.Is(err, db.ErrTodoCompleted) && writeDelivered(err) == "none" {
		err = errAskStale
	}
	if err != nil {
		return batchDecisionResult{
			TodoID: item.TodoID,
			OK:     false,
			Status: batchDecisionErrStatus(err),
			Error:  s.batchDecisionErrText(ctx, err),
		}
	}
	if !result.taken {
		return batchDecisionResult{
			TodoID: item.TodoID,
			OK:     false,
			Status: http.StatusConflict,
			Error:  s.batchDecisionErrText(ctx, errAskStale),
		}
	}
	return batchDecisionResult{
		TodoID: item.TodoID,
		OK:     true,
	}
}

func batchDecisionErrStatus(err error) int {
	if writeDelivered(err) != "none" {
		return http.StatusBadGateway
	}
	switch {
	case errors.Is(err, errAskStale), errors.Is(err, db.ErrTodoCompleted):
		return http.StatusConflict
	case errors.Is(err, errDecisionOption):
		return http.StatusBadRequest
	case errors.Is(err, errAcceptStore):
		return http.StatusServiceUnavailable
	case errors.Is(err, db.ErrTodoNotFound):
		return http.StatusNotFound
	case nodeRefusal(err):
		return http.StatusLocked
	default:
		return http.StatusBadGateway
	}
}

func (s *Service) batchDecisionErrText(ctx context.Context, err error) string {
	return i18n.Localize(err, s.requestCtxLang(ctx))
}
