package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/prompts"
)

const (
	dispatchSimilarityMax     = 12
	dispatchSimilarityTimeout = 18 * time.Second
	// A just-closed task may still have its Agent. Keep immediate follow-ups on that conversation while it remains linked.
	dispatchRecentClose = 15 * time.Minute
)

type dispatchSimilarityCandidate struct {
	Todo Todo   `json:"-"`
	Text string `json:"text"`
	rank float64
}

func (s *Service) similarDispatch(ctx context.Context, task PlanTask, request *BriefRequest, todos []Todo) (Todo, bool) {
	proposed, candidates := s.dispatchSimilarityInput(task, request, todos, time.Now())
	if len(candidates) == 0 {
		return Todo{}, false
	}
	judgeCtx, cancel := context.WithTimeout(ctx, dispatchSimilarityTimeout)
	defer cancel()
	todo, matched, err := judgeDispatchSimilarity(judgeCtx, s.purposeModelFor("inbox.judge", "dispatch_reuse"), proposed, candidates)
	if err != nil {
		s.logf("dispatch reuse: similarity check unavailable")
	}
	return todo, matched
}

func (s *Service) dispatchSimilarityInput(task PlanTask, request *BriefRequest, todos []Todo, now time.Time) (string, []dispatchSimilarityCandidate) {
	proposed := clip(task.Title, 160) + "\n" + clip(task.Goal, 400) + "\n" + clip(stripContext(task.Prompt), 1200)
	if request != nil {
		proposed += "\n" + clip(request.Text, 400)
	}
	grams := make(map[string]struct{})
	for _, gram := range bigrams(proposed) {
		grams[gram] = struct{}{}
	}

	var candidates []dispatchSimilarityCandidate
	var sharedGrams [][]string
	var textSizes []int
	frequency := make(map[string]int)
	for _, todo := range todos {
		if todo.Cancelled() || todo.DoneAt != nil && now.Sub(*todo.DoneAt) > dispatchRecentClose || !slices.ContainsFunc(todo.Agents, func(agent TodoAgent) bool {
			return agent.active() && agent.SessionID != ""
		}) {
			continue
		}
		brief, _ := s.briefOfTodo(todo.ID)
		subject := clip(todo.Title, 160) + "\n" + clip(brief.Goal, 400)
		background := clip(stripContext(todo.Notes), 900)

		// Count distinct overlap so long repeated notes cannot dominate shorter task descriptions.
		seen := make(map[string]struct{})
		var shared []string
		for _, gram := range bigrams(subject + "\n" + background) {
			if _, counted := seen[gram]; counted {
				continue
			}
			seen[gram] = struct{}{}
			if _, present := grams[gram]; present {
				frequency[gram]++
				shared = append(shared, gram)
			}
		}
		if len(shared) >= 2 {
			candidates = append(candidates, dispatchSimilarityCandidate{Todo: todo, Text: subject + "\n" + background})
			sharedGrams = append(sharedGrams, shared)
			textSizes = append(textSizes, len(seen))
		}
	}
	// Terms found in many tasks are weak clues. The model still decides whether the work actually overlaps.
	for i := range candidates {
		for _, gram := range sharedGrams[i] {
			candidates[i].rank += 1 / float64(frequency[gram]+1)
		}
		candidates[i].rank /= math.Sqrt(float64(textSizes[i]))
	}
	slices.SortFunc(candidates, func(a, b dispatchSimilarityCandidate) int {
		return cmp.Or(cmp.Compare(b.rank, a.rank), cmp.Compare(a.Todo.ID, b.Todo.ID))
	})
	return proposed, candidates[:min(dispatchSimilarityMax, len(candidates))]
}

func judgeDispatchSimilarity(ctx context.Context, model jsonModel, proposed string, candidates []dispatchSimilarityCandidate) (Todo, bool, error) {
	type choice struct {
		Number int    `json:"number"`
		Text   string `json:"text"`
	}
	input := struct {
		Proposed   string   `json:"proposed"`
		Candidates []choice `json:"candidates"`
	}{Proposed: proposed}
	for i, candidate := range candidates {
		input.Candidates = append(input.Candidates, choice{Number: i + 1, Text: candidate.Text})
	}
	raw, _ := json.Marshal(input)
	var result struct {
		Candidate        int    `json:"candidate"`
		ProposedEvidence string `json:"proposed_evidence"`
		ExistingEvidence string `json:"existing_evidence"`
	}
	if err := model.JSON(ctx, prompts.RenderDispatchReuse(), string(raw), &result); err != nil {
		return Todo{}, false, err
	}
	if result.Candidate < 1 || result.Candidate > len(candidates) {
		return Todo{}, false, nil
	}
	candidate := candidates[result.Candidate-1]
	for _, pair := range [][2]string{{proposed, result.ProposedEvidence}, {candidate.Text, result.ExistingEvidence}} {
		quote := strings.TrimSpace(pair[1])
		if utf8.RuneCountInString(quote) < 4 || !strings.Contains(pair[0], quote) {
			return Todo{}, false, nil
		}
	}
	return candidate.Todo, true, nil
}
