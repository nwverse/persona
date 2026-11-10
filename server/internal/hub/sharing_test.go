package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestShareOriginAndIndependentDispatcher(t *testing.T) {
	for _, origin := range []string{"http://share.example.test:18661", "https://share.example.test"} {
		if canonicalShareOrigin(origin) != origin {
			t.Fatal("configured HTTP/HTTPS rejected")
		}
	}
	for _, origin := range []string{"ftp://share.example.test", "http://user:password@share.example.test", "http://share.example.test/path", "http://share.example.test?token=x", "http://share.example.test#fragment", "http://share.example.test:", "http://share.example.test:70000"} {
		if canonicalShareOrigin(origin) != "" {
			t.Fatal("invalid sharing origin accepted")
		}
	}
	cfg := &Config{PublicURL: "http://share.example.test:18660"}
	cfg.dir = t.TempDir()
	cfg.Sharing = &SharingConfig{PublicOrigin: "http://share.example.test:18661"}
	h := &Hub{cfg: cfg}
	if h.sharingOrigin() != cfg.Sharing.PublicOrigin {
		t.Fatal("same host different port rejected")
	}
	cfg.PublicURL = cfg.Sharing.PublicOrigin
	if h.sharingOrigin() != "" {
		t.Fatal("same origin allowed")
	}
	cfg.PublicURL = "http://share.example.test:18660"
	called := 0
	handler := h.sharingDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(204) }))
	for _, p := range []string{"/", "/admin", "/api/v1/health", "/api/v1/auth/tokens", "/api/v1/ws", "/sw.js", "/manifest.webmanifest", "/share/invalid", "/share-assets/missing.js", "/public-share/index.html"} {
		r := httptest.NewRequest("GET", "http://share.example.test:18661"+p, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 404 || called != 0 {
			t.Fatalf("public dispatcher fell through: %s %d", p, w.Code)
		}
		for _, key := range []string{"Cache-Control", "Content-Security-Policy", "Referrer-Policy", "X-Content-Type-Options", "X-Robots-Tag"} {
			if w.Header().Get(key) == "" {
				t.Fatalf("missing %s on error", key)
			}
		}
		if w.Header().Get("Set-Cookie") != "" {
			t.Fatal("public cookies")
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "http://share.example.test:18660/", nil))
	if w.Code != 204 || called != 1 {
		t.Fatal("authenticated app dispatcher changed")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "http://share.example.test:18660/share/invalid", nil))
	if w.Code != 404 || called != 1 {
		t.Fatal("app-origin share used authenticated shell")
	}
}
func TestShareLogMasksCapabilityShapes(t *testing.T) {
	for _, p := range []string{"/share/synthetic-secret", "/api/public/agent-shares/synthetic-secret", "/share/synthetic-secret/extra"} {
		if strings.Contains(loggedPath(p), "synthetic-secret") || strings.Contains(routeOf(httptest.NewRequest("GET", p, nil)), "synthetic-secret") {
			t.Fatal("capability in logs")
		}
	}
}
func TestShareIngressBoundedAdmission(t *testing.T) {
	var s shareIngress
	now := time.Now()
	for i := 0; i < 20; i++ {
		done, ok := s.enter("synthetic-source", "synthetic-hash", now)
		if !ok {
			t.Fatal("premature IP limit")
		}
		done()
	}
	if _, ok := s.enter("synthetic-source", "synthetic-hash", now); ok {
		t.Fatal("IP burst not enforced")
	}
	done, ok := s.enter("synthetic-source", "synthetic-hash", now.Add(time.Minute))
	if !ok {
		t.Fatal("rate did not replenish")
	}
	done()
}
