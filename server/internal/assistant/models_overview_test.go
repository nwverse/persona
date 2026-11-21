package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestModelOverviewUsesOneSavedInventory(t *testing.T) {
	s, hub, _ := modelService(t)
	seenAt := time.Date(2026, 1, 8, 13, 18, 0, 0, time.UTC)
	hub.mu.Lock()
	hub.devices = []Device{
		{ID: "unknown", Name: "Unknown", Online: true},
		{ID: "server", Name: "Server", Kind: "local", Online: true, ClaudeVersion: "2.0.0", LastSeen: seenAt},
		{ID: "laptop", Name: "Laptop", Online: true, ClaudeVersion: "2.0.0"},
	}
	hub.settings = &Settings{Launcher: "claude", MachineID: "server"}
	hub.mu.Unlock()

	ctx := t.Context()
	if err := s.saveModelRecord(ctx, "agent.new", 0, ModelBinding{Provider: "launcher.team-wrapper", Model: "claude-sonnet-4-5", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := s.putSetting(ctx, launchersZshrcKey, map[string][]proto.Launcher{
		"server": {{Name: "team-wrapper", Agent: "claude", Source: "zshrc"}},
		"laptop": {{Name: "claude", Agent: "claude", Source: "path"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.putSetting(ctx, launchersHiddenKey, []string{"team-wrapper"}); err != nil {
		t.Fatal(err)
	}

	read := func() modelOverview {
		t.Helper()
		var calls []DeviceCall
		withTrace := context.WithValue(ctx, deviceCallsForTest{}, &calls)
		r := httptest.NewRecorder()
		s.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/v1/model-config/overview", nil).WithContext(withTrace))
		if r.Code != http.StatusOK {
			t.Fatalf("overview: %d %s", r.Code, r.Body.String())
		}
		if len(calls) != 1 || calls[0].Device != DeviceAll || calls[0].Method != DeviceDevicesList {
			t.Fatalf("overview used more than the hub's device snapshot: %+v", calls)
		}
		var data modelOverview
		if err := json.Unmarshal(r.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	first := read()
	if len(first.Machines) != 3 || first.Machines[0].ID != "server" || !first.Machines[0].LastSeen.Equal(seenAt) {
		t.Fatalf("overview order or machine evidence: %+v", first.Machines)
	}
	expect := []struct {
		id, launcher, model string
		fit                 launcherFit
	}{
		{"server", "team-wrapper", "claude-sonnet-4-5", fitAsSet},
		{"laptop", "claude", "", fitFallback},
		{"unknown", "team-wrapper", "claude-sonnet-4-5", fitUnchecked},
	}
	for i, want := range expect {
		row := first.Machines[i]
		if row.ID != want.id || row.NewAgent.Launcher != want.launcher || row.NewAgent.Model != want.model || row.NewAgent.Fit != want.fit {
			t.Errorf("%s fit: %+v, want %s / %s / %s", row.ID, row.NewAgent, want.launcher, want.model, want.fit)
		}
	}
	if first.Revision == "" || first.GeneratedAt.IsZero() || first.Revision != read().Revision {
		t.Fatal("a stable snapshot should have the same revision on repeated reads")
	}
}

func TestModelOverviewFixtureMatchesWire(t *testing.T) {
	fixture := filepath.Join("..", "..", "..", "spec", "fixtures", "model-overview", "overview.json")
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var data modelOverview
	if err := dec.Decode(&data); err != nil {
		t.Fatal(err)
	}
	if len(data.Machines) != 3 || data.Machines[1].NewAgent.Fit != fitFallback || data.Machines[2].NewAgent.Fit != fitUnchecked {
		t.Fatalf("overview fixture lost its distinct cases: %+v", data.Machines)
	}
}

func TestAgentPickerReportsDefaultHereBesideConfiguredDefault(t *testing.T) {
	s, hub, _ := modelService(t)
	hub.mu.Lock()
	hub.devices = []Device{{ID: "server", Name: "Server", Kind: "local", Online: true, Reachable: true, ClaudeVersion: "2.0.0"}}
	hub.zshrc = map[string][]proto.Launcher{"server": {{Name: "claude", Agent: "claude", Source: "path"}}}
	hub.mu.Unlock()
	if err := s.saveModelRecord(t.Context(), "agent.new", 0, ModelBinding{
		Provider: "launcher.team-wrapper", Model: "claude-sonnet-4-5", Effort: "high",
	}); err != nil {
		t.Fatal(err)
	}
	r := request(s, http.MethodGet, "/api/v1/machines/server/launchers", "")
	if r.Code != http.StatusOK {
		t.Fatalf("launcher picker: %d %s", r.Code, r.Body.String())
	}
	var response struct {
		Default struct {
			Provider string        `json:"provider"`
			Model    string        `json:"model"`
			Here     modelAgentFit `json:"here"`
		} `json:"default"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	got := response.Default
	if got.Provider != "team-wrapper" || got.Model != "claude-sonnet-4-5" ||
		got.Here.Fit != fitFallback || got.Here.Launcher != "claude" || got.Here.InsteadOf != "team-wrapper" ||
		got.Here.Model != "" || got.Here.Effort != "" {
		t.Fatalf("default and default here should distinguish configuration from the fit: %+v", got)
	}
}
