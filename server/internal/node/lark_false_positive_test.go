package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestLarkLeadingGlobalOptions(t *testing.T) {
	for _, tc := range []struct {
		name, command, risk, path, actor, deny string
		calls                                  int
		other, write                           bool
	}{
		{"read", `lark-cli --as user wiki spaces get_node --params '{}' 2>&1 | head -c 1500`, "read", "/wiki/spaces/get_node", "以你的身份", "", 1, false, false},
		{"write", `lark-cli --as=user wiki spaces get_node --params '{}' 2>&1 | head -c 1500`, "write", "/wiki/spaces/get_node", "以你的身份", "", 1, false, true},
		{"bot", `lark-cli --as bot im +messages-send --text hi`, "write", "/im/+messages-send", "以机器人身份", "", 1, false, true},
		{"formats", `lark-cli --format=json -q '.data' --output=out wiki spaces get_node`, "read", "/wiki/spaces/get_node", "以你的身份", "", 1, false, false},
		{"unknown option", `lark-cli --weird x wiki spaces get_node`, "read", "", "", "", 0, true, true},
		{"profile", `lark-cli --as user --profile other wiki spaces get_node`, "read", "", "", "deny", 0, false, false},
		{"auth", `lark-cli --as user auth login`, "read", "", "", "deny", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := classifyLark(tc.command, nil, "", func([]string) string { return tc.risk })
			if len(v.calls) != tc.calls || v.other != tc.other || v.cliWrite != tc.write || (v.deny != "") != (tc.deny != "") {
				t.Fatalf("verdict = %+v", v)
			}
			if tc.calls == 1 && (v.calls[0].Path != tc.path || larkCLIActor(v.calls[0].Args) != tc.actor) {
				t.Fatalf("call = %+v, actor = %q", v.calls[0], larkCLIActor(v.calls[0].Args))
			}
		})
	}
}

func TestLarkDefaultRiskLookup(t *testing.T) {
	for _, tc := range []struct{ name, response, want string }{{"read", "Risk: read", "read"}, {"write", "Risk: write", "write"}, {"unknown", "help", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "lark-cli"), []byte("#!/bin/sh\necho '"+tc.response+"'\n"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			n := &Node{}
			risk := n.larkCLIRisk(proto.AssistantLark{})
			if risk == nil {
				t.Fatal("risk lookup is nil")
			}
			if got := risk([]string{"wiki", "spaces", "get_node"}); got != tc.want {
				t.Fatalf("risk = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLarkInertHeredoc(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		cards         int
	}{
		{"cat source file", "cat > /tmp/a.go <<'EOF'\n// lark-cli im +messages-send\nEOF\ngo test ./...", 0},
		{"append source file", "cat >> /tmp/a.go <<'EOF'\n// lark-cli im +messages-send\nEOF\ngo test ./...", 0},
		{"tee source file", "tee /tmp/a.go <<'EOF'\n// lark-cli im +messages-send\nEOF\ngo test ./...", 0},
		{"execute written file", "cat > /tmp/a.sh <<'EOF'\nlark-cli im +messages-send\nEOF\nsh /tmp/a.sh", 1},
		{"execute chmod file", "cat > /tmp/a.sh <<'EOF'\nlark-cli im +messages-send\nEOF\nchmod +x /tmp/a.sh; /tmp/a.sh", 1},
		{"shell stdin", "bash <<'EOF'\nlark-cli im +messages-send\nEOF", 1},
		{"python stdin", "python - <<'EOF'\nlark-cli im +messages-send\nEOF", 1},
		{"unquoted substitution", "cat > /tmp/a.go <<EOF\n$(lark-cli im +messages-send)\nEOF", 1},
	} {
		t.Run(tc.name, func(t *testing.T) { assertAssistantBypassClassification(t, tc.command, tc.cards) })
	}
}

func TestLarkLiteralCommandVariable(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		cards         int
	}{
		{"literal", `A=/usr/bin/ab; timeout 30 $A scroll`, 0},
		{"braced literal", `A=/usr/bin/ab; timeout 30 ${A} scroll`, 0},
		{"substitution", `A=$(which x); $A`, 1},
		{"unassigned", `$A`, 1},
		{"dynamic value", `A=$B; $A`, 1},
		{"lark send", `A=/x/lark-cli; $A im +messages-send --text hi`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) { assertAssistantBypassClassification(t, tc.command, tc.cards) })
	}
}
