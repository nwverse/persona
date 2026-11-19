package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A to-do's relayed writes that keep an operation receipt (#10465, docs/protocol/protocol.md "Operation receipts"):
// the answer to one of its asks, which a notification action or a phone that lost the reply asks again with the same
// X-Persona-Op-Id, gets the first reply back instead of a second effect or a 409 for the round it already ended; a
// message to the to-do on its conversation's route is not typed to its Agent twice. The id is the header
// (withClientOp, whose digest covers the path and the body), never the body; a request without it goes through as
// before. The manager decides the answer; the hub only keeps the reply. With an op id both are relayed as a send
// (forwardSend): a write the link lost while the manager had it may have been taken, so it is 502
// assistant_in_doubt, kept and replayed, never assistant_offline, which says nothing reached a manager and keeps no
// receipt.
//
// While such a write is under way the hub says how it stands (todo_send_state, docs/SUPER_RULES.md 5): processing as
// soon as it has claimed the op id, before the manager has the write (a message may wait for its Agent's computer, or
// for a new Agent to start), then how the relay ended. Like message_send_state for a session's message it is
// transport only: it never says what the to-do or its Agent is doing.

const (
	// todoAnswerOp is the receipt kind of an answer to a to-do's ask.
	todoAnswerOp = "todo_answer"
	// todoRouteOp is the receipt kind of a message to a to-do on its conversation's route.
	todoRouteOp = "todo_route"
)

// todoReceiptRoutes are the relayed to-do routes with a receipt, and the receipt's kind.
var todoReceiptRoutes = map[string]string{
	"POST /api/v1/todos/{id}/asks/{ask}/answer":       todoAnswerOp,
	"POST /api/v1/assistant/conversations/{id}/route": todoRouteOp,
}

// todoRelay relays a to-do write with a receipt to the manager by resolves, as a send. A message on the conversation
// route without an op id, which is all an older app sends, goes as it always went.
func (h *Hub) todoRelay(kind string, by ownerOf) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.owner(w, r, by)
		if !ok {
			return
		}
		if kind == todoRouteOp && r.Header.Get("X-Persona-Op-Id") == "" {
			h.forwardHTTP(w, r, id)
			return
		}
		h.forwardSend(w, r, id)
	}
}

// withTodoReceipt gives next, a relayed to-do write, a receipt of kind under X-Persona-Op-Id. Once the receipt is
// claimed the relay no longer ends with the caller: a write the manager takes is recorded, and its reply kept, even
// when the phone stopped waiting. Only the claimed write says how it stands: a retry of its op id waits for the reply
// or gets it back (withClientOp) and sends nothing, so an older final state never follows a newer one.
func (h *Hub) withTodoReceipt(kind string, next http.HandlerFunc) http.HandlerFunc {
	return h.withClientOp(kind, func(w http.ResponseWriter, r *http.Request) {
		op := strings.ToLower(r.Header.Get("X-Persona-Op-Id"))
		if op == "" {
			next(w, r)
			return
		}
		r = r.WithContext(context.WithoutCancel(r.Context()))
		todo := todoOfWrite(kind, r)
		if todo == "" { // it names no to-do: the manager refuses it
			next(w, r)
			return
		}
		h.todoSendState(todo, op, proto.MessageSendProcessing)
		seen := &keptRecorder{ResponseWriter: w}
		state := proto.MessageSendUnknown // a relay that never answered may have been taken
		defer func() { h.todoSendState(todo, op, state) }()
		next(seen, r)
		state = todoSendOutcome(seen.status, seen.body)
	})
}

// todoOfWrite is the to-do a write under a receipt is about: the path's for an answer, the body's todo_id, read as the
// manager reads it, for a message on the conversation route ("" when it names none).
func todoOfWrite(kind string, r *http.Request) string {
	if kind != todoRouteOp {
		return r.PathValue("id")
	}
	raw, err := io.ReadAll(r.Body) // withClientOp has read it whole into memory already
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var in struct {
		TodoID string `json:"todo_id"`
	}
	if err != nil || json.Unmarshal(raw, &in) != nil {
		return ""
	}
	return in.TodoID
}

// todoSendOutcome is how a to-do write's relay ended, read as messageSendState reads a session message's: delivered on
// 200 (its Agent took the message, or the manager took the answer, one it recorded for the supervisor too, delivered
// "none" in a 200), unknown when the answer says it may have been taken (assistant_in_doubt), else failed: a refusal,
// which the same op id gets again and only a new one sends again.
func todoSendOutcome(status int, body []byte) string {
	var out struct {
		Delivered string `json:"delivered"`
	}
	_ = json.Unmarshal(body, &out)
	switch {
	case status == 0 || status == http.StatusOK:
		return proto.MessageSendDelivered
	case out.Delivered == sendUnknown:
		return proto.MessageSendUnknown
	}
	return proto.MessageSendFailed
}

// todoSendState tells the full devices that asked for todo_send_state (client_hello or ?features=) where the write
// under op id op for to-do todo stands. Outside the event log: a phone that missed it reads the HTTP answer, or asks
// the op id again.
func (h *Hub) todoSendState(todo, op, state string) {
	b, err := json.Marshal(proto.TodoSendState{Type: "todo_send_state", TodoID: todo, ClientOpID: op, State: state, At: time.Now().UTC()})
	if err == nil {
		h.toFullWanting(proto.FeatureTodoSendState, b)
	}
}
