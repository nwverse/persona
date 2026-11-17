package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// askTodoQuestions preserves the form on new records and recovers older ones from their own expired card.
// Missing history leaves a free-text question, never options guessed from localized notes.
func (s *Service) askTodoQuestions(ctx context.Context, a askTodo) []proto.Question {
	if len(a.QuestionDetails) > 0 {
		return a.QuestionDetails
	}
	if st, ok := s.store().(interactionRecorder); ok {
		rec, err := st.InteractionRecord(ctx, a.Interaction)
		if err == nil && rec != nil && rec.Status == "expired" && rec.Interaction.MachineID == a.MachineID &&
			rec.Interaction.SessionID == a.SessionID && len(rec.Interaction.Questions) == len(a.Questions) {
			matches := true
			for i, q := range rec.Interaction.Questions {
				matches = matches && strings.TrimSpace(q.Question) == a.Questions[i]
			}
			if matches {
				return rec.Interaction.Questions
			}
		}
	}
	var questions []proto.Question
	for _, q := range a.Questions {
		questions = append(questions, proto.Question{Question: q})
	}
	return questions
}

func (s *Service) addExpiredQuestionAsks(ctx context.Context, out map[string]todoAsk) {
	b := &s.askTodos
	b.mu.Lock()
	if !s.loadAskTodosLocked(ctx) {
		b.mu.Unlock()
		return
	}
	records := slices.Clone(b.list)
	b.mu.Unlock()
	for _, a := range records {
		t, exists := s.todoNow(a.TodoID)
		if !exists || t.DoneAt != nil || !a.AnsweredAt.IsZero() || !a.DroppedAt.IsZero() || len(a.Questions) == 0 {
			continue
		}
		if _, has := out[a.TodoID]; has {
			continue
		}
		questions := s.askTodoQuestions(ctx, a)
		ask := todoAsk{Outcome: outcomeDecide, Verdict: strings.Join(a.Questions, " / "),
			Question: a.Questions[0], AskedAt: a.AskedAt, Questions: questions}
		// Legacy rows mark their first option as recommended. Only use that contract for a real recommendation;
		// the questions above always retain the original order and choices without inventing one.
		if len(questions) == 1 && !questions[0].MultiSelect {
			q := questions[0]
			if i := slices.IndexFunc(q.Options, func(o proto.QuestionOption) bool { return o.Label == q.Recommend }); i >= 0 {
				ask.Recommend = q.Recommend
				ask.Options = append(ask.Options, DecisionOption{Label: q.Options[i].Label, LeadsTo: q.Options[i].Description})
				for j, o := range q.Options {
					if j != i {
						ask.Options = append(ask.Options, DecisionOption{Label: o.Label, LeadsTo: o.Description})
					}
				}
			}
		}
		out[a.TodoID] = ask
	}
}

// chooseExpiredQuestion uses the same manual-decision ledger and return-to-asker path, not task acceptance.
func (s *Service) chooseExpiredQuestion(ctx context.Context, id, asked, option string, answers map[string]string) (acceptResult, error) {
	s.reviewDismissals.actions.acquire(id)
	defer s.reviewDismissals.actions.release(id)
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return acceptResult{}, errAcceptStore
	}
	s.askTodos.mu.Lock()
	loaded := s.loadAskTodosLocked(ctx)
	s.askTodos.mu.Unlock()
	if !loaded {
		return acceptResult{}, errAcceptStore
	}
	a, exists := s.askTodoOf(ctx, id)
	t, open := s.todoNow(id)
	at, err := time.Parse(time.RFC3339Nano, asked)
	if !exists || !open || t.DoneAt != nil || err != nil || !at.Equal(a.AskedAt) ||
		!a.AnsweredAt.IsZero() || !a.DroppedAt.IsZero() || a.held(time.Now()) {
		return acceptResult{}, nil
	}
	questions := s.askTodoQuestions(ctx, a)
	if len(questions) == 0 || option != "" && answers != nil {
		return acceptResult{}, errDecisionOption
	}
	if len(answers) == 0 {
		if len(questions) != 1 || questions[0].MultiSelect {
			return acceptResult{}, errDecisionOption
		}
		if option == "" {
			option = questions[0].Recommend
		}
		if option == "" || !slices.ContainsFunc(questions[0].Options, func(o proto.QuestionOption) bool { return o.Label == option }) {
			return acceptResult{}, errDecisionOption
		}
		answers = map[string]string{questions[0].Question: option}
	}
	if len(answers) != len(questions) {
		return acceptResult{}, errDecisionOption
	}
	var choices []askChoice
	for _, q := range questions {
		answer := strings.TrimSpace(answers[q.Question])
		if answer == "" || utf8.RuneCountInString(answer) > decisionTextMax {
			return acceptResult{}, errDecisionOption
		}
		choices = append(choices, askChoice{Question: q.Question, Choice: answer})
	}
	st := s.todoDB()
	if st == nil {
		return acceptResult{}, errAcceptStore
	}
	a, claimed, err := s.claimAskTodoChecked(ctx, id, true)
	if err != nil {
		return acceptResult{}, errors.Join(errAcceptStore, err)
	}
	if !claimed {
		return acceptResult{}, nil
	}
	sent := false
	defer func() {
		if !sent {
			s.settleAskClaim(context.WithoutCancel(ctx), id, false)
		}
	}()
	for _, c := range choices {
		// A retry after a failed relay must not record the same owner answer twice.
		key := sha256.Sum256([]byte(id + "\x00" + c.Question + "\x00" + c.Choice))
		row := db.TodoDecisionRow{ID: "dec_ask_" + hex.EncodeToString(key[:16]), TodoID: id,
			Question: decisionText(c.Question), Choice: c.Choice, Source: db.DecisionManual, CreatedAt: decisionNow()}
		inserted, err := s.insertDecision(ctx, st, row)
		if err != nil {
			return acceptResult{}, errors.Join(errAcceptStore, err)
		}
		if inserted {
			s.decisionsChanged(ctx, id, row.CreatedAt)
		}
	}
	if cur, ok := s.todoNow(id); !ok || cur.DoneAt != nil {
		return acceptResult{}, nil
	}
	if err := s.returnAskTodoAnswer(ctx, a, t.Title, choices); err != nil {
		return acceptResult{}, err
	}
	sent = true
	return acceptSucceeded(), nil
}
