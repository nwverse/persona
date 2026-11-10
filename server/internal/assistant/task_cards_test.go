package assistant

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func cardPlan(t *testing.T, s *Service, at time.Time, state string, tasks ...PlanTask) *TaskPlan {
	t.Helper()
	p := &TaskPlan{ID: "plan_card", Conversation: "card_chat", CreatedAt: at, UpdatedAt: at, State: state, Tasks: tasks}
	if err := s.savePlan(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTaskCardsKeepPlanningSlotThroughProgressAndAcceptance(t *testing.T) {
	s, hub := newTaskService(t)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	plan := cardPlan(t, s, at, planOpen, PlanTask{Title: "Improve search"})
	items := []FeedItem{{ID: "first", Kind: feedPlan, Ref: plan.ID, At: at}}
	cards, err := s.taskCards(t.Context(), items, map[string]*TaskPlan{plan.ID: plan})
	if err != nil || len(cards) != 1 || cards[0].State != "not_started" || cards[0].Index == nil || *cards[0].Index != 0 {
		t.Fatalf("planned: %+v, %v", cards, err)
	}
	id := cards[0].ID
	plan.Tasks[0].TodoID, plan.State, plan.UpdatedAt = "todo_card", planStarted, at.Add(time.Minute)
	if err := s.savePlan(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	todo := Todo{ID: "todo_card", Origin: plan.Conversation, PlanID: plan.ID, CreatedAt: at.Add(time.Minute), UpdatedAt: at.Add(time.Minute),
		Agents:      []TodoAgent{{MachineID: "box", SessionID: "session_card"}},
		Supervision: &Supervision{Progress: &TaskProgress{Kind: "progress", Step: 2, Steps: 4, At: at.Add(2 * time.Minute)}}}
	putTodo(s, todo)
	hub.mu.Lock()
	hub.sessions["box"] = []proto.Session{{SessionID: "session_card", State: proto.StateWorking, UpdatedAt: at.Add(time.Minute)}}
	hub.mu.Unlock()
	items = append(items, FeedItem{ID: "started", Kind: feedTask, Ref: todo.ID, At: at.Add(time.Minute)},
		FeedItem{ID: "progress", Kind: feedReport, Ref: todo.ID, At: at.Add(2 * time.Minute)})
	cards, err = s.taskCards(t.Context(), items, map[string]*TaskPlan{plan.ID: plan})
	if err != nil || len(cards) != 1 || cards[0].ID != id || cards[0].State != "working" || !cards[0].At.Equal(at) || cards[0].ReportID != "progress" {
		t.Fatalf("working: %+v, %v", cards, err)
	}
	done := at.Add(3 * time.Minute)
	todo.DoneAt, todo.DoneBy, todo.Supervision.State = &done, db.DoneByReview, reviewAccepted
	putTodo(s, todo)
	items = append(items, FeedItem{ID: "accepted", Kind: feedReceipt, Ref: todo.ID, At: done})
	cards, err = s.taskCards(t.Context(), items, map[string]*TaskPlan{plan.ID: plan})
	if err != nil || len(cards) != 1 || cards[0].ID != id || cards[0].State != "accepted" || cards[0].ReceiptID != "accepted" || !cards[0].At.Equal(at) {
		t.Fatalf("accepted: %+v, %v", cards, err)
	}
	// A review-only since page has no plan event, but keeps the original slot and anchor.
	cards, err = s.taskCards(t.Context(), []FeedItem{{ID: "review", Kind: feedReview, Ref: todo.ID, At: done}}, nil)
	if err != nil || len(cards) != 1 || cards[0].ID != id || cards[0].PlanID != plan.ID || !cards[0].At.Equal(at) {
		t.Fatalf("since: %+v, %v", cards, err)
	}
}

func TestTaskCardsDeduplicateEventsAndRetainLatestEvidence(t *testing.T) {
	s, _ := newTaskService(t)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	plan := cardPlan(t, s, at, planStarted, PlanTask{Title: "Search", TodoID: "one"}, PlanTask{Title: "Index", TodoID: "two"})
	putTodo(s, Todo{ID: "one", PlanID: plan.ID, CreatedAt: at})
	putTodo(s, Todo{ID: "two", PlanID: plan.ID, CreatedAt: at})
	items := []FeedItem{{ID: "p2", Kind: feedPlan, Ref: plan.ID, At: at.Add(time.Hour)},
		{ID: "p1", Kind: feedPlan, Ref: plan.ID, At: at},
		{ID: "r_new", Kind: feedReceipt, Ref: "one", At: at.Add(3 * time.Minute)},
		{ID: "r_old", Kind: feedReceipt, Ref: "one", At: at.Add(time.Minute)},
		{ID: "report", Kind: feedReport, Ref: "one", At: at.Add(2 * time.Minute)},
		{ID: "message", Kind: feedMessage, Ref: "one", At: at.Add(time.Hour)}}
	cards, err := s.taskCards(t.Context(), items, map[string]*TaskPlan{plan.ID: plan})
	if err != nil || len(cards) != 2 || cards[0].ID == cards[1].ID || cards[0].ReceiptID != "r_new" || cards[0].ReportID != "report" {
		t.Fatalf("deduplicated: %+v, %v", cards, err)
	}
	if !cards[0].At.Equal(at) || !cards[1].At.Equal(at) {
		t.Fatal("later events moved the planning anchor")
	}
}

func TestTaskCardsDoNotCallCancellationAcceptanceOrDeleteOnEmptyPage(t *testing.T) {
	s, _ := newTaskService(t)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, state string
		todo        Todo
	}{
		{"cancelled", "cancelled", Todo{DoneAt: &at, DoneBy: db.DoneByCancelled, Supervision: &Supervision{State: reviewAccepted}}},
		{"auto done", "done", Todo{DoneAt: &at, DoneBy: db.DoneByAuto}},
		{"reopened", "not_started", Todo{Supervision: &Supervision{State: reviewAccepted}}},
		{"start failed", "failed", Todo{DoneAt: &at, StartError: "Launcher unavailable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			todo := tc.todo
			todo.ID, todo.CreatedAt = "standalone", at
			putTodo(s, todo)
			cards, err := s.taskCards(t.Context(), []FeedItem{{Kind: feedReceipt, Ref: todo.ID, ID: "receipt", At: at}}, nil)
			if err != nil || len(cards) != 1 || cards[0].ID != "todo:standalone" || cards[0].State != tc.state {
				t.Fatalf("%+v, %v", cards, err)
			}
		})
	}
	cards, err := s.taskCards(t.Context(), nil, nil)
	if err != nil || cards == nil || len(cards) != 0 {
		t.Fatalf("empty upsert page: %+v, %v", cards, err)
	}
}

func TestFeedHTTPAddsTaskCardsWithoutReplacingAuditItems(t *testing.T) {
	s, _ := newTaskService(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	plan := cardPlan(t, s, at, planOpen, PlanTask{Title: "Improve search"})
	if _, err := s.appendFeedAt(t.Context(), plan.Conversation, feedPlan, plan.ID, nil, at); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/assistant/conversations/"+plan.Conversation+"/feed", nil))
	var out struct {
		Items []FeedItem `json:"items"`
		Cards []TaskCard `json:"task_cards"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Items) != 1 || len(out.Cards) != 1 || out.Items[0].Kind != feedPlan {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
}

func TestTaskCardsReuseOnlyCurrentPlan(t *testing.T) {
	s, _ := newTaskService(t)
	at := time.Now().UTC()
	old := &TaskPlan{ID: "old", State: planStarted, CreatedAt: at, Tasks: []PlanTask{{TodoID: "reused"}}}
	current := &TaskPlan{ID: "current", State: planStarted, CreatedAt: at, Tasks: []PlanTask{{TodoID: "reused"}}}
	putTodo(s, Todo{ID: "reused", PlanID: current.ID, CreatedAt: at})
	cards, err := s.taskCards(t.Context(), []FeedItem{{Kind: feedPlan, Ref: old.ID}, {Kind: feedPlan, Ref: current.ID}}, map[string]*TaskPlan{old.ID: old, current.ID: current})
	if err != nil || len(cards) != 1 || cards[0].PlanID != current.ID {
		t.Fatalf("reused task appeared twice: %+v, %v", cards, err)
	}
}

func TestTaskCardsReadEachMachineOnceAndKeepFailuresUnknown(t *testing.T) {
	s, hub := newTaskService(t)
	at := time.Now().UTC()
	putTodo(s, Todo{ID: "one", CreatedAt: at, Agents: []TodoAgent{{MachineID: "box", SessionID: "one"}}})
	putTodo(s, Todo{ID: "two", CreatedAt: at, Agents: []TodoAgent{{MachineID: "box", SessionID: "two"}}})
	hub.mu.Lock()
	hub.sessions["box"] = []proto.Session{{SessionID: "one", State: proto.StateWorking}, {SessionID: "two", State: proto.StateNeedsYou}}
	hub.calls = nil
	hub.mu.Unlock()
	items := []FeedItem{{Kind: feedTask, Ref: "one"}, {Kind: feedTask, Ref: "two"}}
	cards, err := s.taskCards(t.Context(), items, nil)
	if err != nil || len(cards) != 2 {
		t.Fatalf("projection: %+v, %v", cards, err)
	}
	hub.mu.Lock()
	reads := 0
	for _, call := range hub.calls {
		if call.Method == DeviceSessionsList {
			reads++
		}
	}
	hub.sessionsFail["box"] = 1
	hub.mu.Unlock()
	if reads != 1 {
		t.Fatalf("session-list reads = %d, want one per machine", reads)
	}
	cards, err = s.taskCards(t.Context(), items, nil)
	if err == nil || cards != nil {
		t.Fatalf("an unreachable machine must not become stopped: %+v, %v", cards, err)
	}
}

func TestFeedTaskCardReadFailurePreservesOriginalEvents(t *testing.T) {
	s, hub := newTaskService(t)
	at := time.Now().UTC()
	putTodo(s, Todo{ID: "one", CreatedAt: at, Agents: []TodoAgent{{MachineID: "box", SessionID: "one"}}})
	if _, err := s.appendFeedAt(t.Context(), "card_chat", feedTask, "one", nil, at); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	hub.sessionsFail["box"] = 1
	hub.mu.Unlock()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/assistant/conversations/card_chat/feed", nil))
	var out struct {
		Items []FeedItem `json:"items"`
		Cards []TaskCard `json:"task_cards"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Items) != 1 || out.Cards != nil {
		t.Fatalf("a projection failure must retain the audit feed: HTTP %d %s", w.Code, w.Body.String())
	}
}
