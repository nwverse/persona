package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Closing a finished task (docs/assistant/todos.md "Automatic completion", #1092). When a task's agents have all stopped and its receipt
// went out, the judge model reads the task's title, the prompt it was handed out with and what its agents said last,
// and only "done" closes the todo (done_by auto, with why). Anything else — not finished, waiting on someone,
// needing the user, unsure, no model, a model error, an agent whose turn the model service cut off (#1516) — leaves
// it open. A todo the user reopened is not closed again by the same stretch of work. A supervised task (supervise.go)
// is not closed at all: finished, it waits for its supervisor's review. While assistant.done_needs_delivery is on (off
// by default), done also needs the work live and checked (or nothing to ship): one done but held at a PR, merged or off
// by default stays open as undelivered, with where it is held (delivery.go). Every verdict is kept on the todo as its
// check (task_check.go), so an open task can say why it is not done.

const (
	verdictDone      = "done"
	verdictNotDone   = "not_done"
	verdictWaiting   = "waiting"
	verdictNeedsUser = "needs_user"
	verdictUnsure    = "unsure"
	// verdictUndelivered: done as asked, but its work is not live yet (delivery.go); the hub's own, not the model's.
	verdictUndelivered = "undelivered"
)

// taskDoneTimeout bounds one judgment (reading the replies and the model).
var taskDoneTimeout = 90 * time.Second

// taskDoneRetryAfter is how long a failed judgment waits before it is asked once more.
var taskDoneRetryAfter = 3 * time.Second

// taskDoneSignals are cheap hints found in the replies, handed to the model (never enough on their own).
type taskDoneSignals struct {
	MergedPR bool `json:"merged_pr"`
	Build    bool `json:"build"`
}

var (
	mergedWords  = lazyre.New(`(?i)\bmerged\b|\bsquash(ed)?\b|已合并|合并了|已合入|合入了|自行合并`)
	buildNumbers = lazyre.New(`(?i)testflight[^\n]{0,40}?\d{3,}|构建号?\s*[:：#]?\s*\d{3,}|\bbuild\s*#?\s*\d{3,}`)
)

func doneSignals(replies []prompts.TaskDoneReply) taskDoneSignals {
	var s taskDoneSignals
	for _, r := range replies {
		if prLink.MatchString(r.Text) && mergedWords.MatchString(r.Text) {
			s.MergedPR = true
		}
		if buildNumbers.MatchString(r.Text) {
			s.Build = true
		}
	}
	return s
}

// ownerNoAction reports a complete no-action predicate after an owner marker. The sentence or clause must end there:
// "do nothing else but approve" is still an action, while an independent requirement after a period is handled by the
// next marker occurrence rather than being swallowed with this one.
func ownerNoAction(rest string) bool {
	rest = strings.TrimSpace(rest)
	for _, phrase := range []string{"do nothing else", "do nothing"} {
		if !strings.HasPrefix(rest, phrase) {
			continue
		}
		tail := strings.TrimSpace(rest[len(phrase):])
		if tail == "" || strings.ContainsRune(".!?;", rune(tail[0])) {
			return true
		}
	}
	return false
}

// directOwnerMarker reports whether any occurrence of marker starts a direct requirement at the line or a
// sentence/clause boundary. Subordinate and narrative uses ("if …", "evaluates whether …") are not owner actions;
// skipping one such occurrence must not suppress a later direct one. A comma inside that same if-sentence does not end
// its conditional scope; a new sentence or an independent "otherwise" clause does (#5732).
func directOwnerMarker(lower, marker string) bool {
	for from := 0; from < len(lower); {
		i := strings.Index(lower[from:], marker)
		if i < 0 {
			return false
		}
		i += from
		before := strings.TrimSpace(lower[:i])
		direct := before == ""
		for _, end := range []string{".", "!", "?", ":", ";"} {
			direct = direct || strings.HasSuffix(before, end)
		}
		if strings.HasSuffix(before, ",") {
			clause := before
			end := strings.LastIndexAny(clause, ".!?;")
			// A colon starts a clause after whitespace; a time such as 10:30 does not end the conditional scope.
			if i := strings.LastIndex(clause, ": "); i > end {
				end = i
			}
			if i := strings.LastIndex(clause, ":\t"); i > end {
				end = i
			}
			if end >= 0 {
				clause = strings.TrimSpace(clause[end+1:])
			}
			direct = !strings.HasPrefix(clause, "if ") || strings.HasSuffix(clause, "otherwise,")
		}
		if direct && !ownerNoAction(lower[i+len(marker):]) {
			return true
		}
		from = i + len(marker)
	}
	return false
}

// ownerActionLine is a final reply's explicit request for the owner to act. The judge still decides the task as a
// whole; these unambiguous offers and hand-offs are a safety net against calling the work done (#5732). Negated forms
// are informational, not a request.
func ownerActionLine(text string) string {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		compact := strings.Join(strings.Fields(line), "")
		for _, p := range []struct {
			marker    string
			negations []string
		}{
			{"需要你本人操作", []string{"不需要你本人操作", "无需你本人操作", "不用你本人操作", "不必你本人操作"}}, // i18n-ignore: parser markers, not display copy
			{"由你发起", []string{"不需要由你发起", "无需由你发起", "不用由你发起", "不必由你发起"}},        // i18n-ignore: parser markers, not display copy
			{"要不要", []string{"不需要你决定要不要", "无需你决定要不要", "不用你决定要不要", "不是要不要"}},    // i18n-ignore: parser markers, not display copy
		} {
			if !strings.Contains(compact, p.marker) {
				continue
			}
			if p.marker == "要不要" && !strings.ContainsAny(line, "？?") { // i18n-ignore: parser markers, not display copy
				continue // evaluating whether to do something is narrative, not asking the owner to choose (#5732)
			}
			candidate := compact
			for _, n := range p.negations {
				candidate = strings.ReplaceAll(candidate, n, strings.Repeat(" ", len(n)))
			}
			// Negation is occurrence-local here too: masking its own phrase cannot hide a later direct requirement.
			if strings.Contains(candidate, p.marker) {
				return line
			}
		}
		lower := strings.ToLower(line)
		for _, p := range []struct {
			marker    string
			negations []string
		}{
			{"you need to", nil},
			{"you'll need to", nil},
			{"would you like me to", nil},
			{"do you want me to", nil},
			{"should i proceed", nil},
			{"shall i proceed", nil},
		} {
			if !strings.Contains(lower, p.marker) {
				continue
			}
			if p.marker == "you need to" || p.marker == "you'll need to" {
				// Negation is occurrence-local: its own prefix is not direct, and it never hides a later direct requirement.
				if directOwnerMarker(lower, p.marker) {
					return line
				}
				continue
			}
			negated := false
			for _, n := range p.negations {
				negated = negated || strings.Contains(lower, n)
			}
			if !negated {
				return line
			}
		}
	}
	return ""
}

// taskDoneCheck is one judgment, as POST /api/v1/todos/{id}/done-check answers it.
type taskDoneCheck struct {
	Verdict    string          `json:"verdict"`
	Reason     string          `json:"reason"`
	WouldClose bool            `json:"would_close"`
	Skip       string          `json:"skip"` // why the automatic pass would not judge it now; "" when it would
	Replies    int             `json:"replies"`
	Signals    taskDoneSignals `json:"signals"`
	// Delivery: how far the work went out, when the delivery is judged (done_needs_delivery).
	Delivery *TodoDelivery `json:"delivery,omitempty"`
	// AutoCheck: the judge's own check of a supervised task's done_when, when asked (taskDoneInput.Checks,
	// supervise_sample.go).
	AutoCheck *taskAutoCheck `json:"auto_check,omitempty"`
	// reasonKey: the i18n key of the hub's own reason, when it decided without the model (the todo's check says it in
	// the user's language, task_check.go); "" for the judge's.
	reasonKey string
}

// autoDoneSkip says why the hub would not judge t now: "" when it would. work is when one of its agents last started
// a turn (worked false: not known, a hub restarted since).
func autoDoneSkip(t Todo, work time.Time, worked bool) string {
	switch {
	case t.Origin == "":
		return "not_a_task" // a todo the user made by hand: only they close it
	case t.Cancelled():
		return "cancelled"
	case t.DoneAt != nil:
		return "done"
	case t.ReopenedAt != nil && !(worked && work.After(*t.ReopenedAt)):
		return "reopened" // no new turn since the user reopened it
	}
	return ""
}

// taskReplies are the last replies of t's active agents: the whole last answer read from the agent's transcript, or
// the session list's clipped last_text when that cannot be read.
func (s *Service) taskReplies(ctx context.Context, t Todo) (out []prompts.TaskDoneReply, sideTurnBlocked bool) {
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		m, ok := s.device(ctx, a.MachineID)
		if !ok {
			continue
		}
		earlier, items, read := s.lastTwoTurns(ctx, m.ID, a.SessionID)
		if read && taskTurnAwaitingAnswer(items) {
			return nil, false // one unfinished agent invalidates the whole task's completion evidence
		}
		ask, text := turnExchange(items)
		from, source := taskTurnSource(t, items)
		if source != "" {
			current, ok := s.todoNow(t.ID)
			s.tasks.mu.Lock()
			since := s.tasks.working[t.ID]
			s.tasks.mu.Unlock()
			if report, evidence, valid := freshTaskDoneReport(current, items, since); ok && valid && source != "meeting" {
				text = strings.Join(append([]string{text, report}, evidence...), "\n")
			} else {
				sideTurnBlocked = true
			}
		}
		if text == "" {
			if c, ok := s.session(ctx, m.ID, a.SessionID); ok {
				text = strings.TrimSpace(c.LastText)
			}
		}
		if text == "" {
			continue
		}
		if source == "" && sameRequest(ask, t.Notes) {
			ask = "" // the turn that answers the task's own prompt: the prompt is already in the input
		}
		r := prompts.TaskDoneReply{Machine: m.Name, From: from, Source: source, Ask: clipMiddle(ask, 500), Text: clipMiddle(text, 1500)}
		if ask != "" && source == "" {
			// The user's own follow-up in the task's session (not a side conversation): a short "done?" gets a short "yes,
			// all done"; the report of the work is in the turn before, which the judge reads with it (#3111).
			if _, before := turnExchange(earlier); before != "" && before != text {
				r.Earlier = clipMiddle(before, 1500)
			}
		}
		out = append(out, r)
	}
	return out, sideTurnBlocked
}

// lastTurn reads the latest exchange, not necessarily a user request about the task.
// Completion separately retains and gates its peer/injected provenance.
func (s *Service) lastTurn(ctx context.Context, machine, sid string) (ask, answer string) {
	items, _ := s.lastTurnItems(ctx, machine, sid)
	return turnExchange(items)
}

// lastTurnItems are the items of the agent's latest turn, as the node pages them (from its last user prompt on);
// ok is false when the node could not be asked.
func (s *Service) lastTurnItems(ctx context.Context, machine, sid string) (items []proto.Item, ok bool) {
	var res struct {
		Items []proto.Item `json:"items"`
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.call(cctx, machine, DeviceSessionItems, map[string]any{"session_id": sid, "turns": 1}, &res); err != nil {
		return nil, false
	}
	return res.Items, true
}

// lastTwoTurns are the items of the agent's turn before its latest one (earlier; none when there is none) and of its
// latest one (last), from one read of its last two turns; ok is false when the node could not be asked.
func (s *Service) lastTwoTurns(ctx context.Context, machine, sid string) (earlier, last []proto.Item, ok bool) {
	var res struct {
		Items []proto.Item `json:"items"`
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.call(cctx, machine, DeviceSessionItems, map[string]any{"session_id": sid, "turns": 2}, &res); err != nil {
		return nil, nil, false
	}
	earlier, last = splitLastTurn(res.Items)
	return earlier, last, true
}

// splitLastTurn splits items at the last turn's prompt (the last user message not queued, as the node counts turns):
// all of them are the last turn when no prompt is among them.
func splitLastTurn(items []proto.Item) (earlier, last []proto.Item) {
	for i := len(items) - 1; i > 0; i-- {
		if it := items[i]; it.Role == "user" && it.Kind == proto.KindText && !it.Queued && strings.TrimSpace(it.Text) != "" {
			return items[:i], items[i:]
		}
	}
	return nil, items
}

// turnExchange is what a turn's items say: the user's ask (its first user message; "" when none) and the agent's
// last reply ("" when none).
func turnExchange(items []proto.Item) (ask, answer string) {
	for _, it := range items {
		if it.Kind == proto.KindText && it.Role == "user" && strings.TrimSpace(it.Text) != "" {
			ask = strings.TrimSpace(it.Text)
			break
		}
	}
	for i := len(items) - 1; i >= 0; i-- {
		if it := items[i]; it.Kind == proto.KindText && it.Role == "assistant" && strings.TrimSpace(it.Text) != "" {
			return ask, strings.TrimSpace(it.Text)
		}
	}
	return ask, ""
}

// userStoppedNotice is the notice the transcript reader puts where the user stopped a turn (Claude Code's "[Request
// interrupted by user]", Codex's turn_aborted; transcript/user_text.go, codex.go).
const userStoppedNotice = transcript.InterruptedNotice

// silentTurn: a turn the node could read (it has items, at least the prompt) in which the agent neither replied nor
// called a tool. Claude Code ends a turn so, without an error, when the model service answers 200 with nothing in it
// (it retries once with a hidden "no visible output" prompt, then stops); Codex's rollout maps to the same items.
// Thinking alone is no output: the user sees nothing.
func silentTurn(items []proto.Item) bool {
	if len(items) == 0 {
		return false // nothing to go by (an older node, a runtime that does not page items): not judged silent
	}
	for _, it := range items {
		if it.Kind == proto.KindNotice && it.Text == userStoppedNotice {
			return false // the user stopped it (Esc): no failure to report
		}
		if it.Role != "assistant" {
			continue
		}
		switch it.Kind {
		case proto.KindToolUse, proto.KindImage:
			return false
		case proto.KindText:
			if strings.TrimSpace(it.Text) != "" {
				return false
			}
		}
	}
	return true
}

// sameRequest: ask is the task's own prompt (as sent, maybe with context around it).
func sameRequest(ask, prompt string) bool {
	ask, prompt = oneLine(ask), oneLine(prompt)
	if ask == "" || prompt == "" {
		return ask == prompt
	}
	if r := []rune(prompt); len(r) > 80 {
		prompt = string(r[:80])
	}
	return strings.Contains(ask, prompt)
}

// clipMiddle keeps a long text's first and last n runes: a final answer says what was done first and what is left last.
func clipMiddle(s string, n int) string {
	if utf8.RuneCountInString(s) <= 2*n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "\n…\n" + string(r[len(r)-n:])
}

// judgeTaskDone asks the judge model whether t's work is finished. An error: no model, or no answer to go by.
func (s *Service) judgeTaskDone(ctx context.Context, t Todo) (taskDoneCheck, error) {
	replies, sideTurnBlocked := s.taskReplies(ctx, t)
	if c, ok := taskDoneWithoutModel(replies, sideTurnBlocked); ok {
		if sideTurnBlocked {
			s.logf("task %s: side-conversation reply without current task completion evidence: left open", t.ID)
		}
		return c, nil
	}
	in := taskDoneInput{Owner: s.owner(ctx), Language: answerLanguage(s.hello(ctx).Locale), Delivery: s.doneNeedsDelivery(ctx),
		Title: t.Title, Notes: t.Notes, Replies: replies}
	in.Deployed = func(d TodoDelivery) TodoDelivery {
		pr := ""
		for _, r := range replies {
			if prs := prLink.FindAllString(r.Text, -1); len(prs) > 0 {
				pr = prs[len(prs)-1]
			}
		}
		return s.gateDelivery(ctx, t, d, pr)
	}
	// What it is judged against is its brief (#3822): the owner's words and the criteria as scope changes left them, not
	// the latest hand-over prompt's.
	if brief, ok := s.briefOfTodo(t.ID); ok {
		in.Brief = &brief
	}
	if in.Brief != nil && len(in.Brief.Criteria) > 0 {
		in.Criteria = in.Brief.Criteria
	}
	if t.Supervision != nil {
		if len(in.Criteria) == 0 {
			in.Criteria = t.Supervision.DoneWhen
		}
		// Spot-checking on: the judge records each criterion as delivery evidence; it never accepts or closes the task.
		in.Checks = len(in.Criteria) > 0 && s.reviewSamplePercent(ctx) < 100
	}
	return judgeTaskDoneWith(ctx, s.purposeModel("inbox.judge"), in)
}

// jsonModel is the one call each single-call judge makes (done, delegation, triage): a system and a user text in, a
// JSON answer decoded into out. *ai.Client is the real one; tests and behaviour checks give their own.
type jsonModel interface {
	JSON(ctx context.Context, system, user string, out any) error
}

// taskDoneInput is everything the done judge decides on, read from the todo, its agents and the assistant's
// settings (judgeTaskDone) or written out by a test.
type taskDoneInput struct {
	Owner    string                  // how the prompt names the user
	Language string                  // the user's language for the reason ("" Chinese, answerLanguage)
	Delivery bool                    // assistant.done_needs_delivery
	Title    string                  // the todo's title
	Notes    string                  // what it was handed out with (clipped here)
	Criteria []string                // a supervised task's done_when
	Replies  []prompts.TaskDoneReply // its agents' last replies
	Checks   bool                    // also check each criterion and the delivery (supervise_sample.go)
	Brief    *TaskBrief              // what was asked when the work was handed out (task_brief.go); nil: none
	// Deployed checks a delivery said live against the running hub (gateDelivery); nil: as judged.
	Deployed func(TodoDelivery) TodoDelivery
}

// taskDoneWithoutModel is the verdict given before any model is asked: a reply from a side conversation with no
// evidence for this task, no reply at all, or a turn the model service cut off. ok false: the model decides.
func taskDoneWithoutModel(replies []prompts.TaskDoneReply, sideTurnBlocked bool) (c taskDoneCheck, ok bool) {
	c = taskDoneCheck{Replies: len(replies), Signals: doneSignals(replies)}
	if sideTurnBlocked {
		c.Verdict, c.Reason, c.reasonKey = verdictNotDone, verdictWords[verdictNotDone], checkReasonSideTurn
		return c, true
	}
	if len(replies) == 0 {
		c.Verdict, c.Reason, c.reasonKey = verdictUnsure, "没读到 Agent 的回复", checkReasonNoReply
		return c, true
	}
	for _, r := range replies {
		if cutOff(r.Text) { // its turn was cut off by the model service: not finished, whatever came before (#1516)
			c.Verdict, c.Reason, c.reasonKey = verdictNotDone, interruptedSummary, checkReasonInterrupted
			return c, true
		}
	}
	return c, false
}

// judgeTaskDoneWith asks m about in and applies the hub's own rules to what it answers: a verdict it made up is
// unsure, the reason is one short line, and while delivery is judged, done with the work not out yet is undelivered.
// It reads nothing else, so a behaviour check can run it on written-out cases.
func judgeTaskDoneWith(ctx context.Context, m jsonModel, in taskDoneInput) (taskDoneCheck, error) {
	c := taskDoneCheck{Replies: len(in.Replies), Signals: doneSignals(in.Replies)}
	system := prompts.RenderTaskDone(prompts.TaskDone{Owner: in.Owner, Delivery: in.Delivery, Checks: in.Checks, Language: in.Language})
	din := prompts.TaskDoneInput{Title: in.Title, Prompt: clipMiddle(strings.TrimSpace(in.Notes), 1500),
		Criteria: in.Criteria, Replies: in.Replies, MergedPR: c.Signals.MergedPR, Build: c.Signals.Build}
	if b := in.Brief; b != nil { // held to what was asked, not to the latest hand-over prompt (task_brief.go)
		if b.Request != nil {
			din.Asked, din.AskedAt = clipMiddle(b.Request.Text, 600), b.Request.At.UTC().Format("2006-01-02")
		}
		for _, d := range (&taskBrief{rec: b.briefRecord, st: briefState{Log: b.Log}}).dropped() {
			din.Dropped = append(din.Dropped, d.Criterion)
		}
	}
	input := prompts.RenderTaskDoneInput(din)
	var out struct {
		Verdict  string          `json:"verdict"`
		Reason   string          `json:"reason"`
		Delivery string          `json:"delivery"`
		Gate     string          `json:"gate"`
		Unblock  string          `json:"unblock"`
		Checks   []autoCheckItem `json:"checks"`
	}
	// This deterministic safety net does not depend on the judge being available. Keep it with the model error so the
	// supervision path can still mark and push an explicit owner action (#5732).
	ownerAction := ""
	for _, r := range in.Replies {
		if action := ownerActionLine(r.Text); action != "" {
			ownerAction = action
			break
		}
	}
	if err := m.JSON(ctx, system, input, &out); err != nil {
		if ownerAction != "" {
			c.Verdict, c.Reason = verdictNeedsUser, clip(oneLine(ownerAction), 40)
		}
		return c, err
	}
	switch v := strings.ToLower(strings.TrimSpace(out.Verdict)); v {
	case verdictDone, verdictNotDone, verdictWaiting, verdictNeedsUser:
		c.Verdict = v
	default:
		c.Verdict = verdictUnsure // a verdict it made up is no verdict
	}
	c.Reason = clip(oneLine(out.Reason), 40)
	if c.Verdict != verdictNeedsUser && ownerAction != "" {
		c.Verdict, c.Reason = verdictNeedsUser, clip(oneLine(ownerAction), 40)
	}
	// Kept for wire compatibility. A judge may deliver a task for review, never close it (#5732).
	c.WouldClose = false
	if in.Checks {
		d := judgedDelivery(out.Delivery, out.Gate, out.Unblock, in.Replies)
		if !strings.EqualFold(strings.TrimSpace(out.Delivery), deliveryLive) {
			d.State = cmp.Or(strings.ToLower(strings.TrimSpace(out.Delivery)), deliveryNone) // live only on the judge's own word, never the agent's line alone
		}
		if in.Deployed != nil {
			d = in.Deployed(d)
		}
		c.AutoCheck = autoCheckOf(in.Criteria, out.Checks, d.State)
	}
	if in.Delivery {
		d := judgedDelivery(out.Delivery, out.Gate, out.Unblock, in.Replies)
		if in.Deployed != nil {
			d = in.Deployed(d) // live only when the running hub has the work (deploy_check.go)
		}
		d.At = time.Now()
		c.Delivery = &d
		if c.Verdict == verdictDone && !deliveryClosable(d.State) {
			c.Verdict, c.WouldClose = verdictUndelivered, false
		}
	}
	return c, nil
}

// autoFinishTask: t's receipt went out (its agents all stopped). work is when one of them last started a turn. The
// judge records delivery and waiting state when it finds the work finished; it never closes the todo.
func (s *Service) autoFinishTask(t Todo, work time.Time, worked bool) {
	ctx, cancel := context.WithTimeout(context.Background(), taskDoneTimeout)
	defer cancel()
	if skip := autoDoneSkip(t, work, worked); skip != "" {
		if skip == "reopened" {
			s.logf("task %s: reopened by the user and no new turn since: not judged", t.ID)
		}
		s.turnJudged(ctx, t.ID, taskDoneCheck{}, nil, skip) // nothing to judge: the sweep does not ask again (task_turns.go)
		return
	}
	s.taskJudging(t.ID, 1) // the sweep leaves a turn alone while it is judged (resumeTurn)
	defer s.taskJudging(t.ID, -1)
	if !worked {
		work = t.CreatedAt
	}
	if !s.taskCompletionCurrent(ctx, t, work) {
		return
	}
	c, err := s.judgeTaskDone(ctx, t)
	if err != nil && !ai.IsPaused(err) && !ai.IsNotConfigured(err) && ctx.Err() == nil {
		// One failed call (a model service hiccup, a reply that was no verdict) is asked once more before the task is
		// left unjudged (#3111); no model at all is not asked again.
		s.logf("task %s: done judge: %v (asked again)", t.ID, err)
		select {
		case <-time.After(taskDoneRetryAfter):
			c, err = s.judgeTaskDone(ctx, t)
		case <-ctx.Done():
		}
	}
	if !s.taskCompletionCurrent(ctx, t, work) {
		s.logf("task %s: work changed while judged: left open", t.ID)
		return
	}
	s.applyTaskDone(ctx, t, work, worked, c, err)
}

// applyTaskDone writes judgment c (err: the judge could not say) of task t, whose agents last started a turn at work
// (worked false: not known): the verdict on the todo, where its delivery is held, and then what follows a finished
// stretch of work. Every task waits for the main conversation's review or explicit user completion; none is closed
// here. autoFinishTask and Re-check with apply (doneCheckRoute) end here.
func (s *Service) applyTaskDone(ctx context.Context, t Todo, work time.Time, worked bool, c taskDoneCheck, err error) {
	if ai.IsNotConfigured(err) && c.Verdict != verdictNeedsUser {
		// A missing provider is not a transient judge failure: keep the same localized explanation the settings row shows.
		// A deterministic owner-only action remains actionable without a judge and must reach the visible wait (#5732).
		c = taskDoneCheck{Verdict: verdictWaiting, Reason: mc(s.lang(ctx), s.noAPIStatus(ctx))}
	}
	if err == nil || ai.IsNotConfigured(err) {
		s.keepTaskCheck(ctx, t, work, c) // why it is (not) done, on the todo (task_check.go)
	}
	if err == nil && c.Delivery != nil && c.Verdict != verdictNotDone {
		s.setTaskDelivery(ctx, t.ID, *c.Delivery) // where it is held, on its card (delivery.go)
	}
	if t.Supervision == nil {
		// Tasks created before universal supervision still join the main conversation instead of being closed (#5732).
		if main := s.settings(ctx).Main; main != "" {
			s.superviseTodo(ctx, t.ID, main, nil, 0)
			if current, ok := s.todoNow(t.ID); ok {
				t = current
			}
		}
	}
	if t.Supervision != nil {
		s.tasks.completionMu.Lock()
		defer s.tasks.completionMu.Unlock()
		if !s.taskWorkCurrent(t.ID, work) {
			return
		}
		if !worked {
			work = t.CreatedAt
		}
		s.turnJudged(ctx, t.ID, c, err, "")     // on the turn's ledger before its signal: judged once, told once (task_turns.go)
		s.superviseJudged(ctx, t, work, c, err) // never closed here: its supervisor reviews it (supervise.go)
		return
	}
	s.turnJudged(ctx, t.ID, c, err, "")
	if err != nil {
		if !ai.IsPaused(err) {
			s.logf("task %s: done judge: %v (left open)", t.ID, err)
		}
		return
	}
	// Without a main conversation there is nobody to review delivery either. Keep the task open with its check; never
	// fall back to the old automatic close (#5732).
	s.logf("task %s: judge %s: left open for delivery review (%s)", t.ID, c.Verdict, c.Reason)
}

// doneCheckRoute: POST /api/v1/todos/{id}/done-check runs the judgment on a todo as it is and answers it. Without
// apply nothing is written (for looking into why a task was or was not closed). With {"apply": true} (Re-check, #4867)
// it first moves the task's stage and delivery from its agents' last replies, as their turn's end would have
// (advanceFromLastReplies: for a turn end the hub missed), then writes the judgment as a finished stretch of work's is
// (applyTaskDone), when the hub would judge it at all (no skip) and its agents are still: applied says whether it did.
func (s *Service) doneCheckRoute() {
	s.handle("POST /api/v1/todos/{id}/done-check", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot judge todos"))
			return
		}
		if err := s.ensureTodosLoaded(r.Context()); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, errors.New("todos service unavailable"))
			return
		}
		id := r.PathValue("id")
		t, ok, err := s.todo(r.Context(), id)
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, errors.New("todos service unavailable"))
			return
		}
		if !ok {
			apierr.Write(w, http.StatusNotFound, db.ErrTodoNotFound)
			return
		}
		var in struct {
			Apply bool `json:"apply"`
		}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
				apierr.Write(w, http.StatusBadRequest, err)
				return
			}
		}
		s.tasks.mu.Lock()
		work, worked := s.tasks.working[id]
		s.tasks.mu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), taskDoneTimeout)
		defer cancel()
		apply := in.Apply && autoDoneSkip(t, work, worked) == "" && s.taskRuntimeSettled(ctx, t)
		if apply {
			s.advanceFromLastReplies(ctx, t)
			if cur, ok := s.todoNow(id); ok {
				t = cur
			}
		}
		c, err := s.judgeTaskDone(ctx, t)
		if err != nil {
			apierr.Write(w, http.StatusBadGateway, err)
			return
		}
		c.Skip = autoDoneSkip(t, work, worked)
		if c.Skip != "" {
			c.WouldClose = false
		}
		applied := false
		if apply && c.Skip == "" {
			since := work
			if !worked {
				since = t.CreatedAt
			}
			if s.taskCompletionCurrent(ctx, t, since) { // nothing started or changed while it was judged
				s.applyTaskDone(context.WithoutCancel(ctx), t, since, worked, c, nil)
				applied = true
			}
		}
		if !in.Apply {
			apierr.WriteJSON(w, c)
			return
		}
		out := struct {
			taskDoneCheck
			Applied bool   `json:"applied"`
			Stage   string `json:"stage,omitempty"`
		}{taskDoneCheck: c, Applied: applied}
		if cur, ok := s.todoNow(id); ok {
			out.Stage = cur.Stage
		}
		apierr.WriteJSON(w, out)
	})
}

// advanceFromLastReplies moves task t's stage and delivery from its agents' last replies, as the end of their turn
// does (advanceTaskReply: a PR link, merged, deployed and verified words, any "deployed" checked against the running
// hub), and asks for the deployed work to be verified as that does. For Re-check with apply: a turn whose end the hub
// did not see (a restart in the middle of it) moved nothing, and the session list's last words are clipped.
func (s *Service) advanceFromLastReplies(ctx context.Context, t Todo) {
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		m, ok := s.device(ctx, a.MachineID)
		if !ok {
			continue
		}
		text := ""
		if items, read := s.lastTurnItems(ctx, m.ID, a.SessionID); read {
			_, text = turnExchange(items)
		}
		if text == "" {
			if c, ok := s.session(ctx, m.ID, a.SessionID); ok && !strings.HasSuffix(c.LastText, "…") {
				text = strings.TrimSpace(c.LastText)
			}
		}
		if text == "" {
			continue
		}
		if cur, ok := s.todoNow(t.ID); ok {
			t = cur
		}
		if s.advanceTaskReply(ctx, t, m.ID, a.SessionID, text) {
			go s.askAcceptance(t, m.ID, a.SessionID, text) // deployed: verified, by the supervising assistant or the owner
		}
	}
}
