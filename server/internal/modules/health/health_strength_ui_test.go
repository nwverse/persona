package health

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

func strengthPageHTTP(t *testing.T, s *Service, p strengthPageState, action string, values map[string]string, version int) wire.ActionResult {
	t.Helper()
	raw, _ := json.Marshal(p)
	return intakePageResult(t, intakePageHTTP(s, "", action, values, string(raw), version, "full"))
}
func strengthState(t *testing.T, r wire.ActionResult) strengthPageState {
	t.Helper()
	p, ok := readStrengthPage(r.View.State)
	if !ok {
		t.Fatal(r.View.State)
	}
	return p
}
func TestStrengthPagesUndoScopeAndDeleteConfirmation(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	other := e
	other.ID = healthIntakeUUID("other-exercise")
	other.Name = "Row"
	if _, _, err := d.HealthStrengthExerciseSave(ctx, other); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sets, err := d.HealthStrengthSetAdd(ctx, []assistantwire.StrengthSet{{ID: healthIntakeUUID("page-recent"), ExerciseID: e.ID, Weight: 40, Reps: 10, At: now}, {ID: healthIntakeUUID("page-old"), ExerciseID: e.ID, Weight: 50, Reps: 8, At: now.AddDate(0, 0, -100)}, {ID: healthIntakeUUID("page-other"), ExerciseID: other.ID, Weight: 20, Reps: 12, At: now}})
	if err != nil {
		t.Fatal(err)
	}
	p := strengthPageState{Page: "strength.exercise", Exercise: e.ID, Days: 30, Metric: "weight"}
	for _, id := range []string{sets[1].ID, sets[2].ID} {
		strengthPageHTTP(t, s, p, "undo:"+id, nil, 11)
	}
	rows, _ := d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{})
	if len(rows) != 3 {
		t.Fatal("forged undo removed another page's set")
	}
	strengthPageHTTP(t, s, p, "undo:"+sets[0].ID, nil, 1)
	rows, _ = d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{})
	if len(rows) != 2 {
		t.Fatal("listed undo failed")
	}
	p = strengthPageState{Page: "strength.overview", Days: 30, Metric: "weight", Manage: true}
	res := strengthPageHTTP(t, s, p, "", nil, 11)
	confirmed := false
	for _, sec := range res.View.Sections {
		for _, v := range sec.Items {
			if v.Action != nil && v.Action.ID == "delete:"+e.ID {
				confirmed = strings.Contains(v.Action.Confirm, e.Name) && strings.Contains(v.Action.Confirm, "1")
			}
		}
	}
	if !confirmed {
		t.Fatal("delete does not name exercise and its active sets")
	}
	strengthPageHTTP(t, s, p, "delete:"+e.ID, nil, 11)
	rows, _ = d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{Exercise: e.ID})
	if len(rows) != 0 {
		t.Fatal("delete kept active sets")
	}
	all, _ := d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{Exercise: other.ID})
	if len(all) != 1 {
		t.Fatal("delete touched another exercise")
	}
}
func TestStrengthEditPageWorkingCopyCardSyncAndStale(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	out, err := strengthCall(s, ctx, fmt.Sprintf(`{"action":"draft","sets":[{"exercise":%q,"weight":40,"reps":10}]}`, e.ID))
	if err != nil {
		t.Fatal(err)
	}
	var ids struct{ Draft, Card string }
	json.Unmarshal([]byte(out), &ids)
	p := strengthPageState{Page: "strength.edit", Draft: ids.Draft, Days: 30, Metric: "weight", Rev: 1}
	res := strengthPageHTTP(t, s, p, "set:0", map[string]string{"weight_0": "42.5", "reps_0": "9", "warmup_0": "true"}, 11)
	p = strengthState(t, res)
	res = strengthPageHTTP(t, s, p, "add", nil, 11)
	p = strengthState(t, res)
	if len(p.Work) != 2 || *p.Work[0].Weight != 42.5 {
		t.Fatal("working copy lost")
	}
	card, _ := d.AssistantView(ctx, "training", ids.Card)
	if card.Version != 1 {
		t.Fatal("unsaved edit changed card")
	}
	res = strengthPageHTTP(t, s, p, "remove:1", nil, 11)
	p = strengthState(t, res)
	res = strengthPageHTTP(t, s, p, "save", nil, 11)
	if !res.Pop {
		t.Fatal("save did not pop")
	}
	saved, _ := d.HealthStrengthDraft(ctx, ids.Draft)
	card, _ = d.AssistantView(ctx, "training", ids.Card)
	if saved.Revision != 2 || card.Version != 2 || !strings.Contains(string(card.View), "42.5 kg × 9") {
		t.Fatal("draft/card not synchronized", saved, string(card.View))
	}
	rows, _ := d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{})
	if len(rows) != 0 {
		t.Fatal("edit confirmed sets")
	}
	stale := strengthPageHTTP(t, s, p, "save", nil, 11)
	if stale.Toast == "" || stale.Pop || strengthState(t, stale).Rev != 2 {
		t.Fatal("stale revision overwrote draft")
	}
	legacy := strengthPageHTTP(t, s, p, "", nil, 5)
	raw, _ := json.Marshal(legacy)
	if !strings.Contains(string(raw), "Update the app") || strings.Contains(string(raw), `"control"`) {
		t.Fatal("old app got controls")
	}
	if _, err = s.CardAction(ctx, assistantwire.HealthCardAction{Conversation: "training", Key: ids.Card, Version: 2, Action: "discard", ClientMsgID: "discard", Declared: true}); err != nil {
		t.Fatal(err)
	}
	settled := strengthPageHTTP(t, s, p, "", nil, 11)
	raw, _ = json.Marshal(settled)
	if !strings.Contains(string(raw), "discarded") || strings.Contains(string(raw), `"control"`) {
		t.Fatal("settled draft editable")
	}
}
func TestStrengthPagesLanguagesVersionsAndStates(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	now := time.Now()
	d.HealthStrengthSetAdd(ctx, []assistantwire.StrengthSet{{ID: healthIntakeUUID("localization-set"), ExerciseID: e.ID, Weight: 40, Reps: 10, At: now}})
	out, err := strengthCall(s, ctx, fmt.Sprintf(`{"action":"draft","sets":[{"exercise":%q,"weight":40,"reps":10},{"exercise":%q,"weight":35,"reps":12}]}`, e.ID, e.ID))
	if err != nil {
		t.Fatal(err)
	}
	var ids struct{ Draft string }
	json.Unmarshal([]byte(out), &ids)
	for _, lang := range []string{"en", "zh-Hans", "ja"} {
		for _, version := range []int{1, 5, 6, 11} {
			for _, page := range []string{"strength.home", "strength.overview", "strength.exercise", "strength.edit"} {
				p := strengthPageState{Page: page, Exercise: e.ID, Draft: ids.Draft, Days: 30, Metric: "weight"}
				raw, _ := json.Marshal(p)
				body, _ := json.Marshal(wire.UIRequest{State: string(raw), Context: &wire.UIContext{Locale: lang, UIVersion: version}})
				res := intakePageResult(t, intakeHTTP(s, "POST", "/api/v1/assistant/health/intake/ui", string(body), "full"))
				b, _ := json.Marshal(res)
				if strings.Contains(string(b), "assistant-health_strength.") {
					t.Fatal("missing localization", string(b))
				}
			}
		}
	}
	for _, raw := range []string{`{"page":"strength.bad"}`, `{"page":"strength.exercise","exercise":"bad"}`, `{"page":"strength.edit","draft":"bad"}`, `{"page":"strength.home","days":1}`, `{"page":"strength.home","offset":-1}`} {
		if _, ok := readStrengthPage(raw); ok {
			t.Fatal("invalid state accepted", raw)
		}
	}
	// The page remains readable after removing the item; existing data is retained.
	if err := d.PutSetting(ctx, healthItemsKey, healthItemsSetting{Items: []string{}, Revision: 2}); err != nil {
		t.Fatal(err)
	}
	strengthPageHTTP(t, s, strengthPageState{Page: "strength.overview", Days: 30, Metric: "weight"}, "", nil, 11)
}
