package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleCodexConfig = `# my codex
model = "gpt-old"   # the default one
model_reasoning_effort = 'high'
hide_agent_reasoning = false
notify = ["a", "b"]

[model_providers.example]
name = "example"
model = "not-top-level"

[projects."/home/me/code"]
trust_level = "trusted"
`

// Only top-level strings and bools are read; a key of the same name inside a table is not the setting.
func TestCodexTopLevel(t *testing.T) {
	v := tomlTopLevel(sampleCodexConfig)
	if v["model"] != "gpt-old" || v["model_reasoning_effort"] != "high" || v["hide_agent_reasoning"] != false {
		t.Fatalf("%v", v)
	}
	if _, ok := v["notify"]; ok {
		t.Fatal("an array is not a setting we read")
	}
}

// Setting replaces the line in place, adding goes before the first table, removing drops the line; everything else
// in the file stays byte for byte.
func TestCodexSetKeepsTheRest(t *testing.T) {
	got := tomlSet(sampleCodexConfig, "model", "gpt-5.5")
	want := `# my codex
model = "gpt-5.5"
model_reasoning_effort = 'high'
hide_agent_reasoning = false
notify = ["a", "b"]

[model_providers.example]
name = "example"
model = "not-top-level"

[projects."/home/me/code"]
trust_level = "trusted"
`
	if got != want {
		t.Fatalf("replace:\n%s", got)
	}
	got = tomlSet(sampleCodexConfig, "sandbox_mode", "workspace-write")
	if want := `notify = ["a", "b"]
sandbox_mode = "workspace-write"

[model_providers.example]`; !strings.Contains(got, want) {
		t.Fatalf("add:\n%s", got)
	}
	got = tomlSet(sampleCodexConfig, "hide_agent_reasoning", nil)
	if strings.Contains(got, "hide_agent_reasoning") || !strings.Contains(got, "model_reasoning_effort = 'high'\nnotify") {
		t.Fatalf("remove:\n%s", got)
	}
	if got := tomlSet("", "model", `a "quoted" one`); got != "model = \"a \\\"quoted\\\" one\"\n" {
		t.Fatalf("empty file: %q", got)
	}
	if v := tomlTopLevel(tomlSet("", "model", `a "quoted" one`)); v["model"] != `a "quoted" one` {
		t.Fatalf("round trip: %v", v)
	}
}

// End to end on a node: reading, writing a value, rejecting one not in the list, and the file's other lines kept.
func TestCodexSettingsOnTheNode(t *testing.T) {
	home := t.TempDir()
	n := &Node{home: home}
	p := filepath.Join(home, ".codex", "config.toml")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte(sampleCodexConfig), 0o600)
	if err := n.setCodexSetting("model_reasoning_effort", "turbo"); err == nil {
		t.Fatal("an option not in the list was written")
	}
	if err := n.setCodexSetting("approval_policy", "never"); err != nil {
		t.Fatal(err)
	}
	list, err := n.getCodexSettings()
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]any{}
	for _, s := range list {
		vals[s.Key] = s.Value
	}
	if vals["approval_policy"] != "never" || vals["model"] != "gpt-old" || vals["sandbox_mode"] != nil {
		t.Fatalf("%v", vals)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "[projects.\"/home/me/code\"]\ntrust_level = \"trusted\"") {
		t.Fatalf("the rest of the file changed:\n%s", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

// The app shows the node's details as written (iOS does not look them up): they say what an option does in plain
// words and never name a config.toml value (#3855).
func TestCodexDetailsNameNoOptionValue(t *testing.T) {
	for _, s := range codexSettings {
		for _, o := range s.Options {
			if strings.Contains(s.Detail, o) {
				t.Errorf("%s: detail %q names the option %q", s.Key, s.Detail, o)
			}
		}
	}
}

func TestCodexServiceTierSetting(t *testing.T) {
	n := &Node{home: t.TempDir()}
	for _, tier := range []string{"fast", "default"} {
		if err := n.setCodexSetting("service_tier", tier); err != nil {
			t.Fatal(err)
		}
		settings, err := n.getCodexSettings()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, setting := range settings {
			if setting.Key == "service_tier" {
				found = setting.Value == tier
			}
		}
		if !found {
			t.Fatalf("speed %q missing from settings", tier)
		}
	}
	if err := n.setCodexSetting("service_tier", "invented"); err == nil {
		t.Fatal("invalid speed accepted")
	}
	if err := n.setCodexSetting("service_tier", nil); err != nil {
		t.Fatal(err)
	}
}
