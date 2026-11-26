//go:build !persona_without_lark && !persona_core_only

package node

import (
	"context"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/modules/lark/larkgate"
	"github.com/nwverse/persona/server/internal/node/larkgateport"
	"github.com/nwverse/persona/server/internal/proto"
)

// The Lark module on the node: lark-cli command gating and Feishu message card context enrichment (#13404).
func init() {
	larkgateport.Provide("lark", larkGateAdapter{g: larkgate.New()})
}

type larkGateAdapter struct{ g *larkgate.Gate }

func (a larkGateAdapter) IsCLI(name string) bool             { return a.g.IsCLI(name) }
func (a larkGateAdapter) MentionsCLI(text string) bool       { return a.g.MentionsCLI(text) }
func (a larkGateAdapter) NamesCLI(text string) bool          { return a.g.NamesCLI(text) }
func (a larkGateAdapter) CheckCommandDeny(cmd string) string { return a.g.CheckCommandDeny(cmd) }
func (a larkGateAdapter) ParseCLI(args []string, risk larkgateport.RiskLookup) (method, reqPath, query, body, riskLevel, deny string, ok bool) {
	return a.g.ParseCLI(args, risk)
}
func (a larkGateAdapter) RiskReads(risk string) bool          { return a.g.RiskReads(risk) }
func (a larkGateAdapter) RiskNote(method, risk string) string { return a.g.RiskNote(method, risk) }
func (a larkGateAdapter) ActorLabel(args []string) string     { return a.g.ActorLabel(args) }
func (a larkGateAdapter) FlagOn(flags []string, names ...string) bool {
	return a.g.FlagOn(flags, names...)
}
func (a larkGateAdapter) FlagValue(flags []string, name string) string {
	return a.g.FlagValue(flags, name)
}
func (a larkGateAdapter) ShellJoin(args []string) string { return a.g.ShellJoin(args) }
func (a larkGateAdapter) NewSession(shell larkgateport.ShellOutput) larkgateport.Session {
	return larkGateSessionAdapter{s: a.g.NewSession(shell)}
}
func (a larkGateAdapter) FetchCardContext(ctx context.Context, cfg proto.AssistantLark, kind, chatID, messageID string) (chat, replySender, replyText string, inTopic, ok bool) {
	return a.g.FetchCardContext(ctx, cfg, kind, chatID, messageID)
}
func (a larkGateAdapter) FormatMessageSummary(kind, body, chat, replySender, replyText string, inTopic, ctxOK bool, lang i18n.Lang) (string, bool) {
	return a.g.FormatMessageSummary(kind, body, chat, replySender, replyText, inTopic, ctxOK, lang)
}

type larkGateSessionAdapter struct{ s *larkgate.Session }

func (a larkGateSessionAdapter) Risk(cfg proto.AssistantLark) larkgateport.RiskLookup {
	return a.s.Risk(cfg)
}
func (a larkGateSessionAdapter) Bin(cfg *proto.LarkCLI) string { return a.s.Bin(cfg) }
func (a larkGateSessionAdapter) WriteWrapper(dir string, cfg *proto.LarkCLI) {
	a.s.WriteWrapper(dir, cfg)
}
func (a larkGateSessionAdapter) CachedRiskCount() int { return a.s.CachedRiskCount() }
