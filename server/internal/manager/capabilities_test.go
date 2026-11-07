package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/update"
)

func TestCapabilitiesReadOnly(t *testing.T) {
	home := isolate(t)
	var out bytes.Buffer
	if err := Main(context.Background(), update.Build{}, []string{"capabilities", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(reply.Capabilities, assistantwire.CapabilityConfigOwn) {
		t.Fatalf("missing config.own: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".persona")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capability query wrote state: %v", err)
	}
	if err := Main(context.Background(), update.Build{}, []string{"capabilities", "extra"}, &out); !errors.Is(err, ErrUsage) {
		t.Fatalf("unexpected positional argument: %v", err)
	}
}
