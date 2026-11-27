package apierr

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The Orca chat bridge's refusals keep their generic codes (runtime_away, refused, unknown, session_gone) and are worded
// by their reason in the request's language, Orca's own words in a refusal's summary and kept as its detail.
func TestOrcaBridgeRefusalsAreWordedByReason(t *testing.T) {
	for _, tc := range []struct{ code, reason, detail string }{
		{"runtime_away", "orca_away", ""},
		{"refused", "orca_refused", "The chat is busy."},
		{"unknown", "orca_unknown", ""},
		{"session_gone", "orca_session_gone", ""},
		{"unsupported", "orca_closed", ""},
	} {
		for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
			data, _ := json.Marshal(map[string]any{"reason": tc.reason, "detail": tc.detail})
			err := &rpc.Error{Code: tc.code, Message: "node words", Data: data}
			rec := httptest.NewRecorder()
			body := call(t, langWriter{rec, lang}, rec, err)
			want := i18n.T(lang, "rpc-errors."+tc.reason, i18n.Args{"detail": tc.detail})
			if body["error"] != want || body["code"] != tc.code || want == "" || strings.Contains(want, "rpc-errors.") {
				t.Fatalf("%s %s: %v, want %q", tc.reason, lang, body, want)
			}
			if tc.detail != "" && (body["detail"] != tc.detail || !strings.Contains(want, tc.detail)) {
				t.Fatalf("%s %s: detail %v", tc.reason, lang, body)
			}
		}
	}
}
