package node

import (
	"os"
	"path/filepath"
	"testing"
)

// A route variable assigned once to a plain literal expands whatever the control flow around it; anything that could
// change it, or a value that could change the route, keeps the call unreadable and the command carded (#16491).
func TestRouteVariableConstantOverTheCommand(t *testing.T) {
	gates := olGate("127.0.0.1:18000")
	const f = "F=7123456789"
	for _, c := range []struct {
		name, cmd string
		route     string // "": the route must stay unread (unknown)
	}{
		{"after cd &&", `cd /tmp && ` + f + `; ex GET "/conversations/$F/messages"`, "/conversations/7123456789/messages"},
		{"before &&", f + ` && ex GET "/conversations/$F/messages"`, "/conversations/7123456789/messages"},
		{"after cd ;", `cd /tmp; ` + f + `; ex GET "/conversations/$F/messages"`, "/conversations/7123456789/messages"},
		{"paginated loop", `cd /tmp && ` + f + `; rm -f s.txt; cur=""; for i in $(seq 1 11); do q='limit=100&tail=1'; ` +
			`[ -n "$cur" ] && q="limit=100&beforePosition=$cur"; ex GET "/conversations/$F/messages?$q" > p.json; ` +
			`jq -r '.items[].id' p.json >> s.txt; cur=$(jq -r '.nextOlderCursor' p.json); done; wc -l s.txt`,
			"/conversations/7123456789/messages"},

		{"reassigned in a loop", f + `; for x in $(seq 1 2); do F=links; ex GET "/$F/open"; done`, ""},
		{"reassigned later", f + `; cd /tmp && ex GET "/conversations/$F/messages"; F=x`, ""},
		{"a slash in the value", `F=links/open; cd /tmp && ex GET "/$F"`, ""},
		{"dots", `F=..; cd /tmp && ex GET "/conversations/$F/$F/links/open"`, ""},
		{"percent-encoded", `F=%2f; cd /tmp && ex GET "/conversations/$F"`, ""},
		{"used before it is assigned", `cd /tmp && ex GET "/conversations/$F/messages"; F=7`, ""},
		{"read", f + `; read F < x.txt; cd /tmp && ex GET "/conversations/$F/messages"`, ""},
		{"export", `export ` + f + `; cd /tmp && ex GET "/conversations/$F/messages"`, ""},
		{"unset", f + `; unset F; cd /tmp && ex GET "/conversations/$F/messages"`, ""},
		{"eval", f + `; eval "$X"; cd /tmp && ex GET "/conversations/$F/messages"`, ""},
		{"printf -v", f + `; printf -v F links; cd /tmp && ex GET "/$F/open"`, ""},
		{"arithmetic", f + `; cd /tmp && : $((F=3)); ex GET "/conversations/$F/messages"`, ""},
		{"default assignment", `cd /tmp; ex GET "/conversations/${F:=links}/messages"`, ""},
		{"after false &&", `false && ` + f + `; cd /tmp; ex GET "/conversations/$F/messages"`, ""},
		{"after cd to a missing directory", `cd /nonexistent-16491 && ` + f + `; cd /tmp; ex GET "/conversations/$F/messages"`, ""},
		{"in a pipeline", `echo | ` + f + `; cd /tmp; ex GET "/conversations/$F/messages"`, ""},
		{"in a subshell", `(` + f + `); cd /tmp; ex GET "/conversations/$F/messages"`, ""},
		{"lowercase name", `cd /tmp && chat=7123; for i in 1 2; do ex GET "/conversations/$chat/messages"; done`, "/conversations/7123/messages"},
		// Names the shell sets itself: cd rewrites PWD / OLDPWD, =~ fills BASH_REMATCH / match, every command sets $_.
		{"PWD", `PWD=x; cd /; ex GET "/links${PWD}open"`, ""},
		{"OLDPWD", `OLDPWD=x; cd /; cd /tmp; ex GET "/links${OLDPWD}open"`, ""},
		{"BASH_REMATCH", `cd /tmp && BASH_REMATCH=x; [[ open =~ open ]]; ex GET "/links/$BASH_REMATCH"`, ""},
		{"zsh match", `cd /tmp && match=x; [[ open =~ open ]]; ex GET "/links/$match"`, ""},
		{"underscore", `cd /tmp && _=x; true open; ex GET "/links/$_"`, ""},
		{"FUNCNAME", "FUNCNAME=x\nfor i in 1; do cd /tmp; ex GET \"/links/open$FUNCNAME\"; done", ""},
		// cd counts as always succeeding only into an absolute directory anyone may enter.
		{"relative cd", `cd /usr; cd lib && ` + f + `; for i in 1; do ex GET "/conversations/$F/messages"; done`, ""},
		{"cd with ..", `cd /tmp/.. && ` + f + `; for i in 1; do ex GET "/conversations/$F/messages"; done`, ""},
	} {
		v := classifyLark(c.cmd, gates, "/tmp", nil)
		if c.route == "" {
			if !v.unknown || len(v.calls) != 0 {
				t.Errorf("%s: route read (unknown=%v, calls=%v), want it unread", c.name, v.unknown, v.calls)
			}
			continue
		}
		if v.unknown || len(v.calls) == 0 || v.calls[0].Path != c.route || larkDecision(olCfg(), v.calls[0]) != "allow" {
			t.Errorf("%s: unknown=%v calls=%v, want an allowed GET %s", c.name, v.unknown, v.calls, c.route)
		}
	}
	// A directory not everyone may enter can fail to cd into (another user's home).
	private := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if v := classifyLark(`cd `+private+` && `+f+`; for i in 1; do ex GET "/conversations/$F/messages"; done`, gates, "/tmp", nil); !v.unknown {
		t.Errorf("cd into a private directory: calls=%v, want the route unread", v.calls)
	}
	// A write through the constant is read, and still confirmed.
	v := classifyLark(`cd /tmp && `+f+`; ex POST "/conversations/$F/messages" '{"text":"hi"}'`, gates, "/tmp", nil)
	if len(v.calls) != 1 || larkDecision(olCfg(), v.calls[0]) != "confirm" {
		t.Errorf("write: calls=%v, want one confirmed POST", v.calls)
	}
}
