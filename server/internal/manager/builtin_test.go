package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/managerconf"
	"github.com/nwverse/persona/server/internal/update"
)

// builtinHub answers the join like the hub: per code, the manager it is for, or a refusal.
func builtinHub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(builtinJoins())
	t.Cleanup(srv.Close)
	return srv
}

// builtinJoins is builtinHub's handler.
func builtinJoins() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != bus.JoinPath {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Code    string
			Builtin bool
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch {
		case req.Code == "mi_builtin" && !req.Builtin, req.Code == "mi_other" && req.Builtin:
			w.WriteHeader(http.StatusConflict) // a code of the other kind of join, unspent
			_, _ = w.Write([]byte(`{"code":"builtin_code","error":"a code of the other kind"}`))
			return
		}
		switch req.Code {
		case "mi_builtin":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": assistantwire.AssistantID, "name": "助手", "token": "jb_builtin", "hub_id": "h1"})
		case "mi_other":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "m-0a1b2c", "name": "工作", "token": "jb_other", "hub_id": "h1"})
		case "mi_child":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"builtin_not_service","error":"not a service of its own here"}`))
		case "mi_far":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"builtin_local_only","error":"local only"}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"bad_install_code"}`))
		}
	})
}

// The built-in manager's code binds it: the hub's address, the token and the hub's id in its manager.json, its other
// keys as they were, 0600. Another manager's code, a hub that still starts the built-in manager, a refusal from far
// away: configuration errors, nothing written.
func TestJoinBuiltin(t *testing.T) {
	home := isolate(t)
	data := filepath.Join(home, ".persona")
	srv := builtinHub(t)
	file := managerconf.DefaultFile(data)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"database_name":"persona_manager","assistant":{"owner":"me"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := managerconf.ReadBinding(data); !errors.Is(err, managerconf.ErrNotBound) {
		t.Fatalf("before: %v", err)
	}
	for _, code := range []string{"mi_other", "mi_child", "mi_far", "mi_unknown"} {
		_, err := JoinBuiltin(context.Background(), JoinOptions{Hub: srv.URL, Code: code}, data)
		var ce ConfigError
		if !errors.As(err, &ce) {
			t.Errorf("%s: %v", code, err)
		}
		if _, err := managerconf.ReadBinding(data); !errors.Is(err, managerconf.ErrNotBound) {
			t.Errorf("%s bound it: %v", code, err)
		}
	}
	b, err := JoinBuiltin(context.Background(), JoinOptions{Hub: srv.URL + "/", Code: "mi_builtin"}, data)
	if err != nil || b.Token != "jb_builtin" || b.Hub != srv.URL {
		t.Fatalf("join: %+v %v", b, err)
	}
	got, err := managerconf.ReadBinding(data)
	if err != nil || got != (managerconf.Binding{Hub: srv.URL, HubID: "h1", Token: "jb_builtin"}) {
		t.Fatalf("binding %+v %v", got, err)
	}
	// the built-in manager's code given to a manager's join: refused by the hub, unspent, nothing written
	if _, _, err := Join(context.Background(), JoinOptions{Hub: srv.URL, Code: "mi_builtin", DatabaseURL: "postgres://u@127.0.0.1:1/own"}); !errors.As(err, new(ConfigError)) {
		t.Fatalf("manager join with the built-in code: %v", err)
	}
	raw, _ := os.ReadFile(file)
	if !strings.Contains(string(raw), `"database_name": "persona_manager"`) || !strings.Contains(string(raw), `"owner": "me"`) {
		t.Fatalf("the other keys: %s", raw)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi, err)
	}
}

// Not bound, the built-in manager's service does not start: a configuration error (exit 2, not restarted).
func TestRunBuiltinNotBound(t *testing.T) {
	isolate(t)
	err := RunBuiltin(context.Background(), update.Build{}, &strings.Builder{})
	var ce ConfigError
	if !errors.As(err, &ce) || !errors.Is(err, managerconf.ErrNotBound) {
		t.Fatalf("err %v", err)
	}
}

// After the hub refused its token, the built-in manager's service waits until its binding has a new token.
func TestBuiltinWaitsForANewToken(t *testing.T) {
	home := isolate(t)
	data := filepath.Join(home, ".persona")
	defer func(p, r time.Duration, f func(context.Context, *assistant.ManagerConfig) error) {
		revokedPoll, revokedRetry, probeToken = p, r, f
	}(revokedPoll, revokedRetry, probeToken)
	revokedPoll, revokedRetry = 20*time.Millisecond, time.Hour
	probeToken = func(context.Context, *assistant.ManagerConfig) error { return bus.ErrRevoked }
	if err := managerconf.WriteBinding(data, managerconf.Binding{Hub: "http://127.0.0.1:1", Token: "old"}); err != nil {
		t.Fatal(err)
	}
	before := &assistant.ManagerConfig{ID: assistantwire.AssistantID, Hub: "http://127.0.0.1:1", Token: "old"}
	done := make(chan error, 1)
	var out strings.Builder
	go func() { done <- awaitRestartWith(context.Background(), before, builtinToken(data), &out) }()
	select {
	case err := <-done:
		t.Fatalf("returned before a new token: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := managerconf.WriteBinding(data, managerconf.Binding{Hub: "http://127.0.0.1:1", Token: "new"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not see the new token")
	}
}

// A manager.json that does not read stops the built-in join before the code is spent.
func TestJoinBuiltinReadsManagerJSONFirst(t *testing.T) {
	home := isolate(t)
	data := filepath.Join(home, ".persona")
	traded := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { traded = true }))
	defer srv.Close()
	file := managerconf.DefaultFile(data)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := JoinBuiltin(context.Background(), JoinOptions{Hub: srv.URL, Code: "mi_builtin"}, data); !errors.As(err, new(ConfigError)) || traded {
		t.Fatalf("err %v, traded %v", err, traded)
	}
}

// One built-in service at a time: a second one on the same data directory does not start (exit 2).
func TestRunBuiltinOneAtATime(t *testing.T) {
	home := isolate(t)
	data := filepath.Join(home, ".persona")
	if err := os.MkdirAll(managerconf.DefaultDir(data), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := lockFile(filepath.Join(managerconf.DefaultDir(data), builtinLock)); err != nil {
		t.Fatal(err)
	}
	err := RunBuiltin(context.Background(), update.Build{}, &strings.Builder{})
	if !errors.As(err, new(ConfigError)) || !strings.Contains(err.Error(), "another persona-manager assistant") {
		t.Fatalf("second: %v", err)
	}
}
