package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

const toolLoopsSetting = "agents.tool_loops"

type savedToolLoop struct {
	Machine         string             `json:"machine"`
	Session         string             `json:"session"`
	Since           time.Time          `json:"since"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Tool            string             `json:"tool"`
	Checks          int                `json:"checks"`
	CheckedCount    int                `json:"checked_count"`
	Silenced        bool               `json:"silenced"`
	SilencedByInput bool               `json:"silenced_by_input,omitempty"`
	Card            *proto.Interaction `json:"card,omitempty"`
}

// loadToolLoops must finish before checking loops, answering cards or publishing
// a snapshot. Failed reads are retried; an empty fallback would lose decisions.
func (s *Service) loadToolLoops(ctx context.Context) error {
	s.work.loopLoadMu.Lock()
	defer s.work.loopLoadMu.Unlock()
	s.work.mu.Lock()
	loaded := s.work.loopLoaded
	s.work.mu.Unlock()
	if loaded {
		return nil
	}
	var rows []savedToolLoop
	if st := s.store(); st != nil {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := st.Setting(readCtx, toolLoopsSetting, &rows)
		cancel()
		if err != nil {
			return err
		}
	}
	s.work.mu.Lock()
	s.work.init()
	for _, row := range rows {
		key := [2]string{row.Machine, row.Session}
		if row.Machine == "" || row.Session == "" || s.work.loops[key] != nil {
			continue
		}
		updatedAt := row.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = time.Now().UTC()
		}
		state := &toolLoopState{since: row.Since, updatedAt: updatedAt, tool: row.Tool, checks: row.Checks, checkedCount: row.CheckedCount, silenced: row.Silenced, silencedByInput: row.SilencedByInput, uncertain: true}
		if reason, invalid := s.work.loopInvalid[key]; invalid {
			if reason == loopTurnChanged {
				state.checks = 0
			}
			if reason != loopCandidateGone && !state.since.IsZero() {
				state.silenced, state.silencedByInput = true, true
			} else if !state.silenced {
				state.since, state.tool = time.Time{}, ""
			}
		} else if row.Card != nil && row.Card.ID != "" {
			state.cardID = row.Card.ID
			s.work.cards[row.Card.ID] = *row.Card
		}
		s.work.loops[key] = state
	}
	hadInvalid := len(s.work.loopInvalid) != 0
	s.work.loopInvalid = nil
	s.work.loopSaved = len(rows) != 0
	s.work.loopLoaded = true
	s.work.mu.Unlock()
	if hadInvalid {
		return s.saveToolLoops(ctx)
	}
	return nil
}

// saveToolLoops serializes snapshots and takes each snapshot after acquiring the
// write lock. An older captured state cannot overwrite a newer transition.
func (s *Service) saveToolLoops(ctx context.Context, committing ...[2]string) error {
	st := s.store()
	if st == nil {
		return nil
	}
	s.work.loopSaveMu.Lock()
	defer s.work.loopSaveMu.Unlock()
	s.work.mu.Lock()
	rows := make([]savedToolLoop, 0, len(s.work.loops))
	var target *toolLoopState
	for key, state := range s.work.loops {
		commit := len(committing) > 0 && key == committing[0]
		if commit {
			target = state
		}
		updatedAt := state.updatedAt
		if updatedAt.IsZero() {
			updatedAt = time.Now().UTC()
		}
		row := savedToolLoop{Machine: key[0], Session: key[1], Since: state.since, UpdatedAt: updatedAt, Tool: state.tool, Checks: state.checks, CheckedCount: state.checkedCount, Silenced: state.silenced, SilencedByInput: state.silencedByInput}
		switch {
		case state.consuming && (state.consumedSaved || commit):
			// The terminal decision is durable; never restore its old card.
		case state.consuming:
			// Another session's write must not commit this unfinished answer.
			row.Silenced = false
			if card, ok := s.work.cards[state.cardID]; ok {
				row.Card = &card
			}
		case state.pendingCard != nil && (state.pendingSaved || commit):
			card := *state.pendingCard
			row.Card = &card
		case state.pendingCard != nil:
			row.Silenced = state.pendingPriorSilenced
		case state.cardID != "":
			if card, ok := s.work.cards[state.cardID]; ok {
				row.Card = &card
			}
		}
		rows = append(rows, row)
	}
	s.work.mu.Unlock()
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := st.PutSetting(writeCtx, toolLoopsSetting, rows); err != nil {
		return err
	}
	s.work.mu.Lock()
	s.work.loopSaved = len(rows) != 0
	if target != nil && s.work.loops[committing[0]] == target {
		if target.consuming {
			target.consumedSaved = true
		}
		if target.pendingCard != nil {
			target.pendingSaved = true
		}
	}
	s.work.mu.Unlock()
	return nil
}
