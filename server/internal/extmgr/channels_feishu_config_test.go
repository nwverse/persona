package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/i18n"
)

type configureTransport func(*http.Request) (*http.Response, error)

func (f configureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type configureStore struct {
	*botStore
	writes atomic.Int64
}

func (s *configureStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == botSettingsKey {
		s.writes.Add(1)
	}
	return s.botStore.PutSetting(ctx, key, value)
}

type configurePlatform struct {
	mu           sync.Mutex
	requests     int
	code         int
	publishCode  int
	reject       bool
	beforeConfig func()
}

func configureWorld(t *testing.T, code int) (*botWorld, *configureStore, *configurePlatform) {
	t.Helper()
	h := newTestManager(t)
	st := &configureStore{botStore: newBotStore()}
	h.store = st
	f := &configurePlatform{code: code}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests++
		code, publishCode, reject, before := f.code, f.publishCode, f.reject, f.beforeConfig
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			if reject {
				fmt.Fprint(w, `{"code":10003,"msg":"invalid credentials"}`)
			} else {
				fmt.Fprint(w, `{"code":0,"tenant_access_token":"fake-token","expire":7200}`)
			}
		case r.URL.Path == "/open-apis/bot/v3/info":
			fmt.Fprint(w, `{"code":0,"bot":{"app_name":"Example","open_id":"ou_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/config") && r.Method == "PATCH":
			if before != nil {
				before()
			}
			fmt.Fprintf(w, `{"code":%d,"msg":"raw-sensitive-platform-detail"}`, code)
		case strings.HasSuffix(r.URL.Path, "/publish") && r.Method == "POST":
			fmt.Fprintf(w, `{"code":%d,"msg":"raw-sensitive-platform-detail"}`, publishCode)
		case strings.HasPrefix(r.URL.Path, "/open-apis/contact/v3/users/"):
			fmt.Fprint(w, `{"code":0,"data":{"user":{"name":"Example"}}}`)
		default:
			t.Errorf("unexpected platform request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	original := http.DefaultTransport
	http.DefaultTransport = configureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "open.feishu.cn" && r.URL.Host != strings.TrimPrefix(feishubot.DomainLark, "https://") && r.URL.Host != "private.example" {
			return nil, fmt.Errorf("unexpected outbound host")
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return original.RoundTrip(clone)
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	mux := http.NewServeMux()
	h.botRoutes(mux)
	return &botWorld{h: h, mux: mux}, st, f
}

func configureCredentials(t *testing.T, w *botWorld, id, secret, domain string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"step": "credentials", "values": map[string]string{"app_id": id, "app_secret": secret, "domain": domain}})
	status, out := w.do(t, "POST", "/api/v1/channels/feishu/bind", string(body))
	if status != 200 {
		t.Fatalf("credentials: %d %v", status, out)
	}
	return out
}

func configurePoll(t *testing.T, w *botWorld, st *configureStore, f *configurePlatform) map[string]any {
	t.Helper()
	f.mu.Lock()
	beforeRequests := f.requests
	f.mu.Unlock()
	beforeWrites := st.writes.Load()
	next := w.bind(t, "pair")
	f.mu.Lock()
	afterRequests := f.requests
	f.mu.Unlock()
	if beforeRequests != afterRequests || beforeWrites != st.writes.Load() {
		t.Fatalf("poll repeated side effects: HTTP %d -> %d, writes %d -> %d", beforeRequests, afterRequests, beforeWrites, st.writes.Load())
	}
	if strings.Contains(fmt.Sprint(next), "raw-sensitive") || strings.Contains(fmt.Sprint(next), "fake-secret") {
		t.Fatal("raw details reached client")
	}
	return next
}

// Exercise the Hub's real channel bind route, keeping both official cloud URLs and every HTTP request on a fake platform.
func TestFeishuConfigureDiagnosticSurvivesPairPoll(t *testing.T) {
	for _, tc := range []struct {
		name        string
		code        int
		publishCode int
		issue       feishubot.ConfigureIssue
	}{
		{"missing_scope", 230002, 0, feishubot.IssuePatchScope},
		{"bot_capability", 210041, 0, feishubot.IssueBotCapability},
		{"not_self_built", 210001, 0, feishubot.IssueNotSelfBuilt},
		{"incompatible", 210031, 0, feishubot.IssueIncompatible},
		{"unreachable", 99991, 0, feishubot.IssueUnreachable},
		{"publish_failure", 0, 230002, feishubot.IssuePatchScope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, st, f := configureWorld(t, tc.code)
			f.publishCode = tc.publishCode
			first := configureCredentials(t, w, "cli_example", "fake-secret", "feishu")
			want := feishuConfigureNote(i18n.ZhHans, feishubot.ConfigureReport{Issue: tc.issue}, "feishu", "cli_example")
			if first["step"] != "pair" || !strings.Contains(fmt.Sprint(first["hint"]), want) {
				t.Fatalf("first response has no expected diagnostic: %v want=%s", first, want)
			}
			for i := 0; i < 3; i++ {
				next := configurePoll(t, w, st, f)
				if next["hint"] != first["hint"] || next["user_code"] != first["user_code"] {
					t.Fatalf("pair poll replaced diagnostic: first=%v next=%v", first, next)
				}
			}
			for _, lang := range []i18n.Lang{i18n.En, i18n.Ja} {
				w.h.opts.Lang = func() i18n.Lang { return lang }
				p := configurePoll(t, w, st, f)
				want := feishuConfigureNote(lang, feishubot.ConfigureReport{Issue: tc.issue}, "feishu", "cli_example")
				if !strings.Contains(fmt.Sprint(p["hint"]), want) {
					t.Fatalf("poll did not localize retained report: %v", p)
				}
			}
			var saved map[string]any
			_, _ = st.Setting(context.Background(), botSettingsKey, &saved)
			raw, _ := json.Marshal(saved)
			if strings.Contains(string(raw), "raw-sensitive") {
				t.Fatal("raw platform error persisted")
			}
		})
	}
}

func TestFeishuConfigureDiagnosticLifecycle(t *testing.T) {
	t.Run("renewal_and_reload", func(t *testing.T) {
		w, st, f := configureWorld(t, 230002)
		first := configureCredentials(t, w, "cli_example", "fake-secret", "feishu")
		w.h.bots.mu.Lock()
		expires := w.h.bots.set.Pairing.Expires
		w.h.bots.now = func() time.Time { return expires.Add(time.Second) }
		w.h.bots.mu.Unlock()
		renewed := w.bind(t, "pair")
		if renewed["hint"] != first["hint"] {
			t.Fatalf("renewal lost diagnostic: %v", renewed)
		}
		configurePoll(t, w, st, f)
		// A fresh manager loads the saved record and opens it with the same secret key.
		h := newTestManager(t)
		h.secret = w.h.secret
		h.store = st
		mux := http.NewServeMux()
		h.botRoutes(mux)
		reloaded := &botWorld{h: h, mux: mux}
		// Move this manager's clock to the renewed code's lifetime.
		h.bots.now = w.h.bots.now
		p := configurePoll(t, reloaded, st, f)
		if p["hint"] != first["hint"] {
			t.Fatalf("reload lost diagnostic: %v", p)
		}
	})
	t.Run("switch_features_and_unchanged_credentials", func(t *testing.T) {
		w, st, f := configureWorld(t, 230002)
		first := configureCredentials(t, w, "cli_example", "fake-secret", "feishu")
		for _, body := range []string{`{"features":{"cards":false}}`, `{"enabled":false}`, `{"enabled":true}`, `{"app_id":"cli_example","app_secret":"fake-secret","domain":"feishu"}`} {
			status, out := w.do(t, "PUT", "/api/v1/bots/feishu", body)
			if status != 200 {
				t.Fatalf("settings: %d %v", status, out)
			}
		}
		if p := configurePoll(t, w, st, f); p["hint"] != first["hint"] {
			t.Fatalf("unrelated settings cleared diagnostic: %v", p)
		}
	})
	for _, tc := range []struct{ name, body string }{
		{"app", `{"app_id":"cli_replacement","app_secret":"replacement-secret","domain":"feishu"}`},
		{"secret", `{"app_secret":"replacement-secret"}`},
		{"domain", `{"domain":"lark"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, st, f := configureWorld(t, 230002)
			configureCredentials(t, w, "cli_example", "fake-secret", "feishu")
			status, out := w.do(t, "PUT", "/api/v1/bots/feishu", tc.body)
			if status != 200 {
				t.Fatalf("replacement: %d %v", status, out)
			}
			p := w.bind(t, "pair")
			if p["hint"] != i18n.T(i18n.ZhHans, "hub-channels.pair_hint") {
				t.Fatalf("old app diagnostic survived replacement: %v", p)
			}
			configurePoll(t, w, st, f)
		})
	}
	t.Run("rejected_replacement", func(t *testing.T) {
		w, st, f := configureWorld(t, 230002)
		first := configureCredentials(t, w, "cli_example", "fake-secret", "feishu")
		f.mu.Lock()
		f.reject = true
		f.mu.Unlock()
		rejected := configureCredentials(t, w, "cli_other", "replacement-secret", "feishu")
		if rejected["step"] != "credentials" || rejected["error"] == nil || strings.Contains(fmt.Sprint(rejected), "raw-sensitive") {
			t.Fatalf("invalid credential response: %v", rejected)
		}
		if p := configurePoll(t, w, st, f); p["hint"] != first["hint"] {
			t.Fatalf("failed replacement cleared diagnostic: %v", p)
		}
	})
	t.Run("corrected_configuration", func(t *testing.T) {
		w, st, f := configureWorld(t, 230002)
		configureCredentials(t, w, "cli_example", "fake-secret", "feishu")
		f.mu.Lock()
		f.code = 0
		f.mu.Unlock()
		p := configureCredentials(t, w, "cli_example", "fake-secret", "feishu")
		want := i18n.T(i18n.ZhHans, "hub-channels.auto_configured")
		if !strings.Contains(fmt.Sprint(p["hint"]), want) || strings.Contains(fmt.Sprint(p["hint"]), "/app/cli_example") {
			t.Fatalf("corrected configuration kept old guidance: %v", p)
		}
		if p := configurePoll(t, w, st, f); !strings.Contains(fmt.Sprint(p["hint"]), want) {
			t.Fatalf("corrected result not retained: %v", p)
		}
	})
	t.Run("private_deployment", func(t *testing.T) {
		w, st, f := configureWorld(t, 230002)
		p := configureCredentials(t, w, "cli_example", "fake-secret", "https://private.example")
		if p["hint"] != i18n.T(i18n.ZhHans, "hub-channels.pair_hint") {
			t.Fatalf("private deployment acquired diagnostic: %v", p)
		}
		configurePoll(t, w, st, f)
	})
	t.Run("paired_and_unpaired", func(t *testing.T) {
		w, st, f := configureWorld(t, 230002)
		withAssistant(w.h)
		p := configureCredentials(t, w, "cli_example", "fake-secret", "feishu")
		w.h.botPair(feishubot.New("cli_example", "fake-secret", "feishu"), "ou_owner", "oc_owner", p["user_code"].(string))
		if p := configurePoll(t, w, st, f); p["done"] != true {
			t.Fatalf("not paired: %v", p)
		}
		var saved FeishuBotSettings
		_, _ = st.Setting(context.Background(), botSettingsKey, &saved)
		if saved.Configure != nil {
			t.Fatal("pair success kept diagnostic")
		}
		status, out := w.do(t, "DELETE", "/api/v1/bots/feishu/owner", "")
		if status != 200 {
			t.Fatalf("unbind: %d %v", status, out)
		}
		p = w.bind(t, "pair")
		if p["hint"] != i18n.T(i18n.ZhHans, "hub-channels.pair_hint") {
			t.Fatalf("unpair resurrected diagnostic: %v", p)
		}
	})
}

// No response from an older credentials save may supply a diagnosis for the current save, even after A -> B -> A.
func TestFeishuConfigureDiagnosticIgnoresLateResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bodies []string
	}{
		{"new_app", []string{`{"app_id":"cli_replacement","app_secret":"replacement-secret"}`}},
		{"new_secret", []string{`{"app_secret":"replacement-secret"}`}},
		{"new_domain", []string{`{"domain":"lark"}`}},
		{"same_credentials_resaved", []string{`{"app_id":"cli_example","app_secret":"fake-secret","domain":"feishu"}`}},
		{"original_app_restored", []string{`{"app_id":"cli_replacement","app_secret":"replacement-secret"}`, `{"app_id":"cli_example","app_secret":"fake-secret","domain":"feishu"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, st, f := configureWorld(t, 230002)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			f.mu.Lock()
			f.beforeConfig = func() {
				close(entered)
				select {
				case <-release:
				case <-time.After(10 * time.Second):
					t.Error("timed out waiting for configuration release")
				}
			}
			f.mu.Unlock()
			status, out := w.do(t, "PUT", "/api/v1/bots/feishu", `{"enabled":true,"app_id":"cli_example","app_secret":"fake-secret","domain":"feishu"}`)
			if status != 200 {
				t.Fatalf("initial settings: %d %v", status, out)
			}
			w.bind(t, "pair")
			finished := make(chan struct{})
			go func() {
				w.h.configureBotForPairing(context.Background(), "cli_example", "fake-secret", "feishu")
				close(finished)
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("configuration request not reached")
			}
			for _, body := range tc.bodies {
				status, out := w.do(t, "PUT", "/api/v1/bots/feishu", body)
				if status != 200 {
					t.Fatalf("replacement while configuration pending: %d %v", status, out)
				}
			}
			once.Do(func() { close(release) })
			select {
			case <-finished:
			case <-time.After(10 * time.Second):
				t.Fatal("late configuration did not finish")
			}
			p := w.bind(t, "pair")
			if p["hint"] != i18n.T(i18n.ZhHans, "hub-channels.pair_hint") {
				t.Fatalf("late result reached current prompt: %v", p)
			}
			if p := configurePoll(t, w, st, f); p["hint"] != i18n.T(i18n.ZhHans, "hub-channels.pair_hint") {
				t.Fatalf("late result persisted: %v", p)
			}
		})
	}
}
