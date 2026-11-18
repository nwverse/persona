package herdr

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// A start's environment for a new shell (#10365): tab.create, workspace.create and pane.split send it as the API's
// env (an object of strings), and the CLI fallback as --env KEY=VALUE each, in key order. Without one neither appears.
func TestCreateCallsCarryEnv(t *testing.T) {
	env := map[string]string{"DISABLE_UPDATE_PROMPT": "true", "DISABLE_AUTO_UPDATE": "true"}
	var mu sync.Mutex
	got := map[string]map[string]any{}
	sock := fakeServer(t, func(method string, params map[string]any) string {
		mu.Lock()
		got[method] = params
		mu.Unlock()
		return `"result":{"tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2"},"pane":{"pane_id":"w1:p3"}}`
	})
	c := &Client{Socket: sock}
	ctx := context.Background()
	if _, err := c.TabCreate(ctx, "w1", "/tmp", "agent-1", env); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WorkspaceCreate(ctx, "/tmp", "tmp", env); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PaneSplit(ctx, "w1:p2", "right", "", env); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"DISABLE_AUTO_UPDATE": "true", "DISABLE_UPDATE_PROMPT": "true"}
	for _, m := range []string{"tab.create", "workspace.create", "pane.split"} {
		if !reflect.DeepEqual(got[m]["env"], want) {
			t.Errorf("%s env %v", m, got[m]["env"])
		}
	}
	if _, err := c.TabCreate(ctx, "w1", "/tmp", "agent-1", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["tab.create"]["env"]; ok {
		t.Errorf("tab.create without env sent %v", got["tab.create"])
	}

	bin, args := cliScript(t, `{"result":{"tab":{"tab_id":"w1:t2"},"root_pane":{"pane_id":"w1:p2"}}}`)
	cli := &Client{Bin: bin, Socket: filepath.Join(t.TempDir(), "missing.sock")}
	if _, err := cli.TabCreate(ctx, "w1", "/tmp", "agent-1", env); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(args); strings.TrimSpace(string(b)) !=
		"tab create --workspace w1 --cwd /tmp --label agent-1 --no-focus --env DISABLE_AUTO_UPDATE=true --env DISABLE_UPDATE_PROMPT=true" {
		t.Errorf("CLI args %q", b)
	}
	if _, err := cli.WorkspaceCreate(ctx, "/tmp", "tmp", nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(args); strings.Contains(string(b), "--env") {
		t.Errorf("CLI args without env %q", b)
	}
}
