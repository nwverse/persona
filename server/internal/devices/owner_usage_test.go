package devices

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/usagesource"
)

func TestOwnerUsageTestAndValidatedUpsert(t *testing.T) {
	d, c := usageTestDevices(t, `{"future":true}`)
	var logs strings.Builder
	d.logf = func(f string, args ...any) { fmt.Fprintf(&logs, f, args...) }
	srv := fakeRelay(t)
	body := fmt.Sprintf(`{"type":"relay","name":"Example pool","url":%q,"key":%q}`, srv.URL, usageTestKey)
	check := func(method, path, body string, want int) map[string]any {
		t.Helper()
		status, out, w := c.do(method, path, body)
		if status != want {
			t.Fatalf("status %d, want %d", status, want)
		}
		if strings.Contains(w.Body.String(), usageTestKey) || strings.Contains(logs.String(), usageTestKey) {
			t.Fatal("key exposed")
		}
		return out
	}
	for _, path := range []string{"/api/v1/usage-sources/test", "/api/v1/usage-sources/src-example"} {
		method := "POST"
		if !strings.HasSuffix(path, "/test") {
			method = "PUT"
		}
		c.admin = false
		check(method, path, body, http.StatusForbidden)
		c.admin = true
	}
	out := check("POST", "/api/v1/usage-sources/test", body, 200)
	if out["ok"] != true || out["accounts"] != float64(2) || out["windows"] != float64(3) {
		t.Fatalf("counts: %v", out)
	}
	if _, err := os.Stat(d.opts.ConfigFile); !os.IsNotExist(err) { // initial file must remain unchanged
		b, _ := os.ReadFile(d.opts.ConfigFile)
		if string(b) != `{"future":true}` {
			t.Fatal("test wrote config")
		}
	}
	for i := 0; i < 2; i++ {
		out = check("PUT", "/api/v1/usage-sources/src-example", body, 200)
		if out["ok"] != true || out["accounts"] != float64(2) || out["windows"] != float64(3) {
			t.Fatalf("save counts: %v", out)
		}
	}
	cfg, err := d.diskUsage()
	if err != nil || len(cfg.Sources) != 1 || cfg.Sources[0].Key != usageTestKey {
		t.Fatal("saved source did not decrypt or upsert duplicated it")
	}
	// Restart: only ciphertext is on disk, and a new manager can read it.
	restarted := New(Options{ConfigFile: d.opts.ConfigFile})
	if restarted.usageGW.config().Sources[0].Key != usageTestKey {
		t.Fatal("restart lost key")
	}
	_ = filepath.Walk(filepath.Dir(d.opts.ConfigFile), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(path)
			if strings.Contains(string(b), usageTestKey) {
				t.Error("plaintext key persisted")
			}
		}
		return err
	})
	// Editing in the existing admin UI must keep encrypted storage and the saved key.
	check("PUT", "/api/v1/admin/usage-sources/src-example", `{"name":"Renamed"}`, 200)
	cfg, err = d.diskUsage()
	if err != nil || cfg.Sources[0].Key != usageTestKey || cfg.Sources[0].Name != "Renamed" {
		t.Fatal("admin edit lost encrypted key")
	}
	before, _ := os.ReadFile(d.opts.ConfigFile)
	// The gateway/transport error may contain the key. Only a stable error code is returned.
	d.usageGW.fetch = func(context.Context, usagesource.Config, time.Time) (usagesource.Result, error) {
		return usagesource.Result{}, fmt.Errorf("upstream: %s", usageTestKey)
	}
	for _, op := range [][2]string{{"POST", "/api/v1/usage-sources/test"}, {"PUT", "/api/v1/usage-sources/src-example"}, {"PUT", "/api/v1/usage-sources/another"}} {
		out = check(op[0], op[1], body, 200)
		if out["ok"] != false {
			t.Fatal("failed connection succeeded")
		}
	}
	after, _ := os.ReadFile(d.opts.ConfigFile)
	if string(before) != string(after) {
		t.Fatal("failed test changed stored config")
	}
	// JSON decoder errors must not echo malicious field names or values.
	check("POST", "/api/v1/usage-sources/test", `{"name":{},"`+usageTestKey+`":1}`, 400)
	check("PUT", "/api/v1/usage-sources/src-example", `{"type":"relay","name":"x","url":"invalid","key":"`+usageTestKey+`"}`, 400)
	// A source edited through the admin UI must still have no plaintext on disk.
	if strings.Contains(string(after), usageTestKey) {
		t.Fatal("admin edit wrote plaintext")
	}
}

func TestOwnerUsageFailedConnectionCreatesNoFiles(t *testing.T) {
	d, c := usageTestDevices(t, "")
	var logs strings.Builder
	d.logf = func(f string, args ...any) { fmt.Fprintf(&logs, f, args...) }
	d.usageGW.fetch = func(context.Context, usagesource.Config, time.Time) (usagesource.Result, error) {
		return usagesource.Result{}, fmt.Errorf("transport error containing %s", usageTestKey)
	}
	body := `{"type":"relay","name":"Example","url":"https://gateway.example.com","key":"` + usageTestKey + `"}`
	for _, op := range [][2]string{{"POST", "/api/v1/usage-sources/test"}, {"PUT", "/api/v1/usage-sources/src-example"}} {
		status, out, w := c.do(op[0], op[1], body)
		if status != 200 || out["ok"] != false {
			t.Fatalf("failed test: %d %v", status, out)
		}
		if strings.Contains(w.Body.String(), usageTestKey) || strings.Contains(logs.String(), usageTestKey) {
			t.Fatal("secret exposed")
		}
		entries, err := os.ReadDir(filepath.Dir(d.opts.ConfigFile))
		if err != nil || len(entries) != 0 {
			t.Fatal("failed connection created config, key or temporary files")
		}
	}
}

func TestOwnerUsageUpsertPreservesDisabledSource(t *testing.T) {
	d, c := usageTestDevices(t, "")
	d.usageGW.fetch = func(context.Context, usagesource.Config, time.Time) (usagesource.Result, error) {
		return usagesource.Result{Seen: usagesource.Seen{Accounts: 1, Windows: 2}}, nil
	}
	for _, enabled := range []string{`,"enabled":false`, ``, `,"enabled":true`} {
		body := `{"type":"relay","name":"Example","url":"https://gateway.example.com","key":"` + usageTestKey + `"` + enabled + `}`
		status, out, _ := c.do("PUT", "/api/v1/usage-sources/src-example", body)
		if status != 200 || out["ok"] != true {
			t.Fatalf("save: %d %v", status, out)
		}
		cfg, err := d.diskUsage()
		if err != nil || len(cfg.Sources) != 1 {
			t.Fatal("source missing")
		}
		if want := enabled == `,"enabled":true`; cfg.Sources[0].on() != want {
			t.Fatal("omitted enabled changed saved switch")
		}
	}
}

func TestBrokenUsageKeyDoesNotHideOtherSourcesOrCreateAKey(t *testing.T) {
	for _, broken := range []string{"missing", "corrupt", "replaced"} {
		t.Run(broken, func(t *testing.T) {
			d, c := usageTestDevices(t, `{"usage":{"poll_minutes":7,"sources":[{"id":"legacy","type":"generic","name":"Legacy","url":"https://legacy.example.com","key":"legacy-example-key"}]}}`)
			d.usageGW.fetch = func(context.Context, usagesource.Config, time.Time) (usagesource.Result, error) {
				return usagesource.Result{Seen: usagesource.Seen{Accounts: 1, Windows: 2}}, nil
			}
			body := `{"type":"relay","name":"Example","url":"https://gateway.example.com","key":"` + usageTestKey + `"}`
			status, out, _ := c.do("PUT", "/api/v1/usage-sources/src-example", body)
			if status != 200 || out["ok"] != true {
				t.Fatal("initial save failed")
			}
			path := d.usageKeyPath()
			switch broken {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				if err := os.WriteFile(path, []byte(strings.Repeat("x", 32)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := d.diskUsage()
			if err != nil || len(cfg.Sources) != 2 || cfg.PollMinutes != 7 || cfg.Sources[0].Key != "legacy-example-key" || cfg.Sources[0].KeyUnavailable || !cfg.Sources[1].KeyUnavailable || cfg.Sources[1].Key != "" {
				t.Fatal("broken source affected other sources or settings")
			}
			if broken == "missing" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("read created a replacement key")
				}
			}
			status, out, _ = c.do("PUT", "/api/v1/admin/usage-sources", `{"poll_minutes":9}`)
			if status != 200 || out["poll_minutes"] != float64(9) {
				t.Fatal("broken key blocked unrelated settings edit")
			}
			if broken == "missing" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("settings edit created a replacement key")
				}
			}
			restarted := New(Options{ConfigFile: d.opts.ConfigFile})
			if got := restarted.usageGW.config(); len(got.Sources) != 2 || got.Sources[0].Key != "legacy-example-key" || !got.Sources[1].KeyUnavailable {
				t.Fatal("restart lost unaffected source")
			}
			d.usageGW.setConfig(cfg)
			d.usageGW.fetch = func(context.Context, usagesource.Config, time.Time) (usagesource.Result, error) {
				t.Fatal("broken key was sent anonymously")
				return usagesource.Result{}, nil
			}
			d.usageGW.read(context.Background(), cfg.Sources[1], time.Now())
			if got := d.usageGW.status("src-example"); got.Error != usagesource.CodeBadConfig {
				t.Fatalf("status: %+v", got)
			}
		})
	}
}
