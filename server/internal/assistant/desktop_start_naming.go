package assistant

import (
	"context"
	"strings"
	"time"
)

// A new session in Claude's desktop app is seeded under a placeholder title, its folder's name
// (node/claudedesktop_new.go, #13728): the app shows the title the transcript starts with, and the first message made
// an unreadable one that said nothing of the task. Once the start has ended, the hub reports the session
// (StartEnded.Started) and the manager names it from what the start asked, off the start's path, with the prompt, model
// and check of every automatic Agent name (agentNameFromDialogue) and in its ledger. The name stands over the
// placeholder in the apps' lists as a requested one does (agent_naming_request.go): nothing is typed into the session,
// and a rename in the app or in Claude still wins.
//
// Fallback: with the automatic-naming switch off or nothing to name it from, nothing is asked; when the model is not
// set up, fails, times out or gives no usable title, the placeholder stays (never the first message), the reason is
// logged and kept in the ledger (last_failure), and Auto Rename can name it later. A start is named once: reports of it
// share one model call with each other and with a requested name, and a session already named or renamed is not asked
// about again.

// nameDesktopStart names the desktop session an app's start made, in the background: the start was answered before.
func (s *Service) nameDesktopStart(machine string, started StartedSession) {
	text := strings.TrimSpace(started.NameFrom)
	if machine == "" || started.SessionID == "" || text == "" {
		return
	}
	b := &s.agentNames
	b.mu.Lock()
	if s.runCtx().Err() != nil {
		b.mu.Unlock()
		return
	}
	b.runs.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.runs.Done()
		ctx, cancel := context.WithTimeout(s.runCtx(), agentNamingTimeout+15*time.Second)
		defer cancel()
		if m, ok := s.fleetMachine(machine); ok {
			machine = m.ID // an id another one absorbed names that one
		} else if s.fleetOn() {
			return
		}
		prefs, ok := s.savedSettings(ctx)
		if !ok || !quietOn(prefs.AgentAutoNaming) {
			return // switched off: the placeholder is the title
		}
		if hook := b.beforeRequest; hook != nil {
			hook()
		}
		_, _, _ = b.requests.Do(agentNameKey(machine, started.SessionID), func() (any, error) {
			return s.nameDesktopStartNow(ctx, machine, started.SessionID, started.Title, text)
		})
	}()
}

// nameDesktopStartNow asks the model for the name of machine's session sid, seeded under seeded, from text, and keeps
// it in the ledger. It answers as nameAgentNow does, for a requested name that joined the call.
func (s *Service) nameDesktopStartNow(ctx context.Context, machine, sid, seeded, text string) (agentNameResult, error) {
	key := agentNameKey(machine, sid)
	b := &s.agentNames
	b.mu.Lock()
	loaded := s.agentNamesLocked(ctx)
	before := b.records[key]
	b.mu.Unlock()
	if !loaded {
		s.logf("agent naming: desktop start session=%q/%q reason=ledger_unavailable", machine, sid)
		return agentNameResult{}, errAgentNameLedger // a write over an unread ledger would drop every other name
	}
	if before.Title != "" || before.Manual {
		return agentNameResult{Title: before.Title}, nil // named or renamed already: not asked again
	}
	callCtx, cancel := context.WithTimeout(ctx, agentNamingTimeout)
	title, reason := s.agentNameFromDialogue(callCtx, cutRunes(text, agentNamingFirstRunes), "")
	cancel()
	// The raw node row, as the background pass reads it: the name is kept against the native title.
	current, found := s.rawAgentForNaming(ctx, machine, sid)
	b.mu.Lock()
	r := b.records[key]
	r.Machine, r.Session = machine, sid
	if reason != "" {
		r.LastFailure = reason
		err := s.putAgentNameLocked(ctx, key, r)
		b.mu.Unlock()
		if err != nil {
			s.logf("agent naming: desktop start session=%q/%q reason=failure_record_write_failed", machine, sid)
		}
		s.logf("agent naming: desktop start session=%q/%q reason=%s", machine, sid, reason)
		refusal, known := agentNameRefusals[reason]
		if !known {
			refusal = agentNameRefusals["model_call_failed"]
		}
		return agentNameResult{}, &refusal
	}
	raw := seeded
	if found {
		raw = current.Title
	}
	// Renamed while the model was out, the later wish: in the app (the ledger's record) or in Claude (a title other
	// than the one the start seeded).
	if r.Manual || r.Title != "" || found && (current.Assistant || seeded != "" && !sameAgentRawTitle(seeded, current.Title)) {
		b.mu.Unlock()
		s.logf("agent naming: desktop start session=%q/%q reason=renamed_meanwhile", machine, sid)
		return agentNameResult{}, errAgentNameRenamed
	}
	r.Title, r.RawTitle = title, raw
	// An automatic name over a custom title, as a requested one: it holds until the native title changes.
	r.Manual, r.Requested = false, true
	r.NamedAt, r.NamedOutput = time.Now(), current.OutputTokens
	r.Failures, r.LastFailure, r.NextAttempt = 0, "", time.Time{}
	err := s.putAgentNameLocked(ctx, key, r)
	b.mu.Unlock()
	if err != nil {
		s.logf("agent naming: desktop start session=%q/%q reason=result_write_failed", machine, sid)
		return agentNameResult{}, errAgentNameSave
	}
	s.publishSessions(machine) // decorating takes the ledger lock: published after it is released
	s.logf("agent naming: desktop start session=%q/%q result=named", machine, sid)
	return agentNameResult{Title: title, Changed: title != raw}, nil
}
