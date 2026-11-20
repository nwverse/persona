package node

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// asBash runs script the way the node does where the login shell is bash (`bash -ic`), with defs as the ~/.bashrc,
// in asNode's minimal environment (without fixedBins).
func asBash(t *testing.T, defs, script string) string {
	t.Helper()
	return asBashIn(t, t.TempDir(), defs, script)
}

func TestLauncherScanTimeoutDiscardsEmptyFramesAndBoundsChildOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix shell process groups")
	}
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("no zsh")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("SHELL", "/bin/zsh")
	const hang = "exec /bin/sh -c '/bin/sleep 8 & wait'"
	for name, rc := range map[string]string{
		"startup": "echo 'Welcome back'\n" + hang,
		"no rows": "_claude_launchers() { " + hang + "; }",
	} {
		t.Run(name, func(t *testing.T) {
			writeFile(t, filepath.Join(home, ".zshrc"), rc+"\n")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			start := time.Now()
			out, err := scanLaunchers(ctx)
			if err == nil || len(out) != 0 || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("scan should fail without reporting launchers: output=%q error=%v context=%v", out, err, ctx.Err())
			}
			if took := time.Since(start); took > 6*time.Second {
				t.Fatalf("scan waited %s for a child that held stdout open", took)
			}
		})
	}
}

func TestConcurrentLaunchersShareFailedShellScan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a Unix shell fixture")
	}
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("no zsh")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("SHELL", "/bin/zsh")
	launcherCache.Lock()
	oldAt, oldList, oldCodex, oldSettings, oldScripts := launcherCache.at, launcherCache.list, launcherCache.codex, launcherCache.settings, launcherCache.scripts
	launcherCache.at, launcherCache.list = time.Time{}, nil
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list, launcherCache.codex, launcherCache.settings, launcherCache.scripts = oldAt, oldList, oldCodex, oldSettings, oldScripts
		launcherCache.Unlock()
	})
	rc, started, release := filepath.Join(home, ".zshrc"), filepath.Join(home, "started"), filepath.Join(home, "release")
	const recordStart = `printf 'start\n' >> "$HOME/started"` + "\n"
	writeFile(t, rc, recordStart+`while [ ! -f "$HOME/release" ]; do /bin/sleep 0.02; done
exit 1
`)
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	n := &Node{}
	const requests = 5
	ready, done := make(chan struct{}), make(chan error, requests)
	for range requests {
		go func() {
			ready <- struct{}{}
			_, err := n.launchers(t.Context())
			done <- err
		}()
	}
	for range requests {
		<-ready
	}
	waitFor(t, "launcher scan startup", func() bool { _, err := os.Stat(started); return err == nil })
	// All callers have reached the call site. Let them join the running scan before allowing it to fail.
	time.Sleep(100 * time.Millisecond)
	writeFile(t, release, "")
	deadline := time.After(10 * time.Second)
	for range requests {
		select {
		case err := <-done:
			if err == nil {
				t.Error("expected the shell failure to reach every caller")
			}
		case <-deadline:
			t.Fatal("launcher requests did not return after the shell exited")
		}
	}
	if starts, err := os.ReadFile(started); err != nil || string(starts) != "start\n" {
		t.Fatalf("concurrent calls started %q, %v", starts, err)
	}
	writeFile(t, rc, recordStart+"alias retry_test='claude --model model-a'\n")
	for range 2 {
		list, err := n.launchers(t.Context())
		if err != nil || byName(list)["retry_test"].Model != "model-a" {
			t.Fatalf("a new call should retry and cache the recovered launchers: %+v, %v", list, err)
		}
	}
	if starts, err := os.ReadFile(started); err != nil || string(starts) != "start\nstart\n" {
		t.Fatalf("retry and cache should run the shell once more: %q, %v", starts, err)
	}
}

// asBashIn is asBash with dir as the HOME, for a test that puts something in it first.
func asBashIn(t *testing.T, dir, defs, script string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	if err := os.WriteFile(filepath.Join(dir, ".bashrc"), []byte(defs), 0o600); err != nil {
		t.Fatal(err)
	}
	script = strings.ReplaceAll(script, fixedBins, "")
	cmd := exec.CommandContext(context.Background(), bash, "-ic", script)
	cmd.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin", "USER=tester", "TERM=dumb"}
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		t.Fatalf("bash: %v", err)
	}
	return string(out)
}

// A machine whose login shell is bash lists the launchers in its ~/.bashrc: the same aliases as in a ~/.zshrc give
// the same launchers, with the same gateways and models.
func TestBashrcAliasLaunchers(t *testing.T) {
	list, _, _, _ := mergeLaunchers(asBash(t, fixture(t, "aliases.zsh"), launcherScript))
	var names []string
	for _, l := range list {
		names = append(names, l.Name)
	}
	want := []string{"dmcc", "envcc", "expcc", "kcc", "mycc", "ncc", "pathcc", "spaced", "ncodex"}
	if !slices.Equal(names, want) {
		t.Fatalf("launchers %v, want %v", names, want)
	}
	m := byName(list)
	for name, w := range map[string][2]string{
		"kcc":    {"https://gw-a.example.com/anthropic", "model-a[1m]"},
		"spaced": {"https://gw-c.example.com", "model with space"},
		"envcc":  {"https://gw-d.example.com", ""},
		"expcc":  {"https://gw-f.example.com", "eq-model"},
	} {
		if l := m[name]; l.BaseURL != w[0] || l.Model != w[1] {
			t.Errorf("%s: base %q model %q, want %q %q", name, l.BaseURL, l.Model, w[0], w[1])
		}
	}
	if m["ncodex"].Agent != "codex" {
		t.Errorf("ncodex: %+v", m["ncodex"])
	}
}

// No alias that starts claude in ~/.bashrc: plain claude, as with zsh.
func TestBashrcWithoutLaunchers(t *testing.T) {
	list, _, _, _ := mergeLaunchers(asBash(t, `alias ll="ls -l"`, launcherScript))
	list = withoutInstalledWorkAgents(list)
	if len(list) != 1 || list[0].Name != "claude" {
		t.Fatalf("got %v", list)
	}
}

// What the rc files print is not the script's: Ubuntu's /etc/bash.bashrc prints a sudo hint until the user has run
// sudo once (#6310), other rc files a greeting, and an exit trap prints after the script. None of it is a launcher,
// and the last PATH entry (where a wrapper script is) stays whole.
func TestRcFileOutputIsNotLaunchers(t *testing.T) {
	defs := `cat <<'EOF'
To run a command as administrator (user "root"), use "sudo <command>".
See "man sudo_root" for details.

EOF
` + fixture(t, "aliases.zsh") + `export PATH="$PATH:$HOME/bin"
echo "Welcome back, tester"
trap 'echo bye' EXIT
`
	want := []string{"dmcc", "envcc", "expcc", "kcc", "mycc", "ncc", "pathcc", "spaced", "claude-demo", "ncodex"}
	for _, sh := range []struct {
		name string
		run  func(t *testing.T, dir, defs, script string) string
	}{{"bash", asBashIn}, {"zsh", asNodeIn}} {
		t.Run(sh.name, func(t *testing.T) {
			home := t.TempDir()
			writeScript(t, filepath.Join(home, "bin", "claude-demo"), demoScript, 0o755)
			list, _, _, _ := mergeLaunchers(sh.run(t, home, defs, launcherScript))
			list = withoutInstalledWorkAgents(list)
			var names []string
			for _, l := range list {
				names = append(names, l.Name)
			}
			if !slices.Equal(names, want) {
				t.Fatalf("launchers %v, want %v", names, want)
			}
		})
	}
}

// The parser on its own: what comes before the script's begin marker and after its end marker is never a launcher,
// and output without the begin marker has none of the script's sections.
func TestLauncherOutputReadsOnlyBetweenMarkers(t *testing.T) {
	hint := "To run a command as administrator (user \"root\"), use \"sudo <command>\".\nSee \"man sudo_root\" for details.\n"
	body := "\x1dkcc\x1fANTHROPIC_BASE_URL=https://gw.example.com\x1fclaude\x1f\x1e\x1d\x1d\x1d\x1d\x1d"
	for name, c := range map[string]struct {
		out  string
		want []string
	}{
		"framed":          {hint + launcherBegin + body + launcherEnd + "bye\n", []string{"kcc"}},
		"no end marker":   {hint + launcherBegin + body, []string{"kcc"}},
		"no begin marker": {hint + body, []string{"claude"}},
	} {
		list, _, _, _ := mergeLaunchers(c.out)
		var names []string
		for _, l := range list {
			names = append(names, l.Name)
		}
		if !slices.Equal(names, c.want) {
			t.Errorf("%s: launchers %v, want %v", name, names, c.want)
		}
	}
}

func TestShellWords(t *testing.T) {
	for in, want := range map[string][]string{
		`a b  c`:                       {"a", "b", "c"},
		`'x y' "p \"q\" \$r" z\ w`:     {"x y", `p "q" $r`, "z w"},
		`'it'\''s'`:                    {"it's"},
		`export A=1; claude --model=m`: {"export", "A=1", ";", "claude", "--model=m"},
		`cd /x&&claude|tee f &`:        {"cd", "/x", "&&", "claude", "|", "tee", "f", "&"},
		`''`:                           {""},
	} {
		if got := shellWords(in); !slices.Equal(got, want) {
			t.Errorf("shellWords(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBashAliases(t *testing.T) {
	got := bashAliases("alias kcc='ANTHROPIC_MODEL=\"m 1\" claude'\nalias -- odd='claude'\nnot an alias\n")
	want := "kcc\x1fANTHROPIC_MODEL=m 1\x1fclaude\x1f\x1eodd\x1fclaude\x1f\x1e"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPasswdShell(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/bash\nme:x:501:20::/Users/me:/bin/zsh\n# comment\n"
	if s := passwdShell(passwd, 0); s != "bash" {
		t.Errorf("uid 0: %q", s)
	}
	if s := passwdShell(passwd, 501); s != "zsh" {
		t.Errorf("uid 501: %q", s)
	}
	if s := passwdShell(passwd, 7); s != "" {
		t.Errorf("uid 7: %q", s)
	}
}
