package assistant

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// nonOwnerCardTarget resolves who handles a non-six-class card instead of pushing it to the owner:
// the task's supervisor when the session belongs to an open supervised to-do, otherwise the main
// conversation (or the background supervisor when the main conversation itself raised the card).
// An empty target means no conversation is configured. Standalone means the source session has no open to-do.
func (s *Service) nonOwnerCardTarget(ctx context.Context, machine, sessionID string) (target string, standalone bool) {
	cfg := s.settings(ctx)
	if machine != "" && sessionID != "" && s.ensureTodosLoaded(ctx) == nil {
		t, found := s.todoOfSession(ctx, s.canonical(machine), sessionID)
		standalone = !found
		if found && t.Supervision != nil {
			switch t.Supervision.state(t) {
			case reviewAccepted, reviewCancelled:
			default:
				if sup := s.effectiveSupervisor(ctx, cfg, t); sup != "" && sup != sessionID {
					return sup, false
				}
			}
		}
	}
	if cfg.Main != "" && sessionID == cfg.Main && cfg.BackgroundSupervisor != "" && cfg.BackgroundSupervisor != cfg.Main {
		return cfg.BackgroundSupervisor, false
	}
	return cfg.Main, standalone
}

// scheduleFailureTarget resolves who handles a failed scheduled run instead of pushing it to the
// owner: the supervisor of the task's originating session (if any), the originating leader/main
// conversation itself, or the main conversation. Returns ("", false) when none is configured.
func (s *Service) scheduleFailureTarget(ctx context.Context, t Task) (string, bool) {
	cfg := s.settings(ctx)
	if t.SessionID != "" {
		machine := cmp.Or(t.MachineID, cfg.MachineID)
		if machine != "" {
			canon := machine
			if s.devs != nil {
				canon = s.devs.Canonical(machine)
			}
			if td, ok := s.todoOfSession(ctx, canon, t.SessionID); ok && td.Supervision != nil {
				switch td.Supervision.state(td) {
				case reviewAccepted, reviewCancelled:
				default:
					if sup := s.effectiveSupervisor(ctx, cfg, td); sup != "" {
						return sup, true
					}
				}
			}
		}
		if t.SessionID == cfg.Main || slices.Contains(cfg.Leaders, t.SessionID) {
			return t.SessionID, true
		}
	}
	if cfg.Main != "" {
		return cfg.Main, true
	}
	return "", false
}

// handNonOwnerCard intercepts non-six-class interaction cards (agent_permission, agent_question,
// extension_approval) that were not routed by ask_route or delegable, and hands them to the task's
// supervisor or the main conversation instead of pushing them to the owner.
//
// Cards that genuinely belong to one of the owner's six classes — secrets/forms/elicitations,
// outward writes (mail_send, comments, etc.), approvals matching userApproval, and unparsed or
// user-only terminal dialogs (trust folder, delete risk, etc.) — return false so they still reach
// the owner as before. So do the cards of the assistant's own supervising conversations (the main
// conversation, the background supervisor, a leader), as askLadder leaves them (supervisorConversation):
// handed on, such a card was parked where no conversation was woken for it or could hand it up (#15360).
func (s *Service) handNonOwnerCard(ix proto.Interaction) bool {
	src := s.inputCardSource(ix, false)
	if src != cardAgentPermission && src != cardAgentQuestion && src != cardExtensionApproval {
		return false
	}
	if secretOrForm(ix) {
		return false
	}
	if ix.Kind == proto.IxPermission && userApproval(ix) != "" {
		return false
	}
	if ix.Kind == proto.IxScreen && (!supervisedScreen(ix) || userScreen(ix) != "") {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cfg := s.settings(ctx)
	if cfg.supervisorConversation(ix.SessionID) {
		return false
	}
	to, standalone := s.nonOwnerCardTarget(ctx, ix.MachineID, ix.SessionID)
	if to == "" {
		return false
	}
	home := s.canonical(cfg.MachineID)
	standalonePermission := standalone && src == cardAgentPermission && ix.Kind == proto.IxPermission

	s.fleet.mu.Lock()
	p := s.fleet.interactions[ix.ID]
	if p == nil || p.claimed || p.told || !p.ix.CreatedAt.Equal(ix.CreatedAt) {
		s.fleet.mu.Unlock()
		return false
	}
	if p.route != nil {
		s.fleet.mu.Unlock()
		return true
	}
	now := time.Now()
	var standaloneUntil time.Time
	if standalonePermission {
		remaining := p.ix.ExpiresAt.Sub(now)
		if home == "" || p.ix.Private || remaining <= 2*superviseTick {
			p.ix.Delegating = false
			shown := p.ix
			s.fleet.mu.Unlock()
			s.show(shown)
			return true
		}
		standaloneUntil = now.Add(min(askHoldFor, remaining/2))
	}
	p.route = &askRoute{
		machine:         home,
		ladder:          []string{to},
		routedAt:        now,
		since:           now,
		held:            now,
		standaloneUntil: standaloneUntil,
		parked:          !standalonePermission,
		reminders:       askRemindMax,
	}
	p.ix.Delegating = true
	shown := p.ix
	s.fleet.mu.Unlock()
	s.show(shown)

	var b strings.Builder
	fmt.Fprintf(&b, "Non-owner card (%s) routed to you instead of pushing to the user.\n", src)
	fmt.Fprintf(&b, "interaction_id=%s kind=%s", ix.ID, ix.Kind)
	if ix.MachineID != "" {
		fmt.Fprintf(&b, " machine=%s", ix.MachineID)
	}
	if ix.SessionID != "" {
		fmt.Fprintf(&b, " session_id=%s", ix.SessionID)
	}
	if ix.ToolName != "" {
		fmt.Fprintf(&b, " tool=%s", ix.ToolName)
	}
	if ask := clip(oneLine(noticeAsk(ix)), 300); ask != "" {
		fmt.Fprintf(&b, "\n%s", ask)
	}
	msg := relayMsg{
		From:      src,
		FromTitle: cmp.Or(ix.SessionTitle, ix.ToolName, src),
		Tool:      src,
		Text:      b.String(),
		At:        now.UTC(),
	}
	if standalonePermission {
		// The active queue waits for the main conversation to be free and retries under the same message ID.
		// askTick makes the card answerable in the app if it is still waiting at the end of the hold.
		msg.Card = ix.ID
		if s.queueRelay(to, msg) > 0 {
			s.drainAfter(home, to, 0)
			s.publish(ctx)
		}
	} else if _, kept := s.keepRelayLater(to, msg); kept {
		s.publish(ctx)
	}
	s.workEvent(ix.MachineID, ix.SessionID, "ask_routed_nonowner", map[string]any{
		"to":     to,
		"source": src,
		"kind":   ix.Kind,
		"id":     ix.ID,
	})
	return true
}

var errStandalonePermissionStale = errors.New("the main conversation no longer holds this permission")

func (msg relayMsg) standalonePermission() bool {
	return msg.Tool == cardAgentPermission && msg.Card != ""
}

// pendingStandalonePermission binds a relay to the same card and route that queued it.
func (s *Service) pendingStandalonePermission(to string, msg relayMsg, now time.Time) (proto.Interaction, bool) {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	p := s.fleet.interactions[msg.Card]
	if p == nil || p.claimed || p.route == nil || p.route.at() != to || !p.route.routedAt.Equal(msg.At) ||
		!now.Before(p.route.standaloneUntil) || !now.Before(p.ix.ExpiresAt) {
		return proto.Interaction{}, false
	}
	return p.ix, true
}
