package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/sessionlist"
)

type dimensionWireRow struct {
	SessionID string `json:"session_id"`
	MachineID string `json:"machine_id"`
	Assistant bool   `json:"assistant"`
	runtimeDimensionsFields
}

type dimensionWirePage struct {
	Sessions []dimensionWireRow       `json:"sessions"`
	Remove   []string                 `json:"remove"`
	Revision string                   `json:"revision"`
	Cursor   string                   `json:"next_cursor"`
	Total    int                      `json:"total"`
	Facets   sessionlist.Facets       `json:"facets"`
	Groups   []sessionlist.GroupCount `json:"groups"`
}

func TestRuntimeDimensionsWorkScopeHTTP(t *testing.T) {
	h := newTestHub(t)
	h.cfg.Machines = []MachineConfig{{ID: "one", Name: "Same name"}, {ID: "two", Name: "Same name"}, {ID: "foreign"}}
	h.devices.assign = map[string]string{"one": "manager-a", "two": "manager-a", "foreign": "manager-b"}
	for _, cfg := range h.cfg.Machines {
		m := newMachine(h, cfg)
		h.machines, h.byID[cfg.ID] = append(h.machines, m), m
	}
	at := time.Unix(42, 0).UTC()
	git := &proto.SessionInventory{ProjectKind: "remote", ProjectRepo: strings.Repeat("a", 64), CreatedAt: &at}
	row := func(mid, id, title string) proto.Session {
		return proto.Session{MachineID: mid, SessionID: id, Title: title, Cwd: "/work/project",
			State: "offline", Status: proto.StatusOffline, Inventory: git}
	}
	main := row("one", "main", "Work A")
	main.Assistant = true
	health := row("one", "health", "Work B")
	health.Assistant, health.Space = true, "health"
	sideRuntime := row("one", "runtime", "Work C")
	sideRuntime.Assistant, sideRuntime.SourceKind, sideRuntime.Agent = true, proto.SourceRuntime, "codex"
	sideLegacy := row("two", "legacy", "Work D")
	sideLegacy.Assistant = true
	agent := row("two", "agent", "Work E")
	foreign := row("foreign", "main", "Work F")
	foreign.Assistant = true // same session ID on another machine is unrelated
	h.byID["one"].sessions = []proto.Session{main, health, sideRuntime}
	h.byID["two"].sessions = []proto.Session{sideLegacy, agent}
	h.byID["foreign"].sessions = []proto.Session{foreign}
	setMain := func(seq uint64, mid, sid string) {
		t.Helper()
		_, err := (assistantLink{h}).snapshotFrom(context.Background(), "manager-a", assistantwire.Snapshot{
			Epoch: "fixture", Seq: seq, Profile: &assistantwire.Profile{MachineID: mid, Main: sid},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	setMain(1, "one", "main")
	request := func(query string, extension bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/sessions?"+query, nil)
		if extension {
			r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{Role: roleExtension, Manager: "manager-a"}))
		}
		w := httptest.NewRecorder()
		h.listSessions(w, r)
		return w
	}
	page := func(query string, extension bool) dimensionWirePage {
		t.Helper()
		w := request(query, extension)
		var p dimensionWirePage
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &p) != nil {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body)
		}
		return p
	}
	const q = "scope=work&facets=1&group_by=project&sort=title&state=offline&q=work"
	first := page(q+"&summary=1&limit=1", true)
	if first.Total != 3 || first.Cursor == "" || len(first.Sessions) != 1 || first.Sessions[0].SessionID != "runtime" ||
		!first.Sessions[0].Assistant || first.Sessions[0].CreatedAt == nil || !first.Sessions[0].CreatedAt.Equal(at) ||
		first.Sessions[0].HarnessID != "codex" || len(first.Facets.Project) != 1 || first.Facets.Project[0].Count != 3 ||
		len(first.Facets.Device) != 2 || first.Facets.Device[0].ID == first.Facets.Device[1].ID ||
		len(first.Groups) != 1 || first.Groups[0].Count != 3 {
		t.Fatalf("first: %+v", first)
	}
	pid := first.Sessions[0].ProjectID
	combined := page(q+"&machine_id=two&machine_id=one&project_id="+url.QueryEscape(pid)+"&project_id=absent&harness=claude&harness=codex", true)
	if combined.Total != 3 || len(combined.Sessions) != 3 || combined.Sessions[1].SessionID != "legacy" ||
		!combined.Sessions[1].Assistant || combined.Sessions[1].HarnessID != "claude" || combined.Sessions[1].ProjectID != pid {
		t.Fatalf("combined: %+v", combined)
	}
	w := request(q+"&counts=1", true)
	var counts sessionlist.Counts
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &counts) != nil || counts.Total != 3 || counts.State["offline"] != 3 || counts.Facets == nil || counts.Groups == nil ||
		!reflect.DeepEqual(*counts.Facets, first.Facets) || !reflect.DeepEqual(*counts.Groups, first.Groups) {
		t.Fatalf("counts: %d %s", w.Code, w.Body)
	}
	all := page(q, false)
	if all.Total != 4 || all.Sessions[len(all.Sessions)-1].MachineID != "foreign" {
		t.Fatalf("same session ID on a different machine was excluded: %+v", all)
	}
	legacy := page("machine_id=one", false)
	if len(legacy.Sessions) != 3 || legacy.Revision != "" {
		t.Fatalf("legacy list changed: %+v", legacy)
	}
	if p := page("agents_only=1&facets=1", true); p.Total != 1 || p.Sessions[0].SessionID != "agent" {
		t.Fatalf("agents_only changed: %+v", p)
	}
	if w := request(q+"&agents_only=1", true); w.Code != http.StatusBadRequest {
		t.Fatalf("incompatible scopes accepted: %d %s", w.Code, w.Body)
	}
	if w := request(q+"&machine_id=one&machine_id=foreign", true); w.Code != http.StatusForbidden {
		t.Fatalf("foreign machine accepted: %d %s", w.Code, w.Body)
	}

	// A manager profile change by itself updates the work scope and delta.
	// An already issued continuation remains frozen.
	setMain(2, "two", "legacy")
	second := page(q+"&summary=1&limit=1&cursor="+url.QueryEscape(first.Cursor), true)
	if second.Revision != first.Revision || second.Sessions[0].SessionID != "legacy" || !reflect.DeepEqual(second.Groups, first.Groups) {
		t.Fatalf("snapshot drift: %+v", second)
	}
	delta := page(q+"&since="+url.QueryEscape(first.Revision), true)
	if delta.Total != 3 || len(delta.Sessions) != 1 || delta.Sessions[0].SessionID != "main" ||
		!slices.Equal(delta.Remove, []string{runtimeListID("two", "legacy")}) || delta.Facets.Project[0].Count != 3 {
		t.Fatalf("scope delta: %+v", delta)
	}
	// A binding-only change with the same Main ID also updates membership.
	setMain(3, "one", "legacy")
	if p := page(q, true); p.Total != 4 || p.Facets.Project[0].Count != 4 || p.Groups[0].Count != 4 ||
		!slices.ContainsFunc(p.Sessions, func(s dimensionWireRow) bool { return s.SessionID == "legacy" }) {
		t.Fatalf("main binding-only change: %+v", p)
	}
	if !slices.Contains(h.features(), featureSessionsDimensions) || slices.Contains(proto.Features, featureSessionsDimensions) {
		t.Fatal("capability must be advertised by the hub only")
	}
}

func TestRuntimeDimensionsProjectIdentities(t *testing.T) {
	remote := func(key string) *proto.SessionInventory {
		return &proto.SessionInventory{ProjectKind: "remote", ProjectRepo: strings.Repeat(key, 64)}
	}
	local := &proto.SessionInventory{ProjectKind: "local", ProjectRepo: strings.Repeat("c", 64)}
	plain := &proto.SessionInventory{ProjectKind: "non_git"}
	type pair struct{ id, name string }
	project := func(mid, cwd string, inventory *proto.SessionInventory) pair {
		id, name := runtimeProject(mid, &proto.Session{Cwd: cwd, Inventory: inventory})
		return pair{id, name}
	}
	a, b := project("one", "/a/project", remote("a")), project("two", "/b/project", remote("a"))
	if a != b || a.name != "project" || a.id == project("two", "/b/project", remote("b")).id {
		t.Fatalf("remote identities: %+v %+v", a, b)
	}
	if project("one", "/a/project", plain) != project("two", "/b/project", plain) ||
		project("one", "/a/project", local).id == project("two", "/b/project", local).id ||
		project("one", "/a/project", nil).id == project("two", "/b/project", nil).id ||
		project("one", "/a/project", nil).id == project("one", "/a/project", plain).id {
		t.Fatal("local, confirmed non-Git and unknown projects were not kept distinct")
	}
}
