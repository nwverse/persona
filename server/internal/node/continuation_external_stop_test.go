package node

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// A continuation must use the external app-server's send contract. This regression deliberately keeps both
// terminal stop and automatic retirement refused, even when the thread is visible and accepts messages.
func TestContinuationCannotStopExternalCodex(t *testing.T) {
	n := remoteTestNode(t)
	home := t.TempDir()
	connectRemoteFixture(t, n, home, "original", remoteRaw)
	id := n.registerCodex(home, remoteRaw, "")
	for _, retire := range []bool{false, true} {
		stopped, err := n.stopWithCause(t.Context(), id, retire, proto.StopTaskCompleted)
		if err == nil || stopped || !strings.Contains(err.Error(), "this session belongs to another tool") {
			t.Fatalf("retire=%v: stopped=%v error=%v", retire, stopped, err)
		}
		code, _ := refusalOf(t, err)
		want := noTerminal
		if retire {
			want = notRetirable
		}
		if code != want {
			t.Fatalf("retire=%v: code=%q, want %q", retire, code, want)
		}
	}
}
