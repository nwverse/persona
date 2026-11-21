package assistant

import (
	"context"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestOverviewTerminalFitMatchesStart(t *testing.T) {
	s, f := launcherFitHub(t)
	ctx := context.Background()
	if err := s.putSetting(ctx, launchersHiddenKey, []string{"gateway"}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		machine string
		fit     launcherFit
		run     string
		model   string
		effort  string
	}{
		{name: "hidden configured launcher is present", machine: "box", fit: fitAsSet, run: "gateway", model: "gw-model", effort: "high"},
		{name: "known miss uses launcher defaults", machine: "l4", fit: fitFallback, run: "claude"},
		{name: "unavailable inventory leaves settings intact", machine: "server", fit: fitUnchecked, run: "gateway", model: "gw-model", effort: "high"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			machine, ok := s.device(ctx, tt.machine)
			if !ok {
				t.Fatalf("missing fixture computer %q", tt.machine)
			}
			inventory, known := f.zshrc[tt.machine]
			run, fit := fitOn(machine, inventory, known, "gateway", false)
			if run != tt.run || fit != tt.fit {
				t.Fatalf("overview: got %s/%s, want %s/%s", run, fit, tt.run, tt.fit)
			}
			params, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: tt.machine})
			model, _ := params["model"].(string)
			effort, _ := params["effort"].(string)
			if err != nil || params["launcher"] != run || model != tt.model || effort != tt.effort {
				t.Fatalf("start for %s/%s: params %v, error %v", run, fit, params, err)
			}
			if fit == fitFallback {
				if _, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: tt.machine, Params: map[string]any{"launcher": "gateway"}}); err == nil {
					t.Fatal("an explicitly chosen missing launcher must still be refused")
				}
			}
		})
	}
}

func TestOverviewTerminalFallbackOrder(t *testing.T) {
	terminal := proto.Launcher{Name: "shell-codex", Source: "path"}
	for _, tt := range []struct {
		name       string
		requested  string
		everywhere bool
		known      bool
		inventory  []proto.Launcher
		run        string
		fit        launcherFit
	}{
		{name: "implicit built-in", everywhere: true, run: "claude", fit: fitAsSet},
		{name: "managed launcher", requested: "managed", everywhere: true, run: "managed", fit: fitAsSet},
		{name: "unconfirmed inventory", requested: "gateway", inventory: []proto.Launcher{terminal}, run: "gateway", fit: fitUnchecked},
		{name: "first compatible terminal launcher", requested: "gateway", known: true, inventory: []proto.Launcher{
			{Name: "runtime", Source: "paseo"}, {Name: "external", Source: proto.ExternalLauncherSource}, terminal,
		}, run: terminal.Name, fit: fitFallback},
		{name: "listed Claude takes precedence", requested: "gateway", known: true, inventory: []proto.Launcher{terminal, {Name: "claude"}}, run: "claude", fit: fitFallback},
	} {
		t.Run(tt.name, func(t *testing.T) {
			run, fit := fitOn(Device{}, tt.inventory, tt.known, tt.requested, tt.everywhere)
			if run != tt.run || fit != tt.fit {
				t.Fatalf("got %s/%s, want %s/%s", run, fit, tt.run, tt.fit)
			}
		})
	}
}

func TestOverviewMainChatCatalogFit(t *testing.T) {
	compatible := ConfiguredLauncher{Launcher: proto.Launcher{Name: "wrap", Source: "zshrc"}}
	incompatible := ConfiguredLauncher{Launcher: proto.Launcher{Name: "other", Source: "path", DisplayName: "Other Provider"}}
	picker := []ConfiguredLauncher{compatible, incompatible}
	fits := []ConfiguredLauncher{compatible}
	if !mainChatCatalogKnown(picker) || mainChatCatalogKnown([]ConfiguredLauncher{
		{Launcher: proto.Launcher{Name: "old-unclassified"}},
		{Launcher: proto.Launcher{Name: "managed", Source: "persona"}},
	}) {
		t.Fatal("only a source from the computer proves the Assistant picker is known")
	}
	for _, tt := range []struct {
		name  string
		fit   mainChatFit
		label string
	}{
		{name: "wrap", fit: mainChatFits},
		{name: "other", fit: mainChatRefused, label: "Other Provider"},
		{name: "missing", fit: mainChatGone},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fit, label := mainChatFitOn(tt.name, picker, fits)
			if fit != tt.fit || label != tt.label {
				t.Fatalf("got %v/%q, want %v/%q", fit, label, tt.fit, tt.label)
			}
		})
	}
}

func TestOverviewMainChatSubstituteMatchesStart(t *testing.T) {
	s, _ := kindFixture(t)
	ctx := context.Background()
	d := Device{ID: "server", Name: "box"}
	s.recordHealth(ctx, d.ID, "broken", healthResult{err: "not signed in", source: healthSourceCheck})
	launchers := []ConfiguredLauncher{
		{Launcher: proto.Launcher{Name: "claude"}},
		{Launcher: proto.Launcher{Name: "other-agent"}, AgentType: "codex"},
		{Launcher: proto.Launcher{Name: "external", Source: proto.ExternalLauncherSource}},
		{Launcher: proto.Launcher{Name: "broken"}},
		{Launcher: proto.Launcher{Name: "approved"}},
	}
	order := []string{"wrap", "other-agent", "absent", "external", "broken", "approved", "claude"}
	var checked []string
	snapshot := func(name string) bool {
		checked = append(checked, name)
		return name == "broken"
	}
	got := mainChatInsteadOn("wrap", "claude", launchers, order, snapshot)
	if want := s.mainChatInstead(ctx, d, "wrap", "claude", launchers, order); got != "approved" || got != want {
		t.Fatalf("snapshot chose %q, start chose %q, want approved", got, want)
	}
	if !slices.Equal(checked, []string{"broken", "approved"}) {
		t.Fatalf("checked unexpected launchers: %v", checked)
	}
	if got := mainChatInsteadOn("wrap", "claude", launchers, []string{"broken"}, snapshot); got != "" {
		t.Fatalf("chose an unapproved substitute: %q", got)
	}
	if got := mainChatInsteadOn("wrap", "", launchers, order, snapshot); got != "" {
		t.Fatalf("chose a substitute for an unknown agent kind: %q", got)
	}
}
