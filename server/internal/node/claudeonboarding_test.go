package node

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestClaudeOnboardingVerdict(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "missing.json")
	if ready, known := claudeOnboardingVerdict(missing); !known || ready {
		t.Fatalf("missing config: ready=%v known=%v, want not ready, known", ready, known)
	}

	writeConfig := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name      string
		file      string
		wantReady bool
		wantKnown bool
	}{
		{"flag true", writeConfig(t.Name()+"-true.json", `{"hasCompletedOnboarding":true}`), true, true},
		{"flag false", writeConfig(t.Name()+"-false.json", `{"hasCompletedOnboarding":false}`), false, true},
		{"flag absent", writeConfig(t.Name()+"-absent.json", `{}`), false, true},
		{"flag among other keys", writeConfig(t.Name()+"-other.json", `{"theme":"dark","hasCompletedOnboarding":true,"foo":1}`), true, true},
		{"malformed json", writeConfig(t.Name()+"-bad.json", `{not json`), true, false},
		{"empty file", writeConfig(t.Name()+"-empty.json", ``), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ready, known := claudeOnboardingVerdict(tc.file)
			if ready != tc.wantReady || known != tc.wantKnown {
				t.Fatalf("got ready=%v known=%v, want ready=%v known=%v", ready, known, tc.wantReady, tc.wantKnown)
			}
		})
	}

	// A directory where a file is expected is an unreadable-for-another-reason case: unknown, not a verdict.
	if ready, known := claudeOnboardingVerdict(t.TempDir()); known || !ready {
		t.Fatalf("directory path: ready=%v known=%v, want unknown", ready, known)
	}
}

func TestModernClaudeConfig(t *testing.T) {
	for _, p := range []string{
		"/home/me/.claude.json",
		"/home/me/.claude/.claude-custom-oauth.json",
	} {
		if !modernClaudeConfig(p) {
			t.Errorf("%s should carry hasCompletedOnboarding", p)
		}
	}
	for _, p := range []string{
		"/home/me/.claude/.config.json",
		"/home/me/settings.json",
	} {
		if modernClaudeConfig(p) {
			t.Errorf("%s is not a modern onboarding config", p)
		}
	}
}

func TestClaudeOnboardingLaunchShapeAndConfig(t *testing.T) {
	home, service, target := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", service)
	writeFile(t, filepath.Join(service, ".claude.json"), `{"hasCompletedOnboarding":true}`)
	for _, tc := range []struct {
		name, file, body string
		customOAuth      bool
		ready, known     bool
	}{
		{"missing config", ".claude.json", "", false, false, true},
		{"flag absent", ".claude.json", `{"firstStartVersion":"2.1.318"}`, false, false, true},
		{"flag false", ".claude.json", `{"hasCompletedOnboarding":false}`, false, false, true},
		{"onboarded", ".claude.json", `{"hasCompletedOnboarding":true}`, false, true, true},
		{"custom oauth", ".claude-custom-oauth.json", `{"hasCompletedOnboarding":true}`, true, true, true},
		{"legacy config", ".config.json", `{}`, false, true, false},
		{"malformed", ".claude.json", `{not json`, false, true, false},
		{"empty", ".claude.json", " ", false, true, false},
		{"unreadable", ".claude.json", "directory", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(target, tc.name)
			path := filepath.Join(dir, tc.file)
			if tc.body == "directory" {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if tc.body != "" {
				writeFile(t, path, tc.body)
			}
			env := []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + dir}
			if tc.customOAuth {
				env = append(env, "CLAUDE_CODE_CUSTOM_OAUTH_URL=https://auth.example.com")
			}
			if ready, known := claudeOnboardingReady(env, true); ready != tc.ready || known != tc.known {
				t.Fatalf("interactive ready=%v known=%v, want %v %v", ready, known, tc.ready, tc.known)
			}
			if ready, known := claudeOnboardingReady(env, false); !ready || !known {
				t.Fatalf("headless blocked: ready=%v known=%v", ready, known)
			}
		})
	}
	// No explicit config directory uses ~/.claude.json, not the service's profile.
	if ready, known := claudeOnboardingReady([]string{"HOME=" + home}, true); ready || !known {
		t.Fatalf("default directory: ready=%v known=%v", ready, known)
	}
}

func TestOnboardingBlockHintUsesTargetLauncher(t *testing.T) {
	fakeClaudeHome(t, "")
	home := os.Getenv("HOME")
	service, machine, other := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", service)
	t.Setenv("CLAUDE_CODE_CUSTOM_OAUTH_URL", "")
	writeFile(t, filepath.Join(service, ".claude.json"), `{"hasCompletedOnboarding":true}`)
	writeFile(t, filepath.Join(other, ".claude.json"), `{"hasCompletedOnboarding":true}`)
	n := &Node{home: home, dir: filepath.Join(home, ".persona")}
	n.claudeStartDir.Store(&machine)
	n.setManaged([]proto.ManagedLauncher{{Name: "gateway", Env: "CLAUDE_CONFIG_DIR=" + other}})
	writeFile(t, filepath.Join(home, ".zshrc"),
		`target() { CLAUDE_CONFIG_DIR=`+shellQuote(other)+` claude "$@"; }`)
	if hint := n.onboardingBlockHint(context.Background(), "claude"); hint == "" {
		t.Fatal("the machine profile is unfinished; an unrelated onboarded service profile must not hide it")
	}
	for _, launcher := range []string{"gateway", "target"} {
		if hint := n.onboardingBlockHint(context.Background(), launcher); hint != "" {
			t.Fatalf("%s's own profile is onboarded: %s", launcher, hint)
		}
	}
	writeFile(t, filepath.Join(machine, ".claude.json"), `{"hasCompletedOnboarding":true}`)
	writeFile(t, filepath.Join(service, ".claude.json"), `{}`)
	if hint := n.onboardingBlockHint(context.Background(), "claude"); hint != "" {
		t.Fatalf("headless-only service profile must not block the onboarded machine profile: %s", hint)
	}
}
