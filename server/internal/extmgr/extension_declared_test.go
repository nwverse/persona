package extmgr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A declarative kit: its quick prompt, theme and triage template are listed for the apps, its skill and instructions go
// to the assistant; switched off one by one, each leaves.
func TestDeclarativeKit(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	dir := declarativePackage(t, `{"manifest_version": 1, "id": "kit", "name": "Kit", "version": "1.0.0", "contributes": {
		"skills": [{"id": "review", "dir": "skills/review"}],
		"prompts": [{"id": "tone", "kind": "instructions", "file": "assistant/tone.md"},
			{"id": "today", "kind": "quick", "title": "今天", "text": "今天做了什么？"}],
		"themes": [{"id": "forest", "name": "森林", "light": {"accent": "#2E7D32"}, "dark": {"accent": "#FFEB3B"}},
			{"id": "pale", "name": "淡", "light": {"accent": "#FFF59D"}, "dark": {"accent": "#81C784"}}],
		"triage_templates": [{"id": "quiet", "title": "大群安静", "text": "只看提到我的。"}]}}`)
	_ = os.MkdirAll(filepath.Join(dir, "skills", "review"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "skills", "review", "SKILL.md"), []byte("# review\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "assistant"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "assistant", "tone.md"), []byte("Answer in short sentences.\n"), 0o644)
	installForTest(t, h, dir)

	_, out := call(t, mux, "GET", "/api/v1/extensions/-/prompts", master, "")
	if ps := out["prompts"].([]any); len(ps) != 1 || ps[0].(map[string]any)["title"] != "今天" || ps[0].(map[string]any)["extension_name"] != "Kit" {
		t.Fatalf("%v", out)
	}
	_, out = call(t, mux, "GET", "/api/v1/extensions/-/themes", master, "")
	ths := out["themes"].([]any)
	if len(ths) != 2 || ths[0].(map[string]any)["problems"] != nil || ths[1].(map[string]any)["problems"] == nil {
		t.Fatalf("the pale one fails the contrast check: %v", ths)
	}
	_, out = call(t, mux, "GET", "/api/v1/extensions/-/triage-templates", master, "")
	if ts := out["templates"].([]any); len(ts) != 1 || ts[0].(map[string]any)["text"] != "只看提到我的。" {
		t.Fatalf("%v", out)
	}
	skills := h.extensions.extensionSkills()
	if len(skills) != 1 || skills[0].Name != "review" || string(skills[0].Files["SKILL.md"]) != "# review\n" {
		t.Fatalf("%+v", skills)
	}
	if got := h.extensions.extensionInstructions(context.Background(), ""); !strings.Contains(got, "Answer in short sentences.") {
		t.Fatal(got)
	}

	for _, c := range []string{"skills/review", "prompts/tone", "prompts/today", "themes/forest", "triage_templates/quiet"} {
		if code, out := call(t, mux, "PUT", "/api/v1/extensions/kit/-/contributions/"+c, master, `{"enabled": false}`); code != 200 {
			t.Fatalf("%s: %d %v", c, code, out)
		}
	}
	if len(h.extensions.extensionSkills()) != 0 || strings.Contains(h.extensions.extensionInstructions(context.Background(), ""), "short sentences") {
		t.Fatal("switched off: not the assistant's any more")
	}
	if _, out := call(t, mux, "GET", "/api/v1/extensions/-/prompts", master, ""); len(out["prompts"].([]any)) != 0 {
		t.Fatal(out)
	}
	if _, out := call(t, mux, "GET", "/api/v1/extensions/-/themes", master, ""); len(out["themes"].([]any)) != 1 {
		t.Fatal("only the pale one is on:", out)
	}
	if _, out := call(t, mux, "GET", "/api/v1/plugins/-/triage-templates", master, ""); len(out["templates"].([]any)) != 0 {
		t.Fatal("the old path too:", out)
	}
}

func TestDeclarativeWebShells(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	dir := declarativePackage(t, `{"manifest_version": 1, "id": "focus-ui", "name": "Focus UI", "version": "1.0.0", "contributes": {
		"web_shells": [{"id": "focus-4layer", "name": "Focus Workspace", "description": "Compact 4-layer view", "dir": "dist"}]}}`)
	_ = os.MkdirAll(filepath.Join(dir, "dist", "assets"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "dist", "index.html"), []byte("<html><base href=\"/\" />focus</html>"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "dist", "assets", "app.js"), []byte("console.log('focus')"), 0o644)
	installForTest(t, h, dir)

	_, out := call(t, mux, "GET", "/api/v1/extensions/-/web-shells", master, "")
	shells := out["web_shells"].([]any)
	if len(shells) != 1 {
		t.Fatalf("expected 1 web shell, got %v", out)
	}
	first := shells[0].(map[string]any)
	if first["key"] != "focus-ui/focus-4layer" || first["name"] != "Focus Workspace" || first["extension_name"] != "Focus UI" {
		t.Fatalf("unexpected web shell view: %v", first)
	}
	refs := h.extensionWebShells()
	if len(refs) != 1 || refs[0].Extension != "focus-ui" || refs[0].Digest == "" {
		t.Fatalf("unexpected web shell report refs: %+v", refs)
	}
	bundle, ok := h.extensionWebShellBundle("focus-ui", "focus-4layer")
	if !ok || string(bundle.Files["assets/app.js"]) != "console.log('focus')" || bundle.Digest != refs[0].Digest {
		t.Fatalf("unexpected on-demand web shell bundle: ok=%v %+v", ok, bundle)
	}

	if code, out := call(t, mux, "PUT", "/api/v1/extensions/focus-ui/-/contributions/web_shells/focus-4layer", master, `{"enabled": false}`); code != 200 {
		t.Fatalf("disable web_shell contribution: %d %v", code, out)
	}
	if _, out := call(t, mux, "GET", "/api/v1/plugins/-/web-shells", master, ""); len(out["web_shells"].([]any)) != 0 {
		t.Fatalf("disabled web shell should disappear: %v", out)
	}
	if len(h.extensionWebShells()) != 0 {
		t.Fatal("disabled web shell should not be reported to hub")
	}
	if _, ok := h.extensionWebShellBundle("focus-ui", "focus-4layer"); ok {
		t.Fatal("disabled web shell bundle should not be served on demand")
	}
}

func TestPackWebShellExtension(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"manifest_version": 1, "id": "pack-shell", "name": "Pack Shell", "version": "0.1.0", "contributes": {"web_shells": [{"id": "main", "name": "Main", "dir": "web"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "persona-extension.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "web", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "index.html"), []byte("<html><base href=\"/\" />packed</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")
	var stdout, stderr strings.Builder
	if err := packExtension(context.Background(), dir, map[string]string{"out": outDir}, &stdout, &stderr); err != nil {
		t.Fatalf("packExtension failed: %v (stderr: %s)", err, stderr.String())
	}
	archive := filepath.Join(outDir, "pack-shell-0.1.0.tar.gz")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("packed archive missing: %v", err)
	}
	// Verify the packed archive installs cleanly through the real preview + install HTTP flow (including CheckWebShellFiles).
	h, _ := extensionTestHub(t)
	mux := h.routes()
	code, pv := call(t, mux, "POST", "/api/v1/extensions/-/preview", h.cfg.Token, fmt.Sprintf(`{"source": %q}`, "file:"+archive))
	if code != 200 {
		t.Fatalf("preview packed web_shell archive failed: %d %v", code, pv)
	}
	code, out := call(t, mux, "POST", "/api/v1/extensions/-/install", h.cfg.Token,
		fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"]))
	if code != 200 {
		t.Fatalf("install packed web_shell archive failed: %d %v", code, out)
	}
	bundle, ok := h.extensionWebShellBundle("pack-shell", "main")
	if !ok || !strings.Contains(string(bundle.Files["index.html"]), "packed") || string(bundle.Files["assets/app.js"]) != "console.log(1)" {
		t.Fatalf("installed packed bundle mismatch: ok=%v files=%v", ok, bundle.Files)
	}
}
