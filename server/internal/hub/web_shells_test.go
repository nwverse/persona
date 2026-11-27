package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
)

func TestWebShellServingSecurityAndEscapeHatch(t *testing.T) {
	builtinFS := fstest.MapFS{
		"index.html":       {Data: []byte(`<html><base href="/" />BUILTIN</html>`)},
		"sw.js":            {Data: []byte(`self.addEventListener("fetch",()=>{});`)},
		"pake/client.wasm": {Data: []byte("WASM_BYTES")},
	}
	builtin := webFrom(t.TempDir(), builtinFS)

	customEnabled := true
	bundle := extwire.WebShellBundle{
		Extension:     "focus-ui",
		ExtensionName: "Focus UI",
		Version:       "1.0.0",
		Item:          []byte(`{"id":"focus-4layer","name":"Focus Workspace","dir":"web"}`),
		Files: map[string][]byte{
			"index.html":    []byte(`<html><base href="/" />CUSTOM_FOCUS</html>`),
			"assets/app.js": []byte(`window.__focus=1;`),
			"sw.js":         []byte(`// rogue sw`),
		},
	}
	bundle.Digest = extension.WebShellDigest(bundle.Files)
	cached, err := buildCachedWebShell("", "assistant", bundle, extension.WebShellItem{ID: "focus-4layer", Name: "Focus Workspace", Dir: "web"})
	if err != nil {
		t.Fatal(err)
	}
	h := webWithShells(builtin, func(_ context.Context, ext, id string) (*cachedWebShell, bool) {
		if customEnabled && ext == "focus-ui" && id == "focus-4layer" {
			return cached, true
		}
		return nil, false
	})

	// 1. Default without cookie serves built-in Web UI and allows built-in Service Worker fetches.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/overview", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "BUILTIN") {
		t.Fatalf("default should serve builtin: %d %q", w.Code, w.Body.String())
	}
	reqBuiltinSW := httptest.NewRequest("GET", "/sw.js", nil)
	reqBuiltinSW.Header.Set("Service-Worker", "script")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqBuiltinSW)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "addEventListener") {
		t.Fatalf("built-in SW should work when no custom shell is active: %d %q", w.Code, w.Body.String())
	}

	// 2. Cross-site ?shell=<ext>/<id> and empty ?shell= MUST NOT set or clear cookies; they only strip the query param.
	for _, raw := range []string{"/overview?shell=focus-ui/focus-4layer&tab=work", "/overview?shell=&tab=work"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", raw, nil))
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/overview?tab=work" {
			t.Fatalf("non-builtin ?shell redirect for %q: %d %q", raw, w.Code, w.Header().Get("Location"))
		}
		if cookies := w.Result().Cookies(); len(cookies) != 0 {
			t.Fatalf("non-builtin %q must not emit Set-Cookie, got %+v", raw, cookies)
		}
	}

	// 3. Same-origin cookie (both raw "ext/id" and percent-encoded "ext%2Fid") activates the custom shell + headers + /pake/* fallback, while blocking SW registration.
	shellCookie := &http.Cookie{Name: webShellCookie, Value: "focus-ui/focus-4layer"}
	for _, cookieVal := range []string{"focus-ui/focus-4layer", "focus-ui%2Ffocus-4layer"} {
		req := httptest.NewRequest("GET", "/agents/123", nil)
		req.AddCookie(&http.Cookie{Name: webShellCookie, Value: cookieVal})
		req.Header.Set("X-Forwarded-Prefix", "/persona")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "CUSTOM_FOCUS") || !strings.Contains(w.Body.String(), `<base href="/persona/" />`) {
			t.Fatalf("custom shell SPA index for cookie %q: %d %q", cookieVal, w.Code, w.Body.String())
		}
		if got := w.Header().Get(webShellHeader); got != "focus-ui/focus-4layer" {
			t.Fatalf("expected %s header, got %q", webShellHeader, got)
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "private") || !strings.Contains(cc, "no-cache") {
			t.Fatalf("custom shell Cache-Control must be private no-cache, got %q", cc)
		}
	}

	// Custom shell MUST NOT be allowed to fetch/register any Service Worker script on Hub origin.
	reqCustomSW := httptest.NewRequest("GET", "/sw.js", nil)
	reqCustomSW.AddCookie(shellCookie)
	reqCustomSW.Header.Set("Service-Worker", "script")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqCustomSW)
	if w.Code != http.StatusNotFound {
		t.Fatalf("custom shell Service-Worker script fetch must return 404, got %d", w.Code)
	}

	reqAsset := httptest.NewRequest("GET", "/assets/app.js", nil)
	reqAsset.AddCookie(shellCookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqAsset)
	if w.Code != http.StatusOK || w.Body.String() != "window.__focus=1;" || w.Header().Get(webShellHeader) != "focus-ui/focus-4layer" {
		t.Fatalf("custom shell asset: %d %q %q", w.Code, w.Body.String(), w.Header().Get(webShellHeader))
	}
	if cc := w.Header().Get("Cache-Control"); cc != "private, no-cache" || w.Header().Get("ETag") == "" {
		t.Fatalf("custom shell assets must revalidate via ETag across upgrades, got Cache-Control=%q ETag=%q", cc, w.Header().Get("ETag"))
	}

	reqPake := httptest.NewRequest("GET", "/pake/client.wasm", nil)
	reqPake.AddCookie(shellCookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqPake)
	if w.Code != http.StatusOK || w.Body.String() != "WASM_BYTES" {
		t.Fatalf("pake fallback failed: %d %q", w.Code, w.Body.String())
	}

	reqMissingAsset := httptest.NewRequest("GET", "/assets/missing.js", nil)
	reqMissingAsset.AddCookie(shellCookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqMissingAsset)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing custom asset should 404, got %d", w.Code)
	}

	// 4. Disabling/uninstalling the extension falls back seamlessly to builtin even with the cookie still present.
	customEnabled = false
	reqDisabled := httptest.NewRequest("GET", "/", nil)
	reqDisabled.AddCookie(shellCookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqDisabled)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "BUILTIN") {
		t.Fatalf("disabled shell should fallback to builtin: %d %q", w.Code, w.Body.String())
	}

	// 5. ?shell=builtin clears cookie, sends Clear-Site-Data: "cache", and redirects strictly same-origin.
	customEnabled = true
	reqEscape := httptest.NewRequest("GET", "/overview?shell=builtin", nil)
	reqEscape.AddCookie(shellCookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqEscape)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/overview" {
		t.Fatalf("escape hatch redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
	if csd := w.Header().Get("Clear-Site-Data"); csd != `"cache"` {
		t.Fatalf("escape hatch should clear cache only, got %q", csd)
	}
	escCookies := w.Result().Cookies()
	if len(escCookies) != 1 || escCookies[0].Name != webShellCookie || escCookies[0].MaxAge >= 0 {
		t.Fatalf("escape hatch should expire cookie, got %+v", escCookies)
	}

	// 6. Deep-link %2F preservation + open-redirect hardening.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/kb/meetings/team%2Fmeeting?shell=builtin", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/kb/meetings/team%2Fmeeting" {
		t.Fatalf("deep-link %%2F should be preserved on reset redirect, got %d %q", w.Code, w.Header().Get("Location"))
	}
	for _, raw := range []string{
		"/%5Cevil.com?shell=builtin",
		"/%5c%5cevil.com/path?shell=builtin",
		"///evil.com?shell=builtin",
		"/%2f/evil.com?shell=builtin",
		"/foo%00bar?shell=builtin",
	} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", raw, nil))
		loc := w.Header().Get("Location")
		if w.Code != http.StatusFound || !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") || strings.Contains(loc, `\`) {
			t.Fatalf("unsafe redirect for %q: %d %q", raw, w.Code, loc)
		}
	}
}

func TestWebShellMultiManagerPrecedenceAndDedup(t *testing.T) {
	// Default manager "m-beta" vs non-default "m-alpha": discovery (writeMergedWebShells) and serving (resolveWebShellRef)
	// must agree on default-first order AND include shells installed only on non-default managers.
	h := &Hub{}
	h.owners.def = "m-beta"
	h.owners.until = time.Now().Add(time.Hour) // keep cached default owner without DB

	h.keepExtReport("m-alpha", &extwire.Report{
		WebShells: []extwire.WebShellRef{
			{Extension: "shared-ext", Version: "1.0.0", Item: []byte(`{"id":"main","name":"Alpha Main","dir":"web"}`), Digest: "d-alpha"},
			{Extension: "alpha-only", Version: "1.0.0", Item: []byte(`{"id":"solo","name":"Alpha Solo","dir":"web"}`), Digest: "d-solo"},
		},
	})
	h.keepExtReport("m-beta", &extwire.Report{
		WebShells: []extwire.WebShellRef{
			{Extension: "shared-ext", Version: "2.0.0", Item: []byte(`{"id":"main","name":"Beta Main","dir":"web"}`), Digest: "d-beta"},
		},
	})

	// 1. Serving resolution: shared-ext/main resolves to default manager m-beta; alpha-only/solo resolves to m-alpha.
	mgr, ref, _, ok := h.resolveWebShellRef(context.Background(), "shared-ext", "main")
	if !ok || mgr != "m-beta" || ref.Version != "2.0.0" {
		t.Fatalf("shared shell should resolve to default manager m-beta, got ok=%v mgr=%q ver=%q", ok, mgr, ref.Version)
	}
	mgr, ref, _, ok = h.resolveWebShellRef(context.Background(), "alpha-only", "solo")
	if !ok || mgr != "m-alpha" || ref.Digest != "d-solo" {
		t.Fatalf("non-default shell should resolve to m-alpha, got ok=%v mgr=%q digest=%q", ok, mgr, ref.Digest)
	}

	// 2. Discovery merge (answers arrive in default-first order: m-beta then m-alpha): deduplicated with exact same winner.
	answers := []listAnswer{
		{id: "m-beta", status: http.StatusOK, body: []byte(`{"web_shells":[{"extension":"shared-ext","id":"main","key":"shared-ext/main","version":"2.0.0","name":"Beta Main"}]}`)},
		{id: "m-alpha", status: http.StatusOK, body: []byte(`{"web_shells":[{"extension":"shared-ext","id":"main","key":"shared-ext/main","version":"1.0.0","name":"Alpha Main"},{"extension":"alpha-only","id":"solo","key":"alpha-only/solo","version":"1.0.0","name":"Alpha Solo"}]}`)},
	}
	w := httptest.NewRecorder()
	writeMergedWebShells(w, answers)
	if w.Code != http.StatusOK {
		t.Fatalf("writeMergedWebShells status: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		WebShells []map[string]any `json:"web_shells"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.WebShells) != 2 {
		t.Fatalf("expected 2 deduplicated web shells, got %d: %+v", len(out.WebShells), out.WebShells)
	}
	if out.WebShells[0]["key"] != "shared-ext/main" || out.WebShells[0]["manager"] != "m-beta" || out.WebShells[0]["version"] != "2.0.0" {
		t.Fatalf("first shell should match default manager m-beta: %+v", out.WebShells[0])
	}
	if out.WebShells[1]["key"] != "alpha-only/solo" || out.WebShells[1]["manager"] != "m-alpha" {
		t.Fatalf("second shell should include non-default manager m-alpha: %+v", out.WebShells[1])
	}

	// 3. When default manager m-beta is offline (fetch error) and uncached, serving falls through to online m-alpha
	// (matching mergeList skipping 503 managers).
	shell, ok := h.findWebShellWithFetch(context.Background(), "shared-ext", "main", func(_ context.Context, mgrID string) (extwire.WebShellBundle, error) {
		if mgrID == "m-beta" {
			return extwire.WebShellBundle{}, errors.New("assistant_offline")
		}
		return extwire.WebShellBundle{
			Extension: "shared-ext",
			Version:   "1.0.0",
			Item:      []byte(`{"id":"main","name":"Alpha Main","dir":"web"}`),
			Digest:    "d-alpha",
			Files:     map[string][]byte{"index.html": []byte("<html>ALPHA_FALLBACK</html>")},
		}, nil
	})
	if !ok || !strings.Contains(string(shell.fs["index.html"].Data), "ALPHA_FALLBACK") {
		t.Fatalf("expected offline default manager to fall through to online m-alpha, got ok=%v", ok)
	}
}
