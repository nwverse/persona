package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// settingsOnlyStore is a manager's store that keeps the settings table alone (in memory); anything else it is asked
// for is a test failure (a nil method).
type settingsOnlyStore struct {
	assistant.Store
	mu  sync.Mutex
	set map[string][]byte
}

func (s *settingsOnlyStore) Setting(_ context.Context, key string, out any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.set[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, out)
}

func (s *settingsOnlyStore) PutSetting(_ context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set[key] = raw
	return err
}

// stateOn waits (2 s at most) for the hub to list m's session id in state want, and fails with what it shows.
func stateOn(t *testing.T, m *Machine, id, want string) {
	t.Helper()
	got := ""
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) { // sleep-ok: polls the hub's list until a deadline
		for _, s := range m.sessionList() {
			if s.SessionID == id {
				got = s.State
			}
		}
		if got == want {
			return
		}
	}
	t.Fatalf("%s is %q, want %q", id, got, want)
}

// Manual mark as read end to end (#7356): an app's WebSocket seen goes through the hub to the manager, which records
// it when the hub's switch is off or the app says the user pressed Mark as read.
func TestManualReadOverTheHub(t *testing.T) {
	h := newTestHub(t)
	h.store = newMeshStore()
	m := newMachine(h, MachineConfig{ID: "box", Name: "Box", Kind: "local"})
	h.machines, h.byID["box"] = append(h.machines, m), m
	connectAssistant(t, h, &settingsOnlyStore{set: map[string][]byte{}})
	app := &client{device: "phone", out: make(chan []byte, 1024)}
	h.onClientMessage(context.Background(), app, []byte(`{"type":"client_hello"}`))
	<-app.out // client_hello_ack
	h.mu.Lock()
	h.clients[app] = struct{}{}
	h.mu.Unlock()
	seen := func(id string, manual bool) {
		msg, _ := json.Marshal(map[string]any{"type": "seen", "machine_id": "box", "session_id": id, "manual": manual})
		h.onClientMessage(context.Background(), app, msg)
	}
	ended := time.Now().Add(-time.Minute)
	m.setSessions([]proto.Session{
		{SessionID: "auto", Status: proto.StatusDone, UpdatedAt: ended},
		{SessionID: "manual", Status: proto.StatusDone, UpdatedAt: ended},
		{SessionID: "probe", Status: proto.StatusDone, UpdatedAt: ended},
	})
	stateOn(t, m, "auto", proto.StateUnread)

	// off (the default): opening it is reading it
	if w := hubCall(h, "GET", "/api/v1/read-settings", ""); w.Code != http.StatusOK || !jsonHas(t, w.Body.Bytes(), "manual", false) {
		t.Fatalf("GET read-settings: %d %s", w.Code, w.Body)
	}
	seen("auto", false)
	stateOn(t, m, "auto", proto.StateIdle)

	// on: opening it leaves it Unread, Mark as read clears it
	if w := hubCall(h, "PUT", "/api/v1/read-settings", `{"manual":true}`); w.Code != http.StatusOK || !jsonHas(t, w.Body.Bytes(), "manual", true) {
		t.Fatalf("PUT read-settings: %d %s", w.Code, w.Body)
	}
	toldApp(t, app, "read_settings", true) // every app hears of it
	seen("manual", false)
	if w := hubCall(h, "POST", "/api/v1/sessions/box/manual/seen", ""); w.Code != http.StatusOK || !jsonHas(t, w.Body.Bytes(), "recorded", false) {
		t.Fatalf("automatic POST seen with the switch on: %d %s", w.Code, w.Body)
	}
	// the manager takes the app's seens in order: once a later Mark as read shows, the ignored ones were handled
	seen("probe", true)
	stateOn(t, m, "probe", proto.StateIdle)
	stateOn(t, m, "manual", proto.StateUnread)
	seen("manual", true)
	stateOn(t, m, "manual", proto.StateIdle)
}

func jsonHas(t *testing.T, body []byte, key string, want bool) bool {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return v[key] == want
}

// toldApp waits (2 s at most) for app to be sent a message of type typ whose manual is want.
func toldApp(t *testing.T, app *client, typ string, want bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case b := <-app.out:
			var msg struct {
				Type   string `json:"type"`
				Manual *bool  `json:"manual"`
			}
			if json.Unmarshal(b, &msg) == nil && msg.Type == typ && msg.Manual != nil && *msg.Manual == want {
				return
			}
		case <-deadline:
			t.Fatalf("the app was not told %s manual=%v", typ, want)
		}
	}
}

// A hub preference must still protect a session whose owner has its own settings database.
func TestManualReadOnSecondaryManager(t *testing.T) {
	h := newTestHub(t)
	h.store = newMeshStore()
	h.bus.idents = &memIdents{}
	m := newMachine(h, MachineConfig{ID: "box", Name: "Box", Kind: "local"})
	h.machines, h.byID["box"] = append(h.machines, m), m
	owner := "m-0b0b0b"
	h.devices.kept = map[string]string{"box": owner}
	mux := http.NewServeMux()
	h.busRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	dialAssistantWithStore(t, h, srv.URL, false, &settingsOnlyStore{set: map[string][]byte{}})
	events := make(chan assistantwire.FleetSeen, 4)
	b := connectGuardManager(t, h, srv.URL, owner, func(method string, raw json.RawMessage) (any, error) {
		var req assistantwire.HTTPRequest
		if method == assistantwire.MethodHTTP && json.Unmarshal(raw, &req) == nil && req.Path == "/api/v1/sessions/box/session/seen" {
			return assistantwire.HTTPResponse{Status: http.StatusOK, Body: []byte(`{"ok":true,"recorded":true}`)}, nil
		}
		return nil, nil
	})
	b.mu.Lock()
	b.onEvent = func(method string, raw json.RawMessage) {
		var p assistantwire.FleetSeen
		if method == assistantwire.EventSeen && json.Unmarshal(raw, &p) == nil {
			events <- p
		}
	}
	b.mu.Unlock()
	if got := h.sessionOwner("box", "session"); got != owner {
		t.Fatalf("owner = %q, want %q", got, owner)
	}
	set := func(on bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]bool{"manual": on})
		if w := hubCall(h, "PUT", "/api/v1/read-settings", string(body)); w.Code != http.StatusOK {
			t.Fatalf("PUT read-settings: %d %s", w.Code, w.Body)
		}
	}
	post := func(body string, recorded bool) {
		t.Helper()
		if w := hubCall(h, "POST", "/api/v1/sessions/box/session/seen", body); w.Code != http.StatusOK || !jsonHas(t, w.Body.Bytes(), "recorded", recorded) {
			t.Errorf("POST seen %q: %d %s, want recorded=%v", body, w.Code, w.Body, recorded)
		}
	}
	ws := func(session string, manual bool) {
		msg, _ := json.Marshal(map[string]any{"type": "seen", "machine_id": "box", "session_id": session, "manual": manual})
		h.onClientMessage(context.Background(), &client{device: "phone"}, msg)
	}
	expect := func(session string, manual bool) {
		t.Helper()
		select {
		case got := <-events:
			if got.SessionID != session || got.Manual != manual {
				t.Errorf("owner received %+v, want %s manual=%v", got, session, manual)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("owner did not receive %s", session)
		}
	}
	post("", true)
	set(true)
	post("", false)
	post(`{"manual":true}`, true)
	ws("automatic", false)
	ws("explicit", true)
	expect("explicit", true)
	set(false)
	post("", true)
	ws("resumed", false)
	expect("resumed", false)
}

func TestManualReadSettingsPersistAndRejectUnauthorizedWrites(t *testing.T) {
	store := newMeshStore()
	h := newTestHub(t)
	h.store = store
	for _, body := range []string{`{}`, `{"manual":"yes"}`, "not json"} {
		if w := hubCall(h, "PUT", "/api/v1/read-settings", body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid PUT %s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := hubCall(h, "PUT", "/api/v1/read-settings", `{"manual":true}`); w.Code != http.StatusOK {
		t.Fatalf("valid PUT: %d %s", w.Code, w.Body)
	}
	restarted := newTestHub(t)
	restarted.store = store
	req := httptest.NewRequest("PUT", "/api/v1/read-settings", strings.NewReader(`{"manual":false}`))
	w := httptest.NewRecorder()
	restarted.putReadSettings(w, req.WithContext(context.WithValue(req.Context(), principalKey{}, principal{Role: roleAnswer})))
	if w.Code != http.StatusForbidden {
		t.Fatalf("answer-only PUT: %d %s", w.Code, w.Body)
	}
	if w := hubCall(restarted, "GET", "/api/v1/read-settings", ""); w.Code != http.StatusOK || !jsonHas(t, w.Body.Bytes(), "manual", true) {
		t.Fatalf("GET after restart and rejected PUT: %d %s", w.Code, w.Body)
	}
}
