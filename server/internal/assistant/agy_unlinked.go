package assistant

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A task's agy Agent in a herdr pane is read through the agy conversation the pane runs, which only herdr's Antigravity
// integration names (node/agypane.go). Without that report the pane's history is one placeholder notice, while the
// computer lists the same conversation as a runtime row of its own (agy-<conversation id>): the task's judgment had no
// reply to go by and told its supervisor only "couldn't read the reply", and the supervisor had to find that row by
// hand (#13456). agyUnlinked says which pane it is and which rows may hold its reply; it links and reads none of them.

// agyPaneNoticeID is the id of the placeholder an agy pane's history holds while its conversation is not known
// (node/agypane.go agyNotice).
const agyPaneNoticeID = "agy#0"

// agyUnlinkedMax: the candidate conversations named for one pane.
const agyUnlinkedMax = 3

// agyUnlinked is the supervisor's localized word on t's live agy Agents whose herdr panes are not linked to their
// conversations, with the conversations that may hold their replies; "" when t has none.
func (s *Service) agyUnlinked(ctx context.Context, t Todo) string {
	panes := s.agyUnlinkedPanes(ctx, t)
	if len(panes) == 0 {
		return ""
	}
	return strings.TrimSpace(prompts.RenderAgyUnlinked(prompts.AgyUnlinked{Language: answerLanguage(s.hello(ctx).Locale), Panes: panes}))
}

// agyUnlinkedPanes are t's live agents (the most recently linked first) that run agy in a herdr pane whose history is
// still only the placeholder notice, each with its candidates: agy runtime rows on its computer in its directory,
// active after it was linked and on no other open to-do, newest first.
func (s *Service) agyUnlinkedPanes(ctx context.Context, t Todo) []prompts.AgyUnlinkedPane {
	var out []prompts.AgyUnlinkedPane
	for i := len(t.Agents) - 1; i >= 0; i-- {
		a := t.Agents[i]
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		pane, ok := s.session(ctx, a.MachineID, a.SessionID)
		if !ok || pane.Agent != "agy" || pane.PaneID == "" || !s.agyPaneUnlinked(ctx, a.MachineID, a.SessionID) {
			continue
		}
		name := a.MachineID
		if m, ok := s.device(ctx, a.MachineID); ok && m.Name != "" {
			name = m.Name
		}
		p := prompts.AgyUnlinkedPane{Machine: name, Session: pane.SessionID}
		for _, c := range s.agyCandidates(ctx, t, a, pane) {
			p.Candidates = append(p.Candidates, prompts.AgyUnlinkedCandidate{Machine: name, Session: c.SessionID, Title: clip(oneLine(c.Title), 60)})
		}
		out = append(out, p)
	}
	return out
}

// agyPaneUnlinked: the agy pane session sid on machine has nothing but the placeholder notice to read.
func (s *Service) agyPaneUnlinked(ctx context.Context, machine, sid string) bool {
	items, ok := s.lastTurnItems(ctx, machine, sid)
	if !ok || len(items) == 0 {
		return false
	}
	for _, it := range items {
		if it.Kind != proto.KindNotice || it.Role != "system" {
			return false
		}
	}
	return slices.ContainsFunc(items, func(it proto.Item) bool { return it.ID == agyPaneNoticeID })
}

// agyCandidates are the agy conversations on a's computer that may be the one pane runs: runtime rows (not a pane) in
// its directory, active after a was linked to t, on no other open to-do, newest first, at most agyUnlinkedMax.
func (s *Service) agyCandidates(ctx context.Context, t Todo, a TodoAgent, pane proto.Session) []proto.Session {
	list, err := s.sessions(ctx, a.MachineID)
	if err != nil {
		return nil
	}
	taken, ok := s.openTodoSessionIDs(ctx, a.MachineID)
	if !ok {
		return nil // an unreadable to-do book names nothing rather than another task's conversation
	}
	for _, b := range t.Agents {
		if b.MachineID == a.MachineID {
			delete(taken, b.SessionID) // this to-do's own
		}
	}
	dirs := slices.DeleteFunc([]string{pane.Cwd, pane.WorkspaceCwd, a.Cwd}, func(d string) bool { return d == "" })
	var rows []proto.Session
	for _, c := range list {
		if c.SessionID == pane.SessionID || c.Agent != "agy" || c.PaneID != "" || c.SourceKind != proto.SourceRuntime ||
			c.Archived || taken[c.SessionID] || !c.UpdatedAt.After(a.LinkedAt) ||
			!slices.Contains(dirs, c.Cwd) && !slices.Contains(dirs, c.WorkspaceCwd) {
			continue
		}
		rows = append(rows, c)
	}
	slices.SortStableFunc(rows, func(x, y proto.Session) int {
		return cmp.Or(y.UpdatedAt.Compare(x.UpdatedAt), strings.Compare(x.SessionID, y.SessionID))
	})
	return rows[:min(len(rows), agyUnlinkedMax)]
}

// agyUnlinkedLog is panes for a log line: agy_unlinked, each pane and its candidates.
func agyUnlinkedLog(panes []prompts.AgyUnlinkedPane) string {
	parts := make([]string, 0, len(panes))
	for _, p := range panes {
		ids := make([]string, 0, len(p.Candidates))
		for _, c := range p.Candidates {
			ids = append(ids, c.Machine+"/"+c.Session)
		}
		parts = append(parts, fmt.Sprintf("%s/%s (candidates: %s)", p.Machine, p.Session, cmp.Or(strings.Join(ids, ", "), "none")))
	}
	return "agy_unlinked " + strings.Join(parts, "; ")
}
