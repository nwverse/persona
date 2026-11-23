package assistant

import (
	"strings"
	"sync"
	"time"
)

// Terminal recovery on a decision (#13087, docs/assistant/todos.md "Decisions the owner can answer"). Only the owner can
// resume an Agent whose stop was theirs or has no known cause, with Resume on its terminal page. A decision never does it:
// a new one keeps that instruction in its reason and its row links the Agent's terminal; an older one that still offers
// it as an option opens the terminal when that option is pressed, and the hub never takes it as an answer.

// manualResumeKeep: how long a refused resume can still give the next decision on the Agent's task its terminal link.
const manualResumeKeep = 24 * time.Hour

// terminalResumeLabel recognizes the terminal instruction an existing decision may still offer as an option, in the
// three supported languages. It can only offer navigation; it never authorizes a start.
func terminalResumeLabel(label string) bool {
	label = strings.ToLower(strings.TrimSpace(label))
	return strings.HasPrefix(label, "resume ") && strings.Contains(label, "terminal") ||
		strings.Contains(label, "终端") && strings.Contains(label, "恢复") || // i18n-ignore: matches labels supervisors already wrote
		strings.Contains(label, "ターミナル") && strings.Contains(label, "再開") // i18n-ignore: matches labels supervisors already wrote
}

// decisionTerminal is the task-owned Agent an older decision's terminal option names by its session's first eight
// characters. An archived or released binding never redirects the owner's recovery to another Agent.
func decisionTerminal(t Todo, label string) *SessionRef {
	if !terminalResumeLabel(label) {
		return nil
	}
	var match *SessionRef
	for _, a := range t.Agents {
		if !a.active() || len(a.SessionID) < 8 || !strings.Contains(strings.ToLower(label), strings.ToLower(a.SessionID[:8])) {
			continue
		}
		if match != nil {
			return nil
		}
		match = &SessionRef{MachineID: a.MachineID, SessionID: a.SessionID}
	}
	return match
}

// manualResumeBook remembers, by machine and session, when resumeAllowed last refused a supervisor's resume because
// the Agent's stop cause leaves Resume on its terminal page to the owner. The next decision raised on that Agent's task
// links its terminal (manualResumeTarget): the hub's own refusal decides the link, never the wording of the decision's
// reason, so a reason that merely mentions a terminal or says not to resume links nothing. It is kept in memory: after
// a restart a decision simply has no link, and its reason still tells the owner what to do.
type manualResumeBook struct {
	mu      sync.Mutex
	refused map[[2]string]time.Time
}

// noteManualResume records that only the owner can resume session on machine, and forgets refusals too old to link.
func (s *Service) noteManualResume(machine, session string) {
	b := &s.manualResumes
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	for k, at := range b.refused {
		if now.Sub(at) > manualResumeKeep {
			delete(b.refused, k)
		}
	}
	if b.refused == nil {
		b.refused = map[[2]string]time.Time{}
	}
	b.refused[[2]string{machine, session}] = now
}

// manualResumeTarget is the active Agent of task t whose resume the hub refused after it joined t, for the decision
// being raised now. It takes that refusal, so a later decision links the terminal again only after a new one. Nil when
// there is none, or more than one: the link must name the one Agent the decision is about.
func (s *Service) manualResumeTarget(t Todo) *SessionRef {
	b := &s.manualResumes
	b.mu.Lock()
	defer b.mu.Unlock()
	var target *SessionRef
	for _, a := range t.Agents {
		at, ok := b.refused[[2]string{a.MachineID, a.SessionID}]
		if !ok || !a.active() || at.Before(a.LinkedAt) || time.Since(at) > manualResumeKeep {
			continue
		}
		if target != nil {
			return nil
		}
		target = &SessionRef{MachineID: a.MachineID, SessionID: a.SessionID}
	}
	if target != nil {
		delete(b.refused, [2]string{target.MachineID, target.SessionID})
	}
	return target
}

// boundTerminal is ref while it is still one of t's active Agents: a released binding never takes the owner to another
// Agent's terminal.
func boundTerminal(t Todo, ref *SessionRef) *SessionRef {
	if ref == nil {
		return nil
	}
	for _, a := range t.Agents {
		if a.active() && a.MachineID == ref.MachineID && a.SessionID == ref.SessionID {
			return &SessionRef{MachineID: a.MachineID, SessionID: a.SessionID}
		}
	}
	return nil
}
