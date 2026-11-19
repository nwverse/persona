package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// What a to-do's write that failed did with the owner's words (todo_write_outcome.go, #10465): the shared cases of
// spec/fixtures/todo-writes replayed against the conversation route and the asks route, the accept route's answer in
// doubt, and how each kind of error reads. All names and texts are made up.

// writeOutcomeCase is a case of spec/fixtures/todo-writes.
type writeOutcomeCase struct {
	Name  string
	Input struct {
		Route  string
		What   string
		Answer json.RawMessage
	}
	Expected struct {
		Status    int
		Code      string
		Delivered string
	}
}

func writeOutcomeCases(t *testing.T) []writeOutcomeCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "fixtures", "todo-writes", "outcomes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int
		Cases   []writeOutcomeCase
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Version != 1 || len(doc.Cases) == 0 {
		t.Fatalf("spec/fixtures/todo-writes/outcomes.json: version %d, %d cases, %v", doc.Version, len(doc.Cases), err)
	}
	seen := map[string]bool{}
	for _, c := range doc.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("case name %q is empty or not unique", c.Name)
		}
		seen[c.Name] = true
	}
	return doc.Cases
}

// writeFailStore is a database whose writes after a to-do's words went out can be made to fail: the conversation's
// routed item, or the decision the words answer (InsertTodoDecision answers so for a to-do deleted meanwhile).
type writeFailStore struct {
	*db.DB
	failRouted, failDecision atomic.Bool
}

func (s *writeFailStore) AppendFeed(ctx context.Context, r db.FeedRow) error {
	if s.failRouted.Load() && r.Kind == feedRouted {
		return errors.New("the feed could not be written")
	}
	return s.DB.AppendFeed(ctx, r)
}

func (s *writeFailStore) InsertTodoDecision(ctx context.Context, r db.TodoDecisionRow) (bool, error) {
	if s.failDecision.Load() {
		return false, db.ErrTodoNotFound
	}
	return s.DB.InsertTodoDecision(ctx, r)
}

// writeScene sets up what (spec/fixtures/todo-writes/README.md "Scenes") for a write on route, and posts it with
// answer: the manager's reply.
func writeScene(t *testing.T, route, what string, answer json.RawMessage) *httptest.ResponseRecorder {
	t.Helper()
	st := &writeFailStore{DB: todoTestDB(t)}
	s, f, todo := newDecisionAnswerServiceOn(t, st)
	path, body := "/api/v1/assistant/conversations/conv1/route", `{"todo_id":"`+todo.ID+`","text":"Book the earlier train"}`
	a := todo.Agents[0]
	if route == "message" { // its Agent works on it
		f.setSessions(a.MachineID, proto.Session{SessionID: a.SessionID, MachineID: a.MachineID, Cwd: a.Cwd, Agent: "claude",
			Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now()})
	}
	if route == "answer" {
		waitsForReview(t, s, f, todo.ID, "Three ryokan fit the dates.")
		if _, err := reviewErr(s, todo.ID, ryokanDecision()); err != nil {
			t.Fatal(err)
		}
		round := viewRound(t, s, todo.ID, askDecisionPrefix)
		if what == "round_over" {
			at, _ := time.Parse(timeKeyLayout, round)
			round = at.Add(-time.Hour).Format(timeKeyLayout) // an earlier round's, from a screen that is behind
		}
		id := todo.ID
		if what == "no_such_todo" {
			id = "todo_gone"
		}
		path, body = "/api/v1/todos/"+id+"/asks/"+askDecisionPrefix+round+"/answer", string(answer)
	}
	stopped := func() { // the task's Agent stops: the node lists it read-only
		f.setSessions(a.MachineID, proto.Session{SessionID: a.SessionID, MachineID: a.MachineID, Cwd: a.Cwd, Agent: "claude",
			Status: proto.StatusOffline, State: proto.StateOffline, UpdatedAt: time.Now(),
			Capabilities: &proto.SessionCapabilities{Read: true, Terminal: true}, ReadOnlyReason: "stopped"})
	}
	switch what {
	case "taken", "round_over", "no_such_todo":
	case "no_agent":
		f.setSessions(a.MachineID)
	case "not_this_conversation":
		path = "/api/v1/assistant/conversations/conv_other/route"
	case "refused":
		failSends(f, &rpc.Error{Code: "input_draft", Message: "the input box has a draft",
			Data: json.RawMessage(`{"delivered":"none","draft":"half a line"}`)})
	case "not_sent":
		failSends(f, rpc.NotSent(errors.New("the link closed before the request went out")))
	case "in_doubt":
		failSends(f, context.DeadlineExceeded)
	case "node_not_sure":
		failSends(f, &rpc.Error{Message: "typing stopped part way", Data: json.RawMessage(`{"delivered":"unknown"}`)})
	case "start_in_doubt", "start_refused":
		stopped()
		f.mu.Lock()
		f.failStart = context.DeadlineExceeded
		if what == "start_refused" {
			f.failStart = rpc.NotSent(errors.New("launcher not found"))
		}
		f.mu.Unlock()
	case "typed_then_not_kept":
		st.failRouted.Store(true)
	case "typed_then_gone":
		st.failDecision.Store(true)
	default:
		t.Fatalf("no scene %q", what)
	}
	calls := func() (sends, starts int) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, c := range f.calls {
			switch c.Method {
			case DeviceSessionSend:
				sends++
			case DeviceSessionStart:
				starts++
			}
		}
		return sends, starts
	}
	sends, starts := calls()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
	s.waitWorkEvents() // do not let the next case's database truncate an event still in flight
	// The scene happened, not another refusal on the way: the manager called the node's send, or a new Agent's start,
	// or neither.
	nowSends, nowStarts := calls()
	start := what == "start_in_doubt" || what == "start_refused"
	neither := slices.Contains([]string{"no_agent", "not_this_conversation", "round_over", "no_such_todo"}, what)
	want := [2]bool{!start && !neither, start}
	if got := [2]bool{nowSends > sends, nowStarts > starts}; got != want {
		t.Fatalf("%s: sent %v, started %v; want sent %v, started %v: %d %s", what, got[0], got[1], want[0], want[1], rec.Code, rec.Body)
	}
	return rec
}

// Every case of spec/fixtures/todo-writes: the status, the code and what delivered says, and for words that may be
// with the Agent, that it is never retryable and its words say so in place of the refusal's (the refusal is the detail).
func TestTodoWriteOutcomeFixtures(t *testing.T) {
	for _, c := range writeOutcomeCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			rec := writeScene(t, c.Input.Route, c.Input.What, c.Input.Answer)
			var reply map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
				t.Fatalf("reply %d: %s", rec.Code, rec.Body)
			}
			got, _ := reply["delivered"].(string)
			if rec.Code != c.Expected.Status || got != c.Expected.Delivered || c.Expected.Code != "" && reply["code"] != c.Expected.Code {
				t.Fatalf("%d %s, want %d code %q delivered %q", rec.Code, rec.Body, c.Expected.Status, c.Expected.Code, c.Expected.Delivered)
			}
			if c.Expected.Code != codeSendInDoubt {
				return
			}
			keys := routeInDoubtKeys
			if c.Input.Route == "answer" {
				keys = answerInDoubtKeys
			}
			if reply["retryable"] != false || reply["error"] != i18n.T(i18n.En, keys[got]) || reply["detail"] == "" {
				t.Fatalf("in doubt: %s", rec.Body)
			}
		})
	}
}

// The accept route answers words that may be with the Agent as the asks route does (502 send_in_doubt), never
// "nothing was changed"; a send that typed nothing stays 502 accept_not_taken, now saying delivered "none", and a
// node's refusal 423. Either way the decision still waits: the button works again.
func TestAcceptRouteSaysWhatItTyped(t *testing.T) {
	for _, c := range []struct {
		name      string
		fail      error
		option    string
		status    int
		code      string
		delivered string
	}{
		{"the recommendation in doubt", context.DeadlineExceeded, "", http.StatusBadGateway, codeSendInDoubt, "unknown"},
		{"an option in doubt", rpc.ErrClosed, "Kaede", http.StatusBadGateway, codeSendInDoubt, "unknown"},
		{"the node not sure", &rpc.Error{Code: "peer_unconfirmed", Message: "the inbox read it",
			Data: json.RawMessage(`{"delivered":"unknown"}`)}, "Kaede", http.StatusBadGateway, codeSendInDoubt, "unknown"},
		{"never sent", rpc.NotSent(context.Canceled), "Kaede", http.StatusBadGateway, "accept_not_taken", "none"},
		{"refused by the node", &rpc.Error{Code: "dialog_open", Message: "a dialog is open"}, "", http.StatusLocked,
			"dialog_open", "none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, f, todo := newDecisionAnswerServiceOn(t, todoTestDB(t))
			waitsForReview(t, s, f, todo.ID, "Three ryokan fit the dates.")
			if _, err := reviewErr(s, todo.ID, ryokanDecision()); err != nil {
				t.Fatal(err)
			}
			failSends(f, c.fail)
			body := map[string]string{}
			if c.option != "" {
				body["option"] = c.option
			}
			rec := answerAsk(s, todo.ID, body)
			var reply map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || rec.Code != c.status || reply["code"] != c.code ||
				reply["delivered"] != c.delivered {
				t.Fatalf("%d %s, want %d %s delivered %s", rec.Code, rec.Body, c.status, c.code, c.delivered)
			}
			if c.code == codeSendInDoubt && reply["error"] != i18n.T(i18n.En, answerInDoubtKeys["unknown"]) {
				t.Fatalf("in doubt, in other words: %s", rec.Body)
			}
			if listedAsk(t, s, todo.ID) == nil {
				t.Fatal("the decision no longer waits")
			}
			s.waitWorkEvents()
		})
	}
}

// How each kind of failure reads: yes once the words went out and what followed failed, whatever that was; unknown
// for a send or a start whose answer never came (a deadline or a dropped link, in its words only too) or that the node
// is not sure of; none for the rest, a send that never left, an offline computer and a node's refusal included.
func TestWriteDeliveredReadsEachKind(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{wordsSent{errors.New("the feed could not be written")}, "yes"},
		{wordsSent{db.ErrTodoNotFound}, "yes"},
		{wordsSent{context.DeadlineExceeded}, "yes"},
		{context.DeadlineExceeded, "unknown"},
		{context.Canceled, "unknown"},
		{rpc.ErrClosed, "unknown"},
		{&rpc.Error{Message: rpc.ErrClosed.Error()}, "unknown"},
		{&rpc.Error{Message: context.DeadlineExceeded.Error()}, "unknown"}, // a deadline past the bus: its words only
		{io.ErrUnexpectedEOF, "unknown"},
		{rpc.NotSent(rpc.ErrClosed), "none"},
		{fmt.Errorf("box: %w", apierr.ErrOffline), "none"},
		{&rpc.Error{Code: "peer_unconfirmed", Data: json.RawMessage(`{"delivered":"unknown"}`)}, "unknown"},
		{rpc.NotSent(context.DeadlineExceeded), "none"},
		{&rpc.Error{Code: "input_draft", Data: json.RawMessage(`{"delivered":"none"}`)}, "none"},
		{machineOffline("box"), "none"},
		{agentUnavailable("assistant-tasks.route_no_agent"), "none"},
		{i18n.Err("assistant-tasks.route_starting"), "none"},
		{db.ErrTodoCompleted, "none"},
		{errAcceptStore, "none"},
		{fmt.Errorf("%w: %w", errAcceptStore, context.DeadlineExceeded), "none"}, // a save that timed out sent nothing
	} {
		if got := writeDelivered(c.err); got != c.want {
			t.Errorf("%v: %s, want %s", c.err, got, c.want)
		}
	}
}
