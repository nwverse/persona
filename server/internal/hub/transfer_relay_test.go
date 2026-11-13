package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
)

// Computer to computer transfers are the manager's (internal/devices transfer.go); the hub only relays them: a POST
// to the manager of both computers, events only to the full devices that asked.

const transferRelayBody = `{"from":{"machine":"a","path":"x"},"to":{"machine":"b","dest":"inbox"}}`

func transferRelayHub(t *testing.T) (*Hub, *http.ServeMux) {
	t.Helper()
	h := newTestHub(t)
	for _, id := range []string{"a", "b", "c"} {
		h.byID[id] = newMachine(h, MachineConfig{ID: id, Name: id})
	}
	mux := http.NewServeMux()
	h.transferRoutesRelayed(mux)
	return h, mux
}

func TestTransferRelayPicksTheComputersManager(t *testing.T) {
	h, mux := transferRelayHub(t)
	h.cfg.Machines = []MachineConfig{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	h.devices.assign = map[string]string{"c": "mgr-2"}
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/files/transfer", strings.NewReader(body)))
		return rec
	}
	if rec := post(`{`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad body: %d %s", rec.Code, rec.Body)
	}
	if rec := post(`{"from":{"machine":"zz","path":"x"},"to":{"machine":"b"}}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown source: %d %s", rec.Code, rec.Body)
	}
	if rec := post(`{"from":{"machine":"a","path":"x"},"to":{"machine":"zz"}}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown target: %d %s", rec.Code, rec.Body)
	}
	if rec := post(`{"from":{"machine":"a","path":"x"},"to":{"machine":"c"}}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "different_managers") {
		t.Errorf("two managers: %d %s", rec.Code, rec.Body)
	}
	// Both the default's: on to it (away here, so 503 assistant_offline, as the manager's other routes).
	if rec := post(transferRelayBody); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), assistantwire.CodeAssistantOffline) {
		t.Errorf("relayed: %d %s", rec.Code, rec.Body)
	}
	for _, method := range []string{"GET", "DELETE"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/files/transfer/x", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d %s", method, rec.Code, rec.Body)
		}
	}
	// The source's manager is the one, with the body as it came.
	r := httptest.NewRequest("POST", "/api/v1/files/transfer", strings.NewReader(`{"from":{"machine":"c","path":"x"},"to":{"machine":"c"}}`))
	by, ok := h.transferSource(httptest.NewRecorder(), r)
	if !ok {
		t.Fatal("refused")
	}
	if id, err := by(r); err != nil || id != "mgr-2" {
		t.Fatalf("owner %q %v", id, err)
	}
	var again proto.FileTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&again); err != nil || again.From.Machine != "c" {
		t.Fatalf("body not put back: %+v %v", again, err)
	}
}

// Only a full device reaches the routes (an answer-only one is refused before them), and an extension's token gets
// no scope for them.
func TestTransferRoutesAreForFullDevicesOnly(t *testing.T) {
	for _, p := range devicewire.TransferRoutes {
		method, path, _ := strings.Cut(p, " ")
		path = strings.ReplaceAll(path, "{id}", "x")
		for _, role := range []string{roleAnswer, roleLink, roleLinkPair, rolePeer, rolePeerPair} {
			if allowed(principal{Role: role}, method, path) {
				t.Errorf("%s may %s", role, p)
			}
		}
	}
}

// A manager's file_transfer reaches the full devices that declared file_transfer, never the watch or an app that did
// not ask, and stays out of the event log.
func TestTransferEventsRelay(t *testing.T) {
	h := newTestHub(t)
	knows := &client{out: make(chan []byte, 4), role: roleFull, urlFeatures: map[string]bool{proto.FileTransferEvent: true}}
	watch := &client{out: make(chan []byte, 4), role: roleAnswer, urlFeatures: map[string]bool{proto.FileTransferEvent: true}}
	other := &client{out: make(chan []byte, 4), role: roleFull}
	addTestClient(h, knows, watch, other)
	if err := h.relayBroadcastFrom(assistantwire.AssistantID, json.RawMessage(`{"type":"file_transfer","transfer":{"id":"t1","state":"running"}}`)); err != nil {
		t.Fatal(err)
	}
	if len(knows.out) != 1 || len(watch.out) != 0 || len(other.out) != 0 {
		t.Fatalf("%d %d %d", len(knows.out), len(watch.out), len(other.out))
	}
	h.mu.Lock()
	logged := h.events != nil && len(h.events.events) > 0
	h.mu.Unlock()
	if logged {
		t.Fatal("in the event log")
	}
}

// With a manager on the bus (Postgres), the POST reaches it unchanged with the device's role.
func TestTransferRoutesReachTheManager(t *testing.T) {
	h, srv, _ := newBusHub(t)
	h.byID["a"], h.byID["b"] = newMachine(h, MachineConfig{ID: "a", Name: "a"}), newMachine(h, MachineConfig{ID: "b", Name: "b"})
	got := make(chan assistantwire.HTTPRequest, 2)
	helloAssistant(t, h, srv, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method == assistantwire.MethodTools {
			return map[string]any{"tools": []any{}}, nil
		}
		var req assistantwire.HTTPRequest
		if method != assistantwire.MethodHTTP || json.Unmarshal(params, &req) != nil {
			return nil, errors.New("unexpected " + method)
		}
		got <- req
		return assistantwire.HTTPResponse{Status: 202, Body: []byte(`{"id":"t1","state":"running"}`)}, nil
	})
	app := &adminClient{t: t, h: srv.Config.Handler, remote: "127.0.0.1:1", header: http.Header{"Authorization": {"Bearer " + h.cfg.Token}}}
	if status, _, _ := app.do("POST", "/api/v1/files/transfer", transferRelayBody); status != 202 {
		t.Fatalf("POST: %d", status)
	}
	if req := <-got; req.Path != "/api/v1/files/transfer" || string(req.Body) != transferRelayBody || req.Header[assistantwire.RoleHeader][0] != roleFull {
		t.Fatalf("reached the manager as %+v", req)
	}
}
