package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// setupSteps are the step lines of a --json run, by step.
func setupSteps(t *testing.T, out string) map[string]struct {
	OK           bool
	Detail, Next string
} {
	t.Helper()
	steps := map[string]struct {
		OK           bool
		Detail, Next string
	}{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var s struct {
			Step, Detail, Next string
			OK                 bool
		}
		if json.Unmarshal([]byte(l), &s) == nil && s.Step != "" {
			steps[s.Step] = struct {
				OK           bool
				Detail, Next string
			}{s.OK, s.Detail, s.Next}
		}
	}
	return steps
}

func assistantMode(t *testing.T, root string) (mode string, external bool) {
	t.Helper()
	var cfg Config
	b, err := os.ReadFile(filepath.Join(root, "hub.json"))
	if err != nil || json.Unmarshal(b, &cfg) != nil {
		t.Fatalf("hub.json: %v %s", err, b)
	}
	return cfg.Assistant.Mode, cfg.Assistant.External
}

// A new install with --no-service, in a container, or where an earlier run wrote service mode but could not install
// the services: the hub starts the built-in manager itself (child mode), and setup runs no manager program.
func TestSetupChildModeWithoutServices(t *testing.T) {
	for _, c := range []struct {
		name      string
		file      string // hub.json before (no hub has read it)
		noService bool
		container bool
		want      string
	}{
		{name: "--no-service", noService: true},
		{name: "--no-service after a run that wrote service mode", file: `{"database_url":"postgres://x","assistant":{"mode":"service"}}`, noService: true},
		{name: "in a container", container: true},
		{name: "child written by hand stays", file: `{"database_url":"postgres://x","assistant":{"mode":"child"}}`, want: "child"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := setupTestHome(t)
			var token string
			port := fakeHub(t, &token, false)
			cmds := &fakeCommands{replies: map[string]fakeReply{"loginctl show-user": {out: "yes\n"}}}
			o := setupOptions{listen: ":" + strconv.Itoa(port), tls: ":18662", noService: c.noService, json: true}
			e, out := testSetupEnv(t, home, o, cmds, "systemctl", "loginctl")
			e.isContainer = func() bool { return c.container }
			file := c.file
			if file == "" {
				file = `{"database_url":"postgres://x"}`
			}
			if err := os.WriteFile(filepath.Join(e.root, "hub.json"), []byte(file), 0o600); err != nil {
				t.Fatal(err)
			}
			e.pingDB = func(context.Context, string) error {
				cfg, _ := readHubJSON(e.root)
				raw, _ := cfg.get("token")
				_ = json.Unmarshal(raw, &token)
				return nil
			}
			if err := e.setup(context.Background()); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if mode, _ := assistantMode(t, e.root); mode != c.want {
				t.Fatalf("assistant.mode %q, want %q", mode, c.want)
			}
			if cmds.called(filepath.Join(e.root, "bin", "personad") + " manager") {
				t.Fatalf("ran the manager's program: %v", cmds.calls)
			}
			if s := setupSteps(t, out.String())["manager"]; !s.OK || !strings.Contains(s.Detail, "assistant.mode child") {
				t.Fatalf("manager step %+v\n%s", s, out)
			}
		})
	}
}

// An existing hub keeps its mode; --manager-service switches it, restarts the hub so it no longer starts the built-in
// manager, and binds and installs the built-in manager's service. Once it is, again changes nothing.
func TestSetupManagerServiceOnAnExistingHub(t *testing.T) {
	home := setupTestHome(t)
	token := "existing-token"
	port := fakeHub(t, &token, true)
	cmds := &fakeCommands{replies: map[string]fakeReply{"loginctl show-user": {out: "yes\n"}}}
	e, out := testSetupEnv(t, home, setupOptions{listen: ":18660", tls: ":18662", json: true}, cmds, "systemctl", "loginctl")
	file := `{"token":"existing-token","listen":":` + strconv.Itoa(port) + `","database_url":"postgres://x","machines":[{"id":"r1","name":"r1","kind":"ssh"}]}`
	if err := os.WriteFile(filepath.Join(e.root, "hub.json"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.setup(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(e.root, "hub.json")); string(b) != file {
		t.Fatalf("hub.json changed without --manager-service: %s", b)
	}
	if s := setupSteps(t, out.String())["manager"]; !s.OK || !strings.Contains(s.Next, "--manager-service") {
		t.Fatalf("manager step %+v", s)
	}
	install := filepath.Join(e.root, "bin", "personad") + " manager install --builtin"
	if cmds.called(install) {
		t.Fatalf("installed without --manager-service: %v", cmds.calls)
	}

	cmds.calls, cmds.envs = nil, nil
	cmds.replies["systemctl --user is-active"] = fakeReply{}
	out.Reset()
	e.o.managerService = true
	if err := e.setup(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if mode, _ := assistantMode(t, e.root); mode != AssistantModeService {
		t.Fatalf("assistant.mode %q", mode)
	}
	if !cmds.called("systemctl --user restart personad") || !cmds.called(install+" --join http://127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatalf("calls %v", cmds.calls)
	}
	steps := setupSteps(t, out.String())
	if !strings.Contains(steps["service"].Detail, "内置管理者改成了自己的常驻服务") || !steps["manager"].OK || !strings.Contains(steps["manager"].Detail, "persona-manager-assistant") {
		t.Fatalf("steps %+v", steps)
	}

	// Again: the mode is service already, the hub is not restarted; the manager's program keeps a binding that works.
	cmds.calls = nil
	out.Reset()
	if err := e.setup(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if cmds.called("systemctl --user restart") {
		t.Fatalf("restarted again: %v", cmds.calls)
	}
}

// --manager-service on a hub whose built-in manager runs elsewhere (assistant.external) stops: it changes nothing.
func TestSetupManagerServiceLeavesExternal(t *testing.T) {
	home := setupTestHome(t)
	e, out := testSetupEnv(t, home, setupOptions{listen: ":18660", tls: ":18662", managerService: true}, &fakeCommands{})
	file := `{"token":"t","database_url":"postgres://x","assistant":{"external":true}}`
	if err := os.WriteFile(filepath.Join(e.root, "hub.json"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.config(); !errors.Is(err, ErrSetupStopped) || !strings.Contains(out.String(), "assistant.external") {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(e.root, "hub.json")); string(b) != file {
		t.Fatalf("hub.json changed: %s", b)
	}
	// Without the flag: nothing of it is touched, the step says so.
	e.o.managerService = false
	cfg, err := e.config()
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := e.builtinManager(context.Background(), cfg); err != nil || !strings.Contains(out.String(), "assistant.external") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The manager's program failing stops setup with its last line; a service that does not come online is said, and
// setup goes on to the pairing code (the hub itself runs).
func TestSetupBuiltinManagerProblems(t *testing.T) {
	newEnv := func(t *testing.T, online bool) (*setupEnv, *bytes.Buffer, *fakeCommands) {
		home := setupTestHome(t)
		token := "t"
		port := fakeHubWith(t, &token, true, func() bool { return online })
		cmds := &fakeCommands{replies: map[string]fakeReply{"loginctl show-user": {out: "yes\n"}, "systemctl --user is-active": {}}}
		e, out := testSetupEnv(t, home, setupOptions{listen: ":18660", tls: ":18662", json: true}, cmds, "systemctl", "loginctl")
		file := `{"token":"t","listen":":` + strconv.Itoa(port) + `","database_url":"postgres://x","machines":[{"id":"r1","name":"r1","kind":"ssh"}],"assistant":{"mode":"service"}}`
		if err := os.WriteFile(filepath.Join(e.root, "hub.json"), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		return e, out, cmds
	}
	t.Run("install fails", func(t *testing.T) {
		e, out, cmds := newEnv(t, true)
		cmds.replies[filepath.Join(e.root, "bin", "personad")+" manager install"] = fakeReply{err: true,
			out: "Binding: ...\nthe hub did not trade the code, which is still good (HTTP 409): not a service of its own here\n"}
		if err := e.setup(context.Background()); !errors.Is(err, ErrSetupStopped) {
			t.Fatalf("%v\n%s", err, out)
		}
		if s := setupSteps(t, out.String())["manager"]; s.OK || !strings.Contains(s.Detail, "HTTP 409") {
			t.Fatalf("manager step %+v", s)
		}
	})
	t.Run("no code", func(t *testing.T) {
		e, out, cmds := newEnv(t, true)
		e.builtinCode = func(context.Context, *Config) (string, error) { return "", errors.New("no database") }
		if err := e.setup(context.Background()); !errors.Is(err, ErrSetupStopped) || cmds.called(filepath.Join(e.root, "bin", "personad")+" manager") {
			t.Fatalf("%v %v\n%s", err, cmds.calls, out)
		}
	})
	t.Run("not online", func(t *testing.T) {
		e, out, _ := newEnv(t, false)
		if err := e.setup(context.Background()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		s := setupSteps(t, out.String())["manager"]
		if s.OK || !strings.Contains(s.Next, "journalctl --user -u persona-manager-assistant") || !strings.Contains(out.String(), `"done":true`) {
			t.Fatalf("manager step %+v\n%s", s, out)
		}
	})
	t.Run("--no-service on a service-mode hub", func(t *testing.T) {
		e, out, cmds := newEnv(t, true)
		e.o.noService = true
		if err := e.setup(context.Background()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if mode, _ := assistantMode(t, e.root); mode != AssistantModeService || cmds.called(filepath.Join(e.root, "bin", "personad")+" manager") {
			t.Fatalf("mode %q, calls %v", mode, cmds.calls)
		}
		if s := setupSteps(t, out.String())["manager"]; !s.OK || !strings.Contains(s.Next, "manager join --builtin") {
			t.Fatalf("manager step %+v", s)
		}
	})
}

func TestSetupFlagsConflict(t *testing.T) {
	setupTestHome(t)
	var out, errOut bytes.Buffer
	if err := SetupCommand(context.Background(), []string{"--no-service", "--manager-service"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "--manager-service") {
		t.Fatalf("%v", err)
	}
}

func TestFreshManagerMode(t *testing.T) {
	for _, c := range []struct {
		in      string
		service bool
		want    string
	}{
		{`{}`, true, `{"assistant":{"mode":"service"}}`},
		{`{}`, false, `{}`},
		{`{"assistant":{"mode":"service","owner":"me"}}`, false, `{"assistant":{"owner":"me"}}`},
		{`{"assistant":{"mode":"child"}}`, true, `{"assistant":{"mode":"child"}}`},
		{`{"assistant":{"external":true}}`, true, `{"assistant":{"external":true}}`},
		{`{"assistant":{"external":true,"mode":"service"}}`, false, `{"assistant":{"external":true,"mode":"service"}}`},
	} {
		o, err := parseJSONObject([]byte(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if err := freshManagerMode(o, c.service); err != nil {
			t.Fatal(err)
		}
		if got := string(o.marshal()); got != c.want {
			t.Errorf("%s service=%v: %s, want %s", c.in, c.service, got, c.want)
		}
	}
}
