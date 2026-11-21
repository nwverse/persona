package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/db"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The usage windows and the usage sources page are the manager's (internal/devices, docs/server/usage-windows.md); the hub
// only relays them: the sidebar's route from the apps' API, the page from the admin UI with a signed-in session.

func TestAdminUsageSourcesNeedAnAdminSession(t *testing.T) {
	h, _, mux := adminTestHub(t)
	// not signed in; and the hub's own token, which opens the app's API, does not open these
	anon := &adminClient{t: t, h: mux, remote: ip4(192, 168, 1, 20) + ":1"}
	token := &adminClient{t: t, h: mux, remote: ip4(192, 168, 1, 20) + ":1", header: http.Header{"Authorization": {"Bearer " + h.cfg.Token}}}
	for _, c := range []*adminClient{anon, token} {
		for _, r := range [][2]string{
			{"GET", "/api/v1/admin/usage-sources"}, {"PUT", "/api/v1/admin/usage-sources"}, {"POST", "/api/v1/admin/usage-sources"},
			{"PUT", "/api/v1/admin/usage-sources/x"}, {"DELETE", "/api/v1/admin/usage-sources/x"}, {"POST", "/api/v1/admin/usage-sources/test"},
		} {
			if status, out, _ := c.do(r[0], r[1], `{}`); status != 401 || out["code"] != "login_required" {
				t.Errorf("%s %s: %d %v", r[0], r[1], status, out)
			}
		}
	}
	// signed in but without the CSRF token: no write
	in := signedIn(t, mux, ip4(192, 168, 1, 20)+":1")
	in.csrf = ""
	if status, out, _ := in.do("POST", "/api/v1/admin/usage-sources", `{"type":"generic","url":"https://gw.example.com","name":"g"}`); status != 403 || out["code"] != "csrf" {
		t.Errorf("csrf: %d %v", status, out)
	}
	// from outside the admin's scope the page does not exist
	outside := &adminClient{t: t, h: mux, remote: "203.0.113.9:1"}
	if status, _, _ := outside.do("GET", "/api/v1/admin/usage-sources", ""); status == 200 {
		t.Errorf("out of scope: %d", status)
	}
	// signed in, the manager away: the page answers 503 assistant_offline, as the manager's other routes
	if status, out, _ := in.do("GET", "/api/v1/admin/usage-sources", ""); status != 503 || out["code"] != "assistant_offline" {
		t.Errorf("manager away: %d %v", status, out)
	}
}

func TestUsageWindowsAreForFullDevicesOnly(t *testing.T) {
	for _, role := range []string{roleAnswer, roleLink, roleLinkPair, rolePeer, rolePeerPair} {
		if allowed(principal{Role: role}, http.MethodGet, "/api/v1/usage/windows") {
			t.Errorf("%s may read the usage windows", role)
		}
	}
	if !allowed(principal{Role: roleFull}, http.MethodGet, "/api/v1/usage/windows") {
		t.Error("a full device may")
	}
}

// The relayed page carries the hub's word that an admin session asks (a client's own header is dropped); the
// sidebar's route does not, whatever the client sends.
func TestUsageRoutesReachTheManager(t *testing.T) {
	h, srv, _ := newBusHub(t)
	got := make(chan assistantwire.HTTPRequest, 4)
	helloAssistant(t, h, srv, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method == assistantwire.MethodTools {
			return map[string]any{"tools": []any{}}, nil
		}
		var req assistantwire.HTTPRequest
		if method != assistantwire.MethodHTTP || json.Unmarshal(params, &req) != nil {
			return nil, errors.New("unexpected " + method)
		}
		got <- req
		return assistantwire.HTTPResponse{Status: 200, Header: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"accounts":[]}`)}, nil
	})
	app := &adminClient{t: t, h: srv.Config.Handler, remote: "127.0.0.1:1", header: http.Header{"Authorization": {"Bearer " + h.cfg.Token}, assistantwire.AdminHeader: {"1"}}}
	if status, _, _ := app.do("GET", "/api/v1/usage/windows", ""); status != 200 {
		t.Fatalf("windows: %d", status)
	}
	if req := <-got; req.Path != "/api/v1/usage/windows" || req.Header[assistantwire.AdminHeader] != nil {
		t.Fatalf("windows reached the manager as %+v", req)
	}
	// the admin's signed-in routes (the session is checked before them: TestAdminUsageSourcesNeedAnAdminSession)
	signedInMux := http.NewServeMux()
	h.adminUsageRoutesRelayed(signedInMux)
	admin := &adminClient{t: t, h: signedInMux, remote: "127.0.0.1:1", header: http.Header{assistantwire.AdminHeader: {"0"}}}
	if status, _, _ := admin.do("GET", "/api/v1/admin/usage-sources", ""); status != 200 {
		t.Fatalf("page: %d", status)
	}
	if req := <-got; req.Path != "/api/v1/admin/usage-sources" || len(req.Header[assistantwire.AdminHeader]) != 1 || req.Header[assistantwire.AdminHeader][0] != "1" {
		t.Fatalf("page reached the manager as %+v", req)
	}
}

// Owner automation gets exactly two operations, without opening the admin UI.
func TestOwnerUsageRoutesRejectOtherCredentials(t *testing.T) {
	h, srv, store := newBusHub(t)
	got := make(chan assistantwire.HTTPRequest, 4)
	helloAssistant(t, h, srv, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method == assistantwire.MethodTools {
			return map[string]any{"tools": []any{}}, nil
		}
		var req assistantwire.HTTPRequest
		if method != assistantwire.MethodHTTP || json.Unmarshal(params, &req) != nil {
			return nil, errors.New("unexpected method")
		}
		got <- req
		return assistantwire.HTTPResponse{Status: 200, Body: []byte(`{"ok":true,"accounts":2,"windows":3}`)}, nil
	})
	const key = "example-source-secret"
	body := `{"name":"Example","type":"relay","url":"https://gateway.example.com","key":"` + key + `"}`
	var logs strings.Builder
	h.logger = log.New(&logs, "", 0)
	for n, op := range [][2]string{{"POST", "/api/v1/usage-sources/test"}, {"PUT", "/api/v1/usage-sources/src-example"}} {
		for _, role := range []string{roleFull, roleAnswer, roleLink, roleScoped} {
			token := fmt.Sprintf("example-%d-%s", n, role)
			if err := store.CreateDeviceToken(context.Background(), db.DeviceToken{ID: token, Role: role, Kind: "other", Scopes: []string{"agents.read", "agents.control", "agents.permission_mode"}}, hashToken(token)); err != nil {
				t.Fatal(err)
			}
			c := &adminClient{t: t, h: srv.Config.Handler, remote: "127.0.0.1:1", header: http.Header{"Authorization": {"Bearer " + token}, assistantwire.AdminHeader: {"1"}}}
			status, _, w := c.do(op[0], op[1], body)
			if status != 403 {
				t.Fatalf("%s %s accepted %s: %d", op[0], op[1], role, status)
			}
			if strings.Contains(w.Body.String(), key) {
				t.Fatal("response leaked key")
			}
			// Avoid minting the same credential twice in the test database.
			if _, err := store.RevokeDeviceToken(context.Background(), token); err != nil {
				t.Fatal(err)
			}
		}
		anon := &adminClient{t: t, h: srv.Config.Handler, remote: "127.0.0.1:1"}
		if status, _, _ := anon.do(op[0], op[1], body); status != 401 {
			t.Fatalf("anonymous: %d", status)
		}
		query := &adminClient{t: t, h: srv.Config.Handler, remote: "127.0.0.1:1"}
		if status, _, _ := query.do(op[0], op[1]+"?token="+h.cfg.Token, body); status != 403 {
			t.Fatalf("query token: %d", status)
		}
		owner := &adminClient{t: t, h: srv.Config.Handler, remote: "127.0.0.1:1", header: http.Header{"Authorization": {"Bearer " + h.cfg.Token}, assistantwire.AdminHeader: {"0"}}}
		status, out, w := owner.do(op[0], op[1], body)
		if status != 200 || out["accounts"] != float64(2) || out["windows"] != float64(3) {
			t.Fatalf("owner: %d %v", status, out)
		}
		if strings.Contains(w.Body.String(), key) || strings.Contains(logs.String(), key) {
			t.Fatal("secret exposed")
		}
		req := <-got
		if req.Path != op[1] || req.Header[assistantwire.AdminHeader][0] != "1" || req.Header["Authorization"] != nil || string(req.Body) != body {
			t.Fatal("incorrect owner relay")
		}
		select {
		case <-got:
			t.Fatal("rejected request reached manager")
		default:
		}
	}
}
