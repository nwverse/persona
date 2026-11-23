package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// These are trusted card resolutions. Model-supplied quotes enter as inferred
// until H1b verifies them against their source messages.
func (s *Service) ledgerCardDecisions(ctx context.Context, ix proto.Interaction, r proto.Resolution, status string) error {
	st := s.entriesDB()
	if st == nil || status != "resolved" || ix.Kind != proto.IxQuestion || ix.Secret || len(ix.Fields) > 0 || r.Dismiss || r.Cancel || r.AnsweredBy != "" {
		return nil
	}
	if s.isGuest(ctx, ix.MachineID, ix.SessionID) || s.isWorkMachine(ix.MachineID) {
		return nil
	}
	for _, q := range ix.Questions {
		answer, ok := r.Answers[q.Question]
		if !ok || strings.TrimSpace(answer) == "" {
			continue
		}
		options := []string{}
		selected := []string{}
		rejected := []string{}
		for _, o := range q.Options {
			options = append(options, o.Label)
		}
		// Resolution answers are display text, possibly a free-form answer or a
		// multi-selection. Preserve the exact answer rather than guessing splits.
		if !slices.Contains(options, answer) {
			options = append(options, answer)
		}
		selected = append(selected, answer)
		if !q.MultiSelect {
			for _, o := range q.Options {
				if o.Label != answer {
					rejected = append(rejected, o.Label)
				}
			}
		}
		e := proto.AssistantEntry{ID: entrySourceID("card", ix.MachineID, ix.ID, q.Question), Type: "decision", Text: q.Question + ": " + answer, Quote: answer,
			OriginSpace: s.entriesSourceSpace(ctx, ix.MachineID, ix.SessionID), Source: proto.AssistantEntrySource{Machine: ix.MachineID, Session: ix.SessionID, Card: ix.ID}, Scope: proto.AssistantEntryScope{Executor: "system", Machine: []string{ix.MachineID}, Personal: true},
			Trust: "user_verbatim", By: "user", Status: "active", Decision: &proto.AssistantEntryDecision{Options: options, Selected: selected, Rejected: rejected, Reason: r.Reason, Reversible: ix.Reversible}}
		if _, err := st.AppendAssistantEntry(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

func entrySourceID(parts ...string) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return "entry_" + hex.EncodeToString(sum[:16])
}

func (s *Service) toolEntries(ctx context.Context, machine, session, name string, args json.RawMessage) (string, error) {
	// Only private assistant sessions have these tools; still enforce the boundary
	// here so a direct call cannot bypass the catalog.
	_, guest, err := s.guestOf(ctx, machine, session)
	if err != nil || guest || s.isWorkMachine(machine) || isOutwardReader(ctx) {
		return "", errors.New("entries are only available in the owner's assistant conversations")
	}
	if name == "decisions_query" {
		var p proto.AssistantEntriesParams
		if err := strictParams(name, args, &p); err != nil {
			return "", err
		}
		p.Type = "decision"
		out, err := s.listEntries(ctx, p, machine != "" && machine == s.settings(ctx).MachineID)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(out)
		return string(b), err
	}
	var in struct {
		Entry      proto.AssistantEntry   `json:"entry"`
		UserQuotes []proto.AssistantEntry `json:"user_quotes"`
	}
	if err := strictParams(name, args, &in); err != nil {
		return "", err
	}
	st := s.entriesDB()
	if st == nil {
		return "", errors.New("entries require the manager database")
	}
	e := in.Entry
	e.ID = "entry_pending"
	e.Type = "decision"
	e.By = "agent"
	e.Trust = "agent"
	e.Status = "active"
	e.ExtID = ""
	if e.Quote != "" {
		e.By = "user"
		e.Trust = "inferred"
	}
	if e.Source.Machine == "" {
		e.Source.Machine = machine
	}
	if e.Source.Session == "" {
		e.Source.Session = session
	}
	if e.Scope.Executor == "" {
		e.Scope.Executor = "assistant"
	}
	if e.Scope.Personal && machine != s.settings(ctx).MachineID {
		return "", errors.New("personal entries require the owner's machine")
	}
	// Validate every quote before making any writes. user_quotes is a bounded
	// source inlet; it cannot publish verified user trust or invoke I6.
	if len(in.UserQuotes) > 20 {
		return "", errors.New("at most 20 user_quotes")
	}
	for i := range in.UserQuotes {
		q := &in.UserQuotes[i]
		q.ID = entrySourceID("quote", q.Source.Machine, q.Source.Session, q.Source.Message, q.Quote)
		q.By = "user"
		q.Trust = "inferred"
		q.Status = "active"
		q.Supersedes = ""
		q.ExtID = ""
		if q.Type == "" {
			q.Type = "fact"
		}
		if q.Text == "" {
			q.Text = q.Quote
		}
		if q.Scope.Executor == "" {
			q.Scope.Executor = "assistant"
		}
		if q.Scope.Personal && machine != s.settings(ctx).MachineID {
			return "", errors.New("personal quotes require the owner's machine")
		}
		if q.OriginSpace == "" {
			q.OriginSpace = s.entriesSourceSpace(ctx, q.Source.Machine, q.Source.Session)
		}
		if err := db.ValidateAssistantEntry(*q); err != nil {
			return "", err
		}
	}
	if e.OriginSpace == "" {
		e.OriginSpace = s.entriesSourceSpace(ctx, e.Source.Machine, e.Source.Session)
	}
	if err := db.ValidateAssistantEntry(e); err != nil {
		return "", err
	}
	if e.Supersedes != "" {
		previous, err := st.AssistantEntry(ctx, e.Supersedes)
		if err != nil {
			return "", err
		}
		if previous.By != "agent" || previous.Trust != "agent" {
			return "", errors.New("a model cannot supersede user or verified entries")
		}
	}
	// Bind replays to the complete normalized request, before the server assigns at.
	db.NormalizeAssistantEntry(&e)
	rawEntry, _ := json.Marshal(e)
	e.ID = entrySourceID("decision", machine, session, string(rawEntry))
	e, err = st.AppendAssistantEntry(ctx, e)
	if err != nil {
		return "", err
	}
	for _, q := range in.UserQuotes {
		if _, err := st.AppendAssistantEntry(ctx, q); err != nil {
			return "", err
		}
	}
	b, err := json.Marshal(map[string]any{"entry": e})
	return string(b), err
}

func (s *Service) entriesSourceSpace(ctx context.Context, machine, session string) string {
	if row, ok := s.session(ctx, machine, session); ok {
		return spaceOf(row)
	}
	return ""
}
