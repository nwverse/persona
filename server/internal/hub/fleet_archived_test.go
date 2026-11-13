package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// archivedHub is a hub with the machine "box" and five apps, two older (whole lists) and three delta apps, whose
// messages are kept in order.
type archivedHub struct {
	h    *Hub
	m    *Machine
	srv  *httptest.Server
	apps []*client
	got  [][][]byte
}

func newArchivedHub(t *testing.T) *archivedHub {
	t.Helper()
	h := newTestHub(t)
	m := newMachine(h, MachineConfig{ID: "box", Name: "Box", Kind: "local"})
	h.machines, h.byID["box"] = append(h.machines, m), m
	a := &archivedHub{h: h, m: m}
	for i := range 5 {
		c := &client{out: make(chan []byte, 16), subs: map[[2]string]bool{}, role: roleFull}
		if i >= 2 {
			c.delta, c.have = true, map[string]map[string][]byte{}
		}
		addTestClient(h, c)
		a.apps = append(a.apps, c)
	}
	a.got = make([][][]byte, len(a.apps))
	a.srv = httptest.NewServer(h.routes())
	t.Cleanup(a.srv.Close)
	return a
}

// take hands the hub the manager's event as the bus does, then keeps what each app was sent.
func (a *archivedHub) take(t *testing.T, event string, params any) {
	t.Helper()
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	busEvents[event](a.h, nil, b)
	a.drain()
}

func (a *archivedHub) drain() {
	for i, c := range a.apps {
		for len(c.out) > 0 {
			a.got[i] = append(a.got[i], <-c.out)
		}
	}
}

// connect has the delta apps take the machine's whole list, as apps do when they connect.
func (a *archivedHub) connect() {
	for _, c := range a.apps {
		if c.delta {
			a.h.snapshotSessions(c, a.m)
		}
	}
	a.drain()
}

func (a *archivedHub) sessions(t *testing.T) string {
	t.Helper()
	r, _ := http.NewRequest(http.MethodGet, a.srv.URL+"/api/v1/sessions?machine_id=box", nil)
	r.Header.Set("Authorization", "Bearer "+a.h.cfg.Token)
	res, err := a.srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return fmt.Sprint(res.StatusCode, " ", string(b))
}

// archivedFixture is a machine's whole list as an older manager tells it: live rows, two of them archived in place,
// then the archive's own rows (six archived, two restored).
func archivedFixture() []proto.Session {
	at := time.Unix(1700000000, 0).UTC()
	var list []proto.Session
	for i := range 20 {
		list = append(list, proto.Session{MachineID: "box", SessionID: fmt.Sprintf("s%02d", i), Title: "Run the tests",
			Status: proto.StatusIdle, State: proto.StateIdle, Agent: "claude", UpdatedAt: at, LastText: strings.Repeat("x", 100)})
	}
	list[3].Archived, list[3].ArchiveChangedAt = true, &at
	list[9].Archived, list[9].ArchiveChangedAt = true, &at
	for i := range 8 {
		s := proto.Session{MachineID: "box", SessionID: fmt.Sprintf("old%02d", i), Title: "An old task", Agent: "codex",
			Status: proto.StatusOffline, State: proto.StateOffline, UpdatedAt: at.Add(-time.Duration(i) * time.Hour),
			Archived: i < 6, ArchiveChangedAt: &at, Capabilities: &proto.SessionCapabilities{Read: true}}
		list = append(list, s)
	}
	return list
}

// apart is a whole list told apart, as a manager does for a hub that says Hello.ArchivedApart.
func apart(list []proto.Session) (shown, archived []proto.Session) {
	for _, s := range list {
		if s.Archived && !s.Assistant {
			archived = append(archived, s)
		} else {
			shown = append(shown, s)
		}
	}
	return shown, archived
}

// A hub told the archived rows apart gives every app the same bytes as one told the whole list (older apps' lists,
// delta apps' deltas, GET /api/v1/sessions), after a visible row changes, a session is archived and it is restored;
// every session is found by id as before, and an archived session's watch state stays as when it was in the list.
func TestArchivedApartGivesTheAppsTheSameBytes(t *testing.T) {
	whole, split := newArchivedHub(t), newArchivedHub(t)
	for _, a := range []*archivedHub{whole, split} {
		a.m.mu.Lock()
		a.m.lastSeq = map[string]int{"old02": 7}
		a.m.mu.Unlock()
	}
	list := archivedFixture()
	step := func(what string, list []proto.Session) {
		t.Helper()
		whole.take(t, assistantwire.EventFleetSessions, assistantwire.FleetSessions{MachineID: "box", Sessions: list})
		shown, archived := apart(list)
		split.take(t, assistantwire.EventFleetArchived, assistantwire.FleetArchived{MachineID: "box", Sessions: archived})
		split.take(t, assistantwire.EventFleetSessions, assistantwire.FleetSessions{MachineID: "box", Sessions: shown})
		if what == "first" {
			whole.connect()
			split.connect()
		}
		for i := range whole.apps {
			if !reflect.DeepEqual(whole.got[i], split.got[i]) {
				t.Fatalf("%s: app %d got\n%q\nwant\n%q", what, i, split.got[i], whole.got[i])
			}
		}
		if w, s := whole.sessions(t), split.sessions(t); w != s {
			t.Fatalf("%s: GET /api/v1/sessions\n got %s\nwant %s", what, s, w)
		}
		for _, row := range list {
			w, wok := whole.m.session(row.SessionID)
			s, sok := split.m.session(row.SessionID)
			wb, _ := json.Marshal(w)
			sb, _ := json.Marshal(s)
			if wok != sok || string(wb) != string(sb) {
				t.Fatalf("%s: session %s found %v %s, want %v %s", what, row.SessionID, sok, sb, wok, wb)
			}
		}
		if len(split.m.keptSessions()) != len(list)-len(archived) {
			t.Fatalf("%s: the split hub keeps %d rows in its list", what, len(split.m.keptSessions()))
		}
	}
	step("first", list)
	list[5].LastText = strings.Repeat("y", 100)
	step("a visible row changed", list)
	at := time.Unix(1700003600, 0).UTC()
	list[12].Archived, list[12].ArchiveChangedAt = true, &at
	step("a session archived", list)
	list[12].Archived = false
	step("and restored", list)
	for _, a := range []*archivedHub{whole, split} {
		a.m.mu.Lock()
		_, kept := a.m.lastSeq["old02"]
		a.m.mu.Unlock()
		if !kept {
			t.Fatal("an archived session's watch state was dropped")
		}
	}
	if len(whole.got[0]) == 0 || len(whole.got[4]) == 0 {
		t.Fatal("the apps were sent nothing: the test compares nothing")
	}
}

// The archived rows told apart are the manager's that told them: a list from another manager (the machine moved to
// one that tells its list whole) drops them.
func TestArchivedRowsAreTheirManagers(t *testing.T) {
	m := newMachine(newTestHub(t), MachineConfig{ID: "box", Name: "Box", Kind: "local"})
	m.setArchived("m1", []proto.Session{{SessionID: "old", Archived: true}})
	m.archivedOnlyOf("m1")
	if s, ok := m.session("old"); !ok || !s.Archived {
		t.Fatalf("its manager's list dropped the archived rows: %v %+v", ok, s)
	}
	m.archivedOnlyOf("m2")
	if _, ok := m.session("old"); ok {
		t.Fatal("another manager's list kept them")
	}
}

// The hub's hello says it takes the archived rows apart.
func TestHubHelloTakesArchivedApart(t *testing.T) {
	out, err := assistantLink{newTestHub(t)}.Hello(context.Background())
	if err != nil || !out.ArchivedApart {
		t.Fatalf("hello: %v %+v", err, out)
	}
}
