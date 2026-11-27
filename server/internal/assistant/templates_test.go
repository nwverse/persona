package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func templateCall(t *testing.T, s *Service, body, role string) (int, []templateSettingResult) {
	t.Helper()
	w := boardCall(s, "POST", "/api/v1/templates/settings", body, role)
	var out struct {
		Results []templateSettingResult `json:"results"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.Results
}

// A template's settings part (#16772): a dry run says what each item would do and writes nothing; applying writes each
// changed item through its persona_settings setter and says so; applying again finds nothing left to change.
func TestTemplateSettingsDryRunThenApply(t *testing.T) {
	s, _ := navigationService(t, newSettingsBoardStore())
	ctx := context.Background()
	if !s.mainTasksQuiet(ctx) {
		t.Fatal("main_tasks_quiet should start on")
	}
	items := `[{"key":"assistant.main_tasks_quiet","value":false},{"key":"assistant.recent_task_chats","action":"set","value":false}]`

	code, got := templateCall(t, s, `{"dry_run":true,"settings":`+items+`}`, roleFull)
	if code != http.StatusOK || len(got) != 2 {
		t.Fatalf("dry run: %d %+v", code, got)
	}
	if got[0].Status != templateChange || got[0].Text == "" || got[0].Title == "" || got[0].Action != "set" {
		t.Fatalf("dry run of a change: %+v", got[0])
	}
	if got[1].Status != templateUnchanged {
		t.Fatalf("dry run of a setting already at the value: %+v", got[1])
	}
	if !s.mainTasksQuiet(ctx) {
		t.Fatal("a dry run wrote the setting")
	}

	code, got = templateCall(t, s, `{"settings":`+items+`}`, roleFull)
	if code != http.StatusOK || got[0].Status != templateApplied || got[1].Status != templateUnchanged {
		t.Fatalf("apply: %d %+v", code, got)
	}
	if s.mainTasksQuiet(ctx) {
		t.Fatal("apply did not write the setting")
	}
	if _, got = templateCall(t, s, `{"settings":`+items+`}`, roleFull); got[0].Status != templateUnchanged {
		t.Fatalf("applying again: %+v", got[0])
	}
}

// Items a template may not change are answered one by one and the rest still run: a key this manager does not write,
// a setting that grants permissions or speaks for the owner, a value or action its setter refuses.
func TestTemplateSettingsStatuses(t *testing.T) {
	s, _ := navigationService(t, newSettingsBoardStore())
	body := `{"dry_run":true,"settings":[
		{"key":"no.such.setting","value":1},
		{"key":"` + standingAuthorizationsKey + `","value":{}},
		{"key":"` + outboundIdentityKey + `","value":"user"},
		{"key":"assistant.main_tasks_quiet","value":"loud"},
		{"key":"assistant.main_tasks_quiet","action":"drop","value":true},
		{"key":"assistant.main_tasks_quiet","value":false}
	]}`
	code, got := templateCall(t, s, body, roleFull)
	want := []string{templateUnsupported, templateNotAllowed, templateNotAllowed, templateInvalid, templateInvalid, templateChange}
	if code != http.StatusOK || len(got) != len(want) {
		t.Fatalf("%d %+v", code, got)
	}
	for i, status := range want {
		if got[i].Status != status {
			t.Errorf("item %d (%s): %s, want %s (%+v)", i, got[i].Key, got[i].Status, status, got[i])
		}
	}
	if got[3].Error == "" || got[4].Error == "" {
		t.Errorf("refused items say why: %+v %+v", got[3], got[4])
	}
}

// Only a device with full access applies a template; a body without settings or with too many is refused whole.
func TestTemplateSettingsRefusals(t *testing.T) {
	s, _ := navigationService(t, newSettingsBoardStore())
	if code, _ := templateCall(t, s, `{"settings":[{"key":"assistant.main_tasks_quiet","value":false}]}`, roleAnswer); code != http.StatusForbidden {
		t.Fatalf("an answer-only device: %d, want 403", code)
	}
	if !s.mainTasksQuiet(context.Background()) {
		t.Fatal("a refused request wrote the setting")
	}
	many := "[" + strings.TrimSuffix(strings.Repeat(`{"key":"assistant.main_tasks_quiet","value":true},`, maxTemplateSettings+1), ",") + "]"
	for _, bad := range []string{`{}`, `{"settings":null}`, `nope`, `{"settings":` + many + `}`} {
		if code, _ := templateCall(t, s, bad, roleFull); code != http.StatusBadRequest {
			t.Fatalf("%.40s…: %d, want 400", bad, code)
		}
	}
}
