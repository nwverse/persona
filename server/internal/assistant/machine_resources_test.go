package assistant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestMachineResourcesSummaryFieldsAndPrivacy(t *testing.T) {
	f := newFakeHub()
	f.devices = []Device{
		{ID: "hub-box", Name: "Hub", Kind: "local", Online: true, Reachable: true},
		{ID: "worker-a", Name: "Worker A", Kind: "remote", Online: true, Reachable: true},
		{ID: "stale-box", Name: "Stale", Kind: "remote", Online: true, Reachable: true},
		{ID: "offline-box", Name: "Offline", Kind: "remote"},
		{ID: "pending-box", Name: "Pending", Kind: "remote", Online: true},
	}
	f.devicesFull = true
	f.devicesOther = []string{"hidden-peer"}
	f.sessions["hub-box"] = []proto.Session{
		{SessionID: "pub-work", Title: "Public job", Status: proto.StatusWorking, Cwd: "/work/repo"},
		{SessionID: "priv-space", Title: "Private health", Status: proto.StatusWorking, Space: SpaceHealth, Cwd: "/work/other"},
		{SessionID: "priv-dir", Title: "Private dir", Status: proto.StatusWorking, Cwd: identity.HealthDir("/home/owner")},
		{SessionID: "idle-job", Title: "Idle", Status: proto.StatusIdle, Cwd: "/work/repo"},
		{SessionID: "arch-job", Title: "Archived", Status: proto.StatusWorking, Archived: true, Cwd: "/work/repo"},
	}
	f.sessions["worker-a"] = []proto.Session{
		{SessionID: "w1", Status: proto.StatusWorking, Cwd: "/work/repo"},
	}
	f.sessions["stale-box"] = nil

	cpu, load := 22.5, 1.25
	d := &resourceDevs{
		fleetDevs: newFleetDevs(),
		machines: map[string]*proto.Machine{
			"hub-box": {
				Kind:   "local",
				OS:     "linux",
				Arch:   "amd64",
				System: &proto.System{Name: "Linux 6.8", OS: "linux", Arch: "amd64", Cores: 16},
			},
			"worker-a": {
				Kind:   "remote",
				OS:     "darwin",
				Arch:   "arm64",
				System: &proto.System{Name: "macOS 15", OS: "darwin", Arch: "arm64", Cores: 12},
			},
			"stale-box":   {OS: "linux", Arch: "amd64"},
			"offline-box": {OS: "linux", Arch: "amd64", Error: "ssh handshake failed: secret-internal-detail"},
			"pending-box": {},
		},
		samples: map[string]*proto.MetricSample{
			"hub-box": {
				T:         time.Now().Unix(),
				CPU:       &cpu,
				Load:      &load,
				MemTotal:  32 << 30,
				MemUsed:   12 << 30,
				DiskTotal: 500 << 30,
				DiskUsed:  200 << 30,
			},
			"worker-a": {
				T:         time.Now().Unix(),
				CPU:       &cpu,
				Load:      &load,
				MemTotal:  64 << 30,
				MemUsed:   16 << 30,
				DiskTotal: 1000 << 30,
				DiskUsed:  300 << 30,
			},
			"stale-box": {
				T:         time.Now().Add(-45 * time.Second).Unix(),
				CPU:       &cpu,
				Load:      &load,
				MemTotal:  16 << 30,
				MemUsed:   4 << 30,
				DiskTotal: 200 << 30,
				DiskUsed:  50 << 30,
			},
		},
		versions: map[string]*bool{},
	}
	for _, id := range []string{"hub-box", "worker-a", "stale-box"} {
		d.add(id, id)
		d.setSessions(id, f.sessions[id])
	}

	s := newTestService(t, f)
	s.devs = d
	s.health.loaded = true
	s.health.recs = map[string]*LauncherHealth{
		"worker-a/codex":  {Machine: "worker-a", Launcher: "codex", State: healthFailing, Error: "secret oauth error token", CheckedAt: time.Now()},
		"worker-a/claude": {Machine: "worker-a", Launcher: "claude", State: healthFailing, Error: "rate limited", CheckedAt: time.Now()},
		"worker-a/old":    {Machine: "worker-a", Launcher: "old", State: healthFailing, CheckedAt: time.Now().Add(-24 * time.Hour)},
	}

	get := func(role string) (string, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/machine-resources", nil)
		if role != "" {
			req.Header.Set(RoleHeader, role)
		}
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("role=%s status=%d body=%s", role, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return rec.Body.String(), out
	}

	extBody, extOut := get(roleExtension)
	for _, leak := range []string{"secret-internal-detail", "secret oauth error token", "rate limited", "Private health", "priv-space", "priv-dir", "pub-work"} {
		if strings.Contains(extBody, leak) {
			t.Fatalf("machine-resources leaked %q: %s", leak, extBody)
		}
	}
	if extOut["complete"] != true || extOut["excluded_count"] != float64(1) {
		t.Fatalf("top-level flags: %s", extBody)
	}
	rows, _ := extOut["machines"].([]any)
	if len(rows) != 5 {
		t.Fatalf("machines count = %d, want 5", len(rows))
	}
	byID := map[string]map[string]any{}
	for _, item := range rows {
		m := item.(map[string]any)
		byID[m["id"].(string)] = m
	}

	hub := byID["hub-box"]
	if hub["is_hub"] != true || hub["os"] != "linux" || hub["arch"] != "amd64" || hub["online"] != true ||
		hub["cores"] != float64(16) || hub["memory_available_bytes"] != float64(20<<30) || hub["memory_total_bytes"] != float64(32<<30) ||
		hub["disk_free_bytes"] != float64(300<<30) || hub["disk_total_bytes"] != float64(500<<30) {
		t.Fatalf("hub summary: %v", hub)
	}
	// Extension token excludes every space session (both explicit Space and HealthDir) from working_agents.
	if hub["working_agents"] != float64(1) {
		t.Fatalf("extension hub working_agents = %v, want 1", hub["working_agents"])
	}

	worker := byID["worker-a"]
	if worker["is_hub"] != false || worker["os"] != "darwin" || worker["arch"] != "arm64" ||
		worker["disk_free_bytes"] != float64(700<<30) || worker["disk_total_bytes"] != float64(1000<<30) ||
		worker["working_agents"] != float64(1) {
		t.Fatalf("worker summary: %v", worker)
	}
	rawFail, _ := worker["failing_launchers"].([]any)
	gotFail := make([]string, 0, len(rawFail))
	for _, item := range rawFail {
		gotFail = append(gotFail, item.(string))
	}
	if !slices.Equal(gotFail, []string{"claude", "codex"}) {
		t.Fatalf("worker failing_launchers = %v, want [claude codex]", gotFail)
	}

	// Stale, offline, and pending machines return explicit null for unavailable numeric metrics.
	for _, id := range []string{"stale-box", "offline-box", "pending-box"} {
		row := byID[id]
		for _, field := range []string{"cpu_busy_percent", "load_1m", "memory_available_bytes", "memory_total_bytes", "disk_free_bytes", "disk_total_bytes"} {
			val, exists := row[field]
			if !exists || val != nil {
				t.Fatalf("%s.%s = %v (exists=%v), want explicit null", id, field, val, exists)
			}
		}
	}
	if byID["offline-box"]["online"] != false || byID["offline-box"]["state"] != "offline" || byID["offline-box"]["working_agents"] != nil {
		t.Fatalf("offline summary: %v", byID["offline-box"])
	}
	if byID["pending-box"]["online"] != false || byID["pending-box"]["state"] != "pending" {
		t.Fatalf("pending summary: %v", byID["pending-box"])
	}

	// Full role keeps ordinary machines_list counting (counts non-hidden-by-dir working agents including SpaceHealth on a regular cwd).
	_, fullOut := get(roleFull)
	for _, item := range fullOut["machines"].([]any) {
		m := item.(map[string]any)
		if m["id"] == "hub-box" && m["working_agents"] != float64(2) {
			t.Fatalf("full role hub working_agents = %v, want 2", m["working_agents"])
		}
	}
}

func TestMachineResourcesAmbiguousFutureAndReconnectAreNull(t *testing.T) {
	f := newFakeHub()
	f.devices = []Device{
		{ID: "future", Name: "Future", Online: true, Reachable: true},
		{ID: "reconn", Name: "Reconn", Online: true, Reachable: true},
		{ID: "ambig", Name: "Ambig", Online: true, Reachable: true},
	}
	f.devicesFull = true
	d := &resourceDevs{
		fleetDevs: newFleetDevs(),
		machines: map[string]*proto.Machine{
			"future": {OS: "linux", Arch: "amd64"},
			"reconn": {OS: "linux", Arch: "amd64"},
			"ambig":  {OS: "linux", Arch: "amd64"},
		},
		samples: map[string]*proto.MetricSample{
			"future": {T: time.Now().Add(30 * time.Second).Unix(), MemTotal: 100, MemUsed: 40, DiskTotal: 100, DiskUsed: 40},
			"reconn": {T: time.Now().Add(-10 * time.Second).Unix(), MemTotal: 100, MemUsed: 40, DiskTotal: 100, DiskUsed: 40},
			"ambig":  {T: time.Now().Unix(), MemTotal: 100, MemUsed: 0, DiskTotal: 100, DiskUsed: 0},
		},
		versions: map[string]*bool{},
	}
	node := d.add("reconn", "Reconn")
	node.connected = time.Now().Add(-2 * time.Second)
	s := newTestService(t, f)
	s.devs = d

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/machine-resources", nil)
	req.Header.Set(RoleHeader, roleExtension)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Machines []map[string]any `json:"machines"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, m := range out.Machines {
		for _, field := range []string{"memory_available_bytes", "memory_total_bytes", "disk_free_bytes", "disk_total_bytes"} {
			if m[field] != nil {
				t.Fatalf("%s.%s = %v, want null", m["id"], field, m[field])
			}
		}
		if m["id"] == "future" && m["sample_age_seconds"] != nil {
			t.Fatalf("future sample_age_seconds = %v, want null", m["sample_age_seconds"])
		}
	}
}
