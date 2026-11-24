package assistant

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
)

func orgDatabaseCounts(t *testing.T, database *db.DB) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range []string{"todos", "todo_agents", "todo_supervision", "settings", "task_plans", "task_briefs", "task_brief_todos", "assistant_feed"} {
		var count int
		if err := database.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = count
	}
	return out
}

func TestOrganizationSnapshotPostgresColdAndWarmReadsDoNotWrite(t *testing.T) {
	database := todoTestDB(t)
	seed := newTodoEventEnv(t, database)
	setSettings(t, seed.s, func(v *Settings) { v.Main, v.Timezone = "conv_main", "UTC" })
	todo, err := seed.s.createTodo(context.Background(), "Postgres worker", "", "conv_main", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.s.linkTodoAgent(context.Background(), todo.ID, "mac", "worker", "Worker", "/home/user/project", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := seed.s.superviseTodo(context.Background(), todo.ID, "conv_main", []string{"Read contract passes"}, 0); err != nil {
		t.Fatal(err)
	}
	seed.f.setSessions("mac", orgWorkingSession("mac", "worker"))
	seed.f.answerNode(DeviceSessionItems, func(string, json.RawMessage) (any, error) { return historyPage{}, nil })

	cold := newServiceOn(t, seed.f, database)
	before := orgDatabaseCounts(t, database)
	seed.f.mu.Lock()
	modelRequests, events, starts, sends := seed.f.modelRequests, len(seed.f.events), len(seed.f.starts), len(seed.f.sends)
	seed.f.mu.Unlock()
	claims := maps.Clone(cold.admission.machineClaims)
	for read := 1; read <= 2; read++ {
		recorder := httptest.NewRecorder()
		cold.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, organizationSnapshotRoute, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("read %d: %d %s", read, recorder.Code, recorder.Body)
		}
		var snapshot orgSnapshot
		if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, node := range snapshot.Nodes {
			if node.ID == "agent:mac/worker" && node.CurrentGoal.Value != nil && *node.CurrentGoal.Value == "Read contract passes" {
				found = true
			}
		}
		if !found {
			t.Fatalf("read %d did not project the persisted worker goal", read)
		}
		if after := orgDatabaseCounts(t, database); !maps.Equal(before, after) {
			t.Fatalf("read %d changed database rows: before %v after %v", read, before, after)
		}
	}
	seed.f.mu.Lock()
	unchanged := modelRequests == seed.f.modelRequests && events == len(seed.f.events) && starts == len(seed.f.starts) && sends == len(seed.f.sends)
	seed.f.mu.Unlock()
	if !unchanged || !maps.Equal(claims, cold.admission.machineClaims) {
		t.Fatal("cold or warm read caused model, event, start, send, or admission side effects")
	}
}
