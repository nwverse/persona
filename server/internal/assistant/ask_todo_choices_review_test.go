package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

func TestExpiredQuestionIrreversibleDeadlineRemindsButStillNeedsOwnerDecision(t *testing.T) {
	database := todoTestDB(t)
	e := newAskTodoEnv(t, database)
	ix, _ := e.expireWith(strings.Replace(strings.Replace(askDeadlineArgsJSON, "%s", "6h", 1), "%s", "false", 1))
	todo := e.onlyAskTodo()
	e.s.askTodoDue(context.Background(), todo.ID, false, time.Now().Add(7*time.Hour))
	a, _ := e.s.askTodoOf(context.Background(), todo.ID)
	if a.DueAt.IsZero() || a.ByDefault || !a.AnsweredAt.IsZero() {
		t.Fatalf("an irreversible deadline must only remind, not answer: %+v", a)
	}
	again := newAskTodoEnv(t, database)
	view := expiredQuestionPayload(again, todo.ID)
	if view.Ask == nil || view.DoneAt != nil || len(view.Ask.Questions[0].Options) != 2 {
		t.Fatalf("the reminded question must still offer its original choices after reload: %+v", view)
	}
	if code := answerExpiredQuestion(again, todo.ID, ix.CreatedAt, map[string]any{"option": "Wait"}); code != 200 {
		t.Fatalf("the owner must still decide an overdue irreversible question: HTTP %d", code)
	}
	rows, err := database.ListTodoDecisions(context.Background(), todo.ID, true)
	if err != nil || len(rows) != 1 || rows[0].Source != db.DecisionManual || rows[0].Choice != "Wait" ||
		len(again.told("c1x1x1")) != 1 || again.get(todo.ID).DoneAt == nil {
		t.Fatalf("only the owner's decision may finish this question: rows=%+v err=%v", rows, err)
	}
	t.Log("irreversible_due=true ask_present=true owner_http=200 manual_decisions=1 asker_messages=1")
}

func TestExpiredQuestionReversibleDeadlineDefaultIsNotAnotherOwnerDecision(t *testing.T) {
	database := todoTestDB(t)
	e := newAskTodoEnv(t, database)
	ix, _ := e.expireWith(strings.Replace(strings.Replace(askDeadlineArgsJSON, "%s", "6h", 1), "%s", "true", 1))
	todo := e.onlyAskTodo()
	e.s.askTodoDue(context.Background(), todo.ID, false, time.Now().Add(7*time.Hour))
	waitFor(t, "the default closes its question", func() bool { return e.get(todo.ID).DoneAt != nil })
	if view := expiredQuestionPayload(e, todo.ID); view.Ask != nil {
		t.Fatalf("a default already delivered must not ask for another decision: %+v", view.Ask)
	}
	if code := answerExpiredQuestion(e, todo.ID, ix.CreatedAt, map[string]any{"option": "Wait"}); code != 409 {
		t.Fatalf("the default already answered the question: HTTP %d", code)
	}
	rows, err := database.ListTodoDecisions(context.Background(), todo.ID, true)
	if err != nil || len(rows) != 1 || rows[0].Source != db.DecisionAgent || len(e.told("c1x1x1")) != 1 {
		t.Fatalf("only the default must be recorded and relayed: rows=%+v err=%v", rows, err)
	}
	t.Log("reversible_due=true ask_present=false owner_http=409 default_decisions=1 asker_messages=1")
}

func TestExpiredQuestionDefaultInFlightRemainsReservedAfterClaimExpires(t *testing.T) {
	database := todoTestDB(t)
	e := newAskTodoEnv(t, database)
	ix, _ := e.expireWith(strings.Replace(strings.Replace(askDeadlineArgsJSON, "%s", "6h", 1), "%s", "true", 1))
	todo := e.onlyAskTodo()
	// A restart can lose the sending goroutine and outlive its claim, but the persisted default still owns the answer.
	e.s.askTodos.mu.Lock()
	a := &e.s.askTodos.list[0]
	a.Deadline, a.DueAt = time.Now().Add(-time.Minute), time.Now().UTC()
	a.ByDefault, a.ClaimedAt = true, time.Now().Add(-askClaimHold-time.Second)
	err := e.s.saveAskTodosLocked(context.Background())
	e.s.askTodos.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	again := newAskTodoEnv(t, database)
	if view := expiredQuestionPayload(again, todo.ID); view.Ask != nil {
		t.Fatalf("a reserved default must not become a second owner decision after its claim expires: %+v", view.Ask)
	}
	if code := answerExpiredQuestion(again, todo.ID, ix.CreatedAt, map[string]any{"option": "Wait"}); code != 409 {
		t.Fatalf("a persisted default must keep its answer reservation: HTTP %d", code)
	}
	if _, claimed := again.s.claimAskTodo(context.Background(), todo.ID, true); claimed {
		t.Fatal("the manual decision path must not take over a reserved default either")
	}
	t.Log("default_reserved=true claim_expired=true ask_present=false owner_http=409")
}

func TestExpiredQuestionFailedDefaultReturnsToOwnerDespiteNonzeroDueAt(t *testing.T) {
	database := todoTestDB(t)
	e := newAskTodoEnv(t, database)
	ix, _ := e.expireWith(strings.Replace(strings.Replace(askDeadlineArgsJSON, "%s", "6h", 1), "%s", "true", 1))
	todo := e.onlyAskTodo()
	setSettings(t, e.s, func(v *Settings) { v.Main = "" })
	e.sessions("server")
	e.s.askTodoDue(context.Background(), todo.ID, false, time.Now().Add(7*time.Hour))
	waitFor(t, "the failed default returns its claim to the owner", func() bool {
		a, _ := e.s.askTodoOf(context.Background(), todo.ID)
		return !a.DueAt.IsZero() && !a.ByDefault && !a.held(time.Now())
	})
	again := newAskTodoEnv(t, database)
	if view := expiredQuestionPayload(again, todo.ID); view.Ask == nil {
		t.Fatal("a failed default must leave the owner's question answerable despite DueAt")
	}
	if code := answerExpiredQuestion(again, todo.ID, ix.CreatedAt, map[string]any{"option": "Wait"}); code != 200 {
		t.Fatalf("the owner must be able to answer after the default failed: HTTP %d", code)
	}
	rows, err := database.ListTodoDecisions(context.Background(), todo.ID, true)
	if err != nil || len(rows) != 1 || rows[0].Source != db.DecisionManual || len(again.told("c1x1x1")) != 1 {
		t.Fatalf("the failed default must not record a second decision: rows=%+v err=%v", rows, err)
	}
	t.Log("default_failed=true deadline_due=true ask_present=true owner_http=200 manual_decisions=1")
}

func TestExpiredQuestionPersistedAnswerCanRetryAfterRelayFailure(t *testing.T) {
	for _, choice := range []string{"Wait", "Merge (Recommended)"} {
		t.Run(choice, func(t *testing.T) {
			database := todoTestDB(t)
			e := newAskTodoEnv(t, database)
			ix, _ := e.expire()
			todo := e.onlyAskTodo()
			setSettings(t, e.s, func(v *Settings) { v.Main = "" })
			e.sessions("server")
			if code := answerExpiredQuestion(e, todo.ID, ix.CreatedAt, map[string]any{"option": "Wait"}); code != 502 {
				t.Fatalf("an answer with nowhere to relay must fail, not complete: HTTP %d", code)
			}
			rows, err := database.ListTodoDecisions(context.Background(), todo.ID, true)
			if err != nil || len(rows) != 1 || e.get(todo.ID).DoneAt != nil || len(e.told("c1x1x1")) != 0 {
				t.Fatalf("the decision must be persisted before its failed relay: rows=%+v err=%v", rows, err)
			}
			again := newAskTodoEnv(t, database)
			if code := answerExpiredQuestion(again, todo.ID, ix.CreatedAt, map[string]any{"option": choice}); code != 200 {
				t.Fatalf("retrying a persisted answer after recovery must succeed: HTTP %d", code)
			}
			rows, err = database.ListTodoDecisions(context.Background(), todo.ID, true)
			wantRows := 1
			if choice != "Wait" {
				wantRows = 2 // An owner who changes the answer must not silently keep only the earlier choice.
			}
			messages := again.told("c1x1x1")
			if err != nil || len(rows) != wantRows || len(messages) != 1 || !strings.Contains(messages[0], choice) ||
				again.get(todo.ID).DoneAt == nil {
				t.Fatalf("recovery must record the chosen answer and send it once: rows=%+v messages=%q err=%v", rows, messages, err)
			}
			t.Logf("first_http=502 retry_http=200 choice=%q decisions=%d asker_messages=1 closed=true", choice, len(rows))
		})
	}
}

type expiredQuestionInsertStore struct {
	Store
	todoStore
	beforeInsert func()
	afterInsert  func()
}

func (s *expiredQuestionInsertStore) InsertTodoDecision(ctx context.Context, row db.TodoDecisionRow) (bool, error) {
	if s.beforeInsert != nil {
		s.beforeInsert()
	}
	inserted, err := s.todoStore.InsertTodoDecision(ctx, row)
	if err == nil && inserted && s.afterInsert != nil {
		s.afterInsert()
	}
	return inserted, err
}

func TestExpiredQuestionClaimedAnswerSurvivesOwnerCloseOrDelete(t *testing.T) {
	for _, when := range []string{"before decision insert", "after decision insert"} {
		for _, action := range []string{"close", "delete"} {
			t.Run(when+"/"+action, func(t *testing.T) {
				database := todoTestDB(t)
				base := newAskTodoEnv(t, database).s.store()
				store := &expiredQuestionInsertStore{Store: base, todoStore: base.(todoStore)}
				e := newAskTodoEnv(t, store)
				ix, _ := e.expire()
				todo := e.onlyAskTodo()
				interrupt := func() {
					a, _ := e.s.askTodoOf(context.Background(), todo.ID)
					if !a.held(time.Now()) {
						t.Fatal("the owner action must interleave after the answer claimed the question")
					}
					if action == "close" {
						e.must("PATCH", "/api/v1/todos/"+todo.ID, `{"done":true}`)
					} else {
						e.must("DELETE", "/api/v1/todos/"+todo.ID, "")
					}
					if len(e.told("c1x1x1")) != 0 {
						t.Fatal("closing a claimed question must leave its terminal message to the answer")
					}
				}
				if when == "before decision insert" {
					store.beforeInsert = interrupt
				} else {
					store.afterInsert = interrupt
				}
				if code := answerExpiredQuestion(e, todo.ID, ix.CreatedAt, map[string]any{"option": "Wait"}); code != 200 {
					t.Fatalf("a claimed answer must not be abandoned when the owner %ss its to-do: HTTP %d", action, code)
				}
				messages := e.told("c1x1x1")
				a, _ := e.s.askTodoOf(context.Background(), todo.ID)
				if len(messages) != 1 || !strings.Contains(messages[0], "Wait") || a.AnsweredAt.IsZero() ||
					!a.DroppedAt.IsZero() || !a.ClaimedAt.IsZero() {
					t.Fatalf("the asking conversation needs exactly one terminal answer: messages=%q record=%+v", messages, a)
				}
				cur, exists := e.s.todoNow(todo.ID)
				if action == "delete" && exists || action == "close" && (!exists || cur.DoneAt == nil) {
					t.Fatal("delivering the claimed answer must not resurrect or reopen the to-do")
				}
				if code := answerExpiredQuestion(e, todo.ID, ix.CreatedAt, map[string]any{"option": "Wait"}); code != 409 {
					t.Fatalf("the terminal answer must not be replayed: HTTP %d", code)
				}
				t.Logf("interleave=%q action=%s answer_http=200 asker_messages=1 answered=true dropped=false resurrected=false", when, action)
			})
		}
	}
}
