package extmgr

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/lark"
)

func TestLarkCLIConnectorOnlyRegistersWhenConfigured(t *testing.T) {
	h := newTestManager(t)
	h.connectors = h.newConnectors(nil)
	h.cfg.Lark = lark.Config{}
	h.registerConnectors()
	if h.connectors.Get("larkcli") != nil {
		t.Fatal("larkcli registered without lark.cli")
	}

	h = newTestManager(t)
	h.connectors = h.newConnectors(nil)
	bin, err := filepath.Abs(filepath.Join("..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: bin, Profile: "example"}}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "success")
	h.registerConnectors()
	if h.connectors.Get("larkcli") == nil {
		t.Fatal("larkcli was not registered with lark.cli")
	}
	var found bool
	for _, info := range h.listedConnectors(context.Background(), true) {
		if info.ID == "larkcli" {
			found = true
			if info.Status != "ok" || info.Account != "Example User" {
				t.Fatalf("settings connector = %+v", info)
			}
		}
	}
	if !found {
		t.Fatal("larkcli missing from settings connector list")
	}
}
