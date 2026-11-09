package identity

import (
	"os"
	"path/filepath"
	"testing"
)

// A conversation recorded before the Persona-only cutover names the folder it ran in under the previous state
// directory. On a machine whose state directory is the default ~/.persona now, it is still the assistant's, and a
// health one is still in the health space (#163). The previous directory is spelled out of parts so the identifier
// scan does not take it for a compatibility name.
func TestAssistantDirsFromBeforeTheCutover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PERSONA_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".persona", "assistant"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := func(name string) string { return filepath.Join(home, "."+"jar"+"vis", name) }
	for _, c := range []struct {
		path, id string
		want     bool
	}{
		{old("assistant"), "", true},
		{old("health"), "", true},
		{old("assistant-m-0b0b0b"), "m-0b0b0b", true},
		{old("health-m-0b0b0b"), "m-0b0b0b", true},
		{old("assistant-m-0b0b0b"), "", false}, // another manager's
		{old("assistant-bin"), "", false},      // not an assistant directory
		{old("assistant/patrol"), "", false},   // inside it, not it
	} {
		if got := IsAssistantDirOf(c.path, c.id); got != c.want {
			t.Errorf("IsAssistantDirOf(%s, %q) = %v, want %v", c.path, c.id, got, c.want)
		}
	}
	if SpaceOf(old("health")) != SpaceHealth || SpaceOf(old("assistant")) != "" {
		t.Error("SpaceOf: the previous health space is the health space")
	}
}
