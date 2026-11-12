package node

import (
	"os"
	"os/exec"
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
		{"tee append", "tee -a /tmp/a.go <<'EOF'\n// lark-cli im +messages-send\nEOF\ngo test ./...", 0},
		{"assignment heredoc substitution", "cat > /tmp/a.go <<'EOF'\n// lark-cli\nEOF\nA=1 <<EOF\n$(lark-cli im +messages-send)\nEOF", 1},
		{"redirect heredoc substitution", "cat > /tmp/a.go <<'EOF'\n// lark-cli\nEOF\n<<EOF\n$(lark-cli im +messages-send)\nEOF", 1},
		{"execute makefile", "cat > /tmp/Makefile <<'EOF'\nsend:\n\tlark-cli im +messages-send\nEOF\nmake -f /tmp/Makefile send", 1},
		{"execute go program", "cat > /tmp/a.go <<'EOF'\n// lark-cli im +messages-send\nEOF\ngo run /tmp/a.go", 1},
		{"custom cat executable", "/tmp/cat > /tmp/a.go <<'EOF'\n// lark-cli im +messages-send\nEOF", 1},
		{"PATH changes cat executable", "PATH=/tmp/tools:$PATH\ncat > /tmp/a.go <<'EOF'\nlark-cli im +messages-send --as user --text hi\nEOF", 1},
		{"test executable override", "cat > /tmp/a.go <<'EOF'\n// lark-cli im +messages-send\nEOF\ngo test -exec sh ./...", 1},
		{"test executes generated Go code", "cat > /tmp/a_test.go <<'EOF'\npackage x\nimport (\"testing\"; \"os/exec\")\nfunc TestSend(t *testing.T) { exec.Command(\"lark-cli\", \"im\", \"+messages-send\").Run() }\nEOF\ngo test ./...", 1},

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
		{"literal after harmless command", `A=/usr/bin/ab; echo ok; timeout 30 $A scroll`, 0},
		{"literal after external command", `A=/usr/bin/ab; ls /tmp; timeout 30 $A scroll`, 0},
		{"repeated literal command", `A=/usr/bin/ab; $A status; timeout 30 $A scroll`, 0},
		{"repeated wrapped literal command", `A=/usr/bin/ab; timeout 30 $A status; timeout 30 $A scroll`, 0},
		{"unset after literal command", `A=/usr/bin/ab; $A status; unset A; timeout 30 $A scroll`, 1},
		{"dynamic argument after literal command", `A=/usr/bin/ab; $A "$ARG"; timeout 30 $A scroll`, 1},

		{"dynamic argument can assign", `A=/usr/bin/ab; echo ${A:=/x/lark-cli}; timeout 30 $A scroll`, 1},
		{"unset value", `A=/usr/bin/ab; unset A; timeout 30 $A scroll`, 1},
		{"substitution", `A=$(which x); $A`, 1},
		{"unassigned", `$A`, 1},
		{"dynamic value", `A=$B; $A`, 1},
		{"IFS splits literal command", `A=lark-cli/im; IFS=/; $A +messages-send --as user --text hi`, 1},
		{"arithmetic changes IFS", `A=lark-cli0im; B=$((IFS=0)); $A +messages-send --as user --text hi`, 1},
		{"wrapper expansion changes IFS", `A=lark-cli0im; timeout "$((IFS=0))" $A +messages-send --as user --text hi`, 1},
		{"nameref changes command", "builtin declare -n A=B\nA=/usr/bin/ab\nB=lark-cli\n$A im +messages-send --as user --text hi", 1},
		{"lark send", `A=/x/lark-cli; $A im +messages-send --text hi`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) { assertAssistantBypassClassification(t, tc.command, tc.cards) })
	}
}

func TestLarkLiteralGetWithShellVariables(t *testing.T) {
	for _, tc := range []struct {
		name, command string
	}{
		{"single", `ex GET "/documents/read?url=https://example.test/docx/a"`},
		{"for loop", `for x in a b; do ex GET "/documents/read?url=$x" > /tmp/o_$x.json; done`},
		{"while loop", `x=a; while [ "$x" = a ]; do ex GET "/documents/read?url=$x" > /tmp/o_$x.json; x=b; done`},
		{"incident", `for x in "docx a b" "wiki c d"; do set -- $x; ex GET "/documents/read?url=https://example.larkoffice.com/$2/$1" > /tmp/d_$3.json; python3 -I x.py; echo "$3 $(jq -r '.data.title' /tmp/d_$3.json | tr '\n' ' ') $(wc -c < /tmp/d_$3.json)"; done`},
		{"output substitutions", `ex GET /health; echo "$(jq -r '.data.title' /tmp/o.json | tr '\n' ' ') $(wc -c < /tmp/o.json)"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := classifyLark(tc.command, olGate(""), t.TempDir(), nil)
			if v.deny != "" || v.unparsed || v.unknown || v.unsure || len(v.calls) != 1 {
				t.Fatalf("verdict = %+v", v)
			}
			call := v.calls[0]
			if call.Method != "GET" || larkDecision(olCfg(), call) != "allow" || !larkBypassSafe(call) {
				t.Fatalf("call = %+v, default decision = %q, bypass safe = %v", call, larkDecision(olCfg(), call), larkBypassSafe(call))
			}
			assertAssistantBypassClassification(t, tc.command, 0)
		})
	}
}

func TestLarkLoopWritesStayBehindApproval(t *testing.T) {
	for _, tc := range []struct {
		name, command string
	}{
		{"post", `for x in a b; do ex POST "/documents/$x" '{}'; done`},
		{"put", `for x in a b; do ex PUT "/documents/$x" '{}'; done`},
		{"patch", `for x in a b; do ex PATCH "/documents/$x" '{}'; done`},
		{"delete", `for x in a b; do ex DELETE "/documents/$x"; done`},
		{"variable read method", `for method in GET; do ex "$method" /health; done`},
		{"variable write method", `for method in POST; do ex "$method" /documents '{}'; done`},
		{"variable command", `for x in a b; do $CLI GET "/documents/$x"; done`},
		{"dynamic route can hide deletion", `for action in delete; do ex GET "/documents/$action"; done`},
		{"loop shadows previous route", "action=read\nfor action in delete; do ex GET \"/documents/$action\"; done"},
		{"conditional assignment keeps previous route", `action=delete; false && action=read; ex GET "/documents/$action"`},
		{"binary builtin keeps previous route", "action=read\ntrue && printf -v action delete\nex GET \"/documents/$action\""},
		{"getopts keeps previous route", "action=read\ngetopts a action -a\nex GET \"/documents/$action\""},
		{"redirect arithmetic keeps previous route", "action=1\n: > \"/tmp/$((action=2))\"\nex GET \"/documents/$action\""},
		{"append assignment keeps previous route", "action=dele\naction+=te\nex GET \"/documents/$action\""},
		{"array assignment keeps previous route", "action=(read delete)\nex GET \"/documents/${action[1]}\""},
		{"indexed assignment keeps previous route", "action=read\naction[0]=delete\nex GET \"/documents/$action\""},
		{"declaration changes route case", "declare -l action=INITIAL\naction=DELETE\nex GET \"/documents/$action\""},
		{"unparsed redirect gate call", `{ ex</dev/null POST /documents; }`},
		{"unquoted route becomes delete", `for route in '/health -X DELETE'; do ex GET $route; done`},
		{"unquoted query becomes delete", `for query in 'q=a -X DELETE'; do ex GET /health --url-query $query; done`},
		{"command substitution becomes delete", `ex GET /health --url-query $(printf 'q=a -X DELETE')`},
		{"quoted positional expansion becomes delete", `set -- /health -X DELETE; ex GET "$@"`},
		{"brace expansion becomes delete", `for route in ''; do ex GET "$route"{/health,-X,DELETE}; done`},
		{"quoted brace expansion becomes delete", `for route in ''; do ex GET "$route"{/health,"-X",DELETE}; done`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := classifyLark(tc.command, olGate(""), t.TempDir(), nil)
			if v.deny == "" && !v.unknown {
				for _, call := range v.calls {
					if larkDecision(olCfg(), call) == "allow" {
						t.Fatalf("write unexpectedly allowed: %+v", call)
					}
				}
			}
			assertAssistantBypassClassification(t, tc.command, 1)
		})
	}
}

func TestLarkHeredocUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := exec.Command("mkfifo", fifo).Run(); err != nil {
		t.Logf("FIFO case unavailable: %v", err)
		fifo = ""
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("/dev/null", link); err != nil {
		t.Logf("Symlink case unavailable: %v", err)
		link = ""
	}
	for _, tc := range []struct{ name, command string }{
		{"device", "cat > /dev/null <<'EOF'\n lark-cli im +messages-send\nEOF"},
		{"proc fd", "cat > /proc/self/fd/1 <<'EOF'\n lark-cli im +messages-send\nEOF"},
		{"tee fd", "tee /dev/fd/1 <<'EOF'\n lark-cli im +messages-send\nEOF"},
		{"multiple output redirections", "cat > /tmp/a.go > /dev/fd/1 <<'EOF'\n lark-cli im +messages-send\nEOF"},
		{"tee stdout redirect", "tee /tmp/a.go > /dev/fd/1 <<'EOF'\n lark-cli im +messages-send\nEOF"},

		{"fifo", "cat > " + fifo + " <<'EOF'\n lark-cli im +messages-send\nEOF"},
		{"symbolic link", "cat > " + link + " <<'EOF'\n lark-cli im +messages-send\nEOF"},
		{"long exec option", "cat > /tmp/a.go <<'EOF'\n// lark-cli im +messages-send\nEOF\ngo test --exec sh ./..."},
		{"long toolexec option", "cat > /tmp/a.go <<'EOF'\n// lark-cli im +messages-send\nEOF\ngo test --toolexec sh ./..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "fifo" && fifo == "" || tc.name == "symbolic link" && link == "" {
				t.Skip("platform fixture unavailable")
			}
			assertAssistantBypassClassification(t, tc.command, 1)
		})
	}
}
