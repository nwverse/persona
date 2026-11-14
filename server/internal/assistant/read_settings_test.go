package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// readSettingsOf is GET /api/v1/read-settings as role sees it.
func readSettingsOf(t *testing.T, s *Service, role string) (manual, canWrite bool) {
	t.Helper()
	w := boardCall(s, "GET", "/api/v1/read-settings", "", role)
	if w.Code != http.StatusOK {
		t.Fatalf("GET read-settings: HTTP %d %s", w.Code, w.Body)
	}
	var v struct {
		Manual   bool `json:"manual"`
		CanWrite bool `json:"can_write"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.Manual, v.CanWrite
}

func setManualRead(t *testing.T, s *Service, on bool) {
	t.Helper()
	body := `{"manual":false}`
	if on {
		body = `{"manual":true}`
	}
	if w := boardCall(s, "PUT", "/api/v1/read-settings", body, roleFull); w.Code != http.StatusOK {
		t.Fatalf("PUT read-settings: HTTP %d %s", w.Code, w.Body)
	}
}

// wsSeen is an app's WebSocket seen as the hub relays it (manual: the user pressed Mark as read).
func (ft *fleetTest) wsSeen(session string, manual bool) {
	ft.t.Helper()
	raw, _ := json.Marshal(assistantwire.FleetSeen{MachineID: "mac", SessionID: session, Manual: manual})
	if !ft.s.FleetEvent(context.Background(), assistantwire.EventSeen, raw) {
		ft.t.Fatal("fleet.seen not handled")
	}
}

// postSeen is POST /api/v1/sessions/mac/{session}/seen with body; it answers whether the hub recorded it.
func (ft *fleetTest) postSeen(session, body string) bool {
	ft.t.Helper()
	r := httptest.NewRequest("POST", "/api/v1/sessions/mac/"+session+"/seen", strings.NewReader(body))
	r.Header.Set(RoleHeader, roleFull)
	w := httptest.NewRecorder()
	ft.s.fleetHandler(http.NotFoundHandler()).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		ft.t.Fatalf("POST seen %q: HTTP %d %s", body, w.Code, w.Body)
	}
	var v struct {
		Recorded bool `json:"recorded"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return v.Recorded
}

// newReadTest is a fleet with one finished, never-seen session (s1, a conversation or an Agent alike: both are
// sessions the hub lists) whose turn ended a minute ago.
func newReadTest(t *testing.T) *fleetTest {
	ft := newFleetTest(t)
	ft.d.setSessions("mac", []proto.Session{{SessionID: "s1", Status: proto.StatusDone, UpdatedAt: time.Now().UTC().Add(-time.Minute)}})
	ft.s.publishSessions("mac")
	if got := ft.stateOf("mac", "s1"); got != proto.StateUnread {
		t.Fatalf("a finished session nobody looked at is %q, want unread", got)
	}
	return ft
}

// Off by default: a hub that never stored the switch keeps the automatic seen it always had (opening clears Unread).
func TestManualReadOffByDefaultKeepsAutomaticSeen(t *testing.T) {
	ft := newReadTest(t)
	if manual, can := readSettingsOf(t, ft.s, roleFull); manual || !can {
		t.Fatalf("new hub: manual=%v can_write=%v, want off and writable", manual, can)
	}
	ft.wsSeen("s1", false)
	if got := ft.stateOf("mac", "s1"); got != proto.StateIdle {
		t.Fatalf("opened with the switch off: %q, want idle", got)
	}

	ft = newReadTest(t)
	if !ft.postSeen("s1", "") {
		t.Fatal("an HTTP seen without a body was not recorded with the switch off")
	}
	if got := ft.stateOf("mac", "s1"); got != proto.StateIdle {
		t.Fatalf("opened over HTTP with the switch off: %q, want idle", got)
	}
}

// On: opening (an automatic seen, over the WebSocket or HTTP, from any app) leaves Unread; only Mark as read clears
// it, again and again without harm, and the next turn makes it Unread again.
func TestManualReadOnlyExplicitSeenClearsUnread(t *testing.T) {
	ft := newReadTest(t)
	setManualRead(t, ft.s, true)

	ft.wsSeen("s1", false)
	if ft.postSeen("s1", "") || ft.postSeen("s1", `{}`) {
		t.Fatal("an automatic HTTP seen was recorded with the switch on")
	}
	if got := ft.s.seenAt("mac", "s1"); !got.IsZero() {
		t.Fatalf("seen at %v after automatic seens with the switch on", got)
	}
	ft.s.publishSessions("mac")
	if got := ft.stateOf("mac", "s1"); got != proto.StateUnread {
		t.Fatalf("opened with the switch on: %q, want unread", got)
	}

	ft.wsSeen("s1", true)
	if got := ft.stateOf("mac", "s1"); got != proto.StateIdle {
		t.Fatalf("after Mark as read: %q, want idle", got)
	}
	if !ft.postSeen("s1", `{"manual":true}`) {
		t.Fatal("a second Mark as read (HTTP) was not recorded")
	}
	if got := ft.stateOf("mac", "s1"); got != proto.StateIdle {
		t.Fatalf("after marking twice: %q, want idle", got)
	}

	// a new turn ends after the mark: Unread again, and opening it still does not clear it
	ft.d.setSessions("mac", []proto.Session{{SessionID: "s1", Status: proto.StatusDone, UpdatedAt: time.Now().UTC().Add(5 * time.Second)}}) // past the 2 s grace of agentState
	ft.s.publishSessions("mac")
	if got := ft.stateOf("mac", "s1"); got != proto.StateUnread {
		t.Fatalf("after a new turn: %q, want unread", got)
	}
	ft.wsSeen("s1", false)
	if got := ft.stateOf("mac", "s1"); got != proto.StateUnread {
		t.Fatalf("new turn opened with the switch on: %q, want unread", got)
	}
}

// The switch is kept in the settings table: a restarted manager (a new Service on the same store) still has it, the
// apps are told of each change, and turning it off brings the automatic seen back.
func TestManualReadPersistsAndIsBroadcast(t *testing.T) {
	ft := newReadTest(t)
	setManualRead(t, ft.s, true)
	ft.f.mu.Lock()
	told := slices.Contains(ft.f.events, "read_settings")
	ft.f.mu.Unlock()
	if !told {
		t.Fatal("the apps were not told the switch changed")
	}

	restarted := New(Options{Hub: ft.f, Store: func() Store { return ft.st }, Logf: t.Logf, Devices: ft.d})
	if manual, _ := readSettingsOf(t, restarted, roleAnswer); !manual {
		t.Fatal("switch lost across a manager restart")
	}
	ft.s = restarted
	ft.wsSeen("s1", false)
	ft.s.publishSessions("mac")
	if got := ft.stateOf("mac", "s1"); got != proto.StateUnread {
		t.Fatalf("opened after a restart with the switch on: %q, want unread", got)
	}

	setManualRead(t, ft.s, false)
	ft.wsSeen("s1", false)
	if got := ft.stateOf("mac", "s1"); got != proto.StateIdle {
		t.Fatalf("opened after turning the switch off: %q, want idle", got)
	}
}

func TestManualReadSwitchWriteRules(t *testing.T) {
	ft := newFleetTest(t)
	if manual, can := readSettingsOf(t, ft.s, roleAnswer); manual || can {
		t.Fatalf("answer-only device: manual=%v can_write=%v, want off and read-only", manual, can)
	}
	if w := boardCall(ft.s, "PUT", "/api/v1/read-settings", `{"manual":true}`, roleAnswer); w.Code != http.StatusForbidden {
		t.Fatalf("answer-only PUT: HTTP %d", w.Code)
	}
	for _, body := range []string{`{}`, `{"manual":"yes"}`, `not json`} {
		if w := boardCall(ft.s, "PUT", "/api/v1/read-settings", body, roleFull); w.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s: HTTP %d", body, w.Code)
		}
	}
	if manual, _ := readSettingsOf(t, ft.s, roleFull); manual {
		t.Fatal("a rejected PUT changed the switch")
	}
}
