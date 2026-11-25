package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebShellManifestValidation(t *testing.T) {
	good := `{"manifest_version": 1, "id": "focus-ui", "name": "Focus UI", "version": "1.0.0", "contributes": {"web_shells": [{"id": "focus-4layer", "name": "Focus Workspace", "description": "Compact 4-layer sidebar", "dir": "dist"}]}}`
	m, err := Parse([]byte(good))
	if err != nil {
		t.Fatalf("valid web_shells manifest failed: %v", err)
	}
	shells := m.WebShells()
	if len(shells) != 1 || shells[0].ID != "focus-4layer" || shells[0].Dir != "dist" {
		t.Fatalf("unexpected shells: %+v", shells)
	}

	bad := map[string]string{
		"reserved builtin": `{"id": "builtin", "name": "Builtin", "dir": "dist"}`,
		"reserved default": `{"id": "default", "name": "Default", "dir": "dist"}`,
		"empty name":       `{"id": "x", "name": "   ", "dir": "dist"}`,
		"bad dir escape":   `{"id": "x", "name": "X", "dir": "../dist"}`,
		"hidden dir":       `{"id": "x", "name": "X", "dir": ".dist"}`,
		"unknown field":    `{"id": "x", "name": "X", "dir": "dist", "extra": 1}`,
	}
	for label, item := range bad {
		doc := `{"manifest_version": 1, "id": "focus-ui", "name": "Focus UI", "version": "1.0.0", "contributes": {"web_shells": [` + item + `]}}`
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}

func TestCheckAndReadWebShellFiles(t *testing.T) {
	ws := WebShellItem{ID: "focus", Name: "Focus", Dir: "dist"}
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Missing index.html fails.
	if err := CheckWebShellFiles(ws, root); err == nil {
		t.Fatal("expected error when index.html is missing")
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html><base href=\"/\" />focus</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckWebShellFiles(ws, root); err != nil {
		t.Fatalf("expected valid bundle, got %v", err)
	}
	files, err := ReadWebShellFiles(ws, root)
	if err != nil {
		t.Fatalf("ReadWebShellFiles: %v", err)
	}
	if string(files["index.html"]) == "" || string(files["assets/app.js"]) != "console.log(1)" {
		t.Fatalf("unexpected files: %v", files)
	}

	// Hidden files are rejected.
	hidden := filepath.Join(dist, ".env")
	if err := os.WriteFile(hidden, []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckWebShellFiles(ws, root); err == nil || !strings.Contains(err.Error(), "hidden") {
		t.Fatalf("expected hidden path error, got %v", err)
	}
	_ = os.Remove(hidden)

	// Symlinks are rejected.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	_ = os.WriteFile(outside, []byte("nope"), 0o644)
	link := filepath.Join(dist, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckWebShellFiles(ws, root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestWebShellPermissionsAndDigest(t *testing.T) {
	plain, err := Parse([]byte(`{"manifest_version": 1, "id": "kit", "name": "Kit", "version": "1.0.0", "contributes": {"themes": [{"id": "t", "name": "T", "light": {"accent": "#112233"}, "dark": {"accent": "#DDEEFF"}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	one, err := Parse([]byte(`{"manifest_version": 1, "id": "kit", "name": "Kit", "version": "1.1.0", "contributes": {"themes": [{"id": "t", "name": "T", "light": {"accent": "#112233"}, "dark": {"accent": "#DDEEFF"}}], "web_shells": [{"id": "focus", "name": "Focus", "dir": "dist"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	two, err := Parse([]byte(`{"manifest_version": 1, "id": "kit", "name": "Kit", "version": "1.2.0", "contributes": {"web_shells": [{"id": "focus", "name": "Focus", "dir": "dist"}, {"id": "ops", "name": "Ops", "dir": "ops"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if Digest(plain) == Digest(one) {
		t.Fatal("adding web_shells must change permission digest")
	}
	if Digest(one) == Digest(two) {
		t.Fatal("adding another web_shell must change permission digest")
	}
	lines := Explain(one)
	found := false
	for _, line := range lines {
		if line.Key == "web_shells" && strings.Contains(line.Detail, "Focus") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected web_shells permission line in Explain, got %+v", lines)
	}
	installed := func(m *Manifest) *Extension {
		return &Extension{Record: Record{ID: m.ID, Granted: m.Permissions}, Manifest: m, Full: m}
	}
	if !MorePermissions(installed(plain), one) {
		t.Fatal("upgrading from no web_shells to one should require consent")
	}
	if MorePermissions(installed(one), one) {
		t.Fatal("unchanged web_shells should not ask for new consent")
	}
	if !MorePermissions(installed(one), two) {
		t.Fatal("adding a second web_shell should require consent")
	}
	if added := Added(one, two); len(added) != 1 || added[0].Key != "web_shells" || !strings.Contains(added[0].Detail, "Ops") || strings.Contains(added[0].Detail, "Focus") {
		t.Fatalf("unexpected Added lines: %+v", added)
	}
}

func TestParseWebShellKey(t *testing.T) {
	if ext, id, ok := ParseWebShellKey("focus-ui/focus-4layer"); !ok || ext != "focus-ui" || id != "focus-4layer" {
		t.Fatalf("valid key failed: %q %q %v", ext, id, ok)
	}
	for _, bad := range []string{"", "builtin", "a/builtin", "a/default", "a/b/c", "../x/y", "bad space/ok"} {
		if _, _, ok := ParseWebShellKey(bad); ok {
			t.Errorf("ParseWebShellKey(%q) should fail", bad)
		}
	}
}
