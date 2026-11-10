package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	pserve "github.com/nwverse/persona/server/internal/extension/serve"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// secretValueForTest is the one value the fake provider gives: no record, log or event may carry it.
const secretValueForTest = "synthetic-secret-value-7f3a9c"

// serveSecretProviderExtensionForTest resolves exv:// references by their last segment (ok, missing, signed_out,
// limited, refuse, big, boom), and writes each call's method and params, and each secrets.used event, as a line of
// JSON to SECRETS_OUT.
func serveSecretProviderExtensionForTest() {
	out := os.Getenv("SECRETS_OUT")
	var mu sync.Mutex
	note := func(method string, params json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		if f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			b, _ := json.Marshal(map[string]any{"method": method, "params": params})
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}
	_ = pserve.Serve(context.Background(), os.Stdin, os.Stdout, pserve.Program{ID: "example-vault", Version: "1.0.0",
		Namespaces: map[string]pserve.Handler{
			"secrets": func(_ context.Context, method string, params json.RawMessage) (any, error) {
				note(method, params)
				var p struct {
					Reference string `json:"reference"`
				}
				_ = json.Unmarshal(params, &p)
				last := p.Reference[strings.LastIndex(p.Reference, "/")+1:]
				switch method {
				case "used":
					return nil, nil
				case "describe":
					return wire.SecretsDescribeResult{Title: "Item › " + last, Detail: "vault Example"}, nil
				case "resolve":
				default:
					return nil, pserve.UnknownMethod(method)
				}
				switch last {
				case "ok":
					return wire.SecretsResolveResult{Value: secretValueForTest}, nil
				case "missing":
					return nil, &rpc.Error{Code: "not_found", Message: "no such item"}
				case "signed_out":
					return nil, &rpc.Error{Code: "not_signed_in", Message: "token revoked"}
				case "limited":
					return nil, &rpc.Error{Code: "rate_limited", Message: "slow down", Data: json.RawMessage(`{"retry_after": 30}`)}
				case "refuse":
					return nil, &rpc.Error{Code: "refused", Message: "policy\nsays no"}
				case "big":
					return wire.SecretsResolveResult{Value: strings.Repeat("x", extension.SecretMost+1)}, nil
				}
				return nil, errors.New("the vault fell over")
			}}})
}

func secretProviderPackage(t *testing.T, id, scheme, out string) string {
	dir := t.TempDir()
	self, _ := os.Executable()
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "vault"), []byte("#!/bin/sh\nPERSONA_TEST_PLUGIN=secrets exec "+self+" \"$@\"\n"), 0o755)
	manifest := fmt.Sprintf(`{"manifest_version": 1, "id": %q, "name": "Example Vault", "version": "1.0.0",
		"engines": {"plugin_protocol": 1}, "runtime": {"command": {%q: "bin/vault"}, "env": {"SECRETS_OUT": %q}},
		"contributes": {"secret_providers": [{"id": "vault", "name": "Example Vault", "schemes": [%q]}]}}`,
		id, runtime.GOOS+"-"+runtime.GOARCH, out, scheme)
	_ = os.WriteFile(filepath.Join(dir, extension.ManifestFile), []byte(manifest), 0o644)
	return dir
}

func TestSecretProviders(t *testing.T) {
	h, store := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	ctx := context.Background()
	purpose := wire.SecretPurpose{Kind: "extension", ID: "example-notes"}
	code := func(err error) string {
		var se *SecretError
		if !errors.As(err, &se) {
			return fmt.Sprintf("not a SecretError: %v", err)
		}
		return se.Code
	}

	if _, err := h.ResolveSecret(ctx, "exv://Example/item/ok", purpose, "settings"); code(err) != "no_provider" {
		t.Fatalf("no provider installed: %v", err)
	}
	if _, err := h.ResolveSecret(ctx, "https://example.com/x", purpose, "settings"); code(err) != "not_reference" {
		t.Fatalf("an address is not a reference: %v", err)
	}

	out := filepath.Join(t.TempDir(), "calls.jsonl")
	_, pv := call(t, mux, "POST", "/api/v1/plugins/-/preview", master, `{"source": "dir:`+secretProviderPackage(t, "example-vault", "exv", out)+`"}`)
	if !strings.Contains(fmt.Sprint(pv["permissions"]), "secret_provider") || !strings.Contains(fmt.Sprint(pv["permissions"]), "exv://") {
		t.Fatalf("the install page says it supplies exv:// values: %v", pv["permissions"])
	}
	if c, body := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); c != 200 {
		t.Fatalf("install: %d %v", c, body)
	}
	waitLong(t, "the provider found", func() bool {
		p, ok := h.SecretProviderOf("exv://Example/item/ok")
		return ok && p.Extension == "example-vault" && p.Name == "Example Vault"
	})

	v, err := h.ResolveSecret(ctx, "exv://Example/item/ok", purpose, "settings")
	if err != nil || v != secretValueForTest {
		t.Fatalf("resolved: %q %v", v, err)
	}
	for ref, want := range map[string]string{
		"exv://Example/item/missing": "not_found", "exv://Example/item/signed_out": "not_signed_in",
		"exv://Example/item/limited": "rate_limited", "exv://Example/item/refuse": "refused",
		"exv://Example/item/big": "too_long", "exv://Example/item/boom": "failed",
	} {
		_, err := h.ResolveSecret(ctx, ref, purpose, "settings")
		if code(err) != want {
			t.Fatalf("%s: want %s, got %v", ref, want, err)
		}
		if strings.Contains(err.Error(), "\n") {
			t.Fatalf("%s: the provider's words on one line: %q", ref, err.Error())
		}
		var se *SecretError
		if errors.As(err, &se) && want == "rate_limited" && se.RetryAfter != 30 {
			t.Fatalf("the provider's wait is kept: %v", se.RetryAfter)
		}
	}
	var se *SecretError
	_, err = h.ResolveSecret(ctx, "exv://Example/item/refuse", purpose, "settings")
	if !errors.As(err, &se) || !strings.Contains(se.Text(i18n.ZhHans), "Example Vault 拒绝了") || !strings.Contains(se.Text(i18n.En), "policy says no") {
		t.Fatalf("the refusal reads in the asker's language with the provider's words: %v", err)
	}

	d, err := h.DescribeSecret(ctx, "exv://Example/item/ok")
	if err != nil || d.Title != "Item › ok" || d.Detail != "vault Example" || d.ProviderName != "Example Vault" {
		t.Fatalf("described: %+v %v", d, err)
	}
	if _, err := h.DescribeSecret(ctx, "exv://Example/item/ok"); err != nil {
		t.Fatal(err)
	}

	// What the provider was told, and what the hub recorded: never the value.
	waitLong(t, "the used event", func() bool {
		raw, _ := os.ReadFile(out)
		return strings.Contains(string(raw), `"method":"used"`)
	})
	raw, _ := os.ReadFile(out)
	if strings.Contains(string(raw), secretValueForTest) {
		t.Fatal("the provider is never handed a value")
	}
	if n := strings.Count(string(raw), `"method":"describe"`); n != 1 {
		t.Fatalf("a description is kept a while: asked %d times", n)
	}
	var used struct {
		Params wire.SecretsUsedEvent `json:"params"`
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.Contains(line, `"method":"used"`) {
			_ = json.Unmarshal([]byte(line), &used)
		}
	}
	if used.Params.Reference != "exv://Example/item/ok" || used.Params.Purpose != purpose || used.Params.ApprovedBy != "settings" || used.Params.Contribution != "vault" {
		t.Fatalf("secrets.used: %+v", used.Params)
	}
	resolved, failed := 0, 0
	for _, a := range store.audits() {
		if a.plugin != "example-vault" {
			continue
		}
		if strings.Contains(string(a.detail), secretValueForTest) {
			t.Fatalf("an audit row carries the value: %s", a.detail)
		}
		switch a.action {
		case "secret_resolved":
			resolved++
		case "secret_resolve_failed":
			failed++
		default:
			continue
		}
		if a.actor != "settings" || !strings.Contains(string(a.detail), `"reference":"exv://Example/item/`) {
			t.Fatalf("who approved it is the actor, the reference and purpose the detail: %q %s", a.actor, a.detail)
		}
	}
	if resolved != 1 || failed != 7 {
		t.Fatalf("each resolution recorded on the provider: %d resolved, %d failed", resolved, failed)
	}

	// Too many in a minute: refused before reaching it.
	h.extensions.resolves.mu.Lock()
	h.extensions.resolves.most = 8 // the eight above
	h.extensions.resolves.mu.Unlock()
	if _, err := h.ResolveSecret(ctx, "exv://Example/item/ok", purpose, "settings"); code(err) != "rate_limited" {
		t.Fatalf("limited: %v", err)
	}

	// A second extension may not claim the same scheme.
	_, pv2 := call(t, mux, "POST", "/api/v1/plugins/-/preview", master, `{"source": "dir:`+secretProviderPackage(t, "example-vault-two", "exv", out)+`"}`)
	if c, _ := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv2["preview_id"], pv2["permissions_digest"])); c == 200 {
		t.Fatal("a scheme another extension claims fails the install")
	}

	// Switched off: no provider any more.
	if c, _ := call(t, mux, "PUT", "/api/v1/extensions/example-vault/-/enabled", master, `{"enabled": false}`); c != 200 {
		t.Fatalf("switch off: %d", c)
	}
	waitLong(t, "the provider gone", func() bool { _, ok := h.SecretProviderOf("exv://Example/item/ok"); return !ok })
	if _, err := h.ResolveSecret(ctx, "exv://Example/item/ok", purpose, "settings"); code(err) != "no_provider" {
		t.Fatalf("switched off: %v", err)
	}
}
