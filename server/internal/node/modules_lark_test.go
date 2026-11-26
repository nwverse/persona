package node

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/moduleswitch"
	"github.com/nwverse/persona/server/internal/node/larkgateport"
	"github.com/nwverse/persona/server/internal/proto"
)

// needsLarkGate skips a test of the lark-cli gate or Feishu card context in a build without the lark module
// (persona_without_lark, persona_core_only): both come from internal/modules/lark/larkgate through larkgateport.
func needsLarkGate(t testing.TB) {
	t.Helper()
	if larkgateport.Get(moduleswitch.Set{}) == nil {
		t.Skip("the lark module is not in this build")
	}
}

// needsLarkGateForCommand skips only subtests whose command exercises lark-cli when the lark module is absent, so
// generic CommandGate and dynamic-command cases continue to run in persona_without_lark and persona_core_only builds.
func needsLarkGateForCommand(t testing.TB, command string) {
	t.Helper()
	if larkgateport.Get(moduleswitch.Set{}) != nil {
		return
	}
	for _, needle := range []string{"lark-cli", `lark\-cli`, "lark-c?i", "lark-{cli", "lark''-cli", `lark""-cli`, `lark-c\li`} {
		if strings.Contains(command, needle) {
			t.Skip("the lark module is not in this build")
		}
	}
}

// With the lark module switched off (or excluded from the build), generic CommandGate enforcement still works and
// Lark-specific CLI/card-context helpers degrade to safe no-ops without panicking.
func TestLarkGateDisabledDegradesCleanly(t *testing.T) {
	prev := hubModules.Load()
	off := moduleswitch.Off("lark")
	hubModules.Store(&off)
	t.Cleanup(func() { hubModules.Store(prev) })

	if larkGate() != nil {
		t.Fatal("larkGate() should be nil when module lark is switched off")
	}
	if isLarkCLI("lark-cli") || larkMentionsCLI("lark-cli im +chat-list") || larkNamesCLI("lark-cli") {
		t.Fatal("CLI matchers must return false when module lark is switched off")
	}
	if deny := larkCommandDeny("cat ~/.lark-cli/config.json"); deny != "" {
		t.Fatalf("larkCommandDeny with module off = %q, want empty", deny)
	}
	// Generic CommandGate (assistant.gate) continues to classify and decide calls normally.
	gates := olGate("")
	v := classifyLark(`ex GET /conversations/oc_1/messages`, gates, "", nil)
	if v.deny != "" || v.other || len(v.calls) != 1 || larkDecision(proto.AssistantLark{Gates: gates}, v.calls[0]) != "allow" {
		t.Fatalf("generic CommandGate read failed with lark off: %+v", v)
	}
	v = classifyLark(`ex POST /conversations/oc_1/messages '{"text":"hi"}'`, gates, "", nil)
	if v.deny != "" || v.other || len(v.calls) != 1 || larkDecision(proto.AssistantLark{Gates: gates}, v.calls[0]) != "confirm" {
		t.Fatalf("generic CommandGate write failed with lark off: %+v", v)
	}
	n := &Node{}
	if got := n.larkCardContexts(context.Background(), proto.AssistantLark{}, v.calls); len(got) != 1 || got[0].OK {
		t.Fatalf("larkCardContexts with lark off = %+v, want OK=false", got)
	}
	if _, ok := larkMessageSummary(v.calls[0], larkCardContext{OK: true, Chat: "Room"}, i18n.En); ok {
		t.Fatal("larkMessageSummary should fall back when module lark is switched off")
	}
}

// Turning the lark module off at runtime or running a build without it (persona_without_lark, persona_core_only) must
// remove any stale Persona-written lark-cli wrapper from the assistant bin dir so an ungated lark-cli wrapper cannot
// remain on PATH, while leaving unrelated user-placed files untouched.
func TestLarkCLIWrapperRemovedWhenModuleDisabled(t *testing.T) {
	t.Setenv("PERSONA_SKILLS_DIR", t.TempDir())
	t.Setenv("PERSONA_PLUGIN_SKILLS_DIR", t.TempDir())

	t.Run("runtime disable removes wrapper written while enabled", func(t *testing.T) {
		needsLarkGate(t)
		prev := hubModules.Load()
		on := moduleswitch.Set{}
		hubModules.Store(&on)
		t.Cleanup(func() { hubModules.Store(prev) })

		binDir := t.TempDir()
		fakeBin := filepath.Join(binDir, "lark-cli")
		if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		n := &Node{
			dir:       t.TempDir(),
			larkCfgAt: time.Now(),
			logger:    log.New(io.Discard, "", 0),
			larkCfg:   proto.AssistantLark{CLI: &proto.LarkCLI{Bin: fakeBin, Profile: "work"}},
		}
		t.Cleanup(func() { n.gates().close() })
		cwd := t.TempDir()
		wrapper := filepath.Join(n.assistantBin(cwd), "lark-cli")

		env, release := n.assistantLarkEnv(cwd, "")
		release()
		if _, err := os.Stat(wrapper); err != nil {
			t.Fatalf("lark-cli wrapper should exist while lark module is enabled: %v", err)
		}
		if got, ok := envOf(env, larkProfileEnvName); !ok || got != "work" {
			t.Fatalf("%s with lark enabled = %q (ok=%v), want %q", larkProfileEnvName, got, ok, "work")
		}

		off := moduleswitch.Off("lark")
		hubModules.Store(&off)
		if larkGate() != nil {
			t.Fatal("larkGate() should be nil after disabling lark module")
		}

		env, release = n.assistantLarkEnv(cwd, "")
		release()
		if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
			t.Fatalf("stale lark-cli wrapper stayed after lark module was disabled: err=%v", err)
		}
		if got, ok := envOf(env, larkProfileEnvName); ok {
			t.Fatalf("%s stayed in env after lark module was disabled: %q", larkProfileEnvName, got)
		}
	})

	t.Run("nil port cleans stale Persona wrappers and keeps user files", func(t *testing.T) {
		prev := hubModules.Load()
		if larkgateport.Get(moduleswitch.Set{}) != nil {
			off := moduleswitch.Off("lark")
			hubModules.Store(&off)
		} else {
			// In persona_without_lark / persona_core_only builds, verify cleanup works with no runtime switch off.
			empty := moduleswitch.Set{}
			hubModules.Store(&empty)
		}
		t.Cleanup(func() { hubModules.Store(prev) })

		if larkGate() != nil {
			t.Fatal("larkGate() should be nil with lark module disabled or absent")
		}

		cases := []struct {
			name       string
			content    string
			wantRemove bool
		}{
			{
				name:       "modules/lark/larkgate wrapper",
				content:    "#!/bin/sh\n# lark-cli as manager.json lark.cli says (modules/lark/larkgate), on the assistant's PATH.\nPATH=/opt/bin:\"$PATH\" exec /opt/bin/lark-cli --profile work \"$@\"\n",
				wantRemove: true,
			},
			{
				name:       "legacy node/larkcligate.go manager.json wrapper",
				content:    "#!/bin/sh\n# lark-cli as manager.json lark.cli says (node/larkcligate.go), on the assistant's PATH.\nPATH=/opt/bin:\"$PATH\" exec /opt/bin/lark-cli \"$@\"\n",
				wantRemove: true,
			},
			{
				name:       "legacy node/larkcligate.go hub.json wrapper",
				content:    "#!/bin/sh\n# lark-cli as hub.json lark.cli says (node/larkcligate.go), on the assistant's PATH.\nPATH=/opt/bin:\"$PATH\" exec /opt/bin/lark-cli \"$@\"\n",
				wantRemove: true,
			},
			{
				name:       "unrelated user lark-cli script",
				content:    "#!/bin/sh\necho custom\n",
				wantRemove: false,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				n := &Node{
					dir:       t.TempDir(),
					larkCfgAt: time.Now(),
					logger:    log.New(io.Discard, "", 0),
					larkCfg:   proto.AssistantLark{CLI: &proto.LarkCLI{Bin: "/opt/bin/lark-cli", Profile: "work"}},
				}
				t.Cleanup(func() { n.gates().close() })
				cwd := t.TempDir()
				bin := n.assistantBin(cwd)
				if err := os.MkdirAll(bin, 0o755); err != nil {
					t.Fatal(err)
				}
				wrapper := filepath.Join(bin, "lark-cli")
				if err := os.WriteFile(wrapper, []byte(tc.content), 0o755); err != nil {
					t.Fatal(err)
				}

				env, release := n.assistantLarkEnv(cwd, "")
				release()
				if got, ok := envOf(env, larkProfileEnvName); ok {
					t.Fatalf("%s unexpectedly set when lark module is off: %q", larkProfileEnvName, got)
				}
				_, err := os.Stat(wrapper)
				if tc.wantRemove && !os.IsNotExist(err) {
					t.Fatalf("stale wrapper %q was not removed: err=%v", tc.name, err)
				}
				if !tc.wantRemove && err != nil {
					t.Fatalf("user file %q was unexpectedly removed: err=%v", tc.name, err)
				}
			})
		}

		// Direct writeLarkCLIWrapper call also removes a stale wrapper and tolerates an empty dir.
		directDir := t.TempDir()
		directWrapper := filepath.Join(directDir, "lark-cli")
		if err := os.WriteFile(directWrapper, []byte("#!/bin/sh\n# lark-cli as manager.json lark.cli says (modules/lark/larkgate), on the assistant's PATH.\nexec /opt/bin/lark-cli \"$@\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		n := &Node{}
		n.writeLarkCLIWrapper("", &proto.LarkCLI{Bin: "/opt/bin/lark-cli"})
		n.writeLarkCLIWrapper(directDir, &proto.LarkCLI{Bin: "/opt/bin/lark-cli"})
		if _, err := os.Stat(directWrapper); !os.IsNotExist(err) {
			t.Fatalf("direct writeLarkCLIWrapper did not remove stale wrapper: err=%v", err)
		}
	})
}
