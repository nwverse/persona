package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
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
// it only while the switch is off, or when the app says the user pressed Mark as read; the switch itself is the
// manager's route, relayed.
func TestManualReadOverTheHub(t *testing.T) {
	h := newTestHub(t)
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
