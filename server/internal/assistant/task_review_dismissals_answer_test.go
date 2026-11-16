package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func assertAnswerDismissal(t *testing.T, s *Service, st Store, id string, want taskReviewDismissal) {
	t.Helper()
	s.reviewDismissals.mu.Lock()
	got := s.reviewDismissals.byTodo[id]
	s.reviewDismissals.mu.Unlock()
	var saved map[string]taskReviewDismissal
	if _, err := st.Setting(context.Background(), taskReviewDismissalsKey, &saved); err != nil {
		t.Fatal(err)
	}
	if got != want || saved[id] != want {
		t.Fatalf("dismissal changed: memory %+v, stored %+v; want %+v", got, saved[id], want)
	}
}

func dismissalAnswerEnv(t *testing.T) (*todoEnv, *memStore, taskReviewDismissal) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Hour)
	st := &memStore{}
	e := dismissalEnv(t, st, at, true)
	feed := &planMemStore{memStore: st}
	e.s.store = func() Store { return feed }
	e.s.todos.todos["task"].Origin = "conversation"
	e.s.todos.todos["task"].Agents = []TodoAgent{{MachineID: "server", SessionID: "worker"}}
	e.f.sessions["server"] = []proto.Session{{MachineID: "server", SessionID: "worker", Status: proto.StatusIdle}}
	dismissReview(t, e, at)
	return e, st, e.s.reviewDismissals.byTodo["task"]
}

func TestTaskReviewDismissalInvalidAcceptanceKeepsIt(t *testing.T) {
	for _, kind := range []string{"missing ask", "zero ask", "accepted", "old card", "no recommendation", "reopened", "closed", "ended review", "ask read failure"} {
		t.Run(kind, func(t *testing.T) {
			e, st, before := dismissalAnswerEnv(t)
			a := e.s.accept.asks["task"]
			card := ""
			switch kind {
			case "missing ask":
				delete(e.s.accept.asks, "task")
			case "zero ask":
				a.AskedAt = time.Time{}
			case "accepted":
				a.AcceptedAt = time.Now()
			case "old card":
				a.MessageID, a.Messages, card = "current", []string{"current"}, "old"
			case "no recommendation":
				a.Recommend = ""
			case "reopened":
				at := a.AskedAt.Add(time.Second)
				e.s.todos.todos["task"].ReopenedAt = &at
			case "closed":
				at := time.Now()
				e.s.todos.todos["task"].DoneAt = &at
			case "ended review":
				e.s.todos.todos["task"].Supervision.State = reviewRework
			case "ask read failure":
				e.s.accept.loaded = false
				st.failKey(acceptanceKey, errors.New("ask read failed"))
			}
			result, err := e.s.acceptTry(context.Background(), "task", card)
			if result.taken || kind == "ask read failure" && !errors.Is(err, errAcceptStore) || kind != "ask read failure" && err != nil {
				t.Fatalf("invalid acceptance: %v, %v", result, err)
			}
			assertAnswerDismissal(t, e.s, st, "task", before)
			if len(sentToAgent(e.f)) != 0 {
				t.Fatal("invalid acceptance reached the agent")
			}
		})
	}
}

func TestTaskReviewDismissalInvalidAnswersKeepIt(t *testing.T) {
	for _, entry := range []string{"option", "owner response", "already told"} {
		for _, kind := range []string{"invalid value", "stale ask", "accepted", "closed", "ended review"} {
			t.Run(entry+"/"+kind, func(t *testing.T) {
				e, st, before := dismissalAnswerEnv(t)
				a := e.s.accept.asks["task"]
				asked, label, response := a.AskedAt.Format(timeKeyLayout), "Option B", respondExplainFirst
				switch kind {
				case "invalid value":
					label, response = "Not an option", "Not a response"
				case "stale ask":
					if entry == "already told" {
						at := a.AskedAt.Add(time.Second)
						e.s.todos.todos["task"].ReopenedAt = &at
					} else {
						asked = a.AskedAt.Add(-time.Second).Format(timeKeyLayout)
					}
				case "accepted":
					a.AcceptedAt = time.Now()
				case "closed":
					at := time.Now()
					e.s.todos.todos["task"].DoneAt = &at
				case "ended review":
					e.s.todos.todos["task"].Supervision.State = reviewRework
				}
				var ok bool
				var err error
				switch entry {
				case "option":
					var result acceptResult
					result, err = e.s.chooseOption(context.Background(), "task", asked, label)
					ok = result.taken
				case "owner response":
					ok, err = e.s.ownerCannotAnswer(context.Background(), "task", asked, response)
				case "already told":
					ok, err = e.s.decisionTold(context.Background(), "task", label, "m", "conv", "the owner's words")
				}
				wantOptionError := kind == "invalid value" && entry != "owner response"
				if ok || wantOptionError && !errors.Is(err, errDecisionOption) || !wantOptionError && err != nil {
					t.Fatalf("invalid answer: %v, %v", ok, err)
				}
				assertAnswerDismissal(t, e.s, st, "task", before)
			})
		}
	}
}

func dismissalAnswer(s *Service, entry string) (bool, error) {
	ctx := context.Background()
	switch entry {
	case "accept":
		result, err := s.acceptTry(ctx, "task", "")
		return result.taken, err
	case "recommended option":
		result, err := s.chooseOption(ctx, "task", "", "Option A")
		return result.taken, err
	case "alternative option":
		result, err := s.chooseOption(ctx, "task", "", "Option B")
		return result.taken, err
	case "owner response":
		return s.ownerCannotAnswer(ctx, "task", "", respondNotAsked)
	default:
		return s.decisionTold(ctx, "task", "Option B", "m", "conv", "the owner's words")
	}
}

func TestTaskReviewDismissalAnswerDelivery(t *testing.T) {
	for _, entry := range []string{"accept", "recommended option", "alternative option", "owner response", "already told"} {
		for _, failure := range []string{"", "dismissal read", "dismissal write", "delivery"} {
			if failure == "delivery" && (entry == "owner response" || entry == "already told") {
				continue // These record the answer without delivering it to the task agent.
			}
			t.Run(entry+"/"+failure, func(t *testing.T) {
				e, st, before := dismissalAnswerEnv(t)
				switch failure {
				case "dismissal read":
					e.s.reviewDismissals.loaded = false
					st.failKey(taskReviewDismissalsKey, errors.New("read failed"))
				case "dismissal write":
					st.failPut = errors.New("write failed")
				case "delivery":
					e.f.failSend = errors.New("send refused")
				}
				ok, err := dismissalAnswer(e.s, entry)
				if failure != "" {
					if ok || err == nil || failure != "delivery" && !errors.Is(err, errAcceptStore) {
						t.Fatalf("failed answer: %v, %v", ok, err)
					}
					st.failKey(taskReviewDismissalsKey, nil)
					st.failPut = nil
					assertAnswerDismissal(t, e.s, st, "task", before)
					if len(sentToAgent(e.f)) != 0 {
						t.Fatal("a failed answer was delivered")
					}
					if a, _ := e.s.askCopy("task"); !a.AcceptedAt.IsZero() {
						t.Fatal("failed answer accepted the ask")
					}
					return
				}
				if !ok || err != nil {
					t.Fatalf("valid answer: %v, %v", ok, err)
				}
				before.Undone = true
				assertAnswerDismissal(t, e.s, st, "task", before)
				sent := len(sentToAgent(e.f))
				if ok, err := dismissalAnswer(e.s, entry); ok || err != nil || len(sentToAgent(e.f)) != sent {
					t.Fatalf("answer replay: %v, %v", ok, err)
				}
			})
		}
	}
}

func TestTaskReviewDismissalNoAgentHandoffUndoesIt(t *testing.T) {
	for _, option := range []string{"先灰度 10%", "直接全量"} { // i18n-ignore: the shared rollout decision's option labels
		t.Run(option, func(t *testing.T) {
			database := todoTestDB(t)
			s, f, todo := newSupervisedServiceOn(t, database)
			waitsForReview(t, s, f, todo.ID, "Choose a rollout plan.")
			if _, err := reviewErr(s, todo.ID, rolloutDecision()); err != nil {
				t.Fatal(err)
			}
			review := currentDecisionReview(todoNow(s, todo.ID))
			if _, err := s.changeDecisionDismissal(context.Background(), todo.ID, review.ReviewAt, "", false); err != nil {
				t.Fatal(err)
			}
			before := s.reviewDismissals.byTodo[todo.ID]
			f.setSessions("box")
			w := answerAsk(s, todo.ID, map[string]string{"option": option})
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK || body["delivered"] != "none" {
				t.Fatalf("supervisor handoff: %d %s (%v)", w.Code, w.Body, err)
			}
			before.Undone = true
			assertAnswerDismissal(t, s, database, todo.ID, before)
			rows, err := database.ListTodoDecisions(context.Background(), todo.ID, true)
			if err != nil || len(rows) != 1 || rows[0].Choice != option {
				t.Fatalf("recorded choice: %+v, %v", rows, err)
			}
			if w := answerAsk(s, todo.ID, map[string]string{"option": option}); w.Code != http.StatusConflict {
				t.Fatalf("handoff replay: %d %s", w.Code, w.Body)
			}
			key := "undelivered|" + askOf(s, todo.ID).AskedAt.UTC().Format(time.RFC3339Nano)
			waitFor(t, "the supervisor handoff", func() bool {
				s.supervise.mu.Lock()
				defer s.supervise.mu.Unlock()
				return s.supervise.sent[todo.ID+"|"+sigIdle] == key
			})
			s.waitWorkEvents()
		})
	}
}

type dismissalAnswerWriteFails struct {
	Store
	key        string
	onlyUndone bool
}

func (st *dismissalAnswerWriteFails) PutSetting(ctx context.Context, key string, value any) error {
	if key == st.key {
		if !st.onlyUndone || value.(map[string]taskReviewDismissal)["task"].Undone {
			return errors.New("test write refused")
		}
	}
	return st.Store.PutSetting(ctx, key, value)
}

func TestTaskReviewDismissalAcceptanceSaveFailureKeepsIt(t *testing.T) {
	e, st, before := dismissalAnswerEnv(t)
	base := e.s.store()
	e.s.store = func() Store { return &dismissalAnswerWriteFails{Store: base, key: acceptanceKey} }
	if result, err := e.s.acceptTry(context.Background(), "task", ""); result.taken || !errors.Is(err, errAcceptStore) {
		t.Fatalf("unsaved acceptance: %v, %v", result, err)
	}
	assertAnswerDismissal(t, e.s, st, "task", before)
	if len(sentToAgent(e.f)) != 0 {
		t.Fatal("unsaved acceptance was delivered")
	}
}

func TestTaskReviewDismissalCompletionWriteFailureDoesNotResend(t *testing.T) {
	for _, entry := range []string{"accept", "recommended option", "alternative option", "owner response", "already told"} {
		t.Run(entry, func(t *testing.T) {
			e, st, before := dismissalAnswerEnv(t)
			base := e.s.store()
			e.s.store = func() Store {
				return &dismissalAnswerWriteFails{Store: base, key: taskReviewDismissalsKey, onlyUndone: true}
			}
			if ok, err := dismissalAnswer(e.s, entry); !ok || err != nil {
				t.Fatalf("already-delivered answer became retryable: %v, %v", ok, err)
			}
			assertAnswerDismissal(t, e.s, st, "task", before)
			sent := len(sentToAgent(e.f))
			if ok, err := dismissalAnswer(e.s, entry); ok || err != nil || len(sentToAgent(e.f)) != sent {
				t.Fatalf("answer replay: %v, %v", ok, err)
			}
		})
	}
}
