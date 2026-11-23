package node

import (
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/node/imessageport"
)

// want are the node-side modules this build's tags keep (each one's <id>_test.go adds it).
var want []string

// The iMessage module is provided to the node exactly when the build tags keep it. Run with -tags
// persona_without_imessage or persona_core_only as well (scripts/ci/server.sh).
func TestIMessageFollowsTheBuildTags(t *testing.T) {
	if got, want := imessageport.Serve() != nil, slices.Contains(want, "imessage"); got != want {
		t.Fatalf("imessage provided to the node: %v, want %v for this build's tags", got, want)
	}
}
