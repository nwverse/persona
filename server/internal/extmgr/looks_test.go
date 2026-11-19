package extmgr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyDir copies the example package so a test may change it.
func copyDir(t *testing.T, from string) string {
	t.Helper()
	to := t.TempDir()
	err := filepath.Walk(from, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if fi.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return to
}

func exampleLooksDir(t *testing.T) string {
	return copyDir(t, filepath.Join("..", "..", "..", "schema", "extension", "examples", "example-looks"))
}

// The example extension's looks go to the hub in the report with the files they name (SVGs cleaned, nothing else of
// the package); a look switched off or removed leaves it.
func TestExtensionLooksReported(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	dir := exampleLooksDir(t)
	_ = os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("not a look's"), 0o644)
	svg, _ := os.ReadFile(filepath.Join(dir, "looks", "aurora", "avatar.svg"))
	_ = os.WriteFile(filepath.Join(dir, "looks", "aurora", "avatar.svg"),
		[]byte(strings.Replace(string(svg), "<title>", `<script>alert(1)</script><title>`, 1)), 0o644)
	installForTest(t, h, dir)
	looks := map[string]map[string][]byte{}
	for _, lp := range h.report().Looks {
		if lp.Extension != "example-looks" || lp.ExtensionName == "" || lp.Version == "" {
			t.Fatalf("%+v", lp)
		}
		var id struct{ ID string }
		_ = json.Unmarshal(lp.Item, &id)
		looks[id.ID] = lp.Files
	}
	if len(looks) != 2 || looks["aurora"] == nil || looks["pebble"] == nil {
		t.Fatalf("looks %v", looks)
	}
	for name, b := range looks["aurora"] {
		if strings.Contains(name, "secret") || strings.HasSuffix(name, ".svg") && strings.Contains(string(b), "script") {
			t.Fatalf("aurora's %s: %s", name, b)
		}
	}
	if code, out := call(t, mux, "PUT", "/api/v1/extensions/example-looks/-/contributions/looks/aurora", master, `{"enabled": false}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	for _, lp := range h.report().Looks {
		if strings.Contains(string(lp.Item), `"aurora"`) {
			t.Fatal("switched off, still reported")
		}
	}
	if code, out := call(t, mux, "DELETE", "/api/v1/extensions/example-looks/-/", master, ""); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if n := len(h.report().Looks); n != 0 {
		t.Fatalf("removed: %d looks reported", n)
	}
}

// An extension's look may not take a built-in look's id, nor one built into personad; one with a bad picture does not
// install.
func TestExtensionLooksRefused(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	for name, edit := range map[string]func(dir string){
		"a built-in id": func(dir string) {
			b, _ := os.ReadFile(filepath.Join(dir, "persona-extension.json"))
			_ = os.WriteFile(filepath.Join(dir, "persona-extension.json"), []byte(strings.Replace(string(b), `"id": "pebble"`, `"id": "persona"`, 1)), 0o644)
		},
		"a picture that is not a PNG": func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, "looks", "pebble", "avatar-256.png"), []byte("gif"), 0o644)
		},
		"a missing picture": func(dir string) { _ = os.Remove(filepath.Join(dir, "looks", "aurora", "working.png")) },
	} {
		dir := exampleLooksDir(t)
		edit(dir)
		code, out := call(t, mux, "POST", "/api/v1/extensions/-/preview", h.cfg.Token, `{"source": "dir:`+dir+`"}`)
		if code == 200 {
			code, out = call(t, mux, "POST", "/api/v1/extensions/-/install", h.cfg.Token,
				`{"preview_id": "`+out["preview_id"].(string)+`", "permissions_digest": "`+out["permissions_digest"].(string)+`"}`)
		}
		if code == 200 {
			t.Errorf("%s: installed (%v)", name, out)
		}
	}
}
