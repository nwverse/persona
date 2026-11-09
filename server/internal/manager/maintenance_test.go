package manager

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/update"
)

func TestMaintenanceHelpRoutesWithoutManagerConfig(t *testing.T) {
	var out bytes.Buffer
	if err := Main(context.Background(), update.Build{}, []string{"maintenance", "--help"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no exit, resume or update execution") {
		t.Fatal(out.String())
	}
}
