package assistant

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// A conversation the assistant had before the Persona-only cutover, offline now, records the folder it ran in under
// the old state directory (its transcript's cwd, which the node lists it with). It is still the built-in assistant's,
// and a health one is still in the health space; another manager's, or a folder inside it, is not (#163).
func TestConversationsFromBeforeTheCutoverAreTheAssistants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PERSONA_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".persona", "assistant"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(home, "."+"jar"+"vis") // the state directory before the cutover; gone now
	builtin := fleetMachine{ID: "server", Home: home, manager: ""}
	other := fleetMachine{ID: "server", Home: home, manager: "m-0b0b0b"}
	for _, c := range []struct {
		cwd           string
		builtin, mine bool
		space         string
	}{
		{filepath.Join(old, "assistant"), true, false, ""},
		{filepath.Join(old, "health"), true, false, SpaceHealth},
		{filepath.Join(old, "assistant-m-0b0b0b"), false, true, ""},
		{filepath.Join(old, "assistant", "patrol"), false, false, ""},
		{filepath.Join(home, ".persona", "assistant"), true, false, ""},
	} {
		sess := proto.Session{SessionID: "s", Cwd: c.cwd, Status: proto.StatusOffline}
		if got := builtin.isAssistant(sess); got != c.builtin {
			t.Errorf("%s: the built-in's %v, want %v", c.cwd, got, c.builtin)
		}
		if got := other.isAssistant(sess); got != c.mine {
			t.Errorf("%s: m-0b0b0b's %v, want %v", c.cwd, got, c.mine)
		}
		if c.builtin {
			if got := spaceOf(sess); got != c.space {
				t.Errorf("%s: space %q, want %q", c.cwd, got, c.space)
			}
		}
	}
	// A conversation in a host pane is a terminal session, wherever it is.
	if builtin.isAssistant(proto.Session{SessionID: "s", Cwd: filepath.Join(old, "assistant"), PaneID: "w1:p1"}) {
		t.Error("a pane counted as the assistant's")
	}
}
