package assistant

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHealthStrengthPageCoreFitsAndModuleAbsent(t *testing.T) {
	s, f, d, _, key := strengthCardHarness(t)
	draft := strings.TrimPrefix(key, "health-strength-")
	for _, version := range []int{1, 5, 6, 11} {
		for _, page := range []string{"strength.edit", "strength.overview", "strength.home"} {
			raw, _ := json.Marshal(map[string]any{"page": page, "draft": draft})
			res := intakePageResult(t, intakePageHTTP(s, "", "", nil, string(raw), version, "full"))
			b, _ := json.Marshal(res.View)
			if version < 6 && strings.Contains(string(b), `"control"`) {
				t.Fatal("old app got controls")
			}
		}
	}
	off := New(Options{Hub: f, Store: func() Store { return d }, ModulesOff: []string{"health"}})
	if w := intakePageHTTP(off, "", "", nil, `{"page":"strength.overview"}`, 11, "full"); w.Code != 404 {
		t.Fatal("absent module served page", w.Code)
	}
}
