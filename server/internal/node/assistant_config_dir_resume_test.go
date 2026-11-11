package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// resumeCLI resolves its directory as Claude Code does: unset means ~/.claude, a set value (an empty one too) is the
// directory. A resume whose transcript is not there fails the way Claude Code does.
const resumeCLI = `if [ "${CLAUDE_CONFIG_DIR+set}" = set ]; then dir=$CLAUDE_CONFIG_DIR; printf 'set:%s\n' "$dir"; else dir=$HOME/.claude; printf 'unset\n'; fi > "$SDK_FAKE_DIR/cli-profile"
prev=
for a in "$@"; do
  if [ "$prev" = --resume ] && ! ls "$dir"/projects/*/"$a.jsonl" >/dev/null 2>&1; then
    echo "No conversation found with session ID: $a" >&2
    exit 1
  fi
  prev=$a
done
echo '{"type":"system","subtype":"init"}'
while IFS= read -r line; do
  case "$line" in *'"type":"user"'*) echo '{"type":"result","subtype":"success"}' ;; esac
done
`

// resumeTest is sdkTest with resumeCLI as the machine's claude, a home reached through a symlink (as /home -> /data00/home)
// and no CLAUDE_CONFIG_DIR in the node's service environment.
func resumeTest(t *testing.T) (*sdkFixture, string) {
	t.Helper()
	f := sdkTest(t)
	real := os.Getenv("HOME")
	if err := os.WriteFile(filepath.Join(real, "bin", "claude"), []byte("#!/bin/sh\n"+resumeCLI), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	f.n.home = link
	f.n.dir = filepath.Join(link, ".persona")
	f.n.cdirs = newClaudeDirs()
	f.n.cdirs.scan = func() map[int]procEnv { return nil }
	return f, real
}

func writeProfileTranscript(t *testing.T, profile, cwd, id string) {
	t.Helper()
	writeFile(t, filepath.Join(profile, "projects", "-assistant", id+".jsonl"),
		`{"type":"user","cwd":`+jsonString(t, cwd)+`,"message":{"role":"user","content":"earlier"}}`+"\n")
}

// A conversation from before #4310 whose transcript is in ~/.claude resumes with CLAUDE_CONFIG_DIR unset: "default" is
// Claude's unset directory, and Claude Code reads an empty value as a directory of its own (#4504).
func TestAssistantResumesHomeProfileConversationWithTheVariableUnset(t *testing.T) {
	f, real := resumeTest(t)
	n := f.n
	id := "a5500000-0000-4000-8000-000000000101"
	cwd := filepath.Join(n.dir, "assistant")
	writeProfileTranscript(t, filepath.Join(real, ".claude"), cwd, id)
	params := map[string]any{"launcher": "claude", "cwd": cwd, "text": "hello", "session_id": id, "model_fingerprint": "resume-test"}
	if _, err := nodeCall(t, n, "assistant.send", params); err != nil {
		t.Fatalf("resume failed: %v (child saw %q)", err, f.file("cli-profile"))
	}
	if got := f.file("cli-profile"); got != "unset\n" {
		t.Fatalf("child CLAUDE_CONFIG_DIR = %q, want unset", got)
	}
	if st, _ := n.state.get(id); st.ConfigDir != "default" {
		t.Fatalf("recorded profile %q, want default", st.ConfigDir)
	}
}

// A recorded profile that lacks the conversation's transcript (the conversation moved launchers, a rollback resumed it
// elsewhere) never wins over the transcript: the conversation resumes where its newest transcript is.
func TestAssistantResumeFollowsTheTranscriptOverARecordedProfile(t *testing.T) {
	for _, runtime := range []string{"", proto.AssistantRuntimeAgentSDK} {
		for _, scenario := range []string{"recorded-lacks-transcript", "recorded-holds-older-copy"} {
			t.Run(runtime+"/"+scenario, func(t *testing.T) {
				f, _ := resumeTest(t)
				n := f.n
				id := "a5500000-0000-4000-8000-000000000102"
				cwd := filepath.Join(n.dir, "assistant")
				private := filepath.Join(n.dir, "profiles", "cc-old")
				machine := t.TempDir()
				n.cdirs.known = []string{machine}
				if scenario == "recorded-holds-older-copy" {
					writeProfileTranscript(t, private, cwd, id)
					old := filepath.Join(private, "projects", "-assistant", id+".jsonl")
					if err := os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
						t.Fatal(err)
					}
				}
				writeProfileTranscript(t, machine, cwd, id)
				raw, _ := json.Marshal(map[string]any{"launcher": "cc-old", "cwd": cwd, "config_dir": private})
				var st sessionState
				if err := json.Unmarshal(raw, &st); err != nil {
					t.Fatal(err)
				}
				n.state.put(id, st)
				params := map[string]any{"launcher": "claude", "cwd": cwd, "text": "hello", "session_id": id, "runtime": runtime, "model_fingerprint": "resume-test"}
				if _, err := nodeCall(t, n, "assistant.send", params); err != nil {
					t.Fatalf("resume failed: %v (child saw %q)", err, f.file("cli-profile"))
				}
				p := f.proc(id)
				if got := envValue(p.cmd.Environ(), "CLAUDE_CONFIG_DIR"); !sameScopePath(got, machine) {
					t.Fatalf("process profile %q, want %q", got, machine)
				}
				if runtime == "" {
					if got := strings.TrimSuffix(f.file("cli-profile"), "\n"); !sameScopePath(strings.TrimPrefix(got, "set:"), machine) {
						t.Fatalf("CLI child profile %q, want %q", got, machine)
					}
				}
				if st, _ := n.state.get(id); !sameScopePath(st.ConfigDir, machine) {
					t.Fatalf("recorded profile %q, want %q", st.ConfigDir, machine)
				}
			})
		}
	}
}
