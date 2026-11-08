package node

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// typedThenSays has pane show what a start typed into it, then said lines (the shell's answer) and a prompt.
func typedThenSays(f *fakeHerdr, pane string, said ...string) {
	f.after(func(c herdrCall) {
		if c.Method != "pane.send_input" || str(c.Params["pane_id"]) != pane {
			return
		}
		f.setScreen(pane, "$ "+str(c.Params["text"])+"\n"+strings.Join(said, "\n")+"\n$ ")
	})
}

// withShellLaunchers has the node's login shell list names (launchers.go's cache, a minute fresh), not run a real one.
func withShellLaunchers(t *testing.T, names ...string) {
	t.Helper()
	list := make([]proto.Launcher, 0, len(names))
	for _, name := range names {
		list = append(list, proto.Launcher{Name: name})
	}
	launcherCache.Lock()
	oldAt, oldList := launcherCache.at, launcherCache.list
	launcherCache.at, launcherCache.list = time.Now(), list
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list = oldAt, oldList
		launcherCache.Unlock()
	})
}

// newShellTab answers a start's herdr calls for a new tab w1:p2 in which herdr never sees an agent and the shell
// stays in front.
func newShellTab(f *fakeHerdr, shell string) {
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.reply("agent.get", map[string]any{"agent": map[string]any{"agent": "", "agent_status": "unknown", "pane_id": "w1:p2"}})
	f.reply("pane.list", map[string]any{"panes": []any{map[string]any{"pane_id": "w1:p2", "tab_id": "w1:t2"}}})
	f.reply("pane.process_info", foreground([2]string{shell, "-" + shell}))
	f.setScreen("w1:p2", "$ ")
}

// The tab's shell has no such launcher (#1642: herdr's panes in a container whose ~/.bashrc returns early, another
// shell than the one the launchers are listed in): it says so a moment after the command, and the start ends within
// seconds, not claudeStartWait later, with launcher_missing naming the shell, the line and where the launchers are
// listed, and closes the tab it made. Nothing is named or reported to herdr.
func TestHerdrStartWhoseLauncherTheTabLacksFailsAtOnce(t *testing.T) {
	withPaneShell(t, "/bin/bash")
	withShellLaunchers(t, "cc_gone", "cc_work")
	n, f, home := herdrNode(t)
	withStartWaits(n, time.Minute, time.Minute)
	newShellTab(f, "bash")
	typedThenSays(f, "w1:p2", "bash: cc_gone: command not found")
	began := time.Now()
	_, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "launcher": "cc_gone", "prompt": "hi"})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != proto.CodeLauncherMissing {
		t.Fatalf("start: %v", err)
	}
	if d := time.Since(began); d > 5*time.Second {
		t.Errorf("waited %s for a launcher the shell said it does not have", d)
	}
	var data map[string]string
	_ = json.Unmarshal(re.Data, &data)
	lshell, rc := launcherShell()
	if data["launcher"] != "cc_gone" || data["missing"] != "cc_gone" || data["where"] != startMissWhere || data["pane_shell"] != "bash" ||
		data["launcher_shell"] != lshell || data["rc_file"] != rc || data["line"] != "bash: cc_gone: command not found" || data["delivered"] != deliveredNone {
		t.Errorf("data %v", data)
	}
	if strings.Contains(re.Message, "--mcp-config") || !strings.Contains(re.Message, "bash: cc_gone: command not found") {
		t.Errorf("message %q", re.Message)
	}
	if got := f.trace("tab.close"); len(got) != 1 || got[0] != call("tab.close", map[string]any{"tab_id": "w1:t2"}) {
		t.Errorf("closed %v", got)
	}
	if got := f.trace("agent.rename", "pane.report_agent_session"); len(got) != 0 {
		t.Errorf("a start that failed named or reported its pane: %v", got)
	}
}

// A launcher the login shell does not list either (removed from the rc file, a stale name from an old picker): the
// start still ends at once, as plain launcher_missing, not as the terminal's.
func TestHerdrStartOfAnUnlistedLauncherIsPlainMissing(t *testing.T) {
	withPaneShell(t, "/bin/bash")
	withShellLaunchers(t, "cc_work")
	n, f, home := herdrNode(t)
	withStartWaits(n, time.Minute, time.Minute)
	newShellTab(f, "bash")
	typedThenSays(f, "w1:p2", "bash: cc_gone: command not found")
	began := time.Now()
	_, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "launcher": "cc_gone", "prompt": "hi"})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != proto.CodeLauncherMissing || strings.Contains(string(re.Data), `"where"`) {
		t.Fatalf("start: %v", err)
	}
	if d := time.Since(began); d > 5*time.Second {
		t.Errorf("waited %s", d)
	}
}

// A launcher that runs but whose claude the tab's PATH lacks: claude_missing once the shell is back in front.
func TestHerdrStartWhoseClaudeTheTabLacksFailsAtOnce(t *testing.T) {
	withPaneShell(t, "/bin/zsh")
	withShellLaunchers(t, "cc_gone", "cc_work")
	n, f, home := herdrNode(t)
	withStartWaits(n, time.Minute, time.Minute)
	newShellTab(f, "zsh")
	typedThenSays(f, "w1:p2", "cc_work:2: command not found: claude")
	began := time.Now()
	_, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "launcher": "cc_work", "prompt": "hi"})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != proto.CodeClaudeMissing || !strings.Contains(string(re.Data), `"pane_shell":"zsh"`) {
		t.Fatalf("start: %v", err)
	}
	if d := time.Since(began); d > 5*time.Second {
		t.Errorf("waited %s", d)
	}
}

// What the shell prints of something else after the command (rc or prompt noise) does not end the start: claude may
// still come. A start that then times out says what the shell printed instead of guessing at a busy machine.
func TestHerdrStartTimeoutNamesTheShellsLine(t *testing.T) {
	withPaneShell(t, "/bin/bash")
	withShellLaunchers(t, "cc_gone", "cc_work")
	n, f, home := herdrNode(t)
	withStartWaits(n, 1500*time.Millisecond, time.Minute)
	newShellTab(f, "bash")
	typedThenSays(f, "w1:p2", "bash: __git_ps1: command not found")
	_, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "launcher": "cc_work", "prompt": "hi"})
	if err == nil || rpcCode(err) != "" || !strings.Contains(err.Error(), `the tab's shell printed "bash: __git_ps1: command not found"`) ||
		strings.Contains(err.Error(), "is the machine busy") {
		t.Fatalf("start: %v", err)
	}
}

// Started in the background (the app's "New Agent"), the failed session is listed with the refusal's code and details,
// for the hub to word and the app to offer its remedy; a message waiting for it gets the same code.
func TestAsyncStartKeepsTheRefusalsCode(t *testing.T) {
	withPaneShell(t, "/bin/bash")
	withShellLaunchers(t, "cc_gone", "cc_work")
	n, f, home := herdrNode(t)
	withStartWaits(n, time.Minute, time.Minute)
	newShellTab(f, "bash")
	typedThenSays(f, "w1:p2", "bash: cc_gone: command not found")
	began := time.Now()
	if _, err := request(t, n, "session.start", map[string]any{"session_id": startID, "cwd": home, "workspace_id": "w1",
		"launcher": "cc_gone", "async": true}); err != nil {
		t.Fatal(err)
	}
	var s proto.Session
	for deadline := time.Now().Add(10 * time.Second); s.StartError == ""; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("start did not fail: %+v", s)
		}
		if l := listSessions(t, n); len(l) == 1 {
			s = l[0]
		}
	}
	if time.Since(began) > 5*time.Second {
		t.Errorf("failed after %s", time.Since(began))
	}
	if s.StartErrorCode != proto.CodeLauncherMissing || s.StartErrorArgs["launcher"] != "cc_gone" || s.StartErrorArgs["where"] != startMissWhere ||
		s.StartErrorArgs["pane_shell"] != "bash" || s.StartErrorArgs["delivered"] != "" || !s.UpdatedAt.After(began) {
		t.Errorf("listed %+v", s)
	}
	err := n.starts.wait(t.Context(), startID)
	if rpcCode(err) != proto.CodeLauncherMissing {
		t.Errorf("a message waiting for it: %v", err)
	}
}
