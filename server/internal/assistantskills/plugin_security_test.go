package assistantskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPluginsRejectsRootSlashAndUnsafeSkillNames(t *testing.T) {
	dir := t.TempDir()
	keepDir := filepath.Join(dir, "keep-skill")
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keepFile := filepath.Join(keepDir, "SKILL.md")
	if err := os.WriteFile(keepFile, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	badNames := []string{
		"",
		"/",
		".",
		"..",
		".hidden",
		"a/b",
		`a\b`,
		"a,victim=x",
		"a=b",
		"a:b",
		"con",
		"trailing.",
		"trailing ",
		"bad\x00name",
		"bad\nname",
	}
	for _, name := range badNames {
		if _, err := ApplyPlugins(dir, []PluginSkill{{
			Name:   name,
			Plugin: "p",
			Digest: "d1",
			Files:  map[string][]byte{"SKILL.md": []byte("x\n")},
		}}); err == nil {
			t.Errorf("ApplyPlugins accepted unsafe skill name %q", name)
		}
	}
	// Existing directory contents must not have been wiped (e.g. by Name == "/").
	if b, err := os.ReadFile(keepFile); err != nil || string(b) != "keep\n" {
		t.Fatalf("existing plugin skill was damaged: %q, err=%v", b, err)
	}
}

func TestApplyPluginsRejectsStampInjectionAndInstallPluginsValidatesOldStamp(t *testing.T) {
	dir := t.TempDir()
	badMeta := []struct {
		plugin, digest string
	}{
		{"p,../victim=x", "d1"},
		{"p=x", "d1"},
		{"p\nx", "d1"},
		{"p x", "d1"},
		{"p", "d1,../victim=x"},
		{"p", "d1=x"},
		{"p", "d1\nx"},
		{"p", "d1\rx"},
	}
	for _, tc := range badMeta {
		if _, err := ApplyPlugins(dir, []PluginSkill{{
			Name:   "valid-skill",
			Plugin: tc.plugin,
			Digest: tc.digest,
			Files:  map[string][]byte{"SKILL.md": []byte("ok\n")},
		}}); err == nil {
			t.Errorf("ApplyPlugins accepted stamp-injecting metadata plugin=%q digest=%q", tc.plugin, tc.digest)
		}
	}

	// Defense-in-depth: even if a legacy or corrupted .persona-extension-skills stamp on disk
	// contains a traversal name like "../victim=x", installPlugins must never delete outside dir.
	parent := t.TempDir()
	victim := filepath.Join(parent, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	victimFile := filepath.Join(victim, "data.txt")
	if err := os.WriteFile(victimFile, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assistantDir := filepath.Join(parent, "assistant-skills")
	pluginsDir := filepath.Join(parent, "plugins")
	if err := os.MkdirAll(assistantDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assistantDir, pluginsCopied), []byte("../victim=p d1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installPlugins(assistantDir, pluginsDir, nil); err != nil {
		t.Fatalf("installPlugins: %v", err)
	}
	if b, err := os.ReadFile(victimFile); err != nil || string(b) != "precious\n" {
		t.Fatalf("installPlugins deleted directory outside assistantDir via stamp traversal: %v", err)
	}
}

func TestApplyPluginsRejectsUnsafeRelPathsDuplicatesAndCleansStaging(t *testing.T) {
	dir := t.TempDir()
	badRels := []string{
		"",
		"/",
		"../escape",
		`..\escape`,
		"C:drive.txt",
		".persona-plugin",
		".PERSONA-PLUGIN",
		".persona-other",
		"subdir/",
		"a\x00b",
	}
	for _, rel := range badRels {
		if _, err := ApplyPlugins(dir, []PluginSkill{{
			Name:   "my-skill",
			Plugin: "p",
			Digest: "d1",
			Files: map[string][]byte{
				"SKILL.md": []byte("ok\n"),
				rel:        []byte("bad\n"),
			},
		}}); err == nil {
			t.Errorf("ApplyPlugins accepted unsafe file path %q", rel)
		}
	}
	// Failed ApplyPlugins must not leave orphan staging directories behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".new") {
			t.Fatalf("orphan staging entry left after failed ApplyPlugins: %q", e.Name())
		}
	}

	// Duplicate case-folded skill names in one batch must be rejected.
	if _, err := ApplyPlugins(dir, []PluginSkill{
		{Name: "dup-skill", Plugin: "p", Digest: "d1", Files: map[string][]byte{"SKILL.md": []byte("a\n")}},
		{Name: "DUP-SKILL", Plugin: "p", Digest: "d2", Files: map[string][]byte{"SKILL.md": []byte("b\n")}},
	}); err == nil {
		t.Error("ApplyPlugins accepted duplicate case-folded skill names")
	}

	// Case-folded duplicate file paths within one skill must be rejected.
	if _, err := ApplyPlugins(dir, []PluginSkill{{
		Name:   "case-skill",
		Plugin: "p",
		Digest: "d1",
		Files: map[string][]byte{
			"SKILL.md": []byte("a\n"),
			"skill.md": []byte("b\n"),
		},
	}}); err == nil {
		t.Error("ApplyPlugins accepted case-colliding file paths")
	}

	// Empty non-nil Files map should create the skill directory and stamp cleanly instead of failing ENOENT.
	if _, err := ApplyPlugins(dir, []PluginSkill{{
		Name:   "empty-files",
		Plugin: "p",
		Digest: "d1",
		Files:  map[string][]byte{},
	}}); err != nil {
		t.Fatalf("ApplyPlugins with empty Files map failed: %v", err)
	}
}

func TestReferencesRejectsMalformedSingleQuotedSkillTag(t *testing.T) {
	text := `<persona-context kind='skill' ref='abc' bad=>Review</persona-context> hello`
	if _, err := References(text); err == nil {
		t.Fatalf("References silently ignored malformed single-quoted skill tag: %s", text)
	}
}
