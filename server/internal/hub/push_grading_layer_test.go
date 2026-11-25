package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

func gradingLayerHTTP(t *testing.T, h *Hub, p *principal, method, target, body string) (int, []byte) {
	t.Helper()
	mux := http.NewServeMux()
	h.pushGradingRoutes(mux)
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if p != nil {
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, *p))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func extGradingPrincipal(manager, ext string) *principal {
	return &principal{
		ID:        "extension:" + ext,
		Name:      ext,
		Role:      roleExtension,
		Extension: ext,
		Manager:   manager,
		Scopes:    []string{extension.ScopePushGrading},
	}
}

// Scope enforcement through the full hub routes and authorization middleware:
// push.grading is required for /api/v1/push/grading/layer and /api/v1/push/grading/preview,
// is excluded from notify and legacy every-scope / all-scope tokens, and never opens PUT /api/v1/push/grading.
func TestPushGradingLayerScopeAuthorization(t *testing.T) {
	h := newTestHub(t)
	h.push.store = newGradingStore()
	mux := h.routes()
	tokens := extTokensOf(t, h)

	routes := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/push/grading/layer", ""},
		{http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"hold"}}`},
		{http.MethodDelete, "/api/v1/push/grading/layer", ""},
		{http.MethodPost, "/api/v1/push/grading/preview", `{"notification":{"kind":"assistant","level":"P1"}}`},
	}

	for name, tok := range map[string]string{
		"none":         tokens.issue("ext-none", "None", nil),
		"notify-only":  tokens.issue("ext-notify", "Notify", []string{extension.ScopeNotify}),
		"legacy-every": tokens.issue("ext-every", "Every", extension.EveryScope()),
	} {
		t.Run("denied_"+name, func(t *testing.T) {
			for _, rt := range routes {
				code, body := call(t, mux, rt.method, rt.path, tok, rt.body)
				if code != http.StatusForbidden || body["code"] != "permission_denied" || body["scope"] != extension.ScopePushGrading {
					t.Fatalf("%s %s (%s): got %d %v, want 403 permission_denied scope=%s", rt.method, rt.path, name, code, body, extension.ScopePushGrading)
				}
			}
		})
	}

	granted := tokens.issue("ext-grader", "Grader", []string{extension.ScopePushGrading})
	for _, rt := range routes {
		code, body := call(t, mux, rt.method, rt.path, granted, rt.body)
		if code != http.StatusOK {
			t.Fatalf("%s %s with push.grading: got %d %v, want 200", rt.method, rt.path, code, body)
		}
	}

	// push.grading never opens the owner's switch writes.
	for _, switchPath := range []string{"/api/v1/push/grading", "/api/v1/push/ln-mode"} {
		code, body := call(t, mux, http.MethodPut, switchPath, granted, `{"enabled":true}`)
		if code != http.StatusForbidden || body["code"] != "permission_denied" {
			t.Fatalf("PUT %s with push.grading: got %d %v, want 403 permission_denied", switchPath, code, body)
		}
	}

	// Answer-only and scoped credentials are refused on all four routes.
	for _, role := range []string{roleAnswer, roleScoped} {
		p := &principal{ID: "dev-" + role, Role: role}
		for _, rt := range routes {
			code, body := gradingLayerHTTP(t, h, p, rt.method, rt.path, rt.body)
			if code != http.StatusForbidden {
				t.Fatalf("%s %s as %s: got %d %s, want 403", rt.method, rt.path, role, code, body)
			}
		}
	}

	// Full credentials can GET, DELETE, and preview, but cannot PUT an extension-owned layer.
	master := h.cfg.Token
	if code, body := call(t, mux, http.MethodPut, "/api/v1/push/grading/layer", master, `{"version":1,"actions":{"P1":"hold"}}`); code != http.StatusForbidden {
		t.Fatalf("full credential PUT layer: got %d %v, want 403", code, body)
	}
	if code, body := call(t, mux, http.MethodGet, "/api/v1/push/grading/layer", master, ""); code != http.StatusOK {
		t.Fatalf("full credential GET layer: got %d %v, want 200", code, body)
	}
	if code, body := call(t, mux, http.MethodPost, "/api/v1/push/grading/preview", master, `{"notification":{"kind":"assistant","level":"P1"}}`); code != http.StatusOK {
		t.Fatalf("full credential POST preview: got %d %v, want 200", code, body)
	}
	if code, body := call(t, mux, http.MethodDelete, "/api/v1/push/grading/layer", master, ""); code != http.StatusOK {
		t.Fatalf("full credential DELETE layer: got %d %v, want 200", code, body)
	}
}

// Strict layer ownership and manager isolation: an extension can read, write, delete and preview only its own layer
// on its own manager; naming another extension or manager is rejected with 403, and competing singleton writes return 409 layer_taken.
func TestPushGradingLayerOwnershipIsolation(t *testing.T) {
	h, _ := gradingHub(t)
	extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
	extB := extGradingPrincipal(assistantwire.AssistantID, "ext-b")
	extOtherMgr := extGradingPrincipal("manager-b", "ext-a")

	// Ext A stores its versioned singleton layer.
	code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
		`{"version":1,"actions":{"P1":"hold","P2":"push"},"dedup_window":"6h"}`)
	if code != http.StatusOK {
		t.Fatalf("extA PUT: %d %s", code, body)
	}

	// Ext B sees only its own (absent) layer, never Ext A's.
	code, body = gradingLayerHTTP(t, h, extB, http.MethodGet, "/api/v1/push/grading/layer", "")
	if code != http.StatusOK {
		t.Fatalf("extB GET: %d %s", code, body)
	}
	var gotB pushGradingLayerResponse
	if err := json.Unmarshal(body, &gotB); err != nil || gotB.Layer != nil {
		t.Fatalf("extB saw another extension's layer: %s (%v)", body, err)
	}

	// Same extension ID on manager-b also sees null (never crosses managers).
	code, body = gradingLayerHTTP(t, h, extOtherMgr, http.MethodGet, "/api/v1/push/grading/layer", "")
	if code != http.StatusOK {
		t.Fatalf("extOtherMgr GET: %d %s", code, body)
	}
	var gotOtherMgr pushGradingLayerResponse
	if err := json.Unmarshal(body, &gotOtherMgr); err != nil || gotOtherMgr.Layer != nil {
		t.Fatalf("extOtherMgr saw assistant's layer: %s (%v)", body, err)
	}

	// Ext B cannot read, overwrite, delete, or preview Ext A's layer or another manager.
	denied := []struct {
		name, method, target, body, wantCode string
	}{
		{"foreign GET query ext", http.MethodGet, "/api/v1/push/grading/layer?extension=ext-a", "", codeLayerOwnershipDenied},
		{"foreign GET query mgr", http.MethodGet, "/api/v1/push/grading/layer?manager=manager-b", "", codeOtherManager},
		{"foreign PUT query ext", http.MethodPut, "/api/v1/push/grading/layer?extension=ext-a", `{"version":1}`, codeLayerOwnershipDenied},
		{"foreign PUT query mgr", http.MethodPut, "/api/v1/push/grading/layer?manager=manager-b", `{"version":1}`, codeOtherManager},
		{"foreign DELETE query ext", http.MethodDelete, "/api/v1/push/grading/layer?extension=ext-a", "", codeLayerOwnershipDenied},
		{"foreign DELETE query mgr", http.MethodDelete, "/api/v1/push/grading/layer?manager=manager-b", "", codeOtherManager},
		{"foreign preview draft ext", http.MethodPost, "/api/v1/push/grading/preview", `{"notification":{"level":"P1"},"layer":{"extension":"ext-a","enabled":false}}`, codeLayerOwnershipDenied},
		{"foreign preview query ext", http.MethodPost, "/api/v1/push/grading/preview?extension=ext-a", `{"notification":{"level":"P1"}}`, codeLayerOwnershipDenied},
		{"foreign preview notif mgr", http.MethodPost, "/api/v1/push/grading/preview", `{"notification":{"level":"P1","manager":"manager-b"}}`, codeOtherManager},
	}
	for _, tc := range denied {
		code, raw := gradingLayerHTTP(t, h, extB, tc.method, tc.target, tc.body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		if code != http.StatusForbidden || out["code"] != tc.wantCode {
			t.Fatalf("%s: got %d %s, want 403 code=%q", tc.name, code, raw, tc.wantCode)
		}
	}

	// Competing PUT from Ext B or from ext-a on manager-b returns 409 layer_taken and leaves Ext A's layer intact.
	for name, p := range map[string]*principal{"other ext": extB, "other manager": extOtherMgr} {
		code, raw := gradingLayerHTTP(t, h, p, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"silence"}}`)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		if code != http.StatusConflict || out["code"] != codePushGradingLayerTaken {
			t.Fatalf("%s competing PUT: got %d %s, want 409 code=%q", name, code, raw, codePushGradingLayerTaken)
		}
	}

	// Extension principal without an extension ID is refused.
	noExt := &principal{Role: roleExtension, Manager: assistantwire.AssistantID}
	if code, raw := gradingLayerHTTP(t, h, noExt, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1}`); code != http.StatusForbidden {
		t.Fatalf("empty extension identity: got %d %s, want 403", code, raw)
	}

	// Ext B deleting its own (non-existent) layer leaves Ext A's layer untouched.
	code, body = gradingLayerHTTP(t, h, extB, http.MethodDelete, "/api/v1/push/grading/layer", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"removed":false`) {
		t.Fatalf("extB DELETE own empty: %d %s", code, body)
	}
	snap, err := readPushGradingSnapshot(context.Background(), h.push.store)
	if err != nil || snap.Layer == nil || snap.Layer.Source != "extension:ext-a" || snap.Layer.Manager != assistantwire.AssistantID {
		t.Fatalf("extB DELETE removed extA's layer: %+v err=%v", snap, err)
	}

	// Full credential inspects the singleton layer and respects manager/extension filters.
	code, body = gradingLayerHTTP(t, h, nil, http.MethodGet, "/api/v1/push/grading/layer", "")
	if code != http.StatusOK {
		t.Fatalf("owner GET: %d %s", code, body)
	}
	var ownerView pushGradingLayerResponse
	if err := json.Unmarshal(body, &ownerView); err != nil || ownerView.Layer == nil || ownerView.Layer.Source != "extension:ext-a" || ownerView.Layer.Revision != 1 {
		t.Fatalf("owner GET = %s (%v)", body, err)
	}
	code, body = gradingLayerHTTP(t, h, nil, http.MethodGet, "/api/v1/push/grading/layer?manager=manager-b", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"layer":null`) {
		t.Fatalf("owner GET other manager = %d %s, want layer:null", code, body)
	}
}

// Layer updates persist durably in hub.push_grading_layer across reloads, bump revision on renewal,
// never touch hub.ln_mode, and leave stored state unchanged on store failure.
func TestPushGradingLayerPersistenceAcrossReload(t *testing.T) {
	h, _ := gradingHub(t)
	store := h.push.store.(*gradingStore)
	flaky := &flakyGradingLayerStore{gradingStore: store}
	h.push.store = flaky
	extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

	code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
		`{"version":1,"actions":{"P1":"hold","P2":"push"},"dedup_window":"30m"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT v1: %d %s", code, body)
	}
	var res1 pushGradingLayerResponse
	if err := json.Unmarshal(body, &res1); err != nil || res1.Layer == nil || res1.Layer.Revision != 1 || res1.Layer.Source != "extension:ext-a" {
		t.Fatalf("PUT v1 response = %s (%v)", body, err)
	}
	store.mu.Lock()
	_, legacyExists := store.vals[gradingLayersSettingKey]
	store.mu.Unlock()
	if legacyExists {
		t.Fatal("PUT wrote to legacy hub.push_grading.layers key")
	}

	// Read from a fresh hub sharing the same store.
	restarted := &Hub{push: newPusher(t.TempDir(), store, t.Logf)}
	code, body = gradingLayerHTTP(t, restarted, extA, http.MethodGet, "/api/v1/push/grading/layer", "")
	if code != http.StatusOK {
		t.Fatalf("restarted GET: %d %s", code, body)
	}
	var got pushGradingLayerResponse
	if err := json.Unmarshal(body, &got); err != nil || got.Layer == nil || got.Layer.Revision != 1 ||
		got.Layer.Config.Actions.value.P2.value != "push" || got.Layer.Config.DedupWindow.value != 30*time.Minute {
		t.Fatalf("reloaded layer = %s (%v)", body, err)
	}

	// Same-owner renewal increments revision and preserves explicit null resets.
	code, body = gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
		`{"version":1,"actions":{"P1":null,"P2":"silence"},"dedup_window":null}`)
	if code != http.StatusOK {
		t.Fatalf("PUT v2: %d %s", code, body)
	}
	var res2 pushGradingLayerResponse
	if err := json.Unmarshal(body, &res2); err != nil || res2.Layer == nil || res2.Layer.Revision != 2 ||
		!res2.Layer.Config.Actions.value.P1.null || !res2.Layer.Config.DedupWindow.null {
		t.Fatalf("PUT v2 response = %s (%v)", body, err)
	}

	// A store write failure returns 503 and does NOT mutate stored state.
	flaky.mu.Lock()
	flaky.failPut = true
	flaky.mu.Unlock()
	code, body = gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"push"}}`)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("PUT with failing store: %d %s, want 503", code, body)
	}
	flaky.mu.Lock()
	flaky.failPut = false
	flaky.mu.Unlock()
	snap, err := readPushGradingSnapshot(context.Background(), store)
	if err != nil || snap.Layer == nil || snap.Layer.Revision != 2 || snap.Layer.Config.Actions.value.P2.value != "silence" {
		t.Fatalf("stored layer changed after failed store write: %+v err=%v", snap, err)
	}

	// Delete persists across reload too.
	if code, body = gradingLayerHTTP(t, h, extA, http.MethodDelete, "/api/v1/push/grading/layer", ""); code != http.StatusOK || !strings.Contains(string(body), `"removed":true`) {
		t.Fatalf("DELETE: %d %s", code, body)
	}
	restarted3 := &Hub{push: newPusher(t.TempDir(), store, t.Logf)}
	code, body = gradingLayerHTTP(t, restarted3, extA, http.MethodGet, "/api/v1/push/grading/layer", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"layer":null`) {
		t.Fatalf("deleted layer returned after reload: %d %s", code, body)
	}
}

// Removing a layer (by the extension or by the owner) OR disabling / uninstalling / revoking push.grading
// (via extwire.EventReport) removes hub.push_grading_layer durably without ever mutating hub.ln_mode.
func TestPushGradingLayerRemovalAndExtensionTeardownRestoresOwnerSettings(t *testing.T) {
	t.Run("owner switch off preserved across claim and delete", func(t *testing.T) {
		h, _ := gradingHub(t)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"hold"}}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}
		if h.push.pushGradingOn() {
			t.Fatal("extension layer flipped owner's hub.ln_mode switch")
		}
		if code, body := gradingLayerHTTP(t, h, nil, http.MethodGet, "/api/v1/push/grading", ""); code != http.StatusOK || string(body) != "{\"enabled\":false}\n" {
			t.Fatalf("GET /api/v1/push/grading = %d %s, want enabled:false", code, body)
		}
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodDelete, "/api/v1/push/grading/layer", ""); code != http.StatusOK || !strings.Contains(string(body), `"removed":true`) {
			t.Fatalf("DELETE: %d %s", code, body)
		}
		if snap, err := readPushGradingSnapshot(context.Background(), h.push.store); err != nil || snap.Layer != nil {
			t.Fatalf("layer remained after delete: %+v err=%v", snap, err)
		}
	})

	t.Run("owner switch on preserved across claim and owner delete", func(t *testing.T) {
		h, _ := gradingHub(t)
		if err := h.push.setPushGrading(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"push"}}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}
		if !h.push.pushGradingOn() {
			t.Fatal("extension layer disabled owner's hub.ln_mode switch")
		}
		// Owner removes the extension's layer with full credentials -> switch stays on, layer is gone.
		if code, body := gradingLayerHTTP(t, h, nil, http.MethodDelete, "/api/v1/push/grading/layer?extension=ext-a", ""); code != http.StatusOK || !strings.Contains(string(body), `"removed":true`) {
			t.Fatalf("owner DELETE: %d %s", code, body)
		}
		if !h.push.pushGradingOn() {
			t.Fatal("owner DELETE flipped owner's hub.ln_mode switch")
		}
		if snap, err := readPushGradingSnapshot(context.Background(), h.push.store); err != nil || snap.Layer != nil {
			t.Fatalf("layer remained after owner delete: %+v err=%v", snap, err)
		}
	})

	t.Run("extension disable or uninstall via EventReport prunes singleton layer durably", func(t *testing.T) {
		h, _ := gradingHub(t)
		store := h.push.store.(*gradingStore)
		if err := h.push.setPushGrading(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
		extB := extGradingPrincipal(assistantwire.AssistantID, "ext-b")

		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P2":"push"}}`); code != http.StatusOK {
			t.Fatalf("extA PUT: %d %s", code, body)
		}

		// An older manager report without PushGradingDeclared leaves the layer intact.
		rawOld, _ := json.Marshal(extwire.Report{EventsDeclared: true})
		busEvents[extwire.EventReport](h, nil, rawOld)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("reconciler did not settle after legacy report")
		}
		if snap, err := readPushGradingSnapshot(ctx, store); err != nil || snap.Layer == nil || snap.Layer.Source != "extension:ext-a" {
			t.Fatalf("legacy report without PushGradingDeclared pruned layer: %+v err=%v", snap, err)
		}

		// Report that still lists ext-a keeps the layer intact.
		rawKeepA, _ := json.Marshal(extwire.Report{PushGradingDeclared: true, PushGradingExtensions: []string{"ext-a", "ext-b"}})
		busEvents[extwire.EventReport](h, nil, rawKeepA)
		if !h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("reconciler did not settle after keep report")
		}
		if snap, err := readPushGradingSnapshot(ctx, store); err != nil || snap.Layer == nil || snap.Layer.Source != "extension:ext-a" {
			t.Fatalf("active ext-a layer was pruned: %+v err=%v", snap, err)
		}

		// Report with ext-a disabled/uninstalled/scope-removed (only ext-b active) prunes ext-a durably.
		rawDropA, _ := json.Marshal(extwire.Report{PushGradingDeclared: true, PushGradingExtensions: []string{"ext-b"}})
		busEvents[extwire.EventReport](h, nil, rawDropA)
		if !h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("reconciled layer prune did not finish")
		}
		if snap, err := readPushGradingSnapshot(ctx, store); err != nil || snap.Layer != nil {
			t.Fatalf("ext-a layer still present after EventReport dropped ext-a: %+v err=%v", snap, err)
		}
		if !h.push.pushGradingOn() {
			t.Fatal("lifecycle prune mutated owner hub.ln_mode")
		}

		// Now ext-b can claim the singleton layer with revision 1, and dropping all extensions prunes it too.
		code, body := gradingLayerHTTP(t, h, extB, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"silence"}}`)
		if code != http.StatusOK || !strings.Contains(string(body), `"revision":1`) {
			t.Fatalf("extB claim after prune: %d %s", code, body)
		}
		rawDropAll, _ := json.Marshal(extwire.Report{PushGradingDeclared: true})
		busEvents[extwire.EventReport](h, nil, rawDropAll)
		if !h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("final layer prune did not finish")
		}
		if snap, err := readPushGradingSnapshot(ctx, store); err != nil || snap.Layer != nil {
			t.Fatalf("ext-b layer still present after empty report: %+v err=%v", snap, err)
		}
	})
}

func seedLegacyGradingLayer(t *testing.T, h *Hub, p *principal, raw string) {
	t.Helper()
	var in pushGradingLayerInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("decode legacy test layer: %v", err)
	}
	layer, err := in.buildLayer(p.Manager, p.Extension, h.push.now())
	if err != nil {
		t.Fatalf("build legacy test layer: %v", err)
	}
	if _, err := h.push.putGradingLayer(context.Background(), layer); err != nil {
		t.Fatalf("seed legacy test layer: %v", err)
	}
}

// Combination safety: system alerts always pass, active owner attention modes are never weakened by extension layers,
// multiple extension layers compose deterministically (stricter wins), disabled layers are inert, and custom dedup works.
func TestPushGradingLayerCombinesSafelyWithOwnerControls(t *testing.T) {
	t.Run("system alerts bypass catch-all silent layer", func(t *testing.T) {
		h, f := gradingHub(t)
		attempts := gradingAttempts(h)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
		seedLegacyGradingLayer(t, h, extA, `{"grading_enabled":true,"rules":[{"when":{},"action":"silent"}]}`)
		h.push.sendLocalized(func(i18n.Lang) map[string]any {
			return map[string]any{"aps": map[string]any{"alert": map[string]any{"title": "Computer joined"}, "sound": "default"}}
		}, "", nil)
		if *attempts != 1 {
			t.Fatalf("system alert attempts = %d, want 1", *attempts)
		}
		f.calls(t, 1)
	})

	t.Run("owner attention mode is never weakened by extension allow or P0 promotion", func(t *testing.T) {
		h, f := gradingHub(t)
		attempts := gradingAttempts(h)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
		seedLegacyGradingLayer(t, h, extA,
			`{"rules":[{"id":"force-allow","when":{"related_key_prefix":"vip:"},"level":"P0","action":"allow"},{"id":"hold-focused","when":{"related_key_prefix":"noisy:"},"action":"hold","reason":"ext_noisy"}]}`)
		// Immersive focus on qa-plain: out-of-focus vip push is STILL held by immersive mode.
		setAttention(h, assistantwire.AttentionImmersive, time.Hour, &assistantwire.AttentionFocus{Track: "qa-plain"})
		nOut := trackNotification(assistantwire.LevelP1, "qa-other", "", "out of focus vip")
		nOut.RelatedKey = "vip:1"
		out, err := h.deliverPushFrom(assistantwire.AssistantID, nOut)
		if err != nil || heldOf(out) != assistantwire.AttentionReasonImmersiveHeld || *attempts != 0 {
			t.Fatalf("immersive out-of-focus vip: out=%+v err=%v attempts=%d", out, err, *attempts)
		}
		// In-focus P0 that extension explicitly holds IS held (stricter wins).
		nNoisy := trackNotification(assistantwire.LevelP0, "qa-plain", "", "in focus noisy")
		nNoisy.RelatedKey = "noisy:1"
		out, err = h.deliverPushFrom(assistantwire.AssistantID, nNoisy)
		if err != nil || heldOf(out) != "ext_noisy" || *attempts != 0 {
			t.Fatalf("immersive in-focus extension hold: out=%+v err=%v attempts=%d", out, err, *attempts)
		}
		// Quiet mode: even an allowed P0 is STILL hushed (no sound, passive).
		setAttention(h, assistantwire.AttentionQuiet, time.Hour, nil)
		nQuiet := gradingNotification(assistantwire.LevelP0, "vip:quiet", "quiet p0")
		if err := h.deliverPush(nQuiet); err != nil {
			t.Fatal(err)
		}
		calls := f.calls(t, 1)
		if aps := apsOf(t, calls[0]); aps["sound"] != nil || aps["interruption-level"] != "passive" {
			t.Fatalf("quiet mode P0 was not hushed: %+v", aps)
		}
	})

	t.Run("multiple layers compose deterministically with stricter winning", func(t *testing.T) {
		h, _ := gradingHub(t)
		extAllow := extGradingPrincipal(assistantwire.AssistantID, "ext-alpha")
		extHold := extGradingPrincipal(assistantwire.AssistantID, "ext-beta")
		extSilent := extGradingPrincipal(assistantwire.AssistantID, "ext-gamma")

		seedLegacyGradingLayer(t, h, extAllow, `{"rules":[{"id":"allow-pr","when":{"related_key_prefix":"pr:"},"action":"allow"}]}`)
		seedLegacyGradingLayer(t, h, extHold, `{"rules":[{"id":"hold-pr","when":{"related_key_prefix":"pr:"},"action":"hold","reason":"ext_hold_pr"}]}`)
		dec := h.push.previewPushGrading(pushPolicy{
			kind: assistantwire.NotifyPush, manager: assistantwire.AssistantID, level: assistantwire.LevelP1, relatedKey: "pr:9",
		}, nil, false)
		if dec.Allow || dec.Action != pushGradingActionHold || dec.Reason != "ext_hold_pr" || dec.MatchedLayer == nil || dec.MatchedLayer.Extension != "ext-beta" {
			t.Fatalf("allow + hold composition = %+v, want hold by ext-beta", dec)
		}

		// Adding a silent rule beats hold.
		seedLegacyGradingLayer(t, h, extSilent, `{"rules":[{"id":"silent-pr","when":{"related_key_prefix":"pr:"},"action":"silent","reason":"ext_silent_pr"}]}`)
		dec2 := h.push.previewPushGrading(pushPolicy{
			kind: assistantwire.NotifyPush, manager: assistantwire.AssistantID, level: assistantwire.LevelP1, relatedKey: "pr:9",
		}, nil, false)
		if dec2.Allow || dec2.Action != pushGradingActionSilent || dec2.Reason != "ext_silent_pr" || dec2.MatchedLayer == nil || dec2.MatchedLayer.Extension != "ext-gamma" {
			t.Fatalf("allow + hold + silent composition = %+v, want silent by ext-gamma", dec2)
		}
	})

	t.Run("disabled layer is inert and custom dedup window works without suppressing P0", func(t *testing.T) {
		h, f := gradingHub(t)
		attempts := gradingAttempts(h)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

		// Disabled layer does not filter pushes.
		seedLegacyGradingLayer(t, h, extA, `{"enabled":false,"grading_enabled":true}`)
		if err := h.deliverPush(gradingNotification(assistantwire.LevelP1, "k-dis", "disabled layer")); err != nil {
			t.Fatal(err)
		}
		if *attempts != 1 {
			t.Fatalf("disabled layer held push: attempts=%d", *attempts)
		}

		// Custom 20h dedup window (72000s): after 15h (>12h default, <20h layer), non-P0 repeat is still deduped,
		// while P0 still interrupts.
		seedLegacyGradingLayer(t, h, extA, `{"enabled":true,"grading_enabled":true,"dedup_window_seconds":72000}`)
		base := h.push.now()
		if err := h.deliverPush(gradingNotification(assistantwire.LevelP0, "pr:dedup", "first p0")); err != nil {
			t.Fatal(err)
		}
		moveGradingClock(h, base.Add(15*time.Hour))
		out, err := h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "pr:dedup", "repeat p1"))
		if err != nil || heldOf(out) != interceptReasonDedup {
			t.Fatalf("15h repeat under 20h layer: out=%+v err=%v, want ln_dedup_12h", out, err)
		}
		// P0 with the same key still interrupts.
		out, err = h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP0, "pr:dedup", "second p0"))
		if err != nil || heldOf(out) != "" || *attempts != 3 {
			t.Fatalf("P0 under 20h dedup: out=%+v err=%v attempts=%d, want delivered", out, err, *attempts)
		}
		f.calls(t, 3)
	})

	t.Run("HTTP notify matches alert kind and calling extension", func(t *testing.T) {
		h, _ := gradingHub(t)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
		seedLegacyGradingLayer(t, h, extA, `{"rules":[{"id":"mute-ext-bot","when":{"kinds":["alert"],"extension":"noisy-bot"},"action":"silent","reason":"ext_bot_muted"}]}`)
		res, err := h.deliverNotify(notifyRequest{Title: "Ping", Body: "hello", Level: levelActive, PushLevel: assistantwire.LevelP0}, "", "noisy-bot", assistantwire.AssistantID)
		if err != nil || res["sent"] != false || res["held"] != true || res["reason"] != "ext_bot_muted" {
			t.Fatalf("deliverNotify = %+v, err=%v; want held ext_bot_muted", res, err)
		}
	})
}

// POST /api/v1/push/grading/preview returns the combined decision (including draft layers, dedup hits, main
// conversation promotion, and active attention modes) with ZERO side effects on any notification, dedup,
// intercept, delivered-push, layer, WebSocket, or store state.
func TestPushGradingPreviewMatchesDeliveryAndWritesZeroState(t *testing.T) {
	h, f := gradingHub(t)
	h.clients = map[*client]struct{}{}
	mainGradingProfile(h)
	store := h.push.store.(*gradingStore)
	wsClient := &client{device: "web-preview", out: make(chan []byte, 8), meta: clientMeta{features: map[string]bool{"ws_notify": true}}}
	addTestClient(h, wsClient)
	extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

	// Seed one real P0 push (with owner switch on) so dedup state & sent counts are non-empty before previewing.
	if err := h.push.setPushGrading(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := h.deliverPush(gradingNotification(assistantwire.LevelP0, "pr:existing", "real p0")); err != nil {
		t.Fatal(err)
	}
	f.calls(t, 1)
	// Drain the WS notify frame emitted by the initial real push before snapshotting.
	<-wsClient.out
	if !h.push.waitInterceptsSaved(2 * time.Second) {
		t.Fatal("initial push state did not save")
	}
	seedLegacyGradingLayer(t, h, extA, `{"rules":[{"id":"vip-p0","when":{"related_key_prefix":"vip:"},"level":"P0"}]}`)

	// Snapshot every piece of mutable state before running previews.
	snapshotState := func() (intercepts []heldPushRecord, stats []pushDayStats, lastAlert map[string]time.Time, layers []PushGradingLayer, storeVals map[string][]byte, sentRows int) {
		intercepts = h.push.heldPushesView("", 0)
		stats = h.push.pushDailyStats(h.push.now().Add(-24*time.Hour), "")
		h.push.gradingMu.Lock()
		lastAlert = maps.Clone(h.push.gradingLastAlert)
		h.push.gradingMu.Unlock()
		layers = h.push.listGradingLayers("")
		store.mu.Lock()
		storeVals = make(map[string][]byte, len(store.vals))
		for k, v := range store.vals {
			storeVals[k] = append([]byte(nil), v...)
		}
		store.mu.Unlock()
		sentRows = len(store.sentTable())
		return
	}
	beforeIntercepts, beforeStats, beforeLastAlert, beforeLayers, beforeStore, beforeSentRows := snapshotState()
	attempts := gradingAttempts(h)

	preview := func(p *principal, body string) PushGradingDecision {
		t.Helper()
		code, raw := gradingLayerHTTP(t, h, p, http.MethodPost, "/api/v1/push/grading/preview", body)
		if code != http.StatusOK {
			t.Fatalf("preview %s: got %d %s", body, code, raw)
		}
		var dec PushGradingDecision
		if err := json.Unmarshal(raw, &dec); err != nil {
			t.Fatalf("decode preview %s: %v", raw, err)
		}
		return dec
	}

	// 1. Persisted layer promotes vip: P1 to P0 -> allowed, source=extension, matched_layer=ext-a/vip-p0.
	d1 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"vip:urgent"}}`)
	if !d1.Allow || d1.Held || d1.EffectiveLevel != assistantwire.LevelP0 || d1.Source != pushGradingSourceExtension ||
		d1.MatchedLayer == nil || d1.MatchedLayer.Extension != "ext-a" || d1.MatchedLayer.RuleID != "vip-p0" ||
		d1.MatchedLayer.RuleIndex == nil || *d1.MatchedLayer.RuleIndex != 0 {
		t.Fatalf("preview 1 (persisted vip) = %+v", d1)
	}

	// 2. Unmatched P1 -> held by owner switch.
	d2 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"regular:1"}}`)
	if d2.Allow || !d2.Held || d2.Reason != interceptReasonP1 || d2.Source != pushGradingSourceOwner || d2.MatchedLayer != nil {
		t.Fatalf("preview 2 (unmatched P1) = %+v", d2)
	}

	// 3. Existing P0 key -> dedup hit detected read-only.
	d3 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"pr:existing"}}`)
	if d3.Allow || !d3.Held || !d3.DedupHit || d3.Reason != interceptReasonDedup {
		t.Fatalf("preview 3 (dedup hit) = %+v", d3)
	}

	// 4. Previewing a fresh P0 key returns allow=true AND does NOT record it into gradingLastAlert!
	d4 := preview(extA, `{"notification":{"kind":"assistant","level":"P0","related_key":"pr:never-sent"}}`)
	if !d4.Allow || d4.EffectiveLevel != assistantwire.LevelP0 {
		t.Fatalf("preview 4 (fresh P0) = %+v", d4)
	}

	// 5. Main conversation level-less notify_user promotes to P0 in preview just like live delivery.
	d5 := preview(extA, `{"notification":{"kind":"assistant","machine_id":"mac","session_id":"main-conv"}}`)
	if !d5.Allow || d5.InputLevel != assistantwire.LevelP0 || d5.EffectiveLevel != assistantwire.LevelP0 {
		t.Fatalf("preview 5 (main conversation) = %+v", d5)
	}

	// 6. Draft layer overrides ext-a's persisted layer for this preview only (without saving it).
	d6 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"vip:urgent"},"layer":{"enabled":false}}`)
	if d6.Allow || !d6.Held || d6.Reason != interceptReasonP1 || d6.Source != pushGradingSourceOwner {
		t.Fatalf("preview 6 (draft disabled layer) = %+v", d6)
	}
	d7 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"draft:1"},"layer":{"rules":[{"id":"draft-silent","when":{"related_key_prefix":"draft:"},"action":"silent","reason":"ext_draft_mute"}]}}`)
	if d7.Allow || d7.Action != pushGradingActionSilent || d7.Reason != "ext_draft_mute" || d7.MatchedLayer == nil || d7.MatchedLayer.RuleID != "draft-silent" {
		t.Fatalf("preview 7 (draft silent rule) = %+v", d7)
	}

	// Verify ZERO state mutation after all 7 preview calls.
	afterIntercepts, afterStats, afterLastAlert, afterLayers, afterStore, afterSentRows := snapshotState()
	if *attempts != 0 {
		t.Fatalf("preview triggered %d APNs attempts, want 0", *attempts)
	}
	f.calls(t, 0)
	select {
	case msg := <-wsClient.out:
		t.Fatalf("preview emitted WebSocket frame: %s", msg)
	default:
	}
	if !reflect.DeepEqual(beforeIntercepts, afterIntercepts) {
		t.Fatalf("preview mutated intercepts: before=%+v after=%+v", beforeIntercepts, afterIntercepts)
	}
	if !reflect.DeepEqual(beforeStats, afterStats) {
		t.Fatalf("preview mutated daily stats: before=%+v after=%+v", beforeStats, afterStats)
	}
	if !reflect.DeepEqual(beforeLastAlert, afterLastAlert) {
		t.Fatalf("preview mutated gradingLastAlert: before=%+v after=%+v", beforeLastAlert, afterLastAlert)
	}
	if !reflect.DeepEqual(beforeLayers, afterLayers) {
		t.Fatalf("preview mutated persisted layers: before=%+v after=%+v", beforeLayers, afterLayers)
	}
	if !reflect.DeepEqual(beforeStore, afterStore) || beforeSentRows != afterSentRows {
		t.Fatalf("preview wrote to store: sentRows %d→%d, store changed=%v", beforeSentRows, afterSentRows, !reflect.DeepEqual(beforeStore, afterStore))
	}
}

// Strict input validation for PUT /api/v1/push/grading/layer and POST /api/v1/push/grading/preview.
func TestPushGradingLayerValidation(t *testing.T) {
	h, _ := gradingHub(t)
	extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

	badLayers := map[string]string{
		"empty object":          `{}`,
		"not an object":         `[]`,
		"unsupported version":   `{"version":2}`,
		"unknown field":         `{"version":1,"extra":1}`,
		"legacy field":          `{"grading_enabled":true}`,
		"owner-only enabled":    `{"version":1,"enabled":true}`,
		"owner-only null":       `{"version":1,"enabled":null}`,
		"extension holds P0":    `{"version":1,"actions":{"P0":"hold"}}`,
		"extension silences P0": `{"version":1,"actions":{"P0":"silence"}}`,
		"duplicate key":         `{"version":1,"version":1}`,
		"case colliding key":    `{"version":1,"Version":1}`,
		"case colliding action": `{"version":1,"actions":{"P1":"hold","p1":"push"}}`,
		"trailing json":         `{"version":1}{"version":1}`,
		"bad action":            `{"version":1,"actions":{"P1":"drop"}}`,
		"dedup negative":        `{"version":1,"dedup_window":"-1s"}`,
		"dedup too large":       `{"version":1,"dedup_window":"48h1s"}`,
		"dedup non-duration":    `{"version":1,"dedup_window":"12"}`,
		"bad notify level":      `{"version":1,"notify_user":{"main":"P9"}}`,
		"duplicate exemption":   `{"version":1,"dedup_exempt_levels":["P0","P0"]}`,
		"bad escalate reason":   `{"version":1,"cards":{"escalate_reasons":["other"]}}`,
		"bad report level":      `{"version":1,"cards":{"report_level":"urgent"}}`,
	}
	for name, body := range badLayers {
		code, raw := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", body)
		if code != http.StatusBadRequest {
			t.Errorf("PUT %s: got %d %s, want 400", name, code, raw)
		}
	}

	// Oversized request body (>16KiB) returns 413.
	huge := `{"version":1,"dedup_window":"` + strings.Repeat("1", pushGradingLayerBodyLimit+128) + `"}`
	if code, raw := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", huge); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PUT oversized: got %d %s, want 413", code, raw)
	}

	// Bad preview inputs.
	badPreviews := map[string]string{
		"unknown field":     `{"notification":{"level":"P1"},"unknown":true}`,
		"bad kind":          `{"notification":{"kind":"bogus"}}`,
		"bad level":         `{"notification":{"level":"P9"}}`,
		"bad depth":         `{"notification":{"depth":"huge"}}`,
		"bad draft layer":   `{"notification":{"level":"P1"},"layer":{"dedup_window_seconds":1}}`,
		"control char key":  `{"notification":{"related_key":"a\u0001b"}}`,
		"duplicate preview": `{"notification":{"level":"P1"},"notification":{"level":"P2"}}`,
	}
	for name, body := range badPreviews {
		code, raw := gradingLayerHTTP(t, h, extA, http.MethodPost, "/api/v1/push/grading/preview", body)
		if code != http.StatusBadRequest {
			t.Errorf("preview %s: got %d %s, want 400", name, code, raw)
		}
	}

	// Bad DELETE revision query parameter returns 400.
	for _, badRev := range []string{"0", "-1", "abc"} {
		if code, raw := gradingLayerHTTP(t, h, extA, http.MethodDelete, "/api/v1/push/grading/layer?revision="+badRev, ""); code != http.StatusBadRequest {
			t.Fatalf("DELETE revision=%s: got %d %s, want 400", badRev, code, raw)
		}
	}
}

// Concurrent PUT/GET/DELETE/preview/applyPushGrading/reconcilePushGradingLayer must be race-free.
func TestPushGradingLayerConcurrentAccess(t *testing.T) {
	h, _ := gradingHub(t)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := range 6 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			extName := fmt.Sprintf("ext-%d", id%3)
			p := extGradingPrincipal(assistantwire.AssistantID, extName)
			for j := range 25 {
				switch j % 5 {
				case 0:
					code, raw := gradingLayerHTTP(t, h, p, http.MethodPut, "/api/v1/push/grading/layer",
						`{"version":1,"actions":{"P1":"hold"}}`)
					if code != http.StatusOK && code != http.StatusConflict {
						errs <- fmt.Errorf("PUT %s: %d %s", extName, code, bytes.TrimSpace(raw))
						return
					}
				case 1:
					code, raw := gradingLayerHTTP(t, h, p, http.MethodGet, "/api/v1/push/grading/layer", "")
					if code != http.StatusOK {
						errs <- fmt.Errorf("GET %s: %d %s", extName, code, bytes.TrimSpace(raw))
						return
					}
				case 2:
					code, raw := gradingLayerHTTP(t, h, p, http.MethodPost, "/api/v1/push/grading/preview",
						`{"notification":{"kind":"assistant","level":"P1","related_key":"k:1"}}`)
					if code != http.StatusOK {
						errs <- fmt.Errorf("preview %s: %d %s", extName, code, bytes.TrimSpace(raw))
						return
					}
				case 3:
					h.push.applyPushGrading(pushPolicy{
						kind: assistantwire.NotifyPush, manager: assistantwire.AssistantID, level: assistantwire.LevelP1, relatedKey: "k:1",
					})
				case 4:
					if id%2 == 0 {
						code, raw := gradingLayerHTTP(t, h, p, http.MethodDelete, "/api/v1/push/grading/layer", "")
						if code != http.StatusOK {
							errs <- fmt.Errorf("DELETE %s: %d %s", extName, code, bytes.TrimSpace(raw))
							return
						}
					} else {
						h.reconcilePushGradingLayer(assistantwire.AssistantID, []string{"ext-0", "ext-2"})
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !h.waitPushGradingLayerReconciled(ctx) {
		t.Fatal("background layer reconciliation did not settle")
	}
}
