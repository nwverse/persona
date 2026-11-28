package extmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
)

// The routes of the user's per-chat rules (#5278, chat_rules.go, docs/protocol/protocol.md "Connector API"), under
// /api/v1/connectors/sent/ with the ledger they are made from:
//
//	GET    /api/v1/connectors/sent/chat-rules        the rules, newest first
//	POST   /api/v1/connectors/sent/chat-rules        {row}: the rule for the chat a ledger row went to
//	DELETE /api/v1/connectors/sent/chat-rules/{id}   drop one, by the id the list (or a ledger row) gives
//
// A rule is made on the manager from the row's own words (connector.ChatRuleFor): the request names a row and nothing
// else, so no client can say what a rule allows.
//
// Who may: the list and the drop are for who may read the ledger (mayReadLedger, sent_recall_route.go): the user's own full
// devices and the owner on this machine. Dropping a rule only makes the gate ask more, so the owner may. Making one lets
// writes through without a card from then on, so it is for what the user does by hand on their own device
// (byUsersOwnHand): not the owner's hub token, an answer-only device, a pairing token or an extension's token, whatever
// its scopes. That is not a wall against anything that can pair: a pairing token is redeemed for a full device's token
// (POST /api/v1/auth/tokens), and a full device can add a device itself, so what stands between a script and a rule is who
// can make a pairing code or a token: the devices already trusted, and the owner on this machine (docs/protocol/protocol.md
// "Authentication"). Not in extwire.RouteScopes, so an extension's token is turned away before these handlers see it, and
// the handlers check again. Each rule made or dropped is recorded in the audit (connector_chat_rule_added / _removed),
// never the text of the row it came from.

// sentRowStore reads one row of the ledger.
type sentRowStore interface {
	SentAsUserByID(ctx context.Context, id int64) (db.SentAsUser, bool, error)
}

// chatRuleView is a rule as the app shows it. id is what DELETE takes: opaque and safe in a path.
type chatRuleView struct {
	ID        string    `json:"id"`
	Connector string    `json:"connector"`
	Chat      string    `json:"chat"` // a Lark conversation's id, owner/name#number, session:<session_key>, or account:<account_id>
	At        time.Time `json:"at,omitzero"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	MaxUses   int       `json:"max_uses,omitempty"`
	UsedCount int       `json:"used_count,omitempty"`
	Row       int64     `json:"row,omitempty"`     // the ledger row it was made from (the ledger is pruned: it may be gone)
	Summary   string    `json:"summary,omitempty"` // that row's one line
}

func viewOfChatRule(u connector.UserRule) chatRuleView {
	return chatRuleView{
		ID: chatRuleKey(u), Connector: u.Rule.Connector, Chat: u.Chat(), At: u.At,
		ExpiresAt: u.ExpiresAt, MaxUses: u.MaxUses, UsedCount: u.UsedCount,
		Row: u.Row, Summary: u.Summary,
	}
}

// chatRuleState is what a ledger row says of the rule it could give: can: one can be made from it; exists: one is in
// force for its chat (id is that rule's, for DELETE); reason: why none can be made, one of connector.NoChat… A client
// that does not know the field loses only the button.
type chatRuleState struct {
	Can    bool   `json:"can"`
	Exists bool   `json:"exists"`
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// chatRuleStates is the chat_rule of each row of a page (sentItems puts it on the row): what the row could give, and whether
// its chat has a rule already. A rule store that cannot be read says no row has one; the gate does not know them either
// then.
func (h *Manager) chatRuleStates(ctx context.Context, rows []db.SentAsUser) []chatRuleState {
	now := connector.UserRuleNow()
	have := map[string]connector.UserRule{}
	if rules, err := h.chatRules(ctx); err == nil {
		for _, u := range rules {
			if u.Active(now) {
				have[u.ID()] = u
			}
		}
	}
	commands := sync.OnceValue(func() []string { return h.extensions.gateCommands(connector.Lark) }) // read for the first row that needs it
	out := make([]chatRuleState, len(rows))
	for i, row := range rows {
		rule, err := chatRuleOfRowFor(row, commands)
		var no *connector.NoChatRuleError
		switch {
		case err == nil:
			out[i].Can = true
			if u, ok := have[rule.ID()]; ok {
				out[i].Exists, out[i].ID = true, chatRuleKey(u)
			} else if row.Connector == "club" {
				if accRule, accErr := connector.ClubAccountRuleFor(row.Action, row.Target); accErr == nil {
					if u, ok := have[accRule.ID()]; ok {
						out[i].Exists, out[i].ID = true, chatRuleKey(u)
					}
				}
			}
		case errors.As(err, &no):
			out[i].Reason = no.Code
		default:
			out[i].Reason = connector.NoChatUnknown
		}
	}
	return out
}

func (h *Manager) chatRuleRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/connectors/sent/chat-rules", func(w http.ResponseWriter, r *http.Request) {
		if !chatRuleAuth(w, r) {
			return
		}
		rules, err := h.chatRules(r.Context())
		if !chatRuleStoreOK(w, err) {
			return
		}
		out := make([]chatRuleView, 0, len(rules))
		for _, u := range slices.Backward(rules) {
			out = append(out, viewOfChatRule(u))
		}
		writeJSON(w, map[string]any{"rules": out, "most": chatRulesMost})
	})

	api.HandleFunc("POST /api/v1/connectors/sent/chat-rules", func(w http.ResponseWriter, r *http.Request) {
		if !byUsersOwnHand(r.Context()) {
			writeErr(w, http.StatusForbidden, errors.New("only one of the user's own devices with full rights makes a rule"))
			return
		}
		var body struct {
			Row        int64      `json:"row"`
			Scope      string     `json:"scope"`
			SessionKey string     `json:"session_key"`
			AccountID  string     `json:"account_id"`
			ExpiresAt  *time.Time `json:"expires_at"`
			TTLSeconds *int       `json:"ttl_seconds"`
			MaxUses    *int       `json:"max_uses"`
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
		if err != nil {
			writeBodyErr(w, err, err)
			return
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields() // a client cannot hand the manager a raw gate rule, so none that tries is taken as one
		var more json.RawMessage
		if err := dec.Decode(&body); err != nil || dec.Decode(&more) != io.EOF { // one object, nothing after it
			writeErr(w, http.StatusBadRequest, errors.New(`the body is {"row": <a ledger row's id>} and nothing else`))
			return
		}
		fromRow := body.Row >= 1 && body.SessionKey == "" && body.AccountID == ""
		directClub := body.Row == 0 && (body.SessionKey != "") != (body.AccountID != "")
		if !fromRow && !directClub {
			writeErr(w, http.StatusBadRequest, errors.New(`the body is {"row": <a ledger row's id>} and nothing else`))
			return
		}
		if body.Scope != "" && body.Scope != "session" && body.Scope != "account" {
			writeErr(w, http.StatusBadRequest, errors.New(`scope must be "session" or "account"`))
			return
		}
		now := connector.UserRuleNow().UTC()
		expiresAt, hasBounds, ok := parseChatRuleBounds(w, now, body.ExpiresAt, body.TTLSeconds, body.MaxUses)
		if !ok {
			return
		}

		var (
			rule connector.UserRule
			row  db.SentAsUser
		)
		if fromRow {
			st, _ := h.store.(sentRowStore)
			if st == nil {
				writeErrCode(w, http.StatusServiceUnavailable, "no_database", errors.New("no database: there is no ledger"))
				return
			}
			var found bool
			row, found, err = st.SentAsUserByID(r.Context(), body.Row)
			switch {
			case err != nil:
				writeErr(w, http.StatusInternalServerError, err)
				return
			case !found:
				writeErrCode(w, http.StatusNotFound, "not_found", fmt.Errorf("no row %d in the ledger (it is pruned after a while)", body.Row))
				return
			}
			switch body.Scope {
			case "account":
				if row.Connector != "club" {
					err = &connector.NoChatRuleError{Code: connector.NoChatUnknown, Reason: fmt.Sprintf("the gate has no account rule for %q", row.Connector)}
				} else {
					rule, err = connector.ClubAccountRuleFor(row.Action, row.Target)
				}
			default:
				rule, err = h.chatRuleOfRow(row)
				if err == nil && body.Scope == "session" && !strings.HasPrefix(rule.ID(), "club:session:") {
					err = &connector.NoChatRuleError{Code: connector.NoChatTarget, Reason: "the row does not name a club session_key"}
				}
			}
			var no *connector.NoChatRuleError
			if errors.As(err, &no) {
				writeCode(w, http.StatusConflict, "no_chat_rule", no.Reason, map[string]any{"reason": no.Code})
				return
			}
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			rule.At, rule.Row, rule.Summary = now, row.ID, cutRunes(row.Summary, sentSummaryMost)
		} else {
			if h.store == nil {
				writeErrCode(w, http.StatusServiceUnavailable, "no_database", errors.New("no database: the rules cannot be kept"))
				return
			}
			if body.SessionKey != "" {
				if body.Scope != "" && body.Scope != "session" {
					writeErr(w, http.StatusBadRequest, errors.New(`session_key requires scope "session"`))
					return
				}
				rule, err = connector.ClubSessionRule(body.SessionKey)
			} else {
				if body.Scope != "" && body.Scope != "account" {
					writeErr(w, http.StatusBadRequest, errors.New(`account_id requires scope "account"`))
					return
				}
				rule, err = connector.ClubAccountRule(body.AccountID)
			}
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			rule.At = now
		}

		if expiresAt != nil {
			rule.ExpiresAt = *expiresAt
		}
		if body.MaxUses != nil {
			rule.MaxUses = *body.MaxUses
		}
		saved, created, err := h.saveChatRule(r.Context(), rule, hasBounds || directClub)
		var no *connector.NoChatRuleError
		switch {
		case errors.As(err, &no):
			writeCode(w, http.StatusConflict, "no_chat_rule", no.Reason, map[string]any{"reason": no.Code})
		case errors.Is(err, errChatRulesFull):
			writeErrCode(w, http.StatusConflict, "too_many_rules", err)
		case !chatRuleStoreOK(w, err):
		default:
			if created { // asking for a chat that has a rule changes nothing, so records nothing
				detail := map[string]any{"chat": saved.Chat()}
				if body.Row > 0 {
					detail["row"] = body.Row
				}
				h.auditConnector(r.Context(), saved.Rule.Connector, auditConnectorChatRuleAdded, detail)
			}
			writeJSON(w, map[string]any{"rule": viewOfChatRule(saved), "created": created})
		}
	})

	updateRuleHandler := func(w http.ResponseWriter, r *http.Request) {
		if !byUsersOwnHand(r.Context()) {
			writeErr(w, http.StatusForbidden, errors.New("only one of the user's own devices with full rights updates a rule"))
			return
		}
		var body struct {
			ExpiresAt  *time.Time `json:"expires_at"`
			TTLSeconds *int       `json:"ttl_seconds"`
			MaxUses    *int       `json:"max_uses"`
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
		if err != nil {
			writeBodyErr(w, err, err)
			return
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var more json.RawMessage
		if err := dec.Decode(&body); err != nil || dec.Decode(&more) != io.EOF {
			writeErr(w, http.StatusBadRequest, errors.New(`the body must be a JSON object with expires_at, ttl_seconds or max_uses`))
			return
		}
		now := connector.UserRuleNow().UTC()
		expiresAt, hasBounds, ok := parseChatRuleBounds(w, now, body.ExpiresAt, body.TTLSeconds, body.MaxUses)
		if !ok {
			return
		}
		if !hasBounds {
			writeErr(w, http.StatusBadRequest, errors.New(`specify at least one of expires_at, ttl_seconds or max_uses`))
			return
		}
		rule, found, err := h.updateChatRuleKey(r.Context(), r.PathValue("id"), expiresAt, body.MaxUses)
		switch {
		case !chatRuleStoreOK(w, err):
		case !found:
			writeErrCode(w, http.StatusNotFound, "not_found", errors.New("no such rule"))
		default:
			detail := map[string]any{"chat": rule.Chat()}
			if rule.Row > 0 {
				detail["row"] = rule.Row
			}
			h.auditConnector(r.Context(), rule.Rule.Connector, auditConnectorChatRuleAdded, detail)
			writeJSON(w, map[string]any{"rule": viewOfChatRule(rule)})
		}
	}
	api.HandleFunc("PATCH /api/v1/connectors/sent/chat-rules/{id}", updateRuleHandler)
	api.HandleFunc("PUT /api/v1/connectors/sent/chat-rules/{id}", updateRuleHandler)

	api.HandleFunc("DELETE /api/v1/connectors/sent/chat-rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !chatRuleAuth(w, r) {
			return
		}
		rule, had, err := h.removeChatRuleKey(r.Context(), r.PathValue("id"))
		if had { // the rule is off in the gate or gone from the store: either is recorded, a refusal or an id that is none is not
			detail := map[string]any{"chat": rule.Chat()}
			if rule.Row > 0 {
				detail["row"] = rule.Row
			}
			h.auditConnector(r.Context(), rule.Rule.Connector, auditConnectorChatRuleRemoved, detail)
		}
		switch {
		case errors.Is(err, errRuleStillStored) && !errors.Is(err, errNoDatabase):
			// The rule is off in the gate but the store did not drop it, this time or the time before: said as it is, and
			// the removal can be asked again.
			writeErrCode(w, http.StatusInternalServerError, "store_failed", err)
		case !chatRuleStoreOK(w, err):
		case !had:
			writeErrCode(w, http.StatusNotFound, "not_found", errors.New("no such rule"))
		default:
			writeJSON(w, map[string]any{"ok": true})
		}
	})
}

func parseChatRuleBounds(w http.ResponseWriter, now time.Time, expiresAt *time.Time, ttlSeconds, maxUses *int) (*time.Time, bool, bool) {
	if expiresAt != nil && ttlSeconds != nil {
		writeErr(w, http.StatusBadRequest, errors.New("specify either expires_at or ttl_seconds, not both"))
		return nil, false, false
	}
	var resolved *time.Time
	hasBounds := false
	if ttlSeconds != nil {
		if *ttlSeconds < 1 {
			writeErr(w, http.StatusBadRequest, errors.New("ttl_seconds must be at least 1"))
			return nil, false, false
		}
		exp := now.Add(time.Duration(*ttlSeconds) * time.Second).UTC()
		resolved = &exp
		hasBounds = true
	}
	if expiresAt != nil {
		exp := expiresAt.UTC()
		if !exp.After(now) {
			writeErr(w, http.StatusBadRequest, errors.New("expires_at must be in the future"))
			return nil, false, false
		}
		resolved = &exp
		hasBounds = true
	}
	if maxUses != nil {
		if *maxUses < 1 {
			writeErr(w, http.StatusBadRequest, errors.New("max_uses must be at least 1"))
			return nil, false, false
		}
		hasBounds = true
	}
	return resolved, hasBounds, true
}

// chatRuleAuth lets only who may read the ledger through (mayReadLedger) and answers 403 to the rest: the list and the drop.
func chatRuleAuth(w http.ResponseWriter, r *http.Request) bool {
	if !mayReadLedger(r.Context()) {
		writeErr(w, http.StatusForbidden, errors.New("only a device with full rights changes what is asked about"))
		return false
	}
	return true
}

// chatRuleStoreOK answers for a store error (503 no_database, 500 otherwise) and says whether there was none.
func chatRuleStoreOK(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, errNoDatabase):
		writeErrCode(w, http.StatusServiceUnavailable, "no_database", errors.New("no database: the rules cannot be kept"))
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
	return false
}
