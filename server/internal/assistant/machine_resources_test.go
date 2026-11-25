package assistant

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

const machineResourcesPath = "/api/v1/assistant/machine-resources"

type machineResourceTestRow struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	State                string   `json:"state"`
	Online               bool     `json:"online"`
	Cores                *int     `json:"cores"`
	CPUBusyPercent       *float64 `json:"cpu_busy_percent"`
	Load1M               *float64 `json:"load_1m"`
	MemoryAvailableBytes *int64   `json:"memory_available_bytes"`
	MemoryTotalBytes     *int64   `json:"memory_total_bytes"`
	SampleAgeSeconds     *float64 `json:"sample_age_seconds"`
	WorkingAgents        *int     `json:"working_agents"`
}

type machineResourceTestResponse struct {
	Machines      []machineResourceTestRow `json:"machines"`
	Complete      bool                     `json:"complete"`
	ExcludedCount int                      `json:"excluded_count"`
}

func machineResourceTestService(t *testing.T) *Service {
	t.Helper()
	f := newFakeHub()
	f.devices = []Device{
		{ID: "active", Name: "Active", Online: true, Reachable: true},
		{ID: "stale", Name: "Stale", Online: true, Reachable: true},
		{ID: "offline", Name: "Offline"},
		{ID: "pending", Name: "Pending", Online: true},
		{ID: "missing", Name: "Missing", Online: true, Reachable: true},
		{ID: "future", Name: "Future", Online: true, Reachable: true},
	}
	f.devicesFull = true
	f.devicesOther = []string{"excluded"}
	f.sessions["active"] = []proto.Session{
		{SessionID: "public-working", Title: "PUBLIC-TITLE", Status: proto.StatusWorking, State: proto.StateWorking, Cwd: "/work/public"},
		{SessionID: "space-working", Title: "SPACE-TITLE", Status: proto.StatusWorking, State: proto.StateWorking, Space: "health", Cwd: "/work/moved"},
	}
	f.sessions["stale"] = nil
	f.sessionsFail["missing"] = 1
	now := time.Now()
	cpu, load := 0.0, 1.5
	d := &resourceDevs{fleetDevs: newFleetDevs(), machines: map[string]*proto.Machine{
		"active": {System: &proto.System{Cores: 8}},
		"stale":  {}, "offline": {Error: "PRIVATE-CONNECTION-DETAIL"}, "pending": {}, "missing": {}, "future": {},
	}, samples: map[string]*proto.MetricSample{
		"active":  {T: now.Unix(), CPU: &cpu, Load: &load, MemTotal: 100, MemUsed: 40},
		"stale":   {T: now.Add(-31 * time.Second).Unix(), CPU: &cpu, Load: &load, MemTotal: 100, MemUsed: 40},
		"missing": {T: now.Unix(), MemTotal: 100},
		"future":  {T: now.Add(time.Minute).Unix(), CPU: &cpu, Load: &load, MemTotal: 100, MemUsed: 40},
	}}
	for _, machine := range f.devices {
		if machine.ID == "active" || machine.ID == "stale" {
			d.add(machine.ID, machine.Name)
			d.setSessions(machine.ID, f.sessions[machine.ID])
		}
	}
	s := newTestService(t, f)
	s.devs = d
	return s
}

func machineResourceTestRead(t *testing.T, s *Service, role string) (machineResourceTestResponse, string) {
	t.Helper()
	w := serveAs(s, role, http.MethodGet, machineResourcesPath, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s as %q: %d %s", machineResourcesPath, role, w.Code, w.Body.String())
	}
	var out machineResourceTestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Complete || out.ExcludedCount != 1 || len(out.Machines) != 6 {
		t.Fatalf("list metadata: %s", w.Body.String())
	}
	return out, w.Body.String()
}

func machineResourceTestByID(t *testing.T, rows []machineResourceTestRow, id string) machineResourceTestRow {
	t.Helper()
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("no machine %q in %+v", id, rows)
	return machineResourceTestRow{}
}

func TestMachineResourcesMatchMachinesList(t *testing.T) {
	s := machineResourceTestService(t)
	text, err := s.toolMachinesList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var facts struct {
		Machines []machineFacts `json:"machines"`
	}
	if err := json.Unmarshal([]byte(text), &facts); err != nil {
		t.Fatal(err)
	}
	out, body := machineResourceTestRead(t, s, "")
	active := machineResourceTestByID(t, out.Machines, "active")
	fact := facts.Machines[0]
	if active.Name != "Active" || active.State != "online" || !active.Online || active.Cores == nil || *active.Cores != 8 ||
		active.CPUBusyPercent == nil || *active.CPUBusyPercent != 0 || active.Load1M == nil || *active.Load1M != 1.5 ||
		active.MemoryAvailableBytes == nil || *active.MemoryAvailableBytes != 60 || active.MemoryTotalBytes == nil || *active.MemoryTotalBytes != 100 ||
		active.SampleAgeSeconds == nil || *active.SampleAgeSeconds < 0 || *active.SampleAgeSeconds > 30 ||
		active.WorkingAgents == nil || *active.WorkingAgents != 2 || fact.WorkingAgents.Value != float64(2) {
		t.Fatalf("active does not match machines_list:\nsummary: %s\nmachines_list: %s", body, text)
	}
	stale := machineResourceTestByID(t, out.Machines, "stale")
	if stale.State != "online" || !stale.Online || stale.SampleAgeSeconds == nil || *stale.SampleAgeSeconds <= 30 ||
		stale.CPUBusyPercent != nil || stale.Load1M != nil || stale.MemoryAvailableBytes != nil || stale.MemoryTotalBytes != nil || stale.WorkingAgents == nil || *stale.WorkingAgents != 0 {
		t.Fatalf("stale sample was not retired: %s", body)
	}
	offline := machineResourceTestByID(t, out.Machines, "offline")
	if offline.State != "offline" || offline.Online || offline.WorkingAgents != nil || offline.SampleAgeSeconds != nil ||
		strings.Contains(body, "PRIVATE-CONNECTION-DETAIL") {
		t.Fatalf("offline privacy or null fields: %s", body)
	}
	pending := machineResourceTestByID(t, out.Machines, "pending")
	if pending.State != "pending" || !pending.Online || pending.Cores != nil || pending.SampleAgeSeconds != nil || pending.WorkingAgents != nil {
		t.Fatalf("pending machine invented fields: %s", body)
	}
	missing := machineResourceTestByID(t, out.Machines, "missing")
	if missing.CPUBusyPercent != nil || missing.Load1M != nil || missing.MemoryAvailableBytes != nil || missing.MemoryTotalBytes != nil || missing.WorkingAgents != nil {
		t.Fatalf("missing or ambiguous fields were invented: %s", body)
	}
	future := machineResourceTestByID(t, out.Machines, "future")
	if future.SampleAgeSeconds != nil || future.CPUBusyPercent != nil || future.Load1M != nil || future.MemoryAvailableBytes != nil || future.MemoryTotalBytes != nil {
		t.Fatalf("future sample was used: %s", body)
	}
}

func TestMachineWorkingAgentCounts(t *testing.T) {
	sessions := []proto.Session{
		{SessionID: "public", Status: proto.StatusWorking, State: proto.StateWorking},
		{SessionID: "space", Status: proto.StatusWorking, State: proto.StateWorking, Space: "health"},
		{SessionID: "space-directory", Status: proto.StatusWorking, State: proto.StateWorking, Cwd: identity.HealthDir("/home/example")},
		{SessionID: "assistant", Status: proto.StatusWorking, State: proto.StateWorking, Assistant: true},
		{SessionID: "archived", Status: proto.StatusWorking, State: proto.StateWorking, Archived: true},
		{SessionID: "hidden", Status: proto.StatusWorking, State: proto.StateWorking, Hierarchy: &proto.SessionHierarchy{HiddenByDirectory: true}},
		{SessionID: "idle", State: proto.StateIdle},
	}
	full, extension := machineWorkingAgentCounts(sessions)
	if full != 3 || extension != 1 {
		t.Fatalf("counts: full=%d extension=%d", full, extension)
	}
}

func TestMachineResourcesExtensionPrivacyAndSchema(t *testing.T) {
	s := machineResourceTestService(t)
	full, _ := machineResourceTestRead(t, s, "")
	for _, role := range []string{roleExtension, roleScoped} {
		extension, body := machineResourceTestRead(t, s, role)
		fullCount := machineResourceTestByID(t, full.Machines, "active").WorkingAgents
		extensionCount := machineResourceTestByID(t, extension.Machines, "active").WorkingAgents
		if fullCount == nil || *fullCount != 2 || extensionCount == nil || *extensionCount != 1 {
			t.Fatalf("space sessions changed the %s count: full=%v extension=%v body=%s", role, fullCount, extensionCount, body)
		}
		for _, private := range []string{"public-working", "space-working", "PUBLIC-TITLE", "SPACE-TITLE", "/work/public", "/work/moved", "health"} {
			if strings.Contains(body, private) {
				t.Errorf("role %s response contains %q: %s", role, private, body)
			}
		}
		var raw struct {
			Machines []map[string]json.RawMessage `json:"machines"`
		}
		if err := json.Unmarshal([]byte(body), &raw); err != nil {
			t.Fatal(err)
		}
		required := []string{"id", "name", "state", "online", "cores", "cpu_busy_percent", "load_1m", "memory_available_bytes", "memory_total_bytes", "sample_age_seconds", "working_agents"}
		for _, key := range required {
			if _, ok := raw.Machines[0][key]; !ok {
				t.Errorf("response row lacks %q: %s", key, body)
			}
		}
		if len(raw.Machines[0]) != len(required) {
			t.Errorf("response row gained private or unrelated fields: %s", body)
		}
		if age := machineResourceTestByID(t, extension.Machines, "active").SampleAgeSeconds; age != nil && math.IsNaN(*age) {
			t.Fatalf("invalid sample age: %s", body)
		}
	}
}
