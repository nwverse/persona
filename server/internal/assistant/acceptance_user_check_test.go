package assistant

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

func acceptedForUserCheck(t *testing.T) (*Service, *fakeHub, Todo) {
	t.Helper()
	return acceptedForUserCheckOn(t, nil)
}

func acceptedForUserCheckOn(t *testing.T, store Store) (*Service, *fakeHub, Todo) {
	t.Helper()
	s, f, todo, _, _ := conclusionServiceOn(t, store, false)
	waitsForReview(t, s, f, todo.ID, "The layout is ready to inspect.")
	f.judge = `{"changed":"The layout is updated","link":"","how":"Inspect the task page"}`
	reviewTask(t, s, todo.ID, map[string]any{
		"verdict": "accepted", "user_check": true, "note": "Ready for the owner's check",
		"checks": completeReviewChecks(todoNow(s, todo.ID)),
	})
	return s, f, todoNow(s, todo.ID)
}

func TestUserCheckKeepsTaskAndAgentOpenUntilPass(t *testing.T) {
	ctx := context.Background()
	s, f, todo := acceptedForUserCheck(t)
	if todo.DoneAt != nil || todo.DoneBy != "" {
		t.Fatalf("personal verification already closed the task: done_at=%v done_by=%q", todo.DoneAt, todo.DoneBy)
	}
	if got := todo.Supervision.state(todo); got != "user_check" {
		t.Fatalf("personal verification state = %q, want user_check", got)
	}
	if len(s.doneTaskAgents()) != 0 {
		t.Fatal("a personal-verification task entered completed-task retirement")
	}
	a := askOf(s, todo.ID)
	if a == nil || !a.AcceptedAt.IsZero() || waitingForOwner(s) != 1 {
		t.Fatalf("the owner has no pending verification: %+v", a)
	}

	// An owner taking longer than the ordinary stalled-task limit must not lose this Agent.
	old := time.Now().Add(-100 * time.Hour)
	s.todos.mu.Lock()
	s.todos.todos[todo.ID].UpdatedAt = old
	s.todos.mu.Unlock()
	f.setSessions("box", idleAgent(old))
	s.closeDoneTaskAgents(ctx, taskCloseRefused{})
	s.closeIdleAgents(ctx, taskCloseRefused{}, idleSeen{{"box", "s1"}: old})
	if got := f.retires(); len(got) != 0 {
		t.Fatalf("retired an Agent awaiting personal verification: %v", got)
	}
	if got := watchSignals(todoNow(s, todo.ID), nil, time.Now().Add(100*time.Hour)); len(got) != 0 {
		t.Fatalf("personal verification raised supervisor follow-ups: %+v", got)
	}

	if result, err := s.acceptTry(ctx, todo.ID, a.MessageID); err != nil || !result.taken {
		t.Fatalf("owner Pass: %+v, %v", result, err)
	}
	done := todoNow(s, todo.ID)
	if done.DoneAt == nil || done.DoneBy != db.DoneByReview || done.Stage != "verify" || done.Supervision.state(done) != reviewAccepted {
		t.Fatalf("Pass did not finish and verify the task: %+v", done)
	}
	if waitingForOwner(s) != 0 {
		t.Fatal("Pass left the owner-verification ask pending")
	}
	if result, err := s.acceptTry(ctx, todo.ID, a.MessageID); err != nil || result.taken {
		t.Fatalf("duplicate Pass was taken: %+v, %v", result, err)
	}

	idle := taskCloseIdle
	taskCloseIdle = 0
	t.Cleanup(func() { taskCloseIdle = idle })
	s.closeDoneTaskAgents(ctx, taskCloseRefused{})
	if got := f.retires(); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("Pass did not permit normal completed-task retirement: %v", got)
	}
}

func TestUserCheckFeedbackReturnsToTheSameAgent(t *testing.T) {
	s, f, todo := acceptedForUserCheck(t)
	a := askOf(s, todo.ID)
	before := len(sentToAgent(f))
	if !s.acceptReply(context.Background(), "om_feedback", "Please fix the spacing.", false, a.MessageID) {
		t.Fatal("feedback was not recognized")
	}
	got := todoNow(s, todo.ID)
	if got.DoneAt != nil || got.Supervision.state(got) == "user_check" || got.ReopenedAt == nil {
		t.Fatalf("feedback did not return the open task to ongoing work: %+v", got)
	}
	if askOf(s, todo.ID) != nil {
		t.Fatal("feedback left an obsolete Pass action available")
	}
	f.mu.Lock()
	sent := append([]map[string]any(nil), f.agentSent[before:]...)
	f.mu.Unlock()
	if len(sent) != 1 || sent[0]["session_id"] != "s1" || !strings.Contains(sent[0]["text"].(string), "Please fix the spacing.") {
		t.Fatalf("feedback did not reach the original Agent exactly once: %+v", sent)
	}
	if result, err := s.acceptTry(context.Background(), todo.ID, a.MessageID); err != nil || result.taken || todoNow(s, todo.ID).DoneAt != nil {
		t.Fatalf("a superseded verification card closed rework: %+v, %v", result, err)
	}
}

func TestUserCheckPassHTTPCompletesTheTask(t *testing.T) {
	s, _, todo := acceptedForUserCheck(t)
	if todo.DoneAt != nil {
		t.Fatal("task was closed before the web's Pass")
	}
	a := askOf(s, todo.ID)
	body := `{"asked_at":"` + a.AskedAt.Format(timeKeyLayout) + `"}`
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/todos/"+todo.ID+"/accept", strings.NewReader(body)))
	if rec.Code != http.StatusOK || todoNow(s, todo.ID).DoneAt == nil {
		t.Fatalf("web Pass: status=%d body=%s todo=%+v", rec.Code, rec.Body.String(), todoNow(s, todo.ID))
	}
}

func TestUserCheckPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	database := todoTestDB(t)
	_, f, todo := acceptedForUserCheckOn(t, database)
	if row, err := database.GetTodo(ctx, todo.ID); err != nil || row.DoneAt != nil {
		t.Fatalf("verification was not persisted as open: %+v, %v", row, err)
	}
	restarted := newServiceOn(t, f, database)
	if err := restarted.ensureTodosLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	if got := todoNow(restarted, todo.ID); got.Supervision.state(got) != "user_check" {
		t.Fatalf("restart lost pending personal verification: %+v", got)
	}
	if result, err := restarted.acceptTry(ctx, todo.ID, ""); err != nil || !result.taken {
		t.Fatalf("Pass after restart: %+v, %v", result, err)
	}
	if row, err := database.GetTodo(ctx, todo.ID); err != nil || row.DoneAt == nil || row.DoneBy != db.DoneByReview {
		t.Fatalf("Pass was not persisted as completed: %+v, %v", row, err)
	}
}

type userCheckCloseFailure struct {
	*db.DB
	fail bool
}

func (d *userCheckCloseFailure) ReviewDoneTodo(ctx context.Context, id, reason string, accepted bool, at time.Time) (*db.TodoRow, bool, error) {
	if d.fail {
		return nil, false, errors.New("completion store unavailable")
	}
	return d.DB.ReviewDoneTodo(ctx, id, reason, accepted, at)
}

func TestUserCheckFailedCompletionLeavesPassRetryable(t *testing.T) {
	ctx := context.Background()
	store := &userCheckCloseFailure{DB: todoTestDB(t)}
	s, _, todo := acceptedForUserCheckOn(t, store)
	store.fail = true
	if result, err := s.acceptTry(ctx, todo.ID, ""); result.taken || !errors.Is(err, errAcceptStore) {
		t.Fatalf("failed completion was reported accepted: %+v, %v", result, err)
	}
	if got := todoNow(s, todo.ID); got.DoneAt != nil || got.Supervision.state(got) != "user_check" {
		t.Fatalf("failed Pass changed the task: %+v", got)
	}
	if a := askOf(s, todo.ID); a == nil || !a.AcceptedAt.IsZero() {
		t.Fatalf("failed Pass consumed the ask: %+v", a)
	}
	store.fail = false
	if result, err := s.acceptTry(ctx, todo.ID, ""); err != nil || !result.taken || todoNow(s, todo.ID).DoneAt == nil {
		t.Fatalf("retry did not complete: %+v, %v", result, err)
	}
}

func TestUserCheckIsNotOverwrittenByAnotherJudgment(t *testing.T) {
	ctx := context.Background()
	s, _, todo := acceptedForUserCheck(t)
	s.superviseJudged(ctx, todo, time.Now().Add(time.Hour), taskDoneCheck{Verdict: verdictDone}, nil)
	s.superviseJudged(ctx, todo, time.Now().Add(time.Hour), taskDoneCheck{Verdict: verdictNotDone}, nil)
	if got := todoNow(s, todo.ID); got.DoneAt != nil || got.Supervision.state(got) != "user_check" {
		t.Fatalf("a later automatic judgment settled personal verification: %+v", got)
	}
}

func TestUserCheckReworkInvalidatesTheOldPass(t *testing.T) {
	s, _, todo := acceptedForUserCheck(t)
	a := askOf(s, todo.ID)
	reviewTask(t, s, todo.ID, map[string]any{"verdict": "rework", "note": "Fix the spacing before another personal check."})
	got := todoNow(s, todo.ID)
	if got.DoneAt != nil || got.Supervision.state(got) != reviewRework {
		t.Fatalf("supervisor rework did not resume the task: %+v", got)
	}
	if result, err := s.acceptTry(context.Background(), todo.ID, a.MessageID); err != nil || result.taken {
		t.Fatalf("the previous personal check still accepts rework: %+v, %v", result, err)
	}
}

func TestUserCheckReplacesAnEarlierDecision(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	waitsForReview(t, s, f, todo.ID, "The owner needs to choose a layout.")
	reviewTask(t, s, todo.ID, map[string]any{"verdict": "needs_user", "reason": "Choose the layout", "recommend": "Use the compact layout"})
	f.judge = `{"changed":"The layout is updated","link":"","how":"Inspect the task page"}`
	reviewTask(t, s, todo.ID, map[string]any{
		"verdict": "accepted", "user_check": true, "checks": completeReviewChecks(todoNow(s, todo.ID)),
	})
	if a := askOf(s, todo.ID); a == nil || decideAsk(a) || a.Conclusion || !a.AcceptedAt.IsZero() {
		t.Fatalf("the earlier decision displaced personal verification: %+v", a)
	}
	if result, err := s.acceptTry(context.Background(), todo.ID, ""); err != nil || !result.taken || todoNow(s, todo.ID).DoneAt == nil {
		t.Fatalf("personal Pass after a decision did not complete: %+v, %v", result, err)
	}
}
