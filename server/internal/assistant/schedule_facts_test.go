package assistant

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
)

// The facts setting of a routine (schedule.go): on for the default routines of a new hub, turned on for a hub set up before
// only where the routine still holds a default prompt.

// A routine an owner edited keeps its facts off, even when it started from the old default: only a morning or evening routine
// that still holds an earlier default prompt, byte for byte, gets the current one with its facts on.
func TestUpgradeLeavesAnEditedRoutineWithoutFacts(t *testing.T) {
	formerM := prompts.FormerMorningRoutines()
	tasks := []Task{
		{ID: "morning", Repeat: repeatDaily, Prompt: formerM[len(formerM)-1] + "\n另外提醒我喝水。"},
		{ID: "evening", Repeat: repeatDaily, Prompt: "我自己写的晚报"},
	}
	if upgradeDefaultRoutines(tasks) {
		t.Fatal("an edited prompt was upgraded")
	}
	if tasks[0].Facts || tasks[1].Facts {
		t.Fatalf("facts were turned on for an edited routine: %+v", tasks)
	}
}

// A hub set up before, whose morning and evening routines still hold a default prompt, gets the current one and facts on, in
// the stored list too; a routine that holds another task's text under another id is not touched.
func TestUpgradeTurnsFactsOnForADefaultPrompt(t *testing.T) {
	formerM, formerE := prompts.FormerMorningRoutines(), prompts.FormerEveningRoutines()
	tasks := []Task{
		{ID: "morning", Repeat: repeatDaily, Prompt: formerM[0]},
		{ID: "evening", Repeat: repeatDaily, Prompt: formerE[len(formerE)-1]},
		{ID: "rt_mine", Repeat: repeatDaily, Prompt: formerM[1]},
	}
	if !upgradeDefaultRoutines(tasks) {
		t.Fatal("nothing upgraded")
	}
	if !tasks[0].Facts || !tasks[1].Facts || tasks[2].Facts {
		t.Fatalf("facts: morning %v evening %v other %v, want on, on, off", tasks[0].Facts, tasks[1].Facts, tasks[2].Facts)
	}
	if tasks[0].Prompt != prompts.RenderMorningRoutine("") {
		t.Fatalf("the prompt is the current default: %q", tasks[0].Prompt)
	}

	store := &flakyScheduleStore{data: map[string][]byte{}}
	if err := store.PutSetting(context.Background(), tasksKey, []Task{{ID: "morning", Title: "早报", Repeat: repeatDaily, Time: "09:00",
		Enabled: true, Prompt: formerM[len(formerM)-1]}}); err != nil {
		t.Fatal(err)
	}
	h := newServiceOn(t, newFakeHub(), store)
	if got, _ := taskByID(h, "morning"); !got.Facts {
		t.Fatalf("loaded: facts off on a default routine: %+v", got)
	}
	var saved []Task
	if _, err := store.Setting(context.Background(), tasksKey, &saved); err != nil || len(saved) != 1 || !saved[0].Facts {
		t.Fatalf("saved: %+v %v", saved, err)
	}
}

// A new hub starts with facts on for both routines.
func TestNewHubRoutinesHaveFactsOn(t *testing.T) {
	h := newServiceOn(t, newFakeHub(), &flakyScheduleStore{data: map[string][]byte{}})
	for _, id := range []string{"morning", "evening"} {
		if got, ok := taskByID(h, id); !ok || !got.Facts {
			t.Fatalf("%s: %+v", id, got)
		}
	}
}

// An app that does not know the setting leaves it out of a change and the task keeps it; one that sends it sets it.
func TestEditingATaskKeepsOrSetsItsFacts(t *testing.T) {
	h := newStorelessService(t, newFakeHub().withMachine())
	setTasks(h, Task{ID: "morning", Title: "早报", Prompt: "p", Repeat: repeatDaily, Time: "09:00", Enabled: true, Facts: true})
	put := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/assistant/tasks/morning", strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	put(`{"title":"早报","prompt":"p","repeat":"daily","time":"09:30","enabled":true}`)
	if got, _ := taskByID(h, "morning"); !got.Facts || got.Time != "09:30" {
		t.Fatalf("a change without the setting lost it: %+v", got)
	}
	put(`{"title":"早报","prompt":"p","repeat":"daily","time":"09:30","enabled":true,"facts":false}`)
	if got, _ := taskByID(h, "morning"); got.Facts {
		t.Fatalf("facts did not go off: %+v", got)
	}
	put(`{"title":"早报","prompt":"p","repeat":"daily","time":"09:30","enabled":true,"facts":true}`)
	if got, _ := taskByID(h, "morning"); !got.Facts {
		t.Fatalf("facts did not go on: %+v", got)
	}
}
