package node

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A Persona-managed launcher made from a Paseo provider keeps the provider's name; the runtime offering that name
// must not take its starts or its assistant conversations: personad launch runs it, as the pickers list it.
func TestManagedLauncherNamedLikeARuntimeProviderIsNotTheRuntimes(t *testing.T) {
	n, _ := runtimeNode(t)
	hello := dshHello()
	hello["caps"].(map[string]any)["assistant"] = true
	host := dialRuntime(t, n, hello)
	started := 0
	host.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.start" {
			started++
			return map[string]any{"session": map[string]any{"session_id": "session-new", "cwd": "/w", "status": "idle"}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	launcherCache.Lock()
	launcherCache.at, launcherCache.list = time.Now(), []proto.Launcher{{Name: "claude"}}
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list = time.Time{}, nil
		launcherCache.Unlock()
	})
	if n.runtimeOf("dsh") == nil {
		t.Fatal("the runtime's own launcher: the runtime runs it")
	}
	n.setManaged([]proto.ManagedLauncher{{Name: "dsh", BaseURL: "https://gw.example", Model: "m"}})
	if n.runtimeOf("dsh") != nil || n.assistantRuntime("dsh") != nil {
		t.Fatal("a managed launcher of the runtime's name went to the runtime")
	}
	if _, ok := n.runtimeLauncherModels("dsh"); ok {
		t.Fatal("the managed launcher's models came from the runtime")
	}
	_, _, ok := n.runtimeStart(context.Background(), runtimeStartParams{Cwd: "/w", Launcher: "dsh", Prompt: "hi"})
	if ok || started != 0 {
		t.Fatalf("the start went to the runtime (ok %v, starts %d)", ok, started)
	}
}

// Paseo's providers start agents only where Paseo is an enabled host: elsewhere its connected runtime offers none in
// launchers.list, and a start naming one does not go to it.
func TestEngineRuntimeProvidersNeedTheHostEnabled(t *testing.T) {
	n, _ := runtimeNode(t)
	hello := dshHello()
	hello["runtime"], hello["launcher"] = proto.EnginePaseo, "cc-relay"
	dialRuntime(t, n, hello)
	launcherCache.Lock()
	launcherCache.at, launcherCache.list = time.Now(), []proto.Launcher{{Name: "claude"}}
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list = time.Time{}, nil
		launcherCache.Unlock()
	})
	names := func() []string {
		raw, err := nodeCall(t, n, "launchers.list", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		var ls struct {
			Launchers []proto.Launcher `json:"launchers"`
		}
		_ = json.Unmarshal(raw, &ls)
		var out []string
		for _, l := range ls.Launchers {
			out = append(out, l.Name)
		}
		return out
	}
	if got := names(); len(got) != 1 || got[0] != "claude" || n.runtimeOf("cc-relay") != nil {
		t.Fatalf("Paseo not enabled: listed %v", got)
	}
	enablePaseo(n)
	if got := names(); len(got) != 2 || got[1] != "cc-relay" || n.runtimeOf("cc-relay") == nil {
		t.Fatalf("Paseo enabled: listed %v", got)
	}
}

// enablePaseo makes Paseo one of n's hosts: its runtime's providers are offered and started only then.
func enablePaseo(n *Node) {
	n.eng.mu.Lock()
	n.eng.set = &proto.EngineChoice{Hosts: []string{proto.EngineHerdr, proto.EnginePaseo}, Default: proto.EngineHerdr}
	n.eng.mu.Unlock()
}
