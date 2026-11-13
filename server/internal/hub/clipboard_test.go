package hub

import (
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The clipboard's routes (#3064) are for full credentials: the watch's answer-only token never reaches the manager
// with them, and no extension scope opens them.
func TestClipboardRoutesNeedFullCredentials(t *testing.T) {
	watch := principal{Role: roleAnswer}
	for _, rq := range [][2]string{
		{http.MethodGet, "/api/v1/machines/mac/clipboard"},
		{http.MethodPut, "/api/v1/machines/mac/clipboard"},
		{http.MethodGet, "/api/v1/machines/mac/clipboard/settings"},
		{http.MethodPut, "/api/v1/machines/mac/clipboard/settings"},
	} {
		if allowed(watch, rq[0], rq[1]) {
			t.Errorf("an answer-only device may %s %s", rq[0], rq[1])
		}
		if !allowed(principal{Role: roleFull}, rq[0], rq[1]) {
			t.Errorf("a full device may not %s %s", rq[0], rq[1])
		}
	}
	for _, p := range []string{"GET /api/v1/machines/{mid}/clipboard", "PUT /api/v1/machines/{mid}/clipboard",
		"GET /api/v1/machines/{mid}/clipboard/settings", "PUT /api/v1/machines/{mid}/clipboard/settings"} {
		if s, ok := extwire.RouteScopes[p]; ok {
			t.Errorf("%s is open to extensions with %s", p, s)
		}
	}
}

// The machine list says why a computer cannot share its clipboard in the reader's language.
func TestClipboardReasonLocalized(t *testing.T) {
	in := []proto.Machine{{ID: "server", Clipboard: &proto.MachineClipboard{ReasonCode: proto.ClipboardReasonNoDisplay}}, {ID: "old"}}
	en := localizedMachines(i18n.En, in)
	zh := localizedMachines(i18n.ZhHans, in)
	if en[0].Clipboard.Reason == "" || zh[0].Clipboard.Reason == "" || en[0].Clipboard.Reason == zh[0].Clipboard.Reason {
		t.Fatalf("reasons: %q %q", en[0].Clipboard.Reason, zh[0].Clipboard.Reason)
	}
	if in[0].Clipboard.Reason != "" {
		t.Fatal("the manager's snapshot was changed")
	}
	if en[1].Clipboard != nil {
		t.Fatal("an older manager's machine has no clipboard")
	}
}
