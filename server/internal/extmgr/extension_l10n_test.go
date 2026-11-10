package extmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

// requestIn is call with the request's language l.
func requestIn(t *testing.T, mux http.Handler, l i18n.Lang, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept-Language", string(l))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// An installed package's translations (#897): the preview, the list and the details answer each request in its
// language (its own, then English unless it is Simplified Chinese, then the manifest's), the settings form and secrets
// included, and what was approved does not change with it.
func TestExtensionTranslationsByRequestLanguage(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	pkg := notesPackage(t, "1.0.0")
	if err := os.MkdirAll(filepath.Join(pkg, extension.L10nDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"ja.json": `{"name": "サンプルノート", "configuration": {"folders": {"title": "フォルダー"}}, "secrets": {"api_key": {"title": "API キー"}}}`,
		"en.json": `{"name": "Example Notes (en)", "description": "Notes into the knowledge base", "configuration": {"folders": {"title": "Folders"}}}`,
	} {
		if err := os.WriteFile(filepath.Join(pkg, extension.L10nDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, pv := requestIn(t, mux, i18n.Ja, "POST", "/api/v1/extensions/-/preview", master, `{"source": "dir:`+pkg+`"}`)
	if code != 200 || pv["manifest"].(map[string]any)["name"] != "サンプルノート" || pv["manifest"].(map[string]any)["description"] != "Notes into the knowledge base" {
		t.Fatalf("preview in Japanese: %d %v", code, pv["manifest"])
	}
	_, op := requestIn(t, mux, i18n.ZhHans, "GET", "/api/v1/extensions/-/operations/"+pv["operation_id"].(string), master, "")
	if shown := op["preview"].(map[string]any); shown["manifest"].(map[string]any)["name"] != "Example Notes" || shown["permissions_digest"] != pv["permissions_digest"] {
		t.Fatalf("the same preview in Simplified Chinese: %v", shown)
	}
	if code, out := call(t, mux, "POST", "/api/v1/extensions/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); code != 200 {
		t.Fatalf("install: %d %v", code, out)
	}
	for _, c := range []struct {
		l                     i18n.Lang
		name, folders, secret string
	}{
		{i18n.Ja, "サンプルノート", "フォルダー", "API キー"},
		{i18n.En, "Example Notes (en)", "Folders", "API Key"},
		{i18n.ZhHans, "Example Notes", "文件夹", "API Key"},
	} {
		_, list := requestIn(t, mux, c.l, "GET", "/api/v1/extensions", master, "")
		var row map[string]any
		for _, x := range list["extensions"].([]any) {
			if x.(map[string]any)["id"] == "example-notes" {
				row = x.(map[string]any)
			}
		}
		if row == nil || row["name"] != c.name || !strings.Contains(fmt.Sprint(row["needs_setup"]), c.secret) {
			t.Errorf("%s list: %v", c.l, row)
		}
		code, d := requestIn(t, mux, c.l, "GET", "/api/v1/extensions/example-notes/-/", master, "")
		if code != 200 || d["name"] != c.name {
			t.Fatalf("%s details: %d %v", c.l, code, d["name"])
		}
		if f := d["form"].(map[string]any)["fields"].([]any)[0].(map[string]any); f["title"] != c.folders {
			t.Errorf("%s form: %v", c.l, f)
		}
		if s := d["secrets"].([]any)[0].(map[string]any); s["title"] != c.secret || s["name"] != "api_key" {
			t.Errorf("%s secret: %v", c.l, s)
		}
	}
	if p := h.extensions.reg.Get("example-notes"); p.Schema.Fields[0].Title != "文件夹" || p.Full.Name != "Example Notes" {
		t.Fatal("the registry's manifest and schema stay as installed")
	}
}

// A market entry's l10n names it in the catalog before anything is downloaded, and the search finds it by any of its
// translations.
func TestCatalogTranslations(t *testing.T) {
	h, mux, phone, mk, _, mid := upgradeHub(t)
	mk.mu.Lock()
	var index map[string]any
	if err := json.Unmarshal(mk.index, &index); err != nil {
		t.Fatal(err)
	}
	index["plugins"].([]any)[0].(map[string]any)["l10n"] = map[string]any{
		"ja": map[string]any{"name": "画面の例", "description": "拡張機能の画面"},
		"en": map[string]any{"name": "UI sample"},
	}
	mk.index, _ = json.Marshal(index)
	mk.mu.Unlock()
	if _, err := h.marketRead(context.Background(), mid); err != nil {
		t.Fatal(err)
	}
	item := func(l i18n.Lang, q string) map[string]any {
		t.Helper()
		code, out := requestIn(t, mux, l, "GET", "/api/v1/extensions/-/catalog?q="+url.QueryEscape(q), phone, "")
		if code != 200 {
			t.Fatalf("catalog: %d %v", code, out)
		}
		for _, x := range out["extensions"].([]any) {
			if x.(map[string]any)["id"] == "example-ui" {
				return x.(map[string]any)
			}
		}
		return nil
	}
	if it := item(i18n.Ja, ""); it == nil || it["name"] != "画面の例" || it["description"] != "拡張機能の画面" {
		t.Fatalf("Japanese: %v", it)
	}
	if it := item(i18n.ZhHans, ""); it == nil || it["name"] != "Example UI" {
		t.Fatalf("Simplified Chinese: the manifest's name: %v", it)
	}
	if it := item(i18n.ZhHans, "ui sample"); it == nil || it["name"] != "Example UI" {
		t.Fatal("found by its English name, shown in the manifest's:", it)
	}
	if item(i18n.En, "no such words") != nil {
		t.Fatal("a search still narrows")
	}
}

// pack takes l10n/ into the package, and the index entry it prints carries the same translations (a broken file is
// said on stderr and left out of both the entry's l10n and the hub's reading of it).
func TestPackCarriesTranslations(t *testing.T) {
	dir := t.TempDir()
	src := `{"manifest_version": 1, "id": "greeter", "name": "问候", "version": "1.0.0",
 "contributes": {"prompts": [{"id": "hi", "kind": "quick", "title": "Hi", "text": "Say hi."}]}}`
	if err := os.WriteFile(filepath.Join(dir, extension.ManifestFile), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, extension.L10nDir), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, extension.L10nDir, "en.json"), []byte(`{"name": "Greeter"}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, extension.L10nDir, "ja.json"), []byte(`{"name": ""`), 0o644)
	var out, errs bytes.Buffer
	if err := packExtension(context.Background(), dir, nil, &out, &errs); err != nil {
		t.Fatalf("pack: %v\n%s", err, errs.String())
	}
	files := tarFiles(t, filepath.Join(dir, "dist", "greeter-1.0.0.tar.gz"))
	if files["greeter-1.0.0/l10n/en.json"] != `{"name": "Greeter"}` || files["greeter-1.0.0/l10n/ja.json"] == "" {
		t.Fatalf("the package's l10n/: %v", files)
	}
	if !strings.Contains(errs.String(), "l10n/ja.json is left out") {
		t.Fatal("the broken file is said:", errs.String())
	}
	entry := out.String()[strings.Index(out.String(), "{"):]
	var fields struct {
		L10n json.RawMessage `json:"l10n"`
	}
	if err := json.Unmarshal([]byte(entry), &fields); err != nil {
		t.Fatal(err, entry)
	}
	ts, err := extension.ParseEntryTranslations(fields.L10n)
	if err != nil || ts.Name(i18n.En, "问候") != "Greeter" || ts.Name(i18n.Ja, "问候") != "Greeter" || len(ts) != 1 {
		t.Fatalf("the entry's l10n: %s %v", fields.L10n, err)
	}
}
