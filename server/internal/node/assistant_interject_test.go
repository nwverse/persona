package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// interjectClaude is a claude that names version in its init, starts answering the first message and never finishes
// it (the conversation stays busy), acknowledges every control request and logs what it reads to ~/stdin.jsonl.
func interjectClaude(t *testing.T, version string) {
	t.Helper()
	fakeClaudeHome(t, `log="$HOME/stdin.jsonl"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$log"
  case "$line" in
  *'"type":"control_request"'*)
    id=$(printf '%s' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
    printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s"}}\n' "$id" ;;
  *'"type":"user"'*)
    if [ ! -f "$HOME/answering" ]; then
      : > "$HOME/answering"
      echo '{"type":"system","subtype":"init","claude_code_version":"`+version+`"}'
      echo '{"type":"stream_event","event":{"type":"message_start","message":{}}}'
    fi ;;
  esac
done
`)
}

func interjectLog(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), "stdin.jsonl"))
	return string(b)
}

// interjectSecond starts a conversation that stays busy answering "first", waits for its init, then sends "second".
// It returns what the fake claude read after "first".
func interjectSecond(t *testing.T, version string, on bool) string {
	t.Helper()
	interjectClaude(t, version)
	n := codexTestNode(t)
	withFakeHub(t, n)
	id, err := n.assistantSend(context.Background(), "", t.TempDir(), "claude", "first", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	n.mu.Lock()
	p := n.procs[id]
	n.mu.Unlock()
	sdkWait(t, "its init", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.claudeVersion == version
	})
	if _, err := n.assistantSend(context.Background(), id, "", "claude", "second", nil, "", "", modelExecution{Interject: on}); err != nil {
		t.Fatal(err)
	}
	sdkWait(t, "the second message", func() bool { return strings.Contains(interjectLog(t), "second") })
	if on && versionAtLeast(version, backgroundTasksMinVersion) {
		sdkWait(t, "background_tasks", func() bool { return strings.Contains(interjectLog(t), "background_tasks") })
	}
	log := interjectLog(t)
	return log[strings.Index(log, `"first"`):]
}

// A message to a conversation that is answering moves its foreground work to the background, after the message is
// in, and tells the model it came mid-turn (#3278).
func TestAssistantInterjectBackgroundsForegroundWork(t *testing.T) {
	after := interjectSecond(t, "2.1.322", true)
	msg, bg := strings.Index(after, `"second"`), strings.Index(after, `"subtype":"background_tasks"`)
	if msg < 0 || bg < 0 || bg < msg {
		t.Fatalf("want the message, then background_tasks:\n%s", after)
	}
	if !strings.Contains(after, `title=\"插话\"`) {
		t.Fatalf("the message does not say it came mid-turn:\n%s", after)
	}
}

// With the setting off, or on a Claude Code without background_tasks, the message goes in as before.
func TestAssistantInterjectOffOrOldClaude(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		on            bool
	}{{"off", "2.1.322", false}, {"old claude", "2.1.138", true}} {
		t.Run(tc.name, func(t *testing.T) {
			after := interjectSecond(t, tc.version, tc.on)
			if strings.Contains(after, "background_tasks") || strings.Contains(after, "插话") {
				t.Fatalf("interjected:\n%s", after)
			}
		})
	}
}

// Only a busy Claude on the CLI whose init named a Claude Code with background_tasks interjects.
func TestAssistantInterjects(t *testing.T) {
	for _, tc := range []struct {
		name          string
		agent, rt, v  string
		busy, expects bool
	}{
		{"busy", "claude", "", "2.1.139", true, true},
		{"idle", "claude", "", "2.1.322", false, false},
		{"before init", "claude", "", "", true, false},
		{"old", "claude", "", "2.1.138", true, false},
		{"codex", "codex", "", "2.1.322", true, false},
		{"agent sdk", "claude", "agentsdk", "2.1.322", true, false},
	} {
		p := &assistantProc{agent: tc.agent, runtime: tc.rt, claudeVersion: tc.v, busy: tc.busy}
		if got := p.interjects(); got != tc.expects {
			t.Errorf("%s: interjects %v, want %v", tc.name, got, tc.expects)
		}
	}
}
