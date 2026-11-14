package assistant

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A recommendation the node refused with a code of its own (a dialog open in the agent's terminal) answers 409 with
// that code, as every other node call does (apierr.Call), and says what to do in the request's language. It used to be
// 502 "internal" with the reason folded into the sentence, which the web app showed as "the service could not complete
// this request (HTTP 502)" however often it was pressed (#7343). Nothing is taken, and the button works again.
func TestAcceptRouteSaysTheTerminalDialogBlocksIt(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.ZhHans, i18n.En} {
		s, f, todo, _, _ := conclusionService(t, false)
		waitsForReview(t, s, f, todo.ID, "两种走法：A 先灰度，B 直接全量。")                                                            // i18n-ignore
		reviewTask(t, s, todo.ID, map[string]any{"verdict": "needs_user", "reason": "先灰度还是直接全量", "recommend": "先灰度 10%"}) // i18n-ignore
		a := askOf(s, todo.ID)
		f.mu.Lock()
		f.failSend = &rpc.Error{Code: "dialog_open", Message: "a dialog is open in the terminal"}
		f.mu.Unlock()
		asked, _ := json.Marshal(a.AskedAt)
		status, body := serveJSON(t, s, "POST", "/api/v1/todos/"+todo.ID+"/accept", string(lang), `{"asked_at":`+string(asked)+`}`)
		want := i18n.T(lang, "rpc-errors.dialog_open")
		if status != http.StatusConflict || body["code"] != "dialog_open" || body["error"] != want || body["delivered"] != "none" {
			t.Fatalf("%s: %d %v, want 409 dialog_open %q", lang, status, body, want)
		}
		if got := askOf(s, todo.ID); !got.AcceptedAt.IsZero() {
			t.Fatalf("%s: taken after all: %+v", lang, got)
		}
	}
}
