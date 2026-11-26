package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/node/larkgateport"
	"github.com/nwverse/persona/server/internal/proto"
)

// Thin core bridge to the Lark module's node-side CLI gate and Feishu message card context (internal/node/larkgateport,
// #13404), together with the generic login-shell PATH and message-route helpers shared with non-Lark command gates.

// larkRisk asks lark-cli for a command's risk level ("" when it cannot say, larkCLIMissing when lark-cli has no such
// command).
type larkRisk = larkgateport.RiskLookup

// larkCLIMissing is the risk of a command lark-cli says it does not have.
const larkCLIMissing = larkgateport.MissingRisk

// larkProfileEnvName is the variable lark-cli takes its profile from when no --profile is given.
const larkProfileEnvName = larkgateport.ProfileEnvName

func larkGate() larkgateport.Gate { return larkgateport.Get(nodeModules()) }

func isLarkCLI(name string) bool {
	g := larkGate()
	return g != nil && g.IsCLI(name)
}

func larkMentionsCLI(text string) bool {
	g := larkGate()
	return g != nil && g.MentionsCLI(text)
}

func larkNamesCLI(text string) bool {
	g := larkGate()
	return g != nil && g.NamesCLI(text)
}

func larkCommandDeny(cmd string) string {
	g := larkGate()
	if g == nil {
		return ""
	}
	return g.CheckCommandDeny(cmd)
}

func larkCLIReads(risk string) bool {
	g := larkGate()
	return g != nil && g.RiskReads(risk)
}

func parseLarkCLI(args []string, risk larkRisk) (c larkCall, deny string, ok bool) {
	c = larkCall{Gate: -1, CLI: true, Method: "CLI", Args: args}
	g := larkGate()
	if g == nil {
		return c, "", false
	}
	c.Method, c.Path, c.Query, c.Body, c.Risk, deny, ok = g.ParseCLI(args, risk)
	return c, deny, ok
}

// cliRead: a lark-cli call that only reads (its own risk level, or a GET through `lark-cli api`).
func (c larkCall) cliRead() bool {
	if c.Method == "CLI" {
		return larkCLIReads(c.Risk)
	}
	return c.Method == "GET" || c.Method == "HEAD"
}

func larkRiskNote(c larkCall) string {
	g := larkGate()
	if g == nil {
		return ""
	}
	return g.RiskNote(c.Method, c.Risk)
}

func larkCLIActor(args []string) string {
	g := larkGate()
	if g == nil {
		return "以你的身份"
	}
	return g.ActorLabel(args)
}

func shellJoin(args []string) string {
	g := larkGate()
	if g == nil {
		return strings.Join(args, " ")
	}
	return g.ShellJoin(args)
}

func flagOn(flags []string, names ...string) bool {
	g := larkGate()
	return g != nil && g.FlagOn(flags, names...)
}

func flagValue(flags []string, name string) string {
	g := larkGate()
	if g == nil {
		return ""
	}
	return g.FlagValue(flags, name)
}

func (n *Node) larkGateSession(g larkgateport.Gate) larkgateport.Session {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.larkGateSess == nil {
		n.larkGateSess = g.NewSession(larkShellOutput)
	}
	return n.larkGateSess
}

func (n *Node) larkCLIRisk(cfg proto.AssistantLark) larkRisk {
	g := larkGate()
	if g == nil {
		return nil
	}
	profile := ""
	if cfg.CLI != nil {
		profile = cfg.CLI.Profile
	}
	sessRisk := n.larkGateSession(g).Risk(cfg)
	return func(words []string) string {
		key := profile + "\x00" + strings.Join(words, " ")
		n.mu.Lock()
		r, ok := n.larkRisks[key]
		n.mu.Unlock()
		if ok {
			return r
		}
		return sessRisk(words)
	}
}

func (n *Node) larkCLIBin(c *proto.LarkCLI) string {
	g := larkGate()
	if g == nil {
		return ""
	}
	return n.larkGateSession(g).Bin(c)
}

// larkCLIWrapperPrefix and larkCLIWrapperSuffix match the comment line in every lark-cli PATH wrapper Persona writes:
// both the current modules/lark/larkgate wrapper and earlier node/larkcligate.go wrappers (manager.json and hub.json).
const (
	larkCLIWrapperPrefix = "# lark-cli as "
	larkCLIWrapperSuffix = " lark.cli says ("
)

// removeStaleLarkCLIWrapper deletes a Persona-written lark-cli wrapper from dir when the lark module is absent or
// switched off, so an ungated lark-cli wrapper cannot remain on the assistant's PATH while unrelated user files stay.
func removeStaleLarkCLIWrapper(dir string) {
	if dir == "" {
		return
	}
	p := filepath.Join(dir, "lark-cli")
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	s := string(b)
	if strings.Contains(s, larkCLIWrapperPrefix) && strings.Contains(s, larkCLIWrapperSuffix) {
		_ = os.Remove(p)
	}
}

func (n *Node) writeLarkCLIWrapper(dir string, c *proto.LarkCLI) {
	g := larkGate()
	if g == nil {
		removeStaleLarkCLIWrapper(dir)
		return
	}
	n.larkGateSession(g).WriteWrapper(dir, c)
}

// larkCardContext is the human context of one gated Lark message write. OK is false when its metadata could not
// be read; larkSummaryWithContext then keeps the old method/path/body summary instead of delaying the card.
type larkCardContext struct {
	OK          bool
	Chat        string
	ReplySender string
	ReplyText   string
	InTopic     bool
}

// larkContextFromCLI reads only the message's own chat and, for a reply, the message it answers. It is a variable
// so tests can substitute a fake binary or reader; no gate decision depends on it.
var larkContextFromCLI = func(ctx context.Context, _ *Node, cfg proto.AssistantLark, c larkCall) larkCardContext {
	g := larkGate()
	if g == nil {
		return larkCardContext{}
	}
	kind, chatID, messageID := larkMessageTarget(c)
	if kind == "" {
		return larkCardContext{}
	}
	chat, sender, replyText, inTopic, ok := g.FetchCardContext(ctx, cfg, kind, chatID, messageID)
	if !ok {
		return larkCardContext{}
	}
	return larkCardContext{OK: true, Chat: chat, ReplySender: sender, ReplyText: replyText, InTopic: inTopic}
}

// larkCardContexts enriches each message write independently, with a short total budget per call. Any other gated
// call, or a failed read, is left to the old summary; nothing here grants or denies a call.
func (n *Node) larkCardContexts(ctx context.Context, cfg proto.AssistantLark, calls []larkCall) []larkCardContext {
	out := make([]larkCardContext, len(calls))
	for i, c := range calls {
		if c.CLI {
			continue
		}
		if kind, _, _ := larkMessageTarget(c); kind == "" {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		out[i] = larkContextFromCLI(cctx, n, cfg, c)
		cancel()
	}
	return out
}

// larkMessageSummary renders a send or reply without its HTTP route or internal JSON fields. ok is false when
// metadata was unavailable or the lark module is absent, in which case the caller keeps the old summary.
func larkMessageSummary(c larkCall, meta larkCardContext, lang i18n.Lang) (string, bool) {
	g := larkGate()
	if g == nil {
		return "", false
	}
	kind, _, _ := larkMessageTarget(c)
	return g.FormatMessageSummary(kind, c.Body, meta.Chat, meta.ReplySender, meta.ReplyText, meta.InTopic, meta.OK, lang)
}

// larkMessageTarget recognizes only the message-send and message-reply routes whose body is user-visible text.
// Shared between card context enrichment and editable gated message approval (larkgate_edit.go).
func larkMessageTarget(c larkCall) (kind, chatID, messageID string) {
	if c.Method != "POST" {
		return "", "", ""
	}
	parts := strings.Split(strings.Trim(c.Path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "conversations" && parts[2] == "messages" {
		if len(parts) == 3 {
			return "send", parts[1], ""
		}
		if len(parts) == 5 && parts[4] == "replies" {
			return "reply", parts[1], parts[3]
		}
	}
	if len(parts) == 3 && parts[0] == "messages" && parts[2] == "replies" {
		return "reply", "", parts[1]
	}
	for i, part := range parts {
		if part == "chats" && i+2 < len(parts) && parts[i+2] == "messages" && i+2 == len(parts)-1 {
			return "send", parts[i+1], ""
		}
		if part == "messages" && i+2 < len(parts) && parts[i+2] == "replies" && i+2 == len(parts)-1 {
			return "reply", "", parts[i+1]
		}
	}
	return "", "", ""
}

// larkShellOutput is what a login shell printed for script, for the gate's asks (where lark-cli is, the shell's PATH),
// which run inside a tool call's hook. Output waits for stdout to close, not for the shell to exit, and a background
// job the user's shell startup leaves running (an update check, a daemon started from ~/.zshrc) inherits stdout: without
// WaitDelay that job would decide how long the call waits, past ctx's timeout. What the shell printed is kept either way.
func larkShellOutput(ctx context.Context, script string) []byte {
	cmd := loginShell(ctx, script)
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	return out
}

// larkLoginPATHRetry: how long a failed ask of the login shell's PATH stands before another try.
const larkLoginPATHRetry = 5 * time.Minute

// larkLoginPATHMark starts the line the login shell prints its PATH on, apart from what the user's shell startup
// prints.
const larkLoginPATHMark = "__PERSONA_LOGIN_PATH__"

// larkLoginPATH is the login shell's PATH directories: the ones the assistant's Bash resolves programs in. Claude Code
// runs each command through the user's shell snapshot (larkgate_env.go), which restores that PATH, and an npm global
// tool and the node it starts live in a version manager's directory that this process's own PATH need not have
// (#12483). Asked at most once per larkLoginPATHRetry; nil when the shell cannot say.
func (n *Node) larkLoginPATH() []string {
	n.mu.Lock()
	dirs, at := n.larkPATH, n.larkPATHAt
	n.mu.Unlock()
	if dirs != nil || time.Since(at) < larkLoginPATHRetry {
		return dirs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	out := larkShellOutput(ctx, `printf '\n`+larkLoginPATHMark+`%s\n' "$PATH"`)
	cancel()
	dirs = parseLoginPATH(out)
	n.mu.Lock()
	n.larkPATHAt = time.Now()
	if dirs != nil {
		n.larkPATH = dirs
	}
	n.mu.Unlock()
	return dirs
}

// parseLoginPATH reads the last marked line of a login shell's output: its PATH directories. Anything else the shell
// printed (a profile's greeting) counts for nothing, and only absolute directories are kept. nil when the line is not
// there, so the ask is tried again later.
func parseLoginPATH(out []byte) []string {
	pathVal, seen := "", false
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), larkLoginPATHMark); ok {
			pathVal, seen = v, true
		}
	}
	if !seen {
		return nil
	}
	dirs := []string{}
	for _, d := range strings.Split(pathVal, ":") {
		if d != "" && filepath.IsAbs(d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}
