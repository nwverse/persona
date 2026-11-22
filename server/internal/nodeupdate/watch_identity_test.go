package nodeupdate

import (
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/osproc"
)

func TestAliveIsIdentityFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		precise  string
		current  string
		known    bool
		birth    string
		err      error
		want     bool
		fallback bool
	}{
		{name: "exact-match", precise: "ticks:1", current: "ticks:1", known: true, want: true},
		{name: "exact-mismatch", precise: "ticks:1", current: "ticks:2", known: true},
		{name: "gone", precise: "ticks:1", known: true, birth: "start"},
		{name: "gone-legacy", known: true, birth: "start"},
		{name: "unknown-match", precise: "ticks:1", birth: "start", want: true, fallback: true},
		{name: "unknown-mismatch", precise: "ticks:1", birth: "another start", fallback: true},
		{name: "unknown-empty", precise: "ticks:1", fallback: true},
		{name: "unknown-error", precise: "ticks:1", birth: "start", err: errors.New("ps failed"), fallback: true},
		{name: "legacy-live", current: "ticks:1", known: true, birth: "start", want: true, fallback: true},
		{name: "legacy-unknown", birth: "start", want: true, fallback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := aliveIsWith(123, "start", tc.precise, func(pid int) (osproc.ProcIdentity, bool) {
				if pid != 123 {
					t.Fatalf("identity pid = %d", pid)
				}
				return osproc.ProcIdentity{Start: tc.current}, tc.known
			}, func(pid int) (string, error) {
				calls++
				if pid != 123 {
					t.Fatalf("fallback pid = %d", pid)
				}
				return tc.birth, tc.err
			})
			if got != tc.want || (calls == 1) != tc.fallback || calls > 1 {
				t.Fatalf("alive = %v, fallback calls = %d; want %v, fallback = %v", got, calls, tc.want, tc.fallback)
			}
		})
	}
}
