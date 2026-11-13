package hub

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestNodeOutdatedMatchesPreviousResponse(t *testing.T) {
	h := newTestHub(t)
	for _, lang := range []string{"", "en", "ja", "zh-Hans"} {
		t.Run(lang, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Accept-Language", lang)
			got, want := httptest.NewRecorder(), httptest.NewRecorder()
			key := "hub-fileedit.outdated"
			args := i18n.Args{"name": "computer"}
			if !h.nodeOutdated(got, r, &rpc.Error{Code: "unknown_method", Message: "unknown method file.rename"}, key, args) {
				t.Fatal("unknown method was not recognized")
			}
			writeCode(want, http.StatusConflict, proto.CodeNodeOutdated, i18n.T(h.requestLang(r), key, args), nil)
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Fatalf("got %d %q, want %d %q", got.Code, got.Body.String(), want.Code, want.Body.String())
			}
		})
	}
}
