package node

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/compat"
)

// The hub names the assistant's directories relative to HOME (".persona/assistant", ".persona/health", ".persona/guest",
// or with the former name's spelling, which a hub sends for older nodes). The node prepares them in the state directory it
// runs on, whichever spelling it is sent: a custom PERSONA_HOME's root (#5505, nothing written into the default
// ~/.persona), the real one of ~/.persona and the former state directory (compat.StateDir) on the default layouts, and
// never a state directory that is not there (#1681: the former spelling joined to HOME made the former directory again
// once its alias was gone).
func TestAssistantDirsFollowTheDataDir(t *testing.T) {
	t.Setenv("PERSONA_SKILLS_DIR", t.TempDir())
	prepare := func(t *testing.T, n *Node, method, dir string) (string, error) {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"dir": dir, "claude_md": "# example"})
		out, err := n.handle(context.Background(), method, params)
		if err != nil {
			return "", err
		}
		cwd, _ := out.(map[string]any)["cwd"].(string)
		if _, err := os.Stat(filepath.Join(cwd, "CLAUDE.md")); err != nil {
			t.Fatalf("%s: no CLAUDE.md in %s: %v", dir, cwd, err)
		}
		return cwd, nil
	}
	former := compat.StateDir
	type dirCase struct{ method, dir, sub string }
	var cases []dirCase
	for _, root := range []string{".persona", former} {
		cases = append(cases,
			dirCase{"assistant.prepare", root + "/assistant", "assistant"},
			dirCase{"assistant.prepare", root + "/health", "health"},
			dirCase{"assistant.guest_prepare", root + "/guest", "guest"})
	}
	mkdir := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	absent := func(t *testing.T, path string) {
		t.Helper()
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("%s was made: %v", path, err)
		}
	}
	empty := func(t *testing.T, dir string) {
		t.Helper()
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("%s was written: %v %v", dir, entries, err)
		}
	}
	layouts := []struct {
		name string
		// setup makes the layout in home and returns the node's data directory (identity.DataDir's answer) and where
		// the assistant's directories belong.
		setup func(t *testing.T, home string) (dataDir, want string)
		check func(t *testing.T, home string)
	}{
		{"custom PERSONA_HOME", func(t *testing.T, home string) (string, string) {
			root := filepath.Join(t.TempDir(), "root")
			mkdir(t, root)
			t.Setenv("PERSONA_HOME", root)
			return root, root
		}, func(t *testing.T, home string) {
			absent(t, filepath.Join(home, ".persona"))
			absent(t, filepath.Join(home, former))
		}},
		{"migrated", func(t *testing.T, home string) (string, string) {
			mkdir(t, filepath.Join(home, ".persona"))
			return filepath.Join(home, ".persona"), filepath.Join(home, ".persona")
		}, func(t *testing.T, home string) { absent(t, filepath.Join(home, former)) }},
		{"migrated, a stray former directory", func(t *testing.T, home string) (string, string) {
			mkdir(t, filepath.Join(home, ".persona"))
			mkdir(t, filepath.Join(home, former))
			t.Setenv("PERSONA_HOME", filepath.Join(home, ".persona")) // as the services run: two stores need it
			return filepath.Join(home, ".persona"), filepath.Join(home, ".persona")
		}, func(t *testing.T, home string) { empty(t, filepath.Join(home, former)) }},
		{"kept under the former name, the current one its alias", func(t *testing.T, home string) (string, string) {
			mkdir(t, filepath.Join(home, former))
			if err := os.Symlink(former, filepath.Join(home, ".persona")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(home, ".persona"), filepath.Join(home, former)
		}, nil},
		{"kept under the former name, no alias", func(t *testing.T, home string) (string, string) {
			mkdir(t, filepath.Join(home, former))
			return filepath.Join(home, former), filepath.Join(home, former)
		}, func(t *testing.T, home string) { absent(t, filepath.Join(home, ".persona")) }},
	}
	for _, l := range layouts {
		t.Run(l.name, func(t *testing.T) {
			n, home := testNode(t)
			t.Setenv("PERSONA_HOME", "")
			dataDir, want := l.setup(t, home)
			n.dir, n.logger = dataDir, log.New(io.Discard, "", 0)
			for _, c := range cases {
				cwd, err := prepare(t, n, c.method, c.dir)
				if err != nil {
					t.Fatalf("%s: %v", c.dir, err)
				}
				if cwd != filepath.Join(want, c.sub) {
					t.Errorf("%s: prepared %s, want %s", c.dir, cwd, filepath.Join(want, c.sub))
				}
			}
			if l.check != nil {
				l.check(t, home)
			}
		})
	}

	t.Run("the state directory is not there", func(t *testing.T) {
		n, home := testNode(t)
		t.Setenv("PERSONA_HOME", "")
		n.dir, n.logger = filepath.Join(home, ".persona"), log.New(io.Discard, "", 0)
		for _, c := range cases {
			if cwd, err := prepare(t, n, c.method, c.dir); err == nil {
				t.Errorf("%s: prepared %s in a state directory made again", c.dir, cwd)
			}
		}
		absent(t, filepath.Join(home, ".persona"))
		absent(t, filepath.Join(home, former))
	})
}
