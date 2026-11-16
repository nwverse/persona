package assistant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/remoteimage"
)

// The remote image route (#9473): full devices only, addresses refused before anything is fetched, every refusal a
// code with a sentence, a picture answered so that nothing runs or sniffs it.
func TestRemoteImageRoute(t *testing.T) {
	s := newTestService(t, newFakeHub())
	get := func(role, addr string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/remote-image?size=512&url="+url.QueryEscape(addr), nil)
		if role != "" {
			r.Header.Set(RoleHeader, role)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	codeOf := func(w *httptest.ResponseRecorder) string {
		var body struct{ Code, Error string }
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body.Error == "" {
			t.Errorf("%d %s: no sentence", w.Code, w.Body)
		}
		return body.Code
	}
	for _, role := range []string{roleExtension, "limited"} {
		if w := get(role, "https://img.example.com/a.png"); w.Code != http.StatusForbidden {
			t.Errorf("as %s: %d, want 403", role, w.Code)
		}
	}
	for addr, want := range map[string]string{
		"":                           remoteimage.CodeBadURL,
		"file:///etc/passwd":         remoteimage.CodeBadURL,
		"https://u:p@img.test/a.png": remoteimage.CodeBadURL,
		"http://127.0.0.1:9/a.png":   remoteimage.CodeNotAllowed,
		"http://[::1]/a.png":         remoteimage.CodeNotAllowed,
	} {
		for _, role := range []string{"", roleFull} {
			w := get(role, addr)
			if got := codeOf(w); got != want || w.Code >= 500 {
				t.Errorf("%q as %q: %d %s, want %s", addr, role, w.Code, got, want)
			}
		}
	}
}

func TestRemoteImageAnswers(t *testing.T) {
	w := httptest.NewRecorder()
	writeRemoteImage(w, &remoteimage.Image{Type: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff, 0}})
	h := w.Result().Header
	if w.Code != http.StatusOK || h.Get("Content-Type") != "image/jpeg" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || h.Get("Cache-Control") != "private, max-age=86400" || w.Body.Len() != 4 {
		t.Fatalf("picture answered %d %v", w.Code, h)
	}
	w = httptest.NewRecorder()
	writeRemoteImageError(w, &remoteimage.Error{Code: remoteimage.CodeRateLimited, Status: http.StatusTooManyRequests, RetryAfter: 1500 * time.Millisecond})
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("limit answered %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
}
