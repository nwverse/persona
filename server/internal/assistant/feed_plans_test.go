package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// seedFeedPlans puts n plan cards, each naming its own plan, into a new conversation of st and returns the conversation
// and the plan ids in order. task_plans and assistant_feed are not truncated between tests, so the names are fresh.
func seedFeedPlans(tb testing.TB, s *Service, n int) (string, []string) {
	tb.Helper()
	ctx := context.Background()
	conversation := "feedplans_" + randomHex(6)
	prompt := strings.Repeat("Fix the crash on the login page and add a test. ", 30)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var ids []string
	for i := range n {
		p := &TaskPlan{ID: "plan_" + randomHex(6), Conversation: conversation, State: planOpen, CreatedAt: now, UpdatedAt: now,
			Tasks: []PlanTask{{Title: fmt.Sprintf("Task %d", i), Prompt: prompt, Machine: "box", Cwd: "/code/app"}}}
		if err := s.savePlan(ctx, p); err != nil {
			tb.Fatal(err)
		}
		if _, err := s.appendFeedAt(ctx, conversation, feedPlan, p.ID, map[string]any{"plan": p}, now.Add(time.Duration(i)*time.Millisecond)); err != nil {
			tb.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	return conversation, ids
}

// BenchmarkFeedPlans reads a feed of 100 plan cards with their plans from Postgres.
func BenchmarkFeedPlans(b *testing.B) {
	url := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if url == "" {
		b.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, url, db.ManagerSet)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(database.Close)
	s := New(Options{Hub: newFakeHub(), Store: func() Store { return database }, Logf: func(string, ...any) {}})
	conversation, ids := seedFeedPlans(b, s, 100)
	b.ReportAllocs()
	for b.Loop() {
		items, plans, err := s.feed(ctx, conversation, time.Time{})
		if err != nil || len(items) != len(ids) || len(plans) != len(ids) {
			b.Fatalf("feed: %d items, %d plans, %v", len(items), len(plans), err)
		}
	}
}

// countingPlans counts the plan reads that reach the database; with failBatch set, the batch read fails.
type countingPlans struct {
	*db.DB
	one, batch atomic.Int64
	failBatch  atomic.Bool
}

func (c *countingPlans) TaskPlan(ctx context.Context, id string) (*db.TaskPlanRow, error) {
	c.one.Add(1)
	return c.DB.TaskPlan(ctx, id)
}

func (c *countingPlans) TaskPlansByID(ctx context.Context, ids []string) ([]db.TaskPlanRow, error) {
	c.batch.Add(1)
	if c.failBatch.Load() {
		return nil, errors.New("connection reset")
	}
	return c.DB.TaskPlansByID(ctx, ids)
}

// A feed's plans come from one read however many plan cards it has (one read per card was a round trip each), and
// they are the plans loadPlan reads; a card whose plan is gone has none, and when the read fails the cards still come.
func TestFeedReadsPlansInOneQuery(t *testing.T) {
	st := &countingPlans{DB: todoTestDB(t)}
	s := newServiceOn(t, newFakeHub(), st)
	ctx := context.Background()
	conversation, ids := seedFeedPlans(t, s, 20)
	// The first plan's card once more, and a card whose plan is gone.
	for _, ref := range []string{ids[0], "plan_gone"} {
		if _, err := s.appendFeed(ctx, conversation, feedPlan, ref, nil); err != nil {
			t.Fatal(err)
		}
	}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/assistant/conversations/"+conversation+"/feed", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("feed: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Items []FeedItem           `json:"items"`
		Plans map[string]*TaskPlan `json:"plans"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if one, batch := st.one.Load(), st.batch.Load(); one != 0 || batch != 1 {
		t.Fatalf("%d TaskPlan and %d TaskPlansByID calls, want one TaskPlansByID", one, batch)
	}
	if len(out.Items) != len(ids)+2 || len(out.Plans) != len(ids) {
		t.Fatalf("%d items and %d plans, want %d and %d", len(out.Items), len(out.Plans), len(ids)+2, len(ids))
	}

	_, plans, err := s.feed(ctx, conversation, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		want, err := s.loadPlan(ctx, id)
		if err != nil || out.Plans[id] == nil || !reflect.DeepEqual(plans[id], want) {
			t.Fatalf("plan %s: %+v, loadPlan %+v (%v)", id, plans[id], want, err)
		}
	}
	if _, ok := plans["plan_gone"]; ok {
		t.Fatal("a plan that is gone is in the map")
	}

	st.failBatch.Store(true)
	items, plans, err := s.feed(ctx, conversation, time.Time{})
	if err != nil || len(items) != len(ids)+2 || len(plans) != 0 {
		t.Fatalf("failed plan read: %d items, %d plans, %v", len(items), len(plans), err)
	}
}

// Main chat hydrates all default-space state=open plans across this manager's conversations (including scheduled
// routines and side conversations) on both /feed and /assistant without leaking non-open, health-space, archived,
// deleted, or extension-hidden plans, while non-main feeds stay scoped to their own plans.
func TestMainFeedHydratesCrossConversationOpenPlans(t *testing.T) {
	healthDir := identity.HealthDir("/home/owner")
	todoStores(t, func(t *testing.T, e *todoEnv, _ *db.DB) {
		ctx := context.Background()
		suffix := randomHex(4)
		mainID := "conv1"
		routineID := "routine_" + suffix
		sideID := "side_" + suffix
		healthID := "health_" + suffix
		archivedID := "arch_" + suffix
		deletedID := "del_" + suffix

		sess := func(id, title, cwd, origin string) proto.Session {
			s := scopeSession(id, title, cwd)
			s.Assistant = true
			s.Origin = origin
			return s
		}
		e.sessions("server",
			sess(mainID, "Persona", "/home/owner/.persona/assistant", "user"),
			sess(routineID, "Nightly audit", "/home/owner/.persona/assistant", "task"),
			sess(sideID, "Release prep", "/home/owner/.persona/assistant", "user"),
			sess(healthID, "Checkup notes", healthDir, "user"),
			sess(archivedID, "Archived chat", "/home/owner/.persona/assistant", "user"),
			sess(deletedID, "Deleted chat", "/home/owner/.persona/assistant", "user"),
		)
		e.s.noteConversationSpaces(ctx, []db.ConversationSpace{
			{MachineID: "server", SessionID: healthID, Space: SpaceHealth},
			{MachineID: "server", SessionID: mainID, Space: ""},
			{MachineID: "server", SessionID: routineID, Space: ""},
			{MachineID: "server", SessionID: sideID, Space: ""},
		})
		if _, err := e.s.updateSettings(ctx, func(st *Settings) error {
			st.Main = mainID
			st.MachineID = "server"
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		now := time.Now().UTC().Truncate(time.Microsecond)
		makePlan := func(id, conv, state string, task PlanTask, at time.Time) *TaskPlan {
			t.Helper()
			p := &TaskPlan{ID: id, Conversation: conv, State: state, CreatedAt: at, UpdatedAt: at, Tasks: []PlanTask{task}}
			if err := e.s.savePlan(ctx, p); err != nil {
				t.Fatal(err)
			}
			if _, err := e.s.appendFeedAt(ctx, conv, feedPlan, p.ID, map[string]any{"plan": p}, at); err != nil {
				t.Fatal(err)
			}
			return p
		}

		pMain := makePlan("plan_main_"+suffix, mainID, planOpen, PlanTask{Title: "Main work", Prompt: "p", Machine: "server", Cwd: "/code/app"}, now)
		pRoutine := makePlan("plan_routine_"+suffix, routineID, planOpen, PlanTask{Title: "Nightly fix", Prompt: "p", Machine: "server", Cwd: "/code/app", Schedule: "sched_1", ScheduleTitle: "Nightly audit"}, now.Add(time.Second))
		pRoutineDismissed := makePlan("plan_routine_dismissed_"+suffix, routineID, planDismissed, PlanTask{Title: "Old routine plan", Prompt: "p", Machine: "server", Cwd: "/code/app"}, now.Add(2*time.Second))
		pSide := makePlan("plan_side_"+suffix, sideID, planOpen, PlanTask{Title: "Side work", Prompt: "p", Machine: "server", Cwd: "/code/app"}, now.Add(3*time.Second))
		pHealth := makePlan("plan_health_"+suffix, healthID, planOpen, PlanTask{Title: "Health plan", Prompt: "p", Machine: "server", Cwd: "/code/app"}, now.Add(4*time.Second))
		pArchived := makePlan("plan_arch_"+suffix, archivedID, planOpen, PlanTask{Title: "Archived work", Prompt: "p", Machine: "server", Cwd: "/code/app"}, now.Add(5*time.Second))
		pDeleted := makePlan("plan_del_"+suffix, deletedID, planOpen, PlanTask{Title: "Deleted work", Prompt: "p", Machine: "server", Cwd: "/code/app"}, now.Add(6*time.Second))

		// Archive archivedID and mark deletedID deleted.
		e.s.archived.mu.Lock()
		e.s.archived.loaded = true
		if e.s.archived.m == nil {
			e.s.archived.m = map[string]*convArchived{}
		}
		e.s.archived.m[archivedID] = &convArchived{MachineID: "server", At: now, By: "user"}
		e.s.archived.mu.Unlock()
		tomb := deletedConversation{Machine: "server", ID: deletedID, At: now}
		if ds, ok := e.s.store().(durableConversationDeletions); ok {
			raw, _ := json.Marshal(tomb)
			if err := ds.PutConversationDeletion(ctx, "server", deletedID, raw); err != nil {
				t.Fatal(err)
			}
		} else {
			e.s.deletedConversations.mu.Lock()
			e.s.deletedConversations.loaded = true
			if e.s.deletedConversations.rows == nil {
				e.s.deletedConversations.rows = map[string]deletedConversation{}
			}
			e.s.deletedConversations.rows[deletedID] = tomb
			e.s.deletedConversations.mu.Unlock()
		}

		// Main chat feed (plain and paginated) hydrates all open default-space plans without altering feed items or task_cards.
		for _, q := range []string{"", "?paginate=1"} {
			w := e.must("GET", "/api/v1/assistant/conversations/"+mainID+"/feed"+q, "")
			var out struct {
				Items     []FeedItem           `json:"items"`
				Plans     map[string]*TaskPlan `json:"plans"`
				TaskCards []TaskCard           `json:"task_cards"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			for _, wantID := range []string{pMain.ID, pRoutine.ID, pSide.ID} {
				if out.Plans[wantID] == nil {
					t.Errorf("%s: missing expected open plan %s in main feed plans: %+v", q, wantID, out.Plans)
				}
			}
			for _, forbidID := range []string{pRoutineDismissed.ID, pHealth.ID, pArchived.ID, pDeleted.ID} {
				if out.Plans[forbidID] != nil {
					t.Errorf("%s: unexpected plan %s leaked into main feed plans", q, forbidID)
				}
			}
			for _, it := range out.Items {
				if it.Conversation != mainID {
					t.Errorf("%s: foreign item %s (%s) leaked into main feed items", q, it.ID, it.Conversation)
				}
			}
			for _, card := range out.TaskCards {
				if card.PlanID == pRoutine.ID || card.PlanID == pSide.ID {
					t.Errorf("%s: cross-conversation open plan %s created a main task_card", q, card.PlanID)
				}
			}
		}

		// Non-main conversation feed stays scoped to its own plans.
		wSide := e.must("GET", "/api/v1/assistant/conversations/"+sideID+"/feed", "")
		var sideOut struct {
			Plans map[string]*TaskPlan `json:"plans"`
		}
		if err := json.Unmarshal(wSide.Body.Bytes(), &sideOut); err != nil {
			t.Fatal(err)
		}
		if sideOut.Plans[pSide.ID] == nil || sideOut.Plans[pMain.ID] != nil || sideOut.Plans[pRoutine.ID] != nil {
			t.Fatalf("non-main feed plans = %+v, want only %s", sideOut.Plans, pSide.ID)
		}

		// Health conversation feed tags its own plan with space="health" so clients never merge it into Main chat.
		wHealth := e.must("GET", "/api/v1/assistant/conversations/"+healthID+"/feed", "")
		var healthOut struct {
			Plans map[string]*TaskPlan `json:"plans"`
		}
		if err := json.Unmarshal(wHealth.Body.Bytes(), &healthOut); err != nil {
			t.Fatal(err)
		}
		if healthOut.Plans[pHealth.ID] == nil || healthOut.Plans[pHealth.ID].Space != SpaceHealth {
			t.Fatalf("health feed plan = %+v, want space=%q", healthOut.Plans[pHealth.ID], SpaceHealth)
		}

		// GET /api/v1/assistant hydrates open_plans for default space, omits for health space and extensions.
		wAssist := e.must("GET", "/api/v1/assistant", "")
		var assistOut struct {
			OpenPlans []*TaskPlan `json:"open_plans"`
		}
		if err := json.Unmarshal(wAssist.Body.Bytes(), &assistOut); err != nil {
			t.Fatal(err)
		}
		gotOpen := map[string]bool{}
		for _, p := range assistOut.OpenPlans {
			gotOpen[p.ID] = true
		}
		for _, wantID := range []string{pMain.ID, pRoutine.ID, pSide.ID} {
			if !gotOpen[wantID] {
				t.Errorf("GET /api/v1/assistant missing open_plans[%s]: %+v", wantID, gotOpen)
			}
		}
		for _, forbidID := range []string{pRoutineDismissed.ID, pHealth.ID, pArchived.ID, pDeleted.ID} {
			if gotOpen[forbidID] {
				t.Errorf("GET /api/v1/assistant leaked %s into open_plans", forbidID)
			}
		}
		if wHealthAssist := e.must("GET", "/api/v1/assistant?space=health", ""); strings.Contains(wHealthAssist.Body.String(), `"open_plans"`) {
			t.Errorf("GET /api/v1/assistant?space=health unexpectedly included open_plans: %s", wHealthAssist.Body.String())
		}
		if wExtAssist := e.doAs(roleExtension, "GET", "/api/v1/assistant", ""); strings.Contains(wExtAssist.Body.String(), `"open_plans"`) {
			t.Errorf("extension GET /api/v1/assistant unexpectedly included open_plans: %s", wExtAssist.Body.String())
		}

		// Extension token on Main chat feed does not see health-space plans.
		wExtFeed := e.doAs(roleExtension, "GET", "/api/v1/assistant/conversations/"+mainID+"/feed", "")
		if wExtFeed.Code != http.StatusOK {
			t.Fatalf("extension main feed: %d %s", wExtFeed.Code, wExtFeed.Body.String())
		}
		if strings.Contains(wExtFeed.Body.String(), pHealth.ID) {
			t.Errorf("extension main feed leaked health plan %s", pHealth.ID)
		}

		// Broadcasting a health plan tags space="health" and emits task_plan.
		if tagged := e.s.planWithSpace(ctx, pHealth); tagged == nil || tagged.Space != SpaceHealth {
			t.Errorf("planWithSpace(%s) = %+v, want space=%q", pHealth.ID, tagged, SpaceHealth)
		}
		if tagged := e.s.planWithSpace(ctx, pRoutine); tagged == nil || tagged.Space != "" {
			t.Errorf("planWithSpace(%s) = %+v, want empty space", pRoutine.ID, tagged)
		}
		e.s.broadcastPlan(ctx, pHealth)
		e.f.mu.Lock()
		sawEvent := slices.Contains(e.f.events, "task_plan")
		e.f.mu.Unlock()
		if !sawEvent {
			t.Errorf("broadcastPlan did not emit task_plan event")
		}
	})
}
