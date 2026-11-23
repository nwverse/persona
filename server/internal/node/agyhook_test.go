package node

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// agyStartScene is a herdr node on which an agy start can go through: the new tab, and herdr reporting agy idle in it
// once the start command is typed. herdr's integration.list answers state for the Antigravity integration.
func agyStartScene(t *testing.T, state string) (*Node, *fakeHerdr, string) {
	t.Helper()
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	n, f, home := herdrNode(t)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.after(func(c herdrCall) {
		if c.Method == "pane.send_input" && strings.Contains(c.String(), "HERDR_AGENT=agy") {
			f.reply("session.snapshot", snapshot(nil, nil, []any{
				agentOf(agyAgent, "", proto.StatusIdle, "w1:p2", "w1:t2", "w1", home, agyAgent),
			}, nil))
			f.setScreen("w1:p2", agyIdleScreen)
		}
	})
	if state != "" {
		f.reply("integration.list", herdrIntegrations(state))
	}
	return n, f, home
}

func startAgy(t *testing.T, n *Node, home string) (proto.Session, error) {
	t.Helper()
	res, err := nodeCall(t, n, "session.start", map[string]any{
		"cwd": home, "launcher": agyAgent, "host": proto.EngineHerdr, "workspace_id": "w1", "async": true,
	})
	var started struct{ Session proto.Session }
	if err == nil {
		if jerr := json.Unmarshal(res, &started); jerr != nil {
			t.Fatalf("start answer %s: %v", res, jerr)
		}
	}
	return started.Session, err
}

// writeAgyHook installs herdr's Antigravity hook the way `herdr integration install antigravity-cli` does (0.8.0 to
// 0.9.5): hooks.json runs the script before each invocation, and the script carries herdr's mark.
func writeAgyHook(t *testing.T, dir string) {
	t.Helper()
	script := filepath.Join(dir, "hooks", agyHookScript)
	writeFile(t, script, "#!/bin/sh\n# installed by herdr\n# "+agyHookMark+"\n# HERDR_INTEGRATION_VERSION=4\nprintf '{}\\n'\n")
	hooks := map[string]any{"herdr": map[string]any{"PreInvocation": []any{map[string]any{
		"command": "bash '" + script + "' session", "timeout": 10, "type": "command",
	}}}}
	b, _ := json.Marshal(hooks)
	writeFile(t, filepath.Join(dir, "hooks.json"), string(b))
}

func refusedFor(err error, code string) bool {
	var re *rpc.Error
	return errors.As(err, &re) && re.Code == code
}

// An agy start that would open a herdr pane herdr's Antigravity hook cannot report is refused before any tab opens
// or anything is typed (#13445).
func TestAgyStartRefusedWithoutHerdrHook(t *testing.T) {
	n, f, home := agyStartScene(t, herdr.IntegrationNotInstalled)
	_, err := startAgy(t, n, home)
	if !refusedFor(err, proto.CodeAgyIntegrationMissing) {
		t.Fatalf("start without the hook = %v, want %s", err, proto.CodeAgyIntegrationMissing)
	}
	if !strings.Contains(err.Error(), "herdr integration install antigravity-cli") {
		t.Fatalf("the refusal does not say what to run: %v", err)
	}
	for _, m := range []string{"tab.create", "workspace.create", "pane.send_input", "agent.prompt"} {
		if got := f.trace(m); len(got) != 0 {
			t.Fatalf("refused start still called %s: %v", m, got)
		}
	}
}

// A hook herdr calls current or outdated still reports the conversation: the start goes on as before.
func TestAgyStartWithHerdrHook(t *testing.T) {
	for _, state := range []string{herdr.IntegrationCurrent, herdr.IntegrationOutdated} {
		t.Run(state, func(t *testing.T) {
			n, f, home := agyStartScene(t, state)
			s, err := startAgy(t, n, home)
			if err != nil || s.PaneID != "w1:p2" {
				t.Fatalf("start with a %s hook = %+v, %v", state, s, err)
			}
			if len(f.trace("tab.create")) != 1 {
				t.Fatalf("tab.create calls = %v", f.trace("tab.create"))
			}
		})
	}
}

// A herdr from before the JSON integration API (0.8.x) does not know integration.list: the hook's files decide.
func TestAgyStartOldHerdrUsesHookFiles(t *testing.T) {
	n, f, home := agyStartScene(t, "")
	f.handle("integration.list", func(map[string]any) any {
		return herdrFail{Code: "invalid_request", Message: "unknown variant `integration.list`, expected one of `ping`"}
	})
	if _, err := startAgy(t, n, home); !refusedFor(err, proto.CodeAgyIntegrationMissing) {
		t.Fatalf("old herdr, no hook files: %v", err)
	}
	if len(f.trace("tab.create")) != 0 {
		t.Fatal("old herdr without hook files opened a tab")
	}
	writeAgyHook(t, filepath.Join(home, ".gemini", "config"))
	if s, err := startAgy(t, n, home); err != nil || s.PaneID != "w1:p2" {
		t.Fatalf("old herdr with the hook files: %+v, %v", s, err)
	}
}

// A herdr that cannot be asked says nothing about the hook: the start is left as it was, not refused on a guess.
func TestAgyHookUnknownWhenHerdrDoesNotAnswer(t *testing.T) {
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	n, _ := testNode(t)
	launch := &herdrHost{c: &herdr.Client{Socket: filepath.Join(t.TempDir(), "none.sock")}}
	if got := n.agyHook(context.Background(), launch, ""); got != "" {
		t.Fatalf("unreachable herdr: hook state %q, want unknown", got)
	}
	if err := n.checkAgyPane(context.Background(), launch, ""); err != nil {
		t.Fatalf("unreachable herdr refused the start: %v", err)
	}
	if got := n.agyHook(context.Background(), &herdrCoreHost{}, ""); got != "" {
		t.Fatalf("the Herdr App host: hook state %q, want unknown", got)
	}
}

func TestAgyHookFiles(t *testing.T) {
	dir := t.TempDir()
	if agyHookFiles(dir) || agyHookFiles("") {
		t.Fatal("an empty directory has no hook")
	}
	writeAgyHook(t, dir)
	if !agyHookFiles(dir) {
		t.Fatal("herdr's installed hook was not found")
	}
	other := t.TempDir()
	writeAgyHook(t, other)
	writeFile(t, filepath.Join(other, "hooks", agyHookScript), "#!/bin/sh\n# someone else's script\n")
	if agyHookFiles(other) {
		t.Fatal("a script without herdr's mark counted")
	}
	unwired := t.TempDir()
	writeAgyHook(t, unwired)
	writeFile(t, filepath.Join(unwired, "hooks.json"), `{"herdr":{"PostInvocation":[{"command":"bash hooks/herdr-agent-state.sh session"}]}}`)
	if agyHookFiles(unwired) {
		t.Fatal("a script hooks.json does not run before invocations counted")
	}
	writeFile(t, filepath.Join(unwired, "hooks.json"), `not json`)
	if agyHookFiles(unwired) {
		t.Fatal("an unreadable hooks.json counted")
	}
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", dir)
	if got := agyHookDir("/home/example"); got != dir {
		t.Fatalf("hook dir with ANTIGRAVITY_CLI_CONFIG_DIR = %q", got)
	}
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	if got := agyHookDir("/home/example"); got != filepath.Join("/home/example", ".gemini", "config") {
		t.Fatalf("default hook dir = %q", got)
	}
}

// The launcher check of an agy launcher whose start would open such a pane fails at once with the refusal's reason,
// without running agy.
func TestLauncherCheckAgyWithoutHerdrHook(t *testing.T) {
	n, f, _ := agyStartScene(t, herdr.IntegrationNotInstalled)
	cachedAssistantProbe(t, agyAgent, proto.LauncherDetail{Agent: agyAgent})
	c := n.launcherCheck(context.Background(), agyAgent)
	if c.OK || c.Agent != agyAgent || c.Error != agyIntegrationMissingText(i18n.Default) {
		t.Fatalf("check = %+v", c)
	}
	if len(f.trace("tab.create")) != 0 {
		t.Fatal("the check opened a tab")
	}
}

// node doctor's agy-herdr line: only where herdr answers and agy is used, warn without the hook or with an outdated one.
func TestDoctorAgyHerdrLine(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no agy on PATH unless a case says so
	cases := []struct {
		name      string
		answer    any
		files     bool
		want      string // "" : no line
		wantHint  bool
		wantState string
	}{
		{name: "current", answer: herdrIntegrations(herdr.IntegrationCurrent), want: DoctorOK},
		{name: "missing", answer: herdrIntegrations(herdr.IntegrationNotInstalled), want: DoctorWarn, wantHint: true},
		{name: "outdated", answer: herdrIntegrations(herdr.IntegrationOutdated), want: DoctorWarn, wantHint: true},
		{name: "agy not used", answer: map[string]any{"integrations": []any{map[string]any{"target": herdr.AntigravityIntegration,
			"command": "agy", "available": false, "state": herdr.IntegrationNotInstalled}}}},
		{name: "old herdr with the hook files", answer: herdrFail{Code: "invalid_request", Message: "unknown variant `integration.list`"},
			files: true, want: DoctorOK},
		{name: "old herdr without agy", answer: herdrFail{Code: "invalid_request", Message: "unknown variant `integration.list`"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHerdr(t)
			answer := tc.answer
			f.handle("integration.list", func(map[string]any) any { return answer })
			dir := t.TempDir()
			if tc.files {
				writeAgyHook(t, dir)
			}
			c, ok := checkAgyHerdrIn(context.Background(), f.sock, dir, i18n.Default)
			if tc.want == "" {
				if ok {
					t.Fatalf("line %+v where agy is not used", c)
				}
				return
			}
			if !ok || c.Name != "agy-herdr" || c.Status != tc.want || c.Items == nil || (c.Hint != "") != tc.wantHint {
				t.Fatalf("line = %+v, %v", c, ok)
			}
		})
	}
	if _, ok := checkAgyHerdrIn(context.Background(), filepath.Join(t.TempDir(), "none.sock"), t.TempDir(), i18n.Default); ok {
		t.Fatal("a line without herdr to ask (the herdr line says so)")
	}
}

func TestAgyIntegrationMissingLocalized(t *testing.T) {
	for _, lang := range []i18n.Lang{"en", "zh-Hans", "ja"} {
		if text := agyIntegrationMissingText(lang); !strings.Contains(text, "herdr integration install antigravity-cli") {
			t.Fatalf("%s: %q", lang, text)
		}
	}
}
