package nodeclient

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// sessionIDPattern is the regexp ValidSessionID replaced; its answers are the reference.
const sessionIDPattern = `^[0-9a-fA-F-]{8,64}$`

// ValidSessionID answers as the regexp did, around both length limits and for what the class leaves out: other
// letters, white space, a trailing newline, multibyte and invalid UTF-8.
func TestValidSessionIDMatchesTheRegexp(t *testing.T) {
	re := regexp.MustCompile(sessionIDPattern)
	for _, id := range []string{
		"", "0123456", "01234567", "0d000000-0000-4000-8000-000000000001", "ABCDEFab-cdef-0000", "--------",
		strings.Repeat("a", 64), strings.Repeat("a", 65), "0123456g", "01234567\n", " 01234567", "0123 4567",
		"c0de-0123456789", "0123456\u00e9", "01234567\xff", "agent-0123456789", "0d000000-0000-4000-8000-00000000000Z",
	} {
		if got, want := ValidSessionID(id), re.MatchString(id); got != want {
			t.Errorf("ValidSessionID(%q) = %v, the regexp says %v", id, got, want)
		}
	}
}

func FuzzValidSessionID(f *testing.F) {
	re := regexp.MustCompile(sessionIDPattern)
	for _, seed := range []string{"", "01234567", "0d000000-0000-4000-8000-000000000001", strings.Repeat("F", 64), "0123456é"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, id string) {
		if got, want := ValidSessionID(id), re.MatchString(id); got != want {
			t.Fatalf("ValidSessionID(%q) = %v, the regexp says %v", id, got, want)
		}
	})
}

// The gate's words are whole words: "op" is a command or an argument, not the "op" inside "open" or "stop".
func TestContainsWord(t *testing.T) {
	for _, c := range []struct {
		s, word string
		want    bool
	}{
		{"op vault list", "op", true},
		{"sudo -u me op vault list", "op", true},
		{"echo op", "op", true},
		{"op", "op", true},
		{"open x", "op", false},
		{"stop", "op", false},
		{"grep -r op .", "op", true},
		{"/usr/local/bin/op vault", "op", true},
		{"cua-driver call", "cua-driver", true},
		{"xcua-driver", "cua-driver", false},
		{"", "op", false},
	} {
		if got := containsWord(c.s, c.word); got != c.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", c.s, c.word, got, c.want)
		}
	}
}

func TestMentionsGateWord(t *testing.T) {
	input := func(event, tool string, body map[string]any) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"hook_event_name": event, "tool_name": tool, "tool_input": body})
		return b
	}
	bash := func(command string) json.RawMessage {
		return input("PreToolUse", "Bash", map[string]any{"command": command})
	}
	for _, c := range []struct {
		input json.RawMessage
		want  bool
	}{
		{bash("op vault list"), true},
		{bash("cua-driver call click '{}'"), true},
		{bash("ls -la"), false},
		{bash("open README.md"), false},
		{input("PreToolUse", "Read", map[string]any{"file_path": "op"}), false},
		{input("PostToolUse", "Bash", map[string]any{"command": "op"}), false},
	} {
		if got := mentionsGateWord(c.input, "cua-driver,op"); got != c.want {
			t.Errorf("%s: mentionsGateWord = %v, want %v", c.input, got, c.want)
		}
	}
}
