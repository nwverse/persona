// Package larkgateport is how the Lark module's node-side CLI gate, action rules and Feishu message card context reach the node
// (docs/architecture/core-and-modules.md "The node's modules", #13404). The node's composition root
// (internal/modules/all/node) provides the module's gate; a build without the lark module (persona_without_lark,
// persona_core_only) provides none, and neither does a node whose hub announced the module switched off
// (moduleswitch.FromFeatures). Generic CommandGate (assistant.gate) enforcement stays in internal/node and is
// unaffected when this port is absent. It is its own package so that the composition root need not import internal/node.
//
//persona:layer library
package larkgateport

import (
	"context"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/moduleswitch"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	BoundaryOrdinary = 0
	BoundaryOutbound = 1
	BoundaryDeletion = 2
)

// MissingRisk is the risk level of a command lark-cli reports it does not have: running it only fails with
// "unknown subcommand", so the gate treats it as a read.
const MissingRisk = "missing"

// ProfileEnvName is the environment variable lark-cli reads as --profile when no --profile flag is given.
const ProfileEnvName = "LARKSUITE_CLI_PROFILE"

// RiskLookup asks lark-cli for a command's risk level ("" when unknown, MissingRisk when absent).
type RiskLookup func(words []string) string

// ShellOutput runs script in the user's login shell and returns what it printed to stdout.
type ShellOutput func(ctx context.Context, script string) []byte

// Gate is the Lark module's node-side lark-cli gate, action rules and Feishu message card context provider.
type Gate interface {
	// IsCLI reports whether name invokes lark-cli.
	IsCLI(name string) bool
	// MentionsCLI reports whether text contains a lark-cli call pattern.
	MentionsCLI(text string) bool
	// NamesCLI reports whether text names lark-cli as a standalone word.
	NamesCLI(text string) bool
	// CheckCommandDeny returns a localized refusal message when cmd attempts to mutate LARKSUITE_CLI_PROFILE
	// alongside lark-cli or read/modify ~/.lark-cli, or "" when allowed to continue classification.
	CheckCommandDeny(cmd string) string
	// ParseCLI parses `lark-cli <args...>` using risk to classify subcommands.
	ParseCLI(args []string, risk RiskLookup) (method, reqPath, query, body, riskLevel, deny string, ok bool)
	// RiskReads reports whether a lark-cli risk level changes nothing on Feishu (read or MissingRisk).
	RiskReads(risk string) bool
	// RiskNote returns the phone-card suffix for high-risk or unknown-risk CLI calls.
	RiskNote(method, risk string) string
	// ActorLabel returns the card actor label ("以机器人身份" vs "以你的身份") for args.
	ActorLabel(args []string) string
	// CLIRead reports whether a lark-cli call only reads.
	CLIRead(method, risk string) bool
	// ClassifyAction matches Feishu/Lark-specific action patterns for method and path.
	ClassifyAction(method, path string) (what string, boundary int, matched bool)
	// CLIBypassSafe reports whether a lark-cli subcommand may bypass phone confirmation.
	CLIBypassSafe(path, risk string, args []string) bool
	// CLIBotActor reports whether args explicitly select --as bot under pflag's last-value rules.
	CLIBotActor(args []string) bool
	// CLISummary renders the phone card text for a lark-cli write.
	CLISummary(method, path, risk string, args []string, what string) string
	// DefaultTitle is the default service title shown on lark-cli permission cards.
	DefaultTitle() string
	// FlagOn reports whether any of names is enabled in pflag-style flags.
	FlagOn(flags []string, names ...string) bool
	// FlagValue returns the last effective value of name in pflag-style flags.
	FlagValue(flags []string, name string) string
	// ShellJoin formats args as a shell-escaped command line for phone approval cards.
	ShellJoin(args []string) string
	// NewSession creates a per-Node stateful lark-cli risk and binary-path cache backed by shell.
	NewSession(shell ShellOutput) Session
	// FetchCardContext reads the message's chat and replied-message metadata via read-only lark-cli calls.
	FetchCardContext(ctx context.Context, cfg proto.AssistantLark, kind, chatID, messageID string) (chat, replySender, replyText string, inTopic, ok bool)
	// FormatMessageSummary renders a human-readable send/reply card summary without internal JSON fields.
	FormatMessageSummary(kind, body, chat, replySender, replyText string, inTopic, ctxOK bool, lang i18n.Lang) (string, bool)
}

// Session holds one Node's cached lark-cli binary paths and --help risk lookups.
type Session interface {
	// Risk returns a RiskLookup cached per (profile, command words).
	Risk(cfg proto.AssistantLark) RiskLookup
	// Bin resolves lark-cli's absolute path (configured, PATH, or login shell).
	Bin(cfg *proto.LarkCLI) string
	// WriteWrapper writes or removes the assistant PATH wrapper for lark-cli in dir.
	WriteWrapper(dir string, cfg *proto.LarkCLI)
	// CachedRiskCount reports how many risk lookups are currently cached.
	CachedRiskCount() int
}

var port = moduleswitch.NewPort[Gate]("node/larkgateport")

// Provide sets the Lark module's node gate under its module id; the node's composition root calls it from init.
func Provide(module string, g Gate) { port.Provide(module, g) }

// Get is the module's node gate, nil when this build has no lark module or s switched it off.
func Get(s moduleswitch.Set) Gate {
	g, _ := port.Get(s)
	return g
}
