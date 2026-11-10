package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestShareTrustedRelayAndAnonymousCenterDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := setupTestDB(t)
	h := newTestHub(t)
	h.store = store
	h.cfg.PublicURL = "http://app.example.test:18660"
	h.cfg.Sharing = &SharingConfig{PublicOrigin: "http://share.example.test:18661"}
	if err := h.initHubID(ctx); err != nil {
		t.Fatal(err)
	}
	issuer := h.hubID()
	subject, err := store.CreateOwnerSubject(ctx, issuer, "Synthetic owner", "synthetic-admin", "Synthetic acceptance")
	if err != nil {
		t.Fatal(err)
	}
	const deviceToken = "synthetic-enrolled-device-token"
	const credential = "synthetic-enrolled-device"
	if err = store.CreateDeviceToken(ctx, db.DeviceToken{ID: credential, Name: "Synthetic device", Role: roleFull, Kind: "web"}, hashToken(deviceToken)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnrollOwner(ctx, issuer, credential, subject.Subject, "synthetic-admin", "Synthetic enrollment", 0); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveBusIdentity(ctx, db.BusIdentity{ID: devicesOwner, Role: "manager", Name: "Synthetic manager"}, "synthetic-manager-hash"); err != nil {
		t.Fatal(err)
	}
	const machine = "synthetic-relay-machine"
	h.cfg.Machines = []MachineConfig{{ID: machine, Kind: "local"}}
	h.byID[machine] = &Machine{cfg: MachineConfig{ID: machine}}
	route, err := store.ShareRouting(ctx, issuer, machine, devicesOwner)
	if err != nil {
		t.Fatal(err)
	}
	rawToken := make([]byte, 48)
	copy(rawToken, route.Handle)
	if _, err = rand.Read(rawToken[16:]); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	hubIn, managerOut := io.Pipe()
	managerIn, hubOut := io.Pipe()
	hp, mp := rpc.New("synthetic-hub", hubOut), rpc.New("synthetic-manager", managerOut)
	var mu sync.Mutex
	var captured []assistantwire.HTTPRequest
	mode := "ok"
	mp.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		var req assistantwire.HTTPRequest
		if method != assistantwire.MethodHTTP || json.Unmarshal(params, &req) != nil {
			return nil, errors.New("unexpected synthetic request")
		}
		mu.Lock()
		captured = append(captured, req)
		kind := mode
		mu.Unlock()
		res := assistantwire.HTTPResponse{Status: 200, Body: []byte(`{"revision":1,"content_version":1,"messages":[],"next_cursor":null}`), ShareAuthority: &route.Authority, Header: map[string][]string{"Set-Cookie": {"synthetic=private"}, "Location": {"https://example.test/"}, "WWW-Authenticate": {"Bearer"}, "X-Private": {"synthetic"}}}
		if kind == "missing-authority" {
			res.ShareAuthority = nil
		}
		if kind == "unsafe-error" {
			res.Status = 500
			res.Body = []byte(`{"code":"upstream","error":"` + token + `"}`)
		}
		return res, nil
	}
	go hp.Serve(ctx, hubIn)
	go mp.Serve(ctx, managerIn)
	t.Cleanup(func() {
		cancel()
		hp.Close(io.EOF)
		mp.Close(io.EOF)
		hubIn.Close()
		managerIn.Close()
		hubOut.Close()
		managerOut.Close()
	})
	h.bus.current = map[string]*busConn{devicesOwner: {peer: hp, presence: busPresence{Capabilities: []string{devicewire.SharingCapability}}}}
	routes := h.routes()
	request := func(method, target, body string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(ManagerHeader, devicesOwner)
		r.Header.Set("Cookie", "synthetic=private")
		r.Header.Set(assistantwire.AdminHeader, "1")
		r.Header.Set("X-Persona-Caller", "forged")
		if authenticated {
			r.Header.Set("Authorization", "Bearer "+deviceToken)
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "http://app.example.test:18660/api/v1/sessions/"+machine+"/synthetic-session/share-catalog", "", true)
	if w.Code != 200 {
		t.Fatalf("management relay: %d %s", w.Code, w.Body.String())
	}
	mu.Lock()
	req := captured[len(captured)-1]
	mu.Unlock()
	if req.Caller == nil || req.Caller.Kind != "device" || req.Caller.OwnerSubject != subject.Subject || req.Caller.Issuer != issuer || req.Caller.BindingRevision != 1 || req.ShareAuthority == nil || *req.ShareAuthority != route.Authority {
		t.Fatal("trusted caller/assignment absent")
	}
	for _, key := range []string{"Authorization", "Cookie", assistantwire.AdminHeader, "X-Persona-Caller"} {
		if len(req.Header[key]) != 0 {
			t.Fatal("untrusted identity/header forwarded")
		}
	}
	for _, key := range []string{"Set-Cookie", "Location", "WWW-Authenticate", "X-Private"} {
		if w.Header().Get(key) != "" {
			t.Fatalf("upstream %s forwarded", key)
		}
	}
	w = request("GET", "http://share.example.test:18661/api/public/agent-shares/"+token+"?manager=forged&token=forged", "", true)
	if w.Code != 200 {
		t.Fatalf("anonymous public relay: %d", w.Code)
	}
	mu.Lock()
	req = captured[len(captured)-1]
	mu.Unlock()
	if req.Caller == nil || req.Caller.Kind != "anonymous" || req.Caller.OwnerSubject != "" || strings.Contains(req.Query, "token") || strings.Contains(req.Query, "manager") {
		t.Fatal("public inherited credential/selector")
	}
	// The same exact exemption is composed into the center dispatcher, rather than auth's full-role default.
	w = httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://share.example.test:18661/api/public/agent-shares/"+token, nil)
	r.RemoteAddr = "127.0.0.1:12345"
	h.mesh.centerAPI.Load().(http.Handler).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("center anonymous route: %d", w.Code)
	}
	mu.Lock()
	mode = "missing-authority"
	mu.Unlock()
	w = request("GET", "http://share.example.test:18661/api/public/agent-shares/"+token, "", false)
	if w.Code != 404 {
		t.Fatal("public response without private authority accepted")
	}
	mu.Lock()
	mode = "unsafe-error"
	mu.Unlock()
	w = request("GET", "http://share.example.test:18661/api/public/agent-shares/"+token, "", false)
	if w.Code != 404 || strings.Contains(w.Body.String(), token) {
		t.Fatal("public error leaked bearer")
	}
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	if _, err = store.RevokeDeviceToken(ctx, credential); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "http://app.example.test:18660/api/v1/agent-shares", "", true)
	if w.Code != 403 && w.Code != 401 {
		t.Fatal("cached revoked credential admitted")
	}
	if err = store.RetireShareMachine(ctx, issuer, machine); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "http://share.example.test:18661/api/public/agent-shares/"+token, "", false)
	if w.Code != 404 {
		t.Fatal("old manager authority served public snapshot")
	}
	t.Log("synthetic actual hub RPC relay: fresh enrollment, forged identity stripped, response header allowlist, explicit anonymous direct+center, missing authority, safe errors, revoked credential and machine fence PASS")
}
