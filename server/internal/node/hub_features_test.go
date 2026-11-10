package node

import (
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/nodeproto"
)

func TestHubFeaturesConfiguration(t *testing.T) {
	n, _ := testNode(t)
	if n.hubSupportsPersonaContracts() {
		t.Fatal("a node without a hub announcement must keep legacy aliases")
	}
	for _, tt := range []struct {
		name   string
		params map[string]any
		want   bool
	}{
		{"current hub", map[string]any{"machine_id": "computer", "hub_features": []string{nodeproto.PersonaContracts}}, true},
		{"partial configuration", map[string]any{"keep_sessions": []string{}}, true},
		{"old hub after reconnect", map[string]any{"machine_id": "computer", "hub_features": []string{}}, false},
		{"current hub again", map[string]any{"hub_features": []string{nodeproto.PersonaContracts}}, true},
		{"old manager handover", map[string]any{"machine_id": "computer"}, false},
		{"unknown feature", map[string]any{"hub_features": []string{"future_contracts"}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := nodeCall(t, n, "node.configure", tt.params); err != nil {
				t.Fatal(err)
			}
			if got := n.hubSupportsPersonaContracts(); got != tt.want {
				t.Fatalf("hubSupportsPersonaContracts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHubFeaturesOnlyUpdatePreservesClaudeDirectories(t *testing.T) {
	n, _ := testNode(t)
	known := []string{t.TempDir()}
	n.cdirs = &claudeDirs{known: slices.Clone(known), scan: func() map[int]procEnv { return nil }}
	for _, features := range [][]string{{nodeproto.PersonaContracts}, {}} {
		if _, err := nodeCall(t, n, "node.configure", map[string]any{"hub_features": features}); err != nil {
			t.Fatal(err)
		}
		n.cdirs.mu.Lock()
		got := slices.Clone(n.cdirs.known)
		n.cdirs.mu.Unlock()
		if !slices.Equal(got, known) {
			t.Fatalf("capability-only update forgot Claude directories: %v", got)
		}
	}
}
