package assistant

import (
	"context"
	"encoding/json"
	"github.com/nwverse/persona/server/internal/compat"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The health space (docs/assistant/health.md, #2691): the assistant's conversations about health run in a directory of their
// own next to the assistant's, so Claude Code's auto memory and the instructions are the space's own. Its
// conversations are listed apart (GET /api/v1/assistant?space=health), and the conversation tools of one space see
// only that space's conversations.

// SpaceHealth is the health space's name (Session.Space, StartParams.Space, ?space=).
const SpaceHealth = identity.SpaceHealth

// healthRel is this manager's health space directory on a machine, relative to its home (node assistant.prepare):
// health for the built-in manager, health-<id> for another (identity.HealthDirName), in the state directory, spelled as
// assistantRel is.
func (s *Service) healthRel() string { return path.Join(compat.StateDir, identity.HealthDirName(s.id)) }

// healthCwd is this manager's health space directory on d, or "" while d has not reported its home.
func (s *Service) healthCwd(d Device) string {
	return stateDirOn(d.Home, d.DataDir, identity.HealthDirName(s.id))
}

// spaceCwd is the directory of space ("" the assistant's own) on d; "" for a space that has none on this version
// (only the health space has one so far), so nothing starts in the assistant's directory in its stead.
func (s *Service) spaceCwd(d Device, space string) string {
	switch space {
	case "":
		return s.assistantCwd(d)
	case SpaceHealth:
		return s.healthCwd(d)
	}
	return ""
}

// healthClaudeMD is the health space's CLAUDE.md for machine here (prompts/assistant/health_claude_md.md).
func (s *Service) healthClaudeMD(ctx context.Context, here string) string {
	hello := s.hello(ctx)
	d := prompts.AssistantHealthClaudeMD{Owner: strings.TrimSpace(hello.Owner), Notes: s.notes(ctx), Language: answerLanguage(hello.Locale)}
	if name := identity.HealthDirName(s.id); name != identity.SpaceHealth {
		d.HealthDir = name // another manager's own space (the built-in's is the template's default)
	}
	devices := s.devices(ctx)
	if i := slices.IndexFunc(devices, func(d Device) bool { return d.ID == here }); i >= 0 {
		if cwd := s.assistantCwd(devices[i]); cwd != "" {
			d.StateDir = path.Dir(cwd)
		}
	}
	return prompts.RenderAssistantHealthClaudeMD(d)
}

// spaceOf is the space of an assistant conversation: its own Space when the hub set it, else its directory's.
func spaceOf(c proto.Session) string {
	if c.Space != "" {
		return c.Space
	}
	return identity.SpaceOf(c.Cwd)
}

// runSpace is the space session c works in for hiding and confirming: its own Space or its directory's, a directory
// inside a space counting as the space (identity.SpaceAt: this only hides, so a subfolder counts).
func runSpace(c proto.Session) string {
	if sp := spaceOf(c); sp != "" {
		return sp
	}
	return identity.SpaceAt(c.Cwd)
}

// What leaves a space (docs/assistant/health.md "Outbound", #3194): the user may share their health with anyone they
// like (a doctor, a coach, family), so nothing is refused for being in a space. What must not happen is it leaving
// without them knowing: for a space whose policy says so, a push or a channel says only that there is news, and a
// hand-off to somewhere outside the space is confirmed on the phone each time. The space is the conversation's (the
// trusted session), never a guess at the text.

// selfPolicy is the policy of the space conversation self runs in (the assistant's own, all off, for one that cannot
// be told: as before, the tools that reach other conversations then reach the assistant's only).
func (s *Service) selfPolicy(ctx context.Context, self string) TrackPolicy {
	space, _ := s.selfSpace(ctx, self)
	return s.trackPolicy(ctx, space)
}

// todoPolicy is the policy of the space a task was opened from (its origin conversation's); all off for none.
func (s *Service) todoPolicy(ctx context.Context, t Todo) TrackPolicy {
	if t.Origin == "" {
		return TrackPolicy{}
	}
	return s.selfPolicy(ctx, t.Origin)
}

// ixSpace is the space interaction ix belongs to: its directory's (a directory inside a space counts,
// identity.SpaceAt: this only hides, so a `cd` that left the hook in a subfolder must not show the card, #2368), else
// its conversation's space. spaceOf, which classifies, stays exact.
func (s *Service) ixSpace(ctx context.Context, ix proto.Interaction) string {
	if ix.Cwd != "" {
		return identity.SpaceAt(ix.Cwd)
	}
	if ix.SessionID != "" && ix.MachineID == s.settings(ctx).MachineID {
		space, _ := s.selfSpace(ctx, ix.SessionID)
		return space
	}
	return ""
}

// ixPolicy is the policy of the space interaction ix belongs to (ixSpace).
func (s *Service) ixPolicy(ctx context.Context, ix proto.Interaction) TrackPolicy {
	return s.trackPolicy(ctx, s.ixSpace(ctx, ix))
}

// spaceNotice is what a push or a channel says for a conversation of a space that redacts: that there is news, not the
// news, and not which space.
func spaceNotice(l i18n.Lang) (title, body string) {
	return i18n.T(l, "assistant-health.notice_title"), i18n.T(l, "assistant-health.notice_body")
}

// confirmOutbound asks the phone before conversation self of a space that confirms what leaves it hands what (said as
// summary) to somewhere outside the space (tool: another agent, a new one): true when the user allows it, at once
// when self's space does not confirm.
func (s *Service) confirmOutbound(ctx context.Context, machine, self, tool, summary string, input json.RawMessage) (bool, error) {
	if !s.selfPolicy(ctx, self).ConfirmOutbound {
		return true, nil
	}
	l := s.lang(ctx)
	now := time.Now().UTC()
	ix := proto.Interaction{ID: "ix_" + randomHex(6), MachineID: machine, SessionID: self, Kind: proto.IxPermission, ToolName: tool,
		Summary: i18n.T(l, "assistant-health.outbound", i18n.Args{"what": summary}), ToolInput: input, SessionTitle: "Persona",
		CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if c, ok := s.session(ctx, machine, self); ok {
		ix.Cwd = c.Cwd
	}
	r, err := s.ask(ctx, ix)
	if err != nil {
		return false, err
	}
	return r.Allow != nil && *r.Allow, nil
}
