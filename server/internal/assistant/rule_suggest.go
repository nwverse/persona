package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// A rule after the same answer three times (#4929, docs/assistant/unified-entry.md "A rule after the same answer
// three times"). When the owner gave the same answer to the same kind of card for the same scope ruleSuggestAfter
// times within ruleSuggestWindow, the next card of that kind and scope carries a proto.RuleSuggestion: Always makes
// the rule, Not now silences the line for ruleQuietFor. Three kinds:
//
//   - plan_start: Start on plan cards for tasks that change files in one directory of one computer. Always adds the
//     tasks.trust entry "Start these directly" adds.
//   - standing_authorization: the go-ahead for merge, push or deploy-test in one directory of one computer (a decision's
//     option, or Yes to a plain confirmation). Always grants a standing authorization for it, with the owner's answer
//     as the quote, through the checks and the store every grant goes through.
//   - delegate: a question a supervisor passed up, answered with what it recommended. Always stores no rule: the
//     suggestion names the agents.delegate setting.
//
// Nothing is counted in a table of its own: the plans the owner started (task_plans, PlanTask.OwnerStarted), the
// decisions kept on to-dos (todo_decisions) and the ask_escalated work events are read when a card is made. The tap
// on Always is the owner's own approval, as Start is: only the two routes below make a rule, they take a full
// device's request only, and no tool of a model or an agent reaches them.
const (
	// ruleSuggestAfter is how many identical answers in a row earn the suggestion.
	ruleSuggestAfter = 3
	// ruleSuggestWindow is how far back the answers are counted.
	ruleSuggestWindow = 14 * 24 * time.Hour
	// ruleQuietFor is how long a kind and scope is not suggested again after Not now, or after Always (so that
	// removing the rule does not bring the line straight back).
	ruleQuietFor = 30 * 24 * time.Hour
	// ruleCacheFor is how long the count read for one card serves the next ones.
	ruleCacheFor = 30 * time.Second

	ruleSuggestKey = "assistant.rule_suggestions"

	ruleKindPlanStart = "plan_start"
	ruleKindStanding  = "standing_authorization"
	ruleKindDelegate  = "delegate"

	ruleDelegateSetting = "agents.delegate"
)

var errRuleSuggestionGone = errors.New("no such rule suggestion")

// ruleSuggestBook is the setting assistant.rule_suggestions: when each suggestion (by id) was last answered.
type ruleSuggestBook struct {
	NotNow map[string]time.Time `json:"not_now"`
}

// ruleSuggestState serializes the book's read-modify-writes and keeps the last count for ruleCacheFor.
type ruleSuggestState struct {
	mu    sync.Mutex
	at    time.Time
	cands map[string]*ruleCandidate
}

func (r *ruleSuggestState) forget() {
	r.mu.Lock()
	r.at, r.cands = time.Time{}, nil
	r.mu.Unlock()
}

// ruleCandidate is a rule the owner's answers earned: what Always would make.
type ruleCandidate struct {
	id, kind string
	count    int
	quiet    bool // answered Not now (or Always) within ruleQuietFor
	// plan_start: the kind tasks.trust would remember (trustKey of task).
	task PlanTask
	// standing_authorization: the grant's scope, and the owner's latest answer as its quote.
	machineID, directory, action, quote string
}

func ruleSuggestionID(kind, scope string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + scope))
	return "rs_" + hex.EncodeToString(sum[:8])
}

func ruleStandingScope(machineID, directory, action string) string {
	return machineID + "\x00" + directory + "\x00" + action
}

// ruleStandingConditions are the conditions a suggested grant carries: a merge waits for green checks and a clean
// merge, both checked at run time (standingConditions); push and deploy-test carry none.
func ruleStandingConditions(action string) []string {
	if action == "merge" {
		return []string{"ci_green", "no_conflicts"}
	}
	return []string{}
}

// ---- reading an answer ----

// ruleActions are the only actions a rule is ever suggested for: deploy-production, send, delete and spend cannot be
// undone or are seen by others, and stay asked each time.
var ruleActions = []string{"merge", "push", "deploy-test"}

var ruleActionNames = map[string]string{
	"merge": "merge", "push": "push", "deploy-test": "deploy-test", "deploy to test": "deploy-test",
	"合并": "merge", "推送": "push", "部署测试": "deploy-test", "部署到测试": "deploy-test", // i18n-ignore: matched option labels
	"マージ": "merge", "プッシュ": "push", // i18n-ignore: matched option labels
}

// ruleActionLabel is an option that is the action and nothing else ("Merge", "Merge the PR now", "Deploy to test").
var ruleActionLabel = lazyre.New(`(?i)^(merge|push|deploy-test|deploy to test|合并|推送|部署测试|部署到测试|マージ|プッシュ)(?:\s*(?:it|now|(?:the |this )?(?:pr|branch|changes)|现在))*[.!。！]?$`) // i18n-ignore: matched option labels

// ruleNever: words that put a decision out of reach of a rule whatever its option says.
var ruleNever = lazyre.New(`(?i)force|prod|delete|remove|send|spend|pay|release|publish|生产|线上|强制|删除|发送|花钱|付款|发布|本番|強制|削除|送信|支払|公開`) // i18n-ignore: matched decision text

var ruleRecommendedSuffix = lazyre.New(`(?i)\s*[（(]\s*(?:recommended|推荐|おすすめ)\s*[)）]$`) // i18n-ignore: matched option labels

var (
	ruleYes = []string{"yes", "continue", "approve", "proceed", "是", "继续", "批准", "はい", "続行"} // i18n-ignore: matched option labels
	ruleNo  = []string{"no", "not now", "cancel", "否", "暂不", "取消", "いいえ", "キャンセル"}           // i18n-ignore: matched option labels
)

// ruleAction reads an answer as a go-ahead for one of ruleActions: a plain confirmation of the action ("Merge the PR
// now?", standingRequest) answered Yes or No, or a decision's option that is the action itself. ok false: the answer
// is about something else, or about something a rule never covers; yes false: the owner said no to the action.
func ruleAction(question, choice string) (action string, yes, ok bool) {
	question = strings.TrimSpace(question)
	choice = strings.ToLower(strings.TrimSpace(ruleRecommendedSuffix.ReplaceAllString(strings.TrimSpace(choice), "")))
	if m := standingRequest.FindStringSubmatch(question); len(m) > 0 {
		action = ruleActionNames[strings.ToLower(m[1])]
		switch {
		case !slices.Contains(ruleActions, action):
			return "", false, false
		case slices.Contains(ruleYes, choice):
			return action, true, true
		case slices.Contains(ruleNo, choice):
			return action, false, true
		}
		return "", false, false
	}
	m := ruleActionLabel.FindStringSubmatch(choice)
	if len(m) == 0 || ruleNever.MatchString(question) {
		return "", false, false
	}
	return ruleActionNames[strings.ToLower(m[1])], true, true
}

// ---- counting ----

// ruleEvidenceStore is what the count reads besides the settings: the database has both.
type ruleEvidenceStore interface {
	TodoDecisionsSince(ctx context.Context, since time.Time) ([]db.TodoDecisionRow, error)
	TaskPlansMadeSince(ctx context.Context, since time.Time) ([]db.TaskPlanRow, error)
}

var _ ruleEvidenceStore = (*db.DB)(nil)

// ruleCandidates are the rules the owner's answers earned as of now, by id, with the quiet ones marked. They are read
// again after ruleCacheFor, and at once when fresh.
func (s *Service) ruleCandidates(ctx context.Context, fresh bool) (map[string]*ruleCandidate, error) {
	r := &s.rules
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if !fresh && r.cands != nil && now.Sub(r.at) < ruleCacheFor {
		return r.cands, nil
	}
	since := now.Add(-ruleSuggestWindow)
	out := map[string]*ruleCandidate{}
	add := func(c *ruleCandidate) { out[c.id] = c }
	if err := s.rulePlanStarts(ctx, since, add); err != nil {
		return nil, err
	}
	if err := s.ruleDecisions(ctx, since, add); err != nil {
		return nil, err
	}
	var book ruleSuggestBook
	if st := s.store(); st != nil && len(out) > 0 {
		if _, err := st.Setting(ctx, ruleSuggestKey, &book); err != nil {
			return nil, err
		}
	}
	for id, c := range out {
		if at, ok := book.NotNow[id]; ok && now.Sub(at) < ruleQuietFor {
			c.quiet = true
		}
	}
	r.at, r.cands = now, out
	return out, nil
}

// rulePlansSince are the plans made since then that were started or dismissed, oldest first.
func (s *Service) rulePlansSince(ctx context.Context, since time.Time) ([]*TaskPlan, error) {
	var plans []*TaskPlan
	switch st := s.store().(type) {
	case nil:
		s.tasks.mu.Lock()
		for _, p := range s.tasks.plans {
			if !p.CreatedAt.Before(since) && p.State != planOpen {
				cp := *p
				cp.Tasks = append([]PlanTask(nil), p.Tasks...)
				plans = append(plans, &cp)
			}
		}
		s.tasks.mu.Unlock()
		sort.Slice(plans, func(i, j int) bool { return plans[i].CreatedAt.Before(plans[j].CreatedAt) })
	case ruleEvidenceStore:
		rows, err := st.TaskPlansMadeSince(ctx, since)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			plans = append(plans, planFromRow(row))
		}
	}
	return plans, nil
}

// ruleStartable: Start on a plan with t in it is an answer about t's kind: a task of its own that changes files. A
// read-only task never waits for Start, and a task leader's worker is started by its leader.
func ruleStartable(t PlanTask) bool {
	return !t.Readonly && t.Parent == "" && t.Machine != "" && t.Cwd != ""
}

// rulePlanStarts counts, per kind (trustKey), the plans the owner started in a row; a dismissed plan starts its
// kinds' count again. A kind already in tasks.trust earns nothing.
func (s *Service) rulePlanStarts(ctx context.Context, since time.Time, add func(*ruleCandidate)) error {
	plans, err := s.rulePlansSince(ctx, since)
	if err != nil || len(plans) == 0 {
		return err
	}
	runs := map[string]int{}
	tasks := map[string]PlanTask{}
	for _, p := range plans {
		seen := map[string]bool{} // a plan counts once for a kind, however many of its tasks are of it
		for _, t := range p.Tasks {
			k := trustKey(t)
			if !ruleStartable(t) || seen[k] {
				continue
			}
			seen[k] = true
			switch {
			case p.State == planDismissed:
				runs[k] = 0
			case t.OwnerStarted:
				runs[k]++
				tasks[k] = PlanTask{Machine: t.Machine, Cwd: t.Cwd}
			}
		}
	}
	trusted, err := s.trustedKindsErr(ctx)
	if err != nil {
		return err
	}
	for k, n := range runs {
		if n >= ruleSuggestAfter && !trusted[k] {
			add(&ruleCandidate{id: ruleSuggestionID(ruleKindPlanStart, k), kind: ruleKindPlanStart, count: n, task: tasks[k]})
		}
	}
	return nil
}

// ruleDecisions counts the decisions kept on to-dos: the go-aheads for one action in one directory in a row (a No to
// the same confirmation starts the count again), and the escalated questions answered as their supervisor recommended.
func (s *Service) ruleDecisions(ctx context.Context, since time.Time, add func(*ruleCandidate)) error {
	st, ok := s.store().(ruleEvidenceStore)
	if !ok {
		return nil
	}
	rows, err := st.TodoDecisionsSince(ctx, since)
	if err != nil || len(rows) == 0 {
		return err
	}
	type run struct {
		n                            int
		machineID, directory, action string
		quote                        string
	}
	runs := map[string]*run{}
	plans := map[string]*TaskPlan{}
	byCard := map[string][]db.TodoDecisionRow{}
	for _, row := range rows {
		// The owner's own answers only: a card they answered, or a decision's option they chose (kept as manual).
		if row.Source != db.DecisionCard && row.Source != db.DecisionManual {
			continue
		}
		if row.Source == db.DecisionCard && row.SourceMessage != "" {
			byCard[row.SourceMessage] = append(byCard[row.SourceMessage], row)
		}
		action, yes, ok := ruleAction(row.Question, row.Choice)
		if !ok {
			continue
		}
		t, found := s.todoNow(row.TodoID)
		if !found {
			continue
		}
		machineID, directory, ok := s.ruleTodoScope(ctx, t, plans)
		if !ok {
			continue
		}
		k := ruleStandingScope(machineID, directory, action)
		if runs[k] == nil {
			runs[k] = &run{machineID: machineID, directory: directory, action: action}
		}
		if !yes {
			runs[k].n = 0
			continue
		}
		runs[k].n++
		runs[k].quote = clip(oneLine(row.Question), 300) + " → " + clip(oneLine(row.Choice), 120)
	}
	if len(runs) > 0 {
		grants, err := s.standingAuthorizations(ctx)
		if err != nil {
			return err
		}
		for k, r := range runs {
			if r.n < ruleSuggestAfter || slices.ContainsFunc(grants.Authorizations, func(g StandingAuthorization) bool {
				return g.MachineID == r.machineID && slices.Contains(g.Actions, r.action) && standingDirectoryContains(g.Directory, r.directory)
			}) {
				continue
			}
			add(&ruleCandidate{id: ruleSuggestionID(ruleKindStanding, k), kind: ruleKindStanding, count: r.n,
				machineID: r.machineID, directory: r.directory, action: r.action, quote: r.quote})
		}
	}
	if n := s.ruleRecommendedRun(ctx, since, byCard); n >= ruleSuggestAfter {
		add(&ruleCandidate{id: ruleSuggestionID(ruleKindDelegate, ""), kind: ruleKindDelegate, count: n})
	}
	return nil
}

// ruleTodoScope is where task t works, as a standing authorization names it: the computer and the directory its plan
// gave it (the repository, whatever checkout its agent got), else its agent's. None for a directory no grant may name,
// and for the health space.
func (s *Service) ruleTodoScope(ctx context.Context, t Todo, plans map[string]*TaskPlan) (machineID, directory string, ok bool) {
	if t.PlanID != "" {
		p, seen := plans[t.PlanID]
		if !seen {
			p, _ = s.loadPlan(ctx, t.PlanID)
			plans[t.PlanID] = p
		}
		if p != nil {
			if i := slices.IndexFunc(p.Tasks, func(pt PlanTask) bool { return pt.TodoID == t.ID }); i >= 0 {
				if m, found := s.machineByName(ctx, p.Tasks[i].Machine); found {
					machineID, directory = m.ID, p.Tasks[i].Cwd
				}
			}
		}
	}
	if machineID == "" {
		for _, a := range t.Agents {
			if a.ReleasedAt == nil && a.DismissedAt == nil && a.Cwd != "" {
				machineID, directory = a.MachineID, a.Cwd
				break
			}
		}
	}
	if machineID == "" || !validStandingDirectory(directory) || identity.InHealthSpace(directory) {
		return "", "", false
	}
	return machineID, directory, true
}

// ruleRecommendedRun is how many question cards in a row, up to the latest, the owner answered with what the
// supervisor that passed them up recommended (the ask_escalated work event keeps the card and the recommendation).
// A card answered another way starts the count again; one nobody answered does not count either way.
func (s *Service) ruleRecommendedRun(ctx context.Context, since time.Time, byCard map[string][]db.TodoDecisionRow) int {
	if len(byCard) == 0 {
		return 0
	}
	var events []db.WorkEvent
	if st := s.workDB(); st != nil {
		var err error
		if events, err = st.WorkEvents(ctx, since); err != nil {
			s.logf("rule suggestions: work events: %v", err)
			return 0
		}
	} else {
		s.work.mu.Lock()
		events = append(events, s.work.testEvents...)
		s.work.mu.Unlock()
	}
	type answered struct {
		at  time.Time
		yes bool
	}
	var cards []answered
	seen := map[string]bool{}
	for _, e := range events {
		card, _ := e.Detail["card"].(string)
		recommend, _ := e.Detail["recommend"].(string)
		rows := byCard[card]
		if e.Kind != "ask_escalated" || card == "" || recommend == "" || seen[card] || len(rows) == 0 {
			continue
		}
		seen[card] = true
		cards = append(cards, answered{at: rows[0].CreatedAt, yes: len(rows) == 1 && ruleSameAnswer(rows[0].Choice, recommend)})
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].at.Before(cards[j].at) })
	n := 0
	for _, c := range cards {
		if c.yes {
			n++
		} else {
			n = 0
		}
	}
	return n
}

func ruleSameAnswer(choice, recommend string) bool {
	norm := func(v string) string {
		return strings.ToLower(strings.TrimSpace(ruleRecommendedSuffix.ReplaceAllString(strings.TrimSpace(v), "")))
	}
	return norm(choice) != "" && norm(choice) == norm(recommend)
}

// ---- on the cards ----

// ruleSuggestion words candidate c in l.
func (s *Service) ruleSuggestion(ctx context.Context, c *ruleCandidate, l i18n.Lang) *proto.RuleSuggestion {
	out := &proto.RuleSuggestion{ID: c.id, Kind: c.kind, Count: c.count}
	switch c.kind {
	case ruleKindPlanStart:
		out.Creates = trustSetting
		out.Scope = proto.RuleScope{Machine: c.task.Machine, Directory: c.task.Cwd}
		if m, ok := s.machineByName(ctx, c.task.Machine); ok {
			out.Scope.MachineID, out.Scope.Machine = m.ID, m.Name
		}
		out.Text = i18n.T(l, "assistant-rule_suggest.plan_start", i18n.Args{"n": c.count, "directory": out.Scope.Directory, "machine": out.Scope.Machine})
	case ruleKindStanding:
		out.Creates = ruleKindStanding
		out.Scope = proto.RuleScope{MachineID: c.machineID, Machine: s.machineName(c.machineID), Directory: c.directory,
			Action: c.action, Conditions: ruleStandingConditions(c.action)}
		out.Text = i18n.T(l, "assistant-rule_suggest."+strings.ReplaceAll(c.action, "-", "_"),
			i18n.Args{"n": c.count, "directory": c.directory, "machine": out.Scope.Machine})
	case ruleKindDelegate:
		out.Setting = ruleDelegateSetting
		out.Text = i18n.T(l, "assistant-rule_suggest.delegate", i18n.Args{"n": c.count})
	}
	return out
}

// ruleFor is the suggestion with id, when the answers earned it and it is not quiet. A count that cannot be read
// suggests nothing: the card goes out as it always did.
func (s *Service) ruleFor(ctx context.Context, id string, l i18n.Lang) *proto.RuleSuggestion {
	cands, err := s.ruleCandidates(ctx, false)
	if err != nil {
		s.logf("rule suggestions: %v", err)
		return nil
	}
	if c := cands[id]; c != nil && !c.quiet {
		return s.ruleSuggestion(ctx, c, l)
	}
	return nil
}

// planWithSuggestion is plan p as the apps get it: with the suggestion to start its kind directly while it waits for
// Start and the owner started that kind ruleSuggestAfter times. p itself is not changed (the suggestion is never kept
// with the plan). A health plan gets none: it waits for Start whatever is trusted.
func (s *Service) planWithSuggestion(ctx context.Context, p *TaskPlan, l i18n.Lang) *TaskPlan {
	if p == nil || p.State != planOpen {
		return p
	}
	for _, t := range p.Tasks {
		if !ruleStartable(t) {
			continue
		}
		if sg := s.ruleFor(ctx, ruleSuggestionID(ruleKindPlanStart, trustKey(t)), l); sg != nil {
			if s.inHealth(ctx, p.Conversation) {
				return p
			}
			cp := *p
			cp.Suggestion = sg
			return &cp
		}
	}
	return p
}

// askSuggestion is the suggestion on task id's decision a: one of its options is the go-ahead the owner gave
// ruleSuggestAfter times for this directory. Never for a decision that cannot be undone or that others will see.
func (s *Service) askSuggestion(ctx context.Context, id string, a *acceptAsk, l i18n.Lang) *proto.RuleSuggestion {
	if !decideAsk(a) || neverDefault(a.WhyOwner) || a.Response != "" {
		return nil
	}
	for _, o := range a.Options {
		action, yes, ok := ruleAction(a.Reason, o.Label)
		if !ok || !yes {
			continue
		}
		t, found := s.todoNow(id)
		if !found {
			return nil
		}
		machineID, directory, ok := s.ruleTodoScope(ctx, t, map[string]*TaskPlan{})
		if !ok {
			return nil
		}
		return s.ruleFor(ctx, ruleSuggestionID(ruleKindStanding, ruleStandingScope(machineID, directory, action)), l)
	}
	return nil
}

// cardSuggestion is the suggestion on question card ix a supervisor passed up to the owner (ringAsks): the go-ahead
// they gave ruleSuggestAfter times for the task's directory when the card is a plain confirmation of it, else the
// agents.delegate setting once they took the supervisors' recommendations that often (and it is off). Only a question
// gets one: tool permissions and plans are approved each time.
func (s *Service) cardSuggestion(ctx context.Context, ix proto.Interaction, l i18n.Lang) *proto.RuleSuggestion {
	if ix.Kind != proto.IxQuestion || len(ix.Questions) == 0 || len(ix.Fields) > 0 || ix.Secret || ix.ToolName == "ask_user" {
		return nil
	}
	if len(ix.Questions) == 1 && !ix.Questions[0].MultiSelect {
		for _, o := range ix.Questions[0].Options {
			action, yes, ok := ruleAction(ix.Questions[0].Question, o.Label)
			if !ok || !yes {
				continue
			}
			if t, found := s.taskOf(ctx, ix.MachineID, ix.SessionID); found {
				if machineID, directory, ok := s.ruleTodoScope(ctx, t, map[string]*TaskPlan{}); ok {
					if sg := s.ruleFor(ctx, ruleSuggestionID(ruleKindStanding, ruleStandingScope(machineID, directory, action)), l); sg != nil {
						return sg
					}
				}
			}
			break
		}
	}
	if s.hello(ctx).Agents.Delegate.On() {
		return nil
	}
	return s.ruleFor(ctx, ruleSuggestionID(ruleKindDelegate, ""), l)
}

// suggestOnCard puts cardSuggestion on pending card ix, so that the apps' lists carry it too; ix as it is now.
func (s *Service) suggestOnCard(ix proto.Interaction) proto.Interaction {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sg := s.cardSuggestion(ctx, ix, s.lang(ctx))
	if sg == nil {
		return ix
	}
	s.fleet.mu.Lock()
	if p := s.fleet.interactions[ix.ID]; p != nil {
		p.ix.Suggestion = sg
	}
	s.fleet.mu.Unlock()
	ix.Suggestion = sg
	return ix
}

// ---- Always and Not now ----

// ruleQuiet keeps that suggestion id was answered now, and drops the answers older than ruleQuietFor.
func (s *Service) ruleQuiet(ctx context.Context, id string) error {
	st := s.store()
	if st == nil {
		return errors.New("no store")
	}
	s.rules.mu.Lock()
	defer s.rules.mu.Unlock()
	var book ruleSuggestBook
	if _, err := st.Setting(ctx, ruleSuggestKey, &book); err != nil {
		return err
	}
	now := time.Now().UTC()
	kept := map[string]time.Time{id: now}
	for k, at := range book.NotNow {
		if k != id && now.Sub(at) < ruleQuietFor {
			kept[k] = at
		}
	}
	if err := st.PutSetting(ctx, ruleSuggestKey, ruleSuggestBook{NotNow: kept}); err != nil {
		return err
	}
	s.rules.at, s.rules.cands = time.Time{}, nil
	return nil
}

// ruleAlways makes the rule suggestion id stands for, on the owner's tap: the tasks.trust entry, or the standing
// authorization (through prepareStandingChange and the revision-checked save every grant goes through); a delegate
// suggestion makes none. errRuleSuggestionGone: the answers do not earn it (any more), or it was answered already.
func (s *Service) ruleAlways(ctx context.Context, id string, l i18n.Lang) (map[string]any, error) {
	cands, err := s.ruleCandidates(ctx, true)
	if err != nil {
		return nil, err
	}
	c := cands[id]
	if c == nil || c.quiet {
		return nil, errRuleSuggestionGone
	}
	out := map[string]any{"ok": true, "kind": c.kind, "creates": ""}
	switch c.kind {
	case ruleKindPlanStart:
		key := trustKey(c.task)
		s.trustKinds(ctx, []PlanTask{c.task})
		if have, err := s.trustedKindsErr(ctx); err != nil || !have[key] {
			return nil, errors.Join(errors.New("the kind was not remembered"), err)
		}
		out["creates"], out["key"] = trustSetting, key
	case ruleKindStanding:
		grant := StandingAuthorization{MachineID: c.machineID, Directory: c.directory, Actions: []string{c.action},
			Conditions: ruleStandingConditions(c.action), Quote: c.quote,
			Source: i18n.T(l, "assistant-rule_suggest.source", i18n.Args{"n": c.count}), Reversible: true}
		s.standingMu.Lock()
		before, err := s.standingAuthorizations(ctx)
		if err != nil {
			s.standingMu.Unlock()
			return nil, err
		}
		in := StandingAuthorizations{Revision: before.Revision, Authorizations: append(slices.Clone(before.Authorizations), grant)}
		after, err := prepareStandingChange(before, in, time.Now())
		if err == nil {
			err = s.saveStandingAuthorizations(ctx, before.Revision, after)
		}
		s.standingMu.Unlock()
		if err != nil {
			return nil, err
		}
		out["creates"], out["authorization"], out["revision"] = ruleKindStanding, after.Authorizations[len(after.Authorizations)-1], after.Revision
	case ruleKindDelegate:
		out["setting"] = ruleDelegateSetting
	}
	if err := s.ruleQuiet(ctx, id); err != nil {
		s.logf("rule suggestions: %s: %v", id, err) // the rule stands; at worst the line shows once more
		s.rules.forget()
	}
	s.askChanged(ctx) // the cards read again without the line
	return out, nil
}

// ruleNotNow silences suggestion id for ruleQuietFor. errRuleSuggestionGone: the answers never earned it.
func (s *Service) ruleNotNow(ctx context.Context, id string) error {
	cands, err := s.ruleCandidates(ctx, true)
	if err != nil {
		return err
	}
	if cands[id] == nil {
		return errRuleSuggestionGone
	}
	if cands[id].quiet {
		return nil // already answered: the same again changes nothing
	}
	if err := s.ruleQuiet(ctx, id); err != nil {
		return err
	}
	s.askChanged(ctx)
	return nil
}

func (s *Service) ruleSuggestionRoutes() {
	fail := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, errRuleSuggestionGone):
			apierr.Write(w, http.StatusNotFound, i18n.Err("assistant-rule_suggest.gone"))
		case errors.Is(err, errStandingConflict):
			apierr.Write(w, http.StatusConflict, i18n.Err("assistant-standing_authorizations.conflict"))
		default:
			s.logf("rule suggestions: %v", err)
			apierr.Write(w, http.StatusServiceUnavailable, i18n.Err("http-status.unavailable"))
		}
	}
	// The owner's own tap, from a device that may do everything: the only way a suggested rule is made.
	s.handle("POST /api/v1/assistant/rule-suggestions/{id}/always", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-rule_suggest.full_access"))
			return
		}
		out, err := s.ruleAlways(r.Context(), r.PathValue("id"), s.requestLang(r))
		if err != nil {
			fail(w, err)
			return
		}
		apierr.WriteJSON(w, out)
	})
	s.handle("POST /api/v1/assistant/rule-suggestions/{id}/not-now", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-rule_suggest.full_access"))
			return
		}
		if err := s.ruleNotNow(r.Context(), r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true})
	})
}

type ownerStartKey struct{}

// ownerStart marks ctx as the owner's own Start of a plan (its route); ownerStarts reads the mark in startPlan.
func ownerStart(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownerStartKey{}, true)
}

func ownerStarts(ctx context.Context) bool {
	on, _ := ctx.Value(ownerStartKey{}).(bool)
	return on
}
