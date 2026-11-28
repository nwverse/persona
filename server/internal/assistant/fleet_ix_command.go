package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// ---- commands waiting for input (#14527, docs/protocol/protocol.md "Commands waiting for input") ----

// A node lists a command an Agent runs that stopped at a prompt (an Antigravity background task asking for a password,
// a token, a sign-in code) as a command-input card in its card list (codex.interactions, node/agyinput.go). The manager
// keeps it as it keeps a Codex card: an answer goes back to the node (codex.answer), which types the line into the
// command and says whether it went in; that error reaches whoever answered, in their language, and the card stays.
// Nothing else makes such a card: reservedCard refuses the tool name on every other path (a hook's interaction.create,
// a runtime's or a daemon's card, an extension's fleet.ask), since the apps show it as Persona's own request for a line.

// commandInputLangWait bounds reading the owner's language for a card's words.
const commandInputLangWait = 2 * time.Second

// commandInputCard is a node's command-input card as the owner reads it: what it asks in their language, the prompt
// as the field's label, the tool's name for people. Nothing the node sent decides what the card may do: its one field
// stays secret and required (proto.IsCommandInputCard checked it).
func commandInputCard(l i18n.Lang, ix proto.Interaction) proto.Interaction {
	var in proto.CommandInput
	_ = json.Unmarshal(ix.ToolInput, &in)
	ix.ToolTitle = i18n.T(l, "command-input.tool")
	if in.Prompt != "" {
		ix.Summary = i18n.T(l, "command-input.summary", i18n.Args{"prompt": in.Prompt})
	} else {
		ix.Summary = i18n.T(l, "command-input.summary_plain")
	}
	label := in.Prompt
	if label == "" {
		label = i18n.T(l, "command-input.field")
	}
	ix.Fields = []proto.Field{{Key: proto.CommandInputField, Label: label, Placeholder: i18n.T(l, "command-input.placeholder"), Secret: true}}
	ix.AlwaysKeys, ix.AlwaysLabel, ix.AlwaysHonored = nil, "", nil
	return ix
}

// commandInputPending is the manager's side of a command-input card: a Codex card's (codexPending), with the node's
// refusals said in the answering app's language.
func (s *Service) commandInputPending(machine string, ix proto.Interaction) *pending {
	p := s.codexPending(machine, ix)
	p.ix.ToolTitle = ix.ToolTitle // codexPending keeps a node's title out; this one is the manager's own
	answer := p.local
	p.local = func(ctx context.Context, r proto.Resolution) error {
		return commandInputError(answer(ctx, r))
	}
	return p
}

// commandInputRefusal is a node's refusal of a command-input answer with the manager's words. Its code stays on the
// wire (the apps act on it: nothing was typed, so answer again, or the line may be in, so look first) and Text reads
// in the answering app's language, as the i18n message it carries.
type commandInputRefusal struct {
	code    string
	message *i18n.Message
}

func (e *commandInputRefusal) Error() string { return e.message.Error() }

// Unwrap keeps the message findable as the *i18n.Message it is (logs, older callers, tests).
func (e *commandInputRefusal) Unwrap() error { return e.message }

// Text is the refusal in l (apierr.localizeFailure reads it).
func (e *commandInputRefusal) Text(l i18n.Lang) string { return e.message.Text(l) }

func commandInputRefused(code, key string) error {
	err := i18n.Err(key)
	if m, ok := err.(*i18n.Message); ok {
		return &commandInputRefusal{code: code, message: m}
	}
	return err
}

// commandInputError is a node's refusal of a command-input answer as the owner reads it (i18n.Message: in the
// request's language), with the node's code kept for the apps. The prompt that moved on keeps its code,
// prompt_changed, which every app knows already.
func commandInputError(err error) error {
	var re *rpc.Error
	if !errors.As(err, &re) {
		return err
	}
	switch re.Code {
	case "command_input_line":
		var d struct {
			Problem string `json:"problem"`
		}
		_ = json.Unmarshal(re.Data, &d)
		switch d.Problem {
		case "empty", "too_long", "not_text", "control":
			return commandInputRefused(re.Code, "command-input.line_"+d.Problem)
		}
		return commandInputRefused(re.Code, "command-input.line_control")
	case "command_input_unreachable":
		return commandInputRefused(re.Code, "command-input.unreachable")
	case "command_input_refused":
		return commandInputRefused(re.Code, "command-input.refused_plain")
	case "command_input_unconfirmed":
		return commandInputRefused(re.Code, "command-input.unconfirmed")
	}
	return err
}

// commandInputLang is the owner's language for the cards of one list, read once and only when the list has such a card.
func (s *Service) commandInputLang(cards []proto.Interaction) i18n.Lang {
	for _, ix := range cards {
		if proto.IsCommandInputCard(ix) {
			ctx, cancel := context.WithTimeout(context.Background(), commandInputLangWait)
			defer cancel()
			return s.lang(ctx)
		}
	}
	return ""
}
