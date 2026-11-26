// Package larkgate implements the Lark module's node-side lark-cli gate and Feishu message card context enrichment
// (docs/plans/plan-lark-cli.md, #13404). It is linked into persona-node only through internal/modules/all/node under
// !persona_without_lark && !persona_core_only and depends only on contracts, leaf libraries and the standard library.
//
//persona:layer application
package larkgate

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// MissingRisk is the risk of a command lark-cli says it does not have (RiskOf): run, it only fails with
// "unknown subcommand", so the gate treats it as a read.
const MissingRisk = "missing"

// ProfileEnvName is the variable lark-cli takes its profile from when no --profile is given (lark-cli 1.0.98).
const ProfileEnvName = "LARKSUITE_CLI_PROFILE"

// Gate provides stateless lark-cli parsing, command deny rules, and Feishu card context enrichment for the node.
type Gate struct{}

// New constructs a Gate.
func New() *Gate { return &Gate{} }

// Session holds one Node's cached lark-cli risk lookups and binary path resolutions.
type Session struct {
	mu    sync.Mutex
	shell func(ctx context.Context, script string) []byte
	risks map[string]string
	paths map[string]string
}

// NewSession constructs a Session that asks shell (the user's login shell) when lark-cli is not on PATH.
func (*Gate) NewSession(shell func(ctx context.Context, script string) []byte) *Session {
	return &Session{shell: shell}
}

var (
	// lark-cli's own commands that only print: help, method schemas, the skills it carries, who is logged in.
	// Stable read commands also work when the manager has not configured CLI risk discovery, or the lookup gets no
	// answer (a wrapper on the login shell's PATH that cannot find the real lark-cli from the node's, #8389). Only an
	// unknown risk falls back here: what lark-cli reports wins. Each is "Risk: read" in lark-cli 1.0.54.
	knownReads = map[string]bool{
		"docs/+fetch": true, "contact/+search-user": true, "im/+chat-messages-list": true, "im/+messages-search": true,
		"im/+messages-mget": true, "im/+threads-messages-list": true, "im/+chat-search": true,
	}
	// readVerbs name what a command does when lark-cli cannot be asked (#11193): a shortcut (`calendar +agenda`,
	// `task +get-my-tasks`, `im +chat-messages-list`) or a generated method (`calendar events list`) named by one of them
	// only reads. A shortcut may carry the verb as its whole name or after its last "-"; only get and list also lead a
	// name. Any other name, a send / reply / create / update / delete included, stays a write.
	readVerbs      = map[string]bool{"fetch": true, "agenda": true, "search": true, "list": true, "get": true, "mget": true}
	leadReadVerbs  = map[string]bool{"get": true, "list": true}
	methodReadVerb = map[string]bool{"list": true, "get": true, "search": true, "mget": true, "batch_get": true}
	// localDownloads save what a message carries (an image, a file) under the current directory and change
	// nothing on Lark, though lark-cli rates them "Risk: write" (lark-cli 1.0.91): /read downloads a group's images
	// and files with them (#11803). lark-cli overwrites an existing file and takes a dotfile name, and without --output
	// the name is the sender's (Content-Disposition), so one is a read only when --output names a plain file in
	// lark-im-resources/ (downloadOutput, the directory --download-resources uses), and only for an ordinary
	// write rating: a high-risk one is not.
	localDownloads = map[string]bool{"im/+messages-resources-download": true}
	downloadOutput = lazyre.New(`^lark-im-resources/[A-Za-z0-9_][A-Za-z0-9._-]{0,199}$`)
	cliReadTop     = map[string]bool{"schema": true, "skills": true, "whoami": true, "doctor": true, "help": true, "completion": true}
	cliAuthRead    = map[string]bool{"status": true, "check": true, "scopes": true, "list": true}
	cliEventRead   = map[string]bool{"list": true, "schema": true, "status": true}
	riskRe         = lazyre.New(`(?m)^\s*Risk:\s*([a-z-]+)`)
	httpMethod     = lazyre.New(`^(GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS)$`)
	// cliRe: a lark-cli call anywhere in a command the gate cannot parse.
	cliRe = lazyre.New("(^|[\\s;&|(\"'`])(\\S*/)?lark-cli(\\s|[\"']|$)")
	// cliNamed: lark-cli as a word of its own anywhere in a command, a variable's value (`L=lark-cli; $L …`)
	// included; what lets a whole-command card say it acts on Lark (wholeCommandTitle).
	cliNamed = lazyre.New(`(^|[^A-Za-z0-9_.-])lark-cli([^A-Za-z0-9_.-]|$)`)
	// profileEnv: a command names lark-cli's profile variable (to set it, unset it or pass it on), which like
	// --profile is the manager's to choose.
	profileEnv = lazyre.New(`(^|[^A-Za-z0-9_])LARKSUITE_CLI_PROFILE([^A-Za-z0-9_]|$)`)
	// profileRead: reading the variable, which cannot change it, so a diagnostic passes: `$LARKSUITE_CLI_PROFILE`,
	// `${LARKSUITE_CLI_PROFILE}` and `printenv LARKSUITE_CLI_PROFILE`. An expansion with an operator
	// (`${LARKSUITE_CLI_PROFILE:=x}`) or any other mention still counts as naming it.
	profileRead = lazyre.New(`\$LARKSUITE_CLI_PROFILE|\$\{LARKSUITE_CLI_PROFILE\}|(^|[\s;&|(])printenv\s+LARKSUITE_CLI_PROFILE`)
)

// IsCLI reports whether name invokes lark-cli.
func (*Gate) IsCLI(name string) bool { return name == "lark-cli" || filepath.Base(name) == "lark-cli" }

// MentionsCLI reports whether text contains a lark-cli call pattern.
func (*Gate) MentionsCLI(text string) bool { return cliRe.MatchString(text) }

// NamesCLI reports whether text names lark-cli as a standalone word.
func (*Gate) NamesCLI(text string) bool { return cliNamed.MatchString(text) }

// CheckCommandDeny refuses attempts to override LARKSUITE_CLI_PROFILE alongside lark-cli or read/modify ~/.lark-cli.
func (*Gate) CheckCommandDeny(cmd string) string {
	if profileEnv.MatchString(profileRead.ReplaceAllString(cmd, "")) && cliRe.MatchString(cmd) {
		return "lark-cli 的配置档由管理者定（manager.json 的 lark.cli.profile），不要自己设或清 LARKSUITE_CLI_PROFILE。"
	}
	if strings.Contains(cmd, ".lark-cli/") || strings.HasSuffix(cmd, ".lark-cli") {
		return "不要读或改 lark-cli 的配置和凭据（~/.lark-cli）：飞书用 lark-cli 命令，它自己带登录。"
	}
	return ""
}

// RiskReads says a lark-cli risk level changes nothing on Feishu: a read, or a command lark-cli does not have.
func (*Gate) RiskReads(risk string) bool { return risk == "read" || risk == MissingRisk }

// ParseCLI reads `lark-cli <words…> [flags]`: the words name the command (`im +messages-send`, `api POST
// /open-apis/…`), the flags are its input. ok is false when the gate cannot tell what the command is.
func (g *Gate) ParseCLI(args []string, risk func([]string) string) (method, reqPath, query, body, riskLevel, deny string, ok bool) {
	method = "CLI"
	var words, flags []string
	for _, f := range args {
		if f == "--profile" || strings.HasPrefix(f, "--profile=") {
			return method, "", "", "", "", "lark-cli 的配置档由管理者定（manager.json 的 lark.cli.profile），不要自己传 --profile。", false
		}
	}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		name, _, inline := strings.Cut(args[i], "=")
		if !strings.Contains(" --as --format -q --jq -o --output ", " "+name+" ") {
			break
		}
		flags = append(flags, args[i])
		i++
		if !inline {
			if i == len(args) {
				return method, "", "", "", "", "", false
			}
			flags = append(flags, args[i])
			i++
		}
	}
	for ; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, args[i:]...)
			break
		}
		words = append(words, a)
	}
	if strings.Contains(strings.Join(words, " "), "$") {
		return method, "", "", "", "", "", false // a variable names the command: its risk is unknown
	}
	reqPath = "/" + strings.Join(words, "/")
	switch {
	case len(words) == 0:
		if g.FlagOn(flags, "-h", "--help", "-v", "--version") {
			return method, reqPath, "", "", "read", "", true
		}
		return method, reqPath, "", "", "", "", false
	case g.FlagOn(flags, "-h", "--help"):
		riskLevel = "read"
	case words[0] == "auth":
		if len(words) > 1 && cliAuthRead[words[1]] {
			return method, reqPath, "", "", "read", "", true
		}
		return method, reqPath, "", "", "", "lark-cli 的登录、退出和授权由他自己在 hub 所在的机器上做（lark-cli auth login），不要替他运行。", false
	case words[0] == "config" || words[0] == "profile" || words[0] == "update":
		return method, reqPath, "", "", "", "不要改 lark-cli 的配置、配置档或版本：这些由他自己做。", false
	case g.FlagOn(flags, "--dry-run"):
		riskLevel = "read" // prints the request, runs nothing
	case cliReadTop[words[0]]:
		riskLevel = "read"
	case words[0] == "event" && len(words) > 1 && cliEventRead[words[1]]:
		riskLevel = "read"
	case words[0] == "api":
		if len(words) < 3 {
			return method, reqPath, "", "", "", "", false
		}
		method = strings.ToUpper(words[1])
		endpoint, err := url.ParseRequestURI(words[2])
		if err != nil || !httpMethod.MatchString(method) || !strings.HasPrefix(words[2], "/") || endpoint.Host != "" {
			return method, reqPath, "", "", "", "", false
		}
		reqPath = path.Clean(endpoint.Path)
		query, body = endpoint.RawQuery, g.FlagValue(flags, "--data")
		if params := g.FlagValue(flags, "--params"); params != "" {
			query = params
		}
	default:
		if risk != nil {
			riskLevel = risk(words)
		}
		if riskLevel == "" && (knownReads[strings.Join(words, "/")] || namedRead(words)) {
			riskLevel = "read"
		}
		if (riskLevel == "" || riskLevel == "write") && localDownloads[strings.Join(words, "/")] && safeDownload(flags) {
			riskLevel = "read"
		}
	}
	return method, reqPath, query, body, riskLevel, "", true
}

// namedRead says a lark-cli command's name marks it a read (readVerbs), for when lark-cli gives no risk
// level: `<service> +<shortcut>` or `<service> <resource…> <method>`. Only the fallback: what lark-cli reports wins.
func namedRead(words []string) bool {
	if len(words) < 2 {
		return false
	}
	last := words[len(words)-1]
	if name, shortcut := strings.CutPrefix(last, "+"); shortcut {
		if len(words) != 2 || name == "" {
			return false
		}
		parts := strings.Split(name, "-")
		return readVerbs[parts[len(parts)-1]] || len(parts) > 1 && leadReadVerbs[parts[0]]
	}
	for _, w := range words {
		if strings.HasPrefix(w, "+") {
			return false
		}
	}
	return len(words) >= 3 && methodReadVerb[last]
}

// downloadFlags are the flags of `im +messages-resources-download` (lark-cli 1.0.91 --help), true for those
// that take a value. The command has no -o: lark-cli rejects it today, but a later version may add one, so a word the
// list does not name keeps the download a write.
var downloadFlags = map[string]bool{
	"--as": true, "--file-key": true, "--format": true, "-q": true, "--jq": true, "--message-id": true, "--output": true,
	"--type": true, "--dry-run": false, "--json": false,
}

// safeDownload: the download can only write a plain file in lark-im-resources/, nowhere else and no dotfile.
// It has to be proved from the words alone, read in order: every one is a flag the command is known to have (no -o in
// any spelling, no shorthand cluster, no "--", no stray argument), there is an --output, and every --output (not
// only the last, which pflag keeps) names such a file.
func safeDownload(flags []string) bool {
	outputs := 0
	for i := 0; i < len(flags); i++ {
		key, val, inline := strings.Cut(flags[i], "=")
		takesValue, known := downloadFlags[key]
		if !known {
			return false
		}
		if !takesValue {
			continue
		}
		if !inline {
			if i+1 == len(flags) {
				return false
			}
			i++
			val = flags[i]
		}
		if key == "--output" {
			if val == "" || strings.Contains(val, "..") || !downloadOutput.MatchString(val) {
				return false
			}
			outputs++
		}
	}
	return outputs > 0
}

// RiskNote marks the card when lark-cli calls a command high-risk, or could not say how risky it is.
func (*Gate) RiskNote(method, risk string) string {
	switch {
	case method != "CLI":
		return ""
	case risk == "high-risk-write":
		return "（lark-cli 标为高风险）"
	case risk == "":
		return "（lark-cli 没给出风险等级）"
	}
	return ""
}

// ActorLabel is who a lark-cli write acts as, for the card: the app's bot with --as bot, the user otherwise (the
// app usually has no send_as_user scope, so the assistant sends messages as the bot).
func (g *Gate) ActorLabel(args []string) string {
	if g.FlagValue(args, "--as") == "bot" {
		return "以机器人身份"
	}
	return "以你的身份"
}

// ShellJoin is the arguments as the user would type them, for the card: quoted only where a shell needs it.
func (*Gate) ShellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n'\"\\$`|&;<>()*?[]{}#~!") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// FlagOn says a switch (--help, --dry-run…) is on. lark-cli's flags are pflag's: "--dry-run=false" turns it off,
// and a flag written without "=" may take the next word as its value ("--text --dry-run" sends the text
// "--dry-run"), so a bare switch counts only when the word before it is not a flag that may be waiting for one.
func (*Gate) FlagOn(flags []string, names ...string) bool {
	on := false
	for i := 0; i < len(flags); i++ {
		f := flags[i]
		if f == "--" {
			break
		}
		if !strings.HasPrefix(f, "-") {
			continue
		}
		key, val, inline := strings.Cut(f, "=")
		matched := false
		for _, name := range names {
			if key == name {
				matched = true
				break
			}
		}
		if matched {
			on = true
			if inline {
				value, err := strconv.ParseBool(val)
				on = err == nil && value
			}
			continue
		}
		if !inline && !boolFlags[key] {
			if i+1 < len(flags) && strings.HasPrefix(flags[i+1], "-") {
				return false
			}
			i++ // a value flag consumes even a dash-prefixed argument
		}
	}
	return on
}

var boolFlags = map[string]bool{
	"--help": true, "-h": true, "--version": true, "-v": true, "--dry-run": true,
	"--reply-in-thread": true,
}

// FlagValue follows pflag's last-value semantics, including --name=value. A value flag consumes
// its next argument even when that argument starts with a dash; -- ends flag parsing.
func (*Gate) FlagValue(flags []string, name string) string {
	value := ""
	for i := 0; i < len(flags); i++ {
		f := flags[i]
		if f == "--" {
			break
		}
		if !strings.HasPrefix(f, "-") {
			continue
		}
		key, val, inline := strings.Cut(f, "=")
		if !inline {
			if boolFlags[key] {
				continue
			}
			// An unknown flag may be boolean or consume the next flag as text. Neither
			// interpretation may retain an earlier bot identity as a bypass exemption.
			if key != name && (i+1 >= len(flags) || strings.HasPrefix(flags[i+1], "-")) {
				return ""
			}
			if i+1 >= len(flags) {
				if key == name {
					value = ""
				}
				break
			}
			i++
			val = flags[i]
		}
		if key == name {
			value = val
		}
	}
	return value
}

// Risk asks lark-cli for the risk level of each command it is given, once per command.
func (s *Session) Risk(cfg proto.AssistantLark) func([]string) string {
	cli := cfg.CLI
	if cli == nil {
		cli = &proto.LarkCLI{}
	}
	return func(words []string) string {
		key := cli.Profile + "\x00" + strings.Join(words, " ")
		s.mu.Lock()
		r, ok := s.risks[key]
		s.mu.Unlock()
		if ok {
			return r
		}
		bin := s.Bin(cli)
		if bin == "" {
			return ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, append(append(cliProfileArgs(cli), words...), "--help")...)
		cmd.Env = cliEnv(bin)
		out, err := cmd.CombinedOutput()
		r = RiskOf(out, words)
		if r == "" || (r == MissingRisk && err != nil) {
			return "" // not cached: lark-cli may be updating, or missing for now; only a clean run says "no such command"
		}
		s.mu.Lock()
		if s.risks == nil {
			s.risks = map[string]string{}
		}
		s.risks[key] = r
		s.mu.Unlock()
		return r
	}
}

// CachedRiskCount returns how many risk lookups have been cached in s.
func (s *Session) CachedRiskCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.risks)
}

// RiskOf reads lark-cli's `<words…> --help`: the level its Risk: line gives; MissingRisk when the help is the
// group's above the command instead, which is what lark-cli prints for a command it does not have (lark-cli 1.0.98:
// `lark-cli im messages get --help` prints `im messages`, whose methods do not include get); "" otherwise. It is the
// group's when every Usage line names exactly lark-cli and the words without the last, the help lists the group's
// Available Commands, and the last word is not one of them. A command that exists but has no Risk: line stays "".
func RiskOf(out []byte, words []string) string {
	if m := riskRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	if len(words) == 0 {
		return ""
	}
	parent := strings.Join(append([]string{"lark-cli"}, words[:len(words)-1]...), " ")
	usage := helpSection(out, "Usage:")
	if len(usage) == 0 {
		return ""
	}
	for _, line := range usage {
		// cobra's two forms: the group's own use line and its `<path> [command]` line.
		if strings.TrimSuffix(strings.TrimSuffix(strings.Join(strings.Fields(line), " "), " [command]"), " [flags]") != parent {
			return ""
		}
	}
	commands := helpSection(out, "Available Commands:")
	if len(commands) == 0 {
		return ""
	}
	for _, line := range commands {
		if strings.Fields(line)[0] == words[len(words)-1] {
			return ""
		}
	}
	return MissingRisk
}

// helpSection is the indented lines under a heading of cobra's help (`Usage:`, `Available Commands:`), up to the
// first blank or unindented line; nil without the heading.
func helpSection(out []byte, heading string) []string {
	var lines []string
	in := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, " \r")
		switch {
		case !in:
			in = line == heading
		case line == "" || !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			return lines
		default:
			lines = append(lines, line)
		}
	}
	return lines
}

// Bin is lark-cli's absolute path: lark.cli.bin as given, else looked up in a login shell (an npm install
// usually lives in a version manager's directory that only the user's shell puts on PATH); "" when it is not there.
func (s *Session) Bin(c *proto.LarkCLI) string {
	bin := ""
	if c != nil {
		bin = c.Bin
	}
	if bin == "" {
		bin = "lark-cli"
	}
	if filepath.IsAbs(bin) {
		return bin
	}
	s.mu.Lock()
	p, ok := s.paths[bin]
	s.mu.Unlock()
	if ok {
		return p
	}
	if p, _ = exec.LookPath(bin); p == "" && s.shell != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		out := s.shell(ctx, "command -v "+shellQuote(bin))
		cancel()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if last := strings.TrimSpace(lines[len(lines)-1]); filepath.IsAbs(last) {
			p = last
		}
	}
	if p != "" {
		s.mu.Lock()
		if s.paths == nil {
			s.paths = map[string]string{}
		}
		s.paths[bin] = p
		s.mu.Unlock()
	}
	return p
}

// WriteWrapper puts `lark-cli` on the assistant's PATH (dir): the configured binary with the configured
// profile. Without lark-cli in the config, or when it cannot be found, there is none.
func (s *Session) WriteWrapper(dir string, c *proto.LarkCLI) {
	wrapperPath := filepath.Join(dir, "lark-cli")
	bin := ""
	if c != nil {
		bin = s.Bin(c)
	}
	if bin == "" || bin == wrapperPath {
		_ = os.Remove(wrapperPath)
		return
	}
	args := ""
	for _, a := range cliProfileArgs(c) {
		args += " " + shellQuote(a)
	}
	wrapper := "#!/bin/sh\n# lark-cli as manager.json lark.cli says (modules/lark/larkgate), on the assistant's PATH.\nPATH=" +
		shellQuote(filepath.Dir(bin)) + ":\"$PATH\" exec " + shellQuote(bin) + args + " \"$@\"\n"
	_ = os.WriteFile(wrapperPath, []byte(wrapper), 0o755)
}

func cliProfileArgs(c *proto.LarkCLI) []string {
	if c == nil || c.Profile == "" {
		return nil
	}
	return []string{"--profile", c.Profile}
}

// cliEnv: lark-cli's own directory first on PATH, for an npm shim that runs `env node`.
func cliEnv(bin string) []string {
	return append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

var shellQuoteEscapes = strings.NewReplacer(`'`, `'\''`, `\`, `'\\'`)

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) < 0 {
		return s
	}
	return "'" + shellQuoteEscapes.Replace(s) + "'"
}
