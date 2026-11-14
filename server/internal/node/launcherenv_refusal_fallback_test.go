package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// refusalFallbackOff is the variable that keeps Claude Code from moving a refused reply to a fallback model of its own.
const refusalFallbackOff = "CLAUDE_CODE_DISABLE_REFUSAL_FALLBACK"

// envEntries is every value env (KEY=value entries) gives k, in order: more than one would leave it to the reader.
func envEntries(env []string, k string) []string {
	var out []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, k+"="); ok {
			out = append(out, v)
		}
	}
	return out
}

// Claude Code's automatic switch after a refusal goes to a model ID of its own, which a gateway may not serve: every
// gateway-shaped provider that is not Anthropic's own API turns it off, whatever its model list says, and its own
// environment, applied last, can set it empty to restore Claude Code's default (#7319).
func TestProviderEnvTurnsRefusalFallbackOffBehindGateways(t *testing.T) {
	const gw = "https://gw.example.com"
	gateway := proto.ManagedLauncher{Name: "gw", Kind: "api", BaseURL: gw, APIKey: "sk-test-1", Model: "m1"}
	with := func(f func(*proto.ManagedLauncher)) proto.ManagedLauncher {
		l := gateway
		f(&l)
		return l
	}
	for _, tc := range []struct {
		name  string
		l     proto.ManagedLauncher
		value string // "-" for unset
	}{
		{"gateway", gateway, "1"},
		{"gateway with a path and /v1", with(func(l *proto.ManagedLauncher) { l.BaseURL = gw + "/prefix/v1/" }), "1"},
		{"gateway shaped by a field, not the kind", proto.ManagedLauncher{Name: "gw", BaseURL: gw, Model: "m1", TimeoutMS: 60000}, "1"},
		{"gateway whose model list names other models", with(func(l *proto.ManagedLauncher) {
			l.Models = []proto.ProviderModel{{ID: "m1"}, {ID: "fallback-model"}}
		}), "1"},
		{"a host that only starts like the official one", with(func(l *proto.ManagedLauncher) { l.BaseURL = "https://api.anthropic.com.gw.example.com" }), "1"},
		{"the official API", with(func(l *proto.ManagedLauncher) { l.BaseURL = "https://api.anthropic.com" }), "-"},
		{"the official API in capitals with /v1", with(func(l *proto.ManagedLauncher) { l.BaseURL = " https://API.Anthropic.com/v1/ " }), "-"},
		{"no base URL", with(func(l *proto.ManagedLauncher) { l.BaseURL = "" }), "-"},
		{"a launcher from before #2455", proto.ManagedLauncher{Name: "gw", BaseURL: gw, APIKey: "sk-test-1", Model: "m1"}, "-"},
		{"its own environment empties it", with(func(l *proto.ManagedLauncher) { l.Env = refusalFallbackOff + "=" }), ""},
		{"its own environment empties it in quotes", with(func(l *proto.ManagedLauncher) { l.Env = "export " + refusalFallbackOff + `=""` }), ""},
		{"its own environment sets it on the official API", with(func(l *proto.ManagedLauncher) {
			l.BaseURL, l.Env = "https://api.anthropic.com", refusalFallbackOff+"=1"
		}), "1"},
	} {
		for _, main := range []bool{false, true} {
			got, set := providerEnv(tc.l, "", "", main)[refusalFallbackOff]
			if tc.value == "-" {
				if set {
					t.Errorf("%s (main %v): %s=%q, want unset", tc.name, main, refusalFallbackOff, got)
				}
				continue
			}
			if !set || got != tc.value {
				t.Errorf("%s (main %v): %s=%q (set %v), want %q", tc.name, main, refusalFallbackOff, got, set, tc.value)
			}
		}
	}
}

// What `personad launch` gets for a provider (launcher.env) and does with it: the variable reaches the claude it execs,
// a Persona conversation's too (PERSONA_ASSISTANT=1), after the shell's, and its settings env, which Claude Code applies
// over the process environment. An empty value from the launcher's own environment replaces the shell's, so claude sees
// it empty, once. A provider that sets none leaves the shell's alone.
func TestRefusalFallbackReachesPersonadLaunch(t *testing.T) {
	n := agentNode(t, false)
	n.setManaged([]proto.ManagedLauncher{
		{Name: "gw", Kind: "api", BaseURL: "https://gw.example.com", APIKey: "sk-test-1", Model: "m1"},
		{Name: "served", Kind: "api", BaseURL: "https://gw.example.com", APIKey: "sk-test-1", Model: "m1", Env: refusalFallbackOff + "="},
		{Name: "official", Kind: "api", BaseURL: "https://api.anthropic.com", APIKey: "sk-test-1", Model: "m1"},
	})
	shell := []string{"PATH=/usr/bin", refusalFallbackOff + "=0"}
	for _, tc := range []struct {
		launcher, want string // want "-": what the shell gave stays
		replaced       bool   // the shell's value is dropped, not only followed
	}{
		{"gw", "1", false},
		{"served", "", true},
		{"official", "-", false},
	} {
		resp := n.launcherEnv(tc.launcher)
		if e, _ := resp["error"].(string); e != "" {
			t.Fatalf("%s: %s", tc.launcher, e)
		}
		env, _ := resp["env"].([]string)
		clear, _ := resp["clear"].([]string)
		if tc.want == "-" {
			if got := envEntries(env, refusalFallbackOff); len(got) != 0 {
				t.Errorf("%s: launch env sets %s=%q", tc.launcher, refusalFallbackOff, got)
			}
			c, e := launchEnvFor(clear, env, false)
			if got := envEntries(append(withoutEnv(shell, c), e...), refusalFallbackOff); len(got) != 1 || got[0] != "0" {
				t.Errorf("%s: the shell's %s became %q", tc.launcher, refusalFallbackOff, got)
			}
			if _, set := managedSettingsEnv(mustManaged(t, n, tc.launcher))[refusalFallbackOff]; set {
				t.Errorf("%s: settings env sets %s", tc.launcher, refusalFallbackOff)
			}
			continue
		}
		for _, assistant := range []bool{false, true} {
			c, e := launchEnvFor(clear, env, assistant)
			final := append(withoutEnv(shell, c), e...) // as Launch builds it
			got := envEntries(final, refusalFallbackOff)
			if len(got) == 0 || got[len(got)-1] != tc.want || tc.replaced && len(got) != 1 {
				t.Errorf("%s (assistant %v): claude gets %s=%q, want %q last (only: %v)", tc.launcher, assistant, refusalFallbackOff, got, tc.want, tc.replaced)
			}
		}
		if got, set := managedSettingsEnv(mustManaged(t, n, tc.launcher))[refusalFallbackOff]; !set || got != tc.want {
			t.Errorf("%s: settings env %s=%q (set %v), want %q", tc.launcher, refusalFallbackOff, got, set, tc.want)
		}
	}
}

func mustManaged(t *testing.T, n *Node, name string) proto.ManagedLauncher {
	t.Helper()
	l, ok := n.managedLauncher(name)
	if !ok {
		t.Fatalf("no managed launcher %s", name)
	}
	return l
}

// The settings files Persona generates carry it: an Agent's start (claude-settings, with Persona's hooks or alone), a
// start whose hooks are a plugin (the env alone), and a Persona conversation's file, which assistantCompactionSettings
// builds over the launcher's for the main conversation and every other. A provider that empties it pins it empty, over
// the user's settings.json.
func TestRefusalFallbackInGeneratedSettings(t *testing.T) {
	n := agentNode(t, false)
	n.setManaged([]proto.ManagedLauncher{
		{Name: "gw", Kind: "api", BaseURL: "https://gw.example.com", APIKey: "sk-test-1", Model: "m1"},
		{Name: "served", Kind: "api", BaseURL: "https://gw.example.com", APIKey: "sk-test-1", Model: "m1", Env: refusalFallbackOff + "="},
		{Name: "official", Kind: "api", BaseURL: "https://api.anthropic.com", APIKey: "sk-test-1", Model: "m1"},
	})
	writeFile(t, filepath.Join(n.dir, "claude-settings.json"), `{"hooks":{"Stop":[]}}`)
	profile := filepath.Join(n.home, "profile")
	writeFile(t, filepath.Join(profile, "settings.json"), `{"env":{"`+refusalFallbackOff+`":"1"}}`)
	cwd := t.TempDir()
	read := func(t *testing.T, path string) map[string]any {
		t.Helper()
		s, err := readLauncherSettings(path, cwd, n.home)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, tc := range []struct {
		launcher, want string // "-" for unset
	}{
		{"gw", "1"},
		{"served", ""},
		{"official", "-"},
	} {
		check := func(what string, s map[string]any) {
			t.Helper()
			env, _ := s["env"].(map[string]any)
			got, set := env[refusalFallbackOff]
			if tc.want == "-" {
				if set {
					t.Errorf("%s, %s: %s=%v, want unset", tc.launcher, what, refusalFallbackOff, got)
				}
				return
			}
			if !set || got != tc.want {
				t.Errorf("%s, %s: %s=%v (set %v), want %q", tc.launcher, what, refusalFallbackOff, got, set, tc.want)
			}
		}
		agent, hooks := n.claudeStartSettings(context.Background(), tc.launcher, "claude", cwd)
		if hooks != "" {
			t.Fatalf("%s: hooks came as a plugin: %s", tc.launcher, hooks)
		}
		s := read(t, agent)
		if s["hooks"] == nil {
			t.Errorf("%s: the Agent's settings lost Persona's hooks: %v", tc.launcher, s)
		}
		check("an Agent's settings", s)
		l := mustManaged(t, n, tc.launcher)
		envOnly, err := n.mergeManagedSettings(tc.launcher, l, "", profile)
		if err != nil {
			t.Fatal(err)
		}
		check("the env-only settings", read(t, envOnly))
		for _, main := range []bool{true, false} {
			settings, _ := n.claudeStartSettings(context.Background(), tc.launcher, "assistant", cwd, profile)
			c := n.assistantCompactionPolicy(tc.launcher, cwd, settings, modelExecution{Model: "m1", IsMain: main})
			path, err := n.assistantCompactionSettings("11111111-2222-3333-4444-555555555555", cwd, settings, c)
			if err != nil {
				t.Fatal(err)
			}
			check("a Persona conversation's settings (main "+map[bool]string{true: "yes", false: "no"}[main]+")", read(t, path))
		}
	}
}

// The main conversation on the Agent SDK: the bridge's child gets it in its environment, replacing the node's, and in
// the settings env the persona_start line pins. An empty value from the launcher's environment reaches both.
func TestRefusalFallbackReachesTheSDKStart(t *testing.T) {
	agentSandboxFakes(t, macSandbox)
	b := sdkBridge{js: jsRuntime{Path: "/opt/bin/node"}, script: "/x/bridge.mjs", claude: "/opt/bin/claude"}
	n := agentNode(t, false)
	profile := filepath.Join(n.home, "profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", profile)
	n.setManaged([]proto.ManagedLauncher{
		{Name: "gw", Kind: "api", BaseURL: "https://gw.example.com", APIKey: "sk-test-1", Model: "m1"},
		{Name: "served", Kind: "api", BaseURL: "https://gw.example.com", APIKey: "sk-test-1", Model: "m1", Env: refusalFallbackOff + "="},
		{Name: "official", Kind: "api", BaseURL: "https://api.anthropic.com", APIKey: "sk-test-1", Model: "m1"},
	})
	for _, tc := range []struct {
		launcher, want string // "-": not set by Persona, the node's stays
	}{
		{"gw", "1"},
		{"served", ""},
		{"official", "-"},
	} {
		s := sdkStart{id: "s1", cwd: filepath.Join(n.dir, "assistant"), configDir: profile, mcp: map[string]any{}}
		cmd, first, err := n.sdkCommand(b, tc.launcher, s, []string{"PATH=/usr/bin", refusalFallbackOff + "=0"})
		if err != nil {
			t.Fatal(err)
		}
		var start struct {
			Options struct{ Settings struct{ Env map[string]any } }
		}
		if err := json.Unmarshal(first, &start); err != nil {
			t.Fatal(err)
		}
		pinned, set := start.Options.Settings.Env[refusalFallbackOff]
		proc := envEntries(cmd.Environ(), refusalFallbackOff)
		if tc.want == "-" {
			if set || len(proc) != 1 || proc[0] != "0" {
				t.Errorf("%s: settings %v (set %v), process %q; want the node's own", tc.launcher, pinned, set, proc)
			}
			continue
		}
		if !set || pinned != tc.want || len(proc) != 1 || proc[0] != tc.want {
			t.Errorf("%s: settings %v (set %v), process %q; want %q in both", tc.launcher, pinned, set, proc, tc.want)
		}
	}
}
