package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/entries"
	"github.com/nwverse/persona/server/internal/nodeclient"
)

type entriesStore interface {
	AppendAssistantEntry(context.Context, entries.Entry) (entries.Entry, error)
	ListAssistantEntries(context.Context, entries.Params, bool) (entries.Result, error)
	AssistantEntry(context.Context, string) (entries.Entry, error)
}

func (s *Service) entriesDB() entriesStore {
	if s.store == nil {
		return nil
	}
	st, _ := s.store().(entriesStore)
	return st
}

// localEntriesReader proves physical co-location through the same-user node
// socket. An old/offline node or an unknown owner machine never grants access.
func (s *Service) localEntriesReader(ctx context.Context) bool {
	owner := s.settings(ctx).MachineID
	if owner == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	dial := s.entriesDial
	if dial == nil {
		dial = nodeclient.DialInterruptible
	}
	raw, err := dial(ctx, nodeclient.Request{Kind: "ping"})
	var pong struct {
		Machine string `json:"machine_id"`
		OK      bool   `json:"ok"`
	}
	return err == nil && json.Unmarshal(raw, &pong) == nil && pong.OK && pong.Machine == owner
}

func (s *Service) listEntries(ctx context.Context, p entries.Params, personal bool) (entries.Result, error) {
	if err := p.Validate(); err != nil {
		return entries.Result{}, conversationsInvalid(assistantwire.MethodAssistantEntriesList, err.Error())
	}
	st := s.entriesDB()
	if st == nil {
		return entries.Result{}, errors.New("entries require the manager database")
	}
	return st.ListAssistantEntries(ctx, p, personal)
}

func (s *Service) entriesRoutes() {
	s.handle("GET /api/v1/assistant/entries", func(w http.ResponseWriter, r *http.Request) {
		p, err := entriesQuery(r)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		// HTTP is the UI/metrics read surface. A bearer extension credential carries
		// no physical instance identity; those readers use the local RPC for personal data.
		personal := r.Header.Get(RoleHeader) != roleExtension
		out, err := s.listEntries(r.Context(), p, personal)
		if err != nil {
			apierr.Call(w, err)
			return
		}
		apierr.WriteJSON(w, out)
	})
	s.handle("POST /api/v1/assistant/entries/{id}/wrong", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("only the owner can mark an entry wrong"))
			return
		}
		e, err := s.markEntryWrong(r.Context(), r.PathValue("id"))
		if errors.Is(err, db.ErrEntryNotFound) {
			apierr.Write(w, http.StatusNotFound, err)
			return
		}
		if errors.Is(err, db.ErrEntrySuperseded) {
			apierr.Write(w, http.StatusConflict, err)
			return
		}
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"entry": e})
	})
}

func entriesQuery(r *http.Request) (entries.Params, error) {
	var p entries.Params
	q := r.URL.Query()
	for k, values := range q {
		if len(values) != 1 {
			return p, errors.New("ledger parameters must occur once")
		}
		v := values[0]
		switch k {
		case "after":
			p.After = v
		case "limit":
			n, err := strconv.Atoi(v)
			if err != nil {
				return p, errors.New("invalid limit")
			}
			p.Limit = n
		case "by":
			p.By = v
		case "trust":
			p.Trust = v
		case "status":
			p.Status = v
		case "tag":
			p.Tag = v
		case "type":
			p.Type = v
		case "scope":
			if err := strictParams("scope", json.RawMessage(v), &p.Scope); err != nil {
				return p, err
			}
		default:
			return p, errors.New("unknown ledger parameter")
		}
	}
	return p, p.Validate()
}

// A wrong mark is an immutable copy pointing to its predecessor. The event names
// the rejected predecessor as well as the new record so consumers retain both.
func (s *Service) markEntryWrong(ctx context.Context, id string) (entries.Entry, error) {
	st := s.entriesDB()
	if st == nil {
		return entries.Entry{}, errors.New("entries require the manager database")
	}
	e, err := st.AssistantEntry(ctx, id)
	if err != nil {
		return e, err
	}
	e.ID = "entry_wrong_" + id
	e.Supersedes = id
	e.Status = "wrong"
	e.At = time.Time{}
	e, err = st.AppendAssistantEntry(ctx, e)
	if err != nil {
		return e, err
	}
	if publisher, ok := s.hub.(entriesEventPublisher); ok && (!e.Scope.Personal || s.localEntriesReader(ctx)) {
		if err := publisher.EntriesEvent(assistantwire.EventAssistantEntriesChanged, assistantwire.AssistantEntriesChanged{ID: id, RecordID: e.ID, Status: "wrong"}); err != nil {
			s.logf("entries: change notification could not be delivered")
		}
	}
	return e, nil
}

type entriesEventPublisher interface{ EntriesEvent(string, any) error }

func (b *BusHub) EntriesEvent(event string, params any) error { return b.notifyHub(event, params) }
