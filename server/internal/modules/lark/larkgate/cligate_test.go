package larkgate

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func larkHelp(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "1.0.98-"+name+"-help.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noRiskHelp(t *testing.T) string {
	t.Helper()
	read := larkHelp(t, "im-messages-read_status")
	help := strings.Replace(read, "Risk: read\n", "", 1)
	if help == read {
		t.Fatal("the read_status fixture has no Risk: line")
	}
	return help
}

func TestRiskOfHelp(t *testing.T) {
	get := larkHelp(t, "im-messages-get")
	im := larkHelp(t, "im")
	for _, tc := range []struct{ name, help, words, want string }{
		{"a method the group does not have", get, "im messages get", MissingRisk},
		{"a shortcut the domain does not have", im, "im +chat-serch", MissingRisk},
		{"a real method", larkHelp(t, "im-messages-read_status"), "im messages read_status", "read"},
		{"a real method without a Risk line", noRiskHelp(t), "im messages read_status", ""},
		{"Usage names a shorter command", get, "im messages get om_x", ""},
		{"Usage names a longer command", get, "im messages", ""},
		{"Usage takes an argument", strings.Replace(get, "  lark-cli im messages [flags]", "  lark-cli im messages <message_id> [flags]", 1), "im messages get", ""},
		{"no Usage", strings.Replace(get, "Usage:", "Use:", 1), "im messages get", ""},
		{"no Available Commands", noRiskHelp(t), "im messages read_status om_x", ""},
		{"the word is an Available Command", get, "im messages delete", ""},
		{"no output", "", "im messages get", ""},
	} {
		if got := RiskOf([]byte(tc.help), strings.Fields(tc.words)); got != tc.want {
			t.Errorf("%s (%s): got %q, want %q", tc.name, tc.words, got, tc.want)
		}
	}
}

func fakeLarkCLI(t *testing.T, helps map[string]string, exit int) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + shellQuote(calls) + "\ncase \"$*\" in\n"
	i := 0
	for words, help := range helps {
		file := filepath.Join(dir, "help"+strconv.Itoa(i))
		i++
		if err := os.WriteFile(file, []byte(help), 0o600); err != nil {
			t.Fatal(err)
		}
		script += shellQuote(words+" --help") + ") cat " + shellQuote(file) + " ;;\n"
	}
	script += "esac\nexit " + strconv.Itoa(exit) + "\n"
	bin = filepath.Join(dir, "lark-cli")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func readCalls(t *testing.T, calls string) []string {
	t.Helper()
	b, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestSessionRiskAndWrapper(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\necho \"$@\" >> " + shellQuote(calls) + "\n" +
		"case \"$*\" in *+messages-send*) echo 'Risk: write' ;; *+chat-list*) echo '  Risk: read' ;; *) echo 'no risk here' ;; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := New()
	s := g.NewSession(nil)
	cfg := proto.AssistantLark{CLI: &proto.LarkCLI{Bin: bin, Profile: "work"}}
	risk := s.Risk(cfg)
	for range 2 {
		if r := risk([]string{"im", "+chat-list"}); r != "read" {
			t.Fatalf("chat-list: %q", r)
		}
	}
	if r := risk([]string{"im", "+messages-send"}); r != "write" {
		t.Fatalf("messages-send: %q", r)
	}
	if r := risk([]string{"wiki", "+move"}); r != "" {
		t.Fatalf("no risk line: %q", r)
	}
	gotCalls := readCalls(t, calls)
	if len(gotCalls) != 3 || gotCalls[0] != "--profile work im +chat-list --help" {
		t.Fatalf("lark-cli asked %d times (cached?): %q", len(gotCalls), gotCalls)
	}
	outDir := t.TempDir()
	wrapper := filepath.Join(outDir, "lark-cli")
	s.WriteWrapper(outDir, cfg.CLI)
	w, err := os.ReadFile(wrapper)
	if err != nil || !strings.Contains(string(w), "exec "+bin+" --profile work \"$@\"") {
		t.Fatalf("wrapper: %v\n%s", err, w)
	}
	s.WriteWrapper(outDir, nil)
	if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
		t.Fatalf("wrapper without lark.cli: %v", err)
	}
}

func TestSessionRiskMissingCaching(t *testing.T) {
	helps := map[string]string{"im messages get": larkHelp(t, "im-messages-get")}
	g := New()
	s := g.NewSession(nil)
	bin, calls := fakeLarkCLI(t, helps, 0)
	risk := s.Risk(proto.AssistantLark{CLI: &proto.LarkCLI{Bin: bin}})
	for range 2 {
		if r := risk([]string{"im", "messages", "get"}); r != MissingRisk {
			t.Fatalf("im messages get: %q", r)
		}
	}
	if got := readCalls(t, calls); len(got) != 1 {
		t.Fatalf("lark-cli asked %d times: %q", len(got), got)
	}

	s = g.NewSession(nil)
	bin, calls = fakeLarkCLI(t, helps, 1)
	risk = s.Risk(proto.AssistantLark{CLI: &proto.LarkCLI{Bin: bin}})
	for range 2 {
		if r := risk([]string{"im", "messages", "get"}); r != "" {
			t.Fatalf("a failed help run: %q", r)
		}
	}
	if got := readCalls(t, calls); len(got) != 2 || s.CachedRiskCount() != 0 {
		t.Fatalf("a failed help run is cached: calls %q, count %d", got, s.CachedRiskCount())
	}

	s = g.NewSession(nil)
	risk = s.Risk(proto.AssistantLark{CLI: &proto.LarkCLI{Bin: filepath.Join(t.TempDir(), "lark-cli")}})
	if r := risk([]string{"im", "messages", "get"}); r != "" || s.CachedRiskCount() != 0 {
		t.Fatalf("no lark-cli: %q, count %d", r, s.CachedRiskCount())
	}
}

func TestParseCLIAndDeny(t *testing.T) {
	g := New()
	if deny := g.CheckCommandDeny("LARKSUITE_CLI_PROFILE=other lark-cli im +chat-list"); deny == "" {
		t.Fatal("expected profile env override to be denied")
	}
	if deny := g.CheckCommandDeny(`echo "$LARKSUITE_CLI_PROFILE"; lark-cli im +chat-list`); deny != "" {
		t.Fatalf("read of profile env unexpectedly denied: %s", deny)
	}
	if deny := g.CheckCommandDeny("cat ~/.lark-cli/config.json"); deny == "" {
		t.Fatal("expected ~/.lark-cli access to be denied")
	}
	method, reqPath, _, _, riskLevel, deny, ok := g.ParseCLI([]string{"im", "+chat-list"}, nil)
	if !ok || deny != "" || method != "CLI" || reqPath != "/im/+chat-list" || !g.RiskReads(riskLevel) {
		t.Fatalf("chat-list parse: method=%q path=%q risk=%q deny=%q ok=%v", method, reqPath, riskLevel, deny, ok)
	}
	_, _, _, _, _, deny, ok = g.ParseCLI([]string{"auth", "login"}, nil)
	if ok || deny == "" {
		t.Fatalf("auth login should be denied: ok=%v deny=%q", ok, deny)
	}
	method, reqPath, query, body, _, deny, ok := g.ParseCLI([]string{"api", "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", "--data", `{"text":"hi"}`}, nil)
	if !ok || deny != "" || method != "POST" || reqPath != "/open-apis/im/v1/messages" || query != "receive_id_type=chat_id" || body != `{"text":"hi"}` {
		t.Fatalf("api parse: method=%q path=%q query=%q body=%q deny=%q ok=%v", method, reqPath, query, body, deny, ok)
	}
}

func TestNamedRead(t *testing.T) {
	for _, tc := range []struct {
		words string
		read  bool
	}{
		{"calendar +agenda", true}, {"docs +fetch", true}, {"docs +search", true}, {"drive +search", true},
		{"im +chat-messages-list", true}, {"im +messages-mget", true}, {"im +chat-search", true},
		{"task +get-my-tasks", true}, {"calendar events list", true}, {"im chats get", true},
		{"contact users batch_get", true}, {"im messages search", true},

		{"im +messages-send", false}, {"im +messages-reply", false}, {"im +reactions-create", false},
		{"docs +create", false}, {"docs +update", false}, {"calendar +create", false}, {"calendar +freebusy", false},
		{"drive +download", false}, {"drive +upload", false}, {"im messages create", false},
		{"calendar events delete", false}, {"im chats patch", false}, {"im +search-replace", false},
		{"+agenda", false}, {"calendar", false}, {"calendar +", false}, {"im +messages-get extra", false},
		{"calendar +agenda list", false}, {"im list", false},
	} {
		t.Run(tc.words, func(t *testing.T) {
			if got := namedRead(strings.Fields(tc.words)); got != tc.read {
				t.Fatalf("namedRead(%q) = %v, want %v", tc.words, got, tc.read)
			}
		})
	}
}
