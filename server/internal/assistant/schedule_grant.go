package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A scheduled task's own start grant (#8045, docs/assistant/unified-entry.md "A scheduled task's own start grant").
//
// A round the hub starts for a scheduled task has no message from the owner, so a writable task it hands out with
// task_plan used to wait on its card however much the owner wanted the schedule to do that work. The owner can now say,
// on the card of one of that schedule's own rounds, "let this scheduled task start these by itself": the kind of each
// task (trustKey: machine + repository or folder + read or edit, as the trust list's) is added to the schedule's
// StartGrant. From then on a task_plan call made in a round the hub started for that schedule starts a task of a kind
// in the grant without the card. Everything else keeps the card:
//
//   - the grant is the schedule's only: another schedule's rounds, follow-ups the assistant set up for itself, every
//     other conversation and every turn the hub did not start for that schedule ask as before;
//   - only the owner adds to it (Start on the card, POST /api/v1/task-plans/{id}/start with schedule_grant): no tool
//     call and no app body sets or widens it (editTask keeps the hub's copy); the schedule's page shows it and removes
//     it (DELETE /api/v1/assistant/tasks/{id}/start-grant);
//   - it holds while the schedule's prompt and target (the machine and conversation its runs go to, its space) are the
//     ones it was given under: a change of any of them drops it (dropStaleGrants, and liveGrant at use);
//   - a round that may have read untrusted outside content never uses it (roundReadsOutside): a schedule whose runs
//     are given the briefing's facts (Inbox matters) or a saved state note, a conversation with anything in it but
//     that schedule's own rounds and the owner's own words, or a tool call outside the short list of Persona's
//     own tools that read nothing from outside (grantSafeTools). What the hub cannot read or tell keeps the card.

// StartGrant is what the owner let one scheduled task's rounds start without the card.
type StartGrant struct {
	// Kinds are trust keys (trustKey): machine|repository or folder|read or edit. One covers its folder tree as a
	// remembered kind does (trustCovers).
	Kinds []string `json:"kinds"`
	// What the schedule was when the owner gave it: the prompt's digest and where its runs go. Another one drops it.
	PromptDigest string    `json:"prompt_digest"`
	MachineID    string    `json:"machine_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Space        string    `json:"space,omitempty"`
	GrantedAt    time.Time `json:"granted_at"`
}

func promptDigest(prompt string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(prompt)))
	return hex.EncodeToString(sum[:])
}

// grantFor is a new, empty grant for t as it is now.
func grantFor(t Task, now time.Time) *StartGrant {
	return &StartGrant{PromptDigest: promptDigest(t.Prompt), MachineID: t.MachineID, SessionID: t.SessionID, Space: t.Space,
		GrantedAt: now.UTC()}
}

// liveGrant is t's grant while it still applies: given under t's current prompt and target, with a kind in it.
func (t Task) liveGrant() *StartGrant {
	g := t.StartGrant
	if g == nil || len(g.Kinds) == 0 || g.PromptDigest != promptDigest(t.Prompt) || g.MachineID != t.MachineID ||
		g.SessionID != t.SessionID || g.Space != t.Space {
		return nil
	}
	return g
}

// dropStaleGrants clears every grant that no longer applies before a list is saved: the owner gave it to a schedule
// that has since changed its prompt or where it runs. liveGrant checks again at use, for what changes in memory only.
func dropStaleGrants(tasks []Task) {
	for i := range tasks {
		if tasks[i].StartGrant != nil && tasks[i].liveGrant() == nil {
			tasks[i].StartGrant = nil
		}
	}
}

type scheduleGrantKey struct{}

// scheduleGranting marks the owner's Start (ownerStart) as also letting the plan's scheduled task start these kinds by
// itself: the card's option (POST …/start schedule_grant). scheduleGrants reads the mark in startPlan.
func scheduleGranting(ctx context.Context) context.Context {
	return context.WithValue(ctx, scheduleGrantKey{}, true)
}

func scheduleGrants(ctx context.Context) bool {
	on, _ := ctx.Value(scheduleGrantKey{}).(bool)
	return on
}

// grantScheduleKind adds t's kind (trustKey, now that admission fixed the folder that runs) to schedule id's grant.
// Only startPlan's owner Start reaches here. A schedule that is gone or in the health space gets nothing.
func (s *Service) grantScheduleKind(ctx context.Context, id string, t PlanTask) {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		s.logf("schedule grant: task %s: %v", id, err)
		return
	}
	i := sc.indexLocked(id)
	if i < 0 || sc.tasks[i].Space == SpaceHealth {
		sc.mu.Unlock()
		return
	}
	tasks := slices.Clone(sc.tasks)
	g := tasks[i].liveGrant()
	if g == nil {
		g = grantFor(tasks[i], time.Now())
	} else {
		cp := *g
		cp.Kinds = slices.Clone(g.Kinds)
		g = &cp
	}
	key := trustKey(t)
	if !slices.Contains(g.Kinds, key) {
		g.Kinds = append(g.Kinds, key)
		sort.Strings(g.Kinds)
	}
	tasks[i].StartGrant = g
	err = s.saveLocked(sc, tasks)
	sc.mu.Unlock()
	if err != nil {
		s.logf("schedule grant: task %s: %v", id, err)
		return
	}
	s.broadcastSchedule()
}

// revokeScheduleGrant removes kind from schedule id's grant, or the whole grant when kind is "". False when the task
// is gone.
func (s *Service) revokeScheduleGrant(ctx context.Context, id, kind string) (bool, error) {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return false, err
	}
	i := sc.indexLocked(id)
	if i < 0 {
		sc.mu.Unlock()
		return false, nil
	}
	tasks := slices.Clone(sc.tasks)
	if g := tasks[i].StartGrant; g != nil && kind != "" {
		cp := *g
		cp.Kinds = slices.DeleteFunc(slices.Clone(g.Kinds), func(k string) bool { return k == kind })
		tasks[i].StartGrant = &cp
		if len(cp.Kinds) == 0 {
			tasks[i].StartGrant = nil
		}
	} else {
		tasks[i].StartGrant = nil
	}
	err = s.saveLocked(sc, tasks)
	sc.mu.Unlock()
	if err != nil {
		return true, err
	}
	s.broadcastSchedule()
	return true, nil
}

func (s *Service) scheduleGrantRoutes() {
	// The schedule's page removes one kind (body {"kind": "machine|folder|edit"}) or the whole grant (no body). There is
	// no route that adds one: only the owner's Start on a round's card does (startPlan).
	s.handle("DELETE /api/v1/assistant/tasks/{id}/start-grant", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change scheduled tasks"))
			return
		}
		var in struct {
			Kind string `json:"kind"`
		}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
				apierr.Write(w, http.StatusBadRequest, err)
				return
			}
		}
		ok, err := s.revokeScheduleGrant(r.Context(), r.PathValue("id"), strings.TrimSpace(in.Kind))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		if !ok {
			apierr.Write(w, http.StatusNotFound, i18n.Err("assistant-schedule.gone"))
			return
		}
		v, _ := s.taskView(i18n.With(r.Context(), s.requestLang(r)), r.PathValue("id"))
		apierr.WriteJSON(w, map[string]any{"task": v})
	})
}

// ---- the rounds the hub started ----

// schedRound is a round the hub sent for scheduled task task into a conversation: what task_plan from that
// conversation checks its turn against (scheduledRoundOf). In memory only: after a hub restart a round's plan asks.
type schedRound struct {
	task string
	at   time.Time
}

// roundTTL bounds how long after it was sent a round's turn may still hand out work under its schedule's grant.
const roundTTL = 2 * time.Hour

func roundKey(machine, session string) string { return machine + "\x00" + session }

// noteRound records that the hub is sending a round of task into session on machine.
func (s *Service) noteRound(machine, session, task string, at time.Time) {
	if machine == "" || session == "" {
		return
	}
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	if s.sched.rounds == nil {
		s.sched.rounds = map[string][]schedRound{}
	}
	k := roundKey(machine, session)
	// Its own earlier round there is over by now (one round of a task at a time, sched.begin): this one replaces it.
	list := slices.DeleteFunc(s.sched.rounds[k], func(r schedRound) bool { return r.task == task || at.Sub(r.at) > roundTTL })
	s.sched.rounds[k] = append(list, schedRound{task: task, at: at})
}

// endRound forgets task's round in session once its turn ended (watchScheduledRun).
func (s *Service) endRound(machine, session, task string) {
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	k := roundKey(machine, session)
	list := slices.DeleteFunc(s.sched.rounds[k], func(r schedRound) bool { return r.task == task })
	if len(list) == 0 {
		delete(s.sched.rounds, k)
		return
	}
	s.sched.rounds[k] = list
}

// liveRound is the one round the hub has under way in session, false when there is none or more than one (two tasks'
// rounds into one conversation: which turn is whose cannot be told, so neither's grant applies).
func (s *Service) liveRound(machine, session string, now time.Time) (schedRound, bool) {
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	var live []schedRound
	for _, r := range s.sched.rounds[roundKey(machine, session)] {
		if now.Sub(r.at) <= roundTTL {
			live = append(live, r)
		}
	}
	if len(live) != 1 {
		return schedRound{}, false
	}
	return live[0], true
}

// scheduledRoundOf is the scheduled task whose round the turn task_plan is called in is, from what the hub knows
// (liveRound) and what the conversation says (its latest turn began with the scheduler's own prompt, sent after the
// round began, and no other message came into it). "" for every other turn. items is that turn as read.
func (s *Service) scheduledRoundOf(ctx context.Context, machine, session string) (task string, items []proto.Item) {
	r, ok := s.liveRound(machine, session, time.Now())
	if !ok {
		return "", nil
	}
	items, read := s.lastTurnItems(ctx, machine, session)
	if !read {
		return "", nil
	}
	_, turn := splitLastTurn(items)
	if !roundTurn(turn, r.at) {
		return "", nil
	}
	return r.task, items
}

// roundEarly is how much earlier than the hub's note of a round its prompt's own time may be (clocks of the hub and
// a machine apart).
const roundEarly = 2 * time.Minute

// roundTurn: turn (its items, prompt first) began with a scheduled prompt (the scheduler's message source, which an
// app's or another conversation's message never carries: prompts.MessageSourceText, ProtectUserMessage) written no
// earlier than the round, and nothing else came into it from outside the agent's own work.
func roundTurn(turn []proto.Item, at time.Time) bool {
	if len(turn) == 0 {
		return false
	}
	prompt := turn[0]
	if prompt.Role != "user" || prompt.Kind != proto.KindText || prompt.Queued || !scheduledPrompt(prompt) {
		return false
	}
	if prompt.TS.IsZero() || prompt.TS.Before(at.Add(-roundEarly)) {
		return false
	}
	for _, it := range turn[1:] {
		if it.Role == "user" && it.Kind != proto.KindToolResult {
			return false // a message that came in while it worked, a picture, a notice injected into the turn
		}
	}
	return true
}

// scheduledPrompt: it is a scheduled task's run prompt (patrol or follow-up), by its message source.
func scheduledPrompt(it proto.Item) bool {
	src := prompts.ItemSource(it)
	return src != nil && (src.Kind == proto.SourcePatrol || src.Kind == proto.SourceFollowup)
}

// ---- outside content ----

// grantSafeTools are Persona's own tools a round may call and still use its schedule's grant: each reads nothing
// written outside Persona (no mail, Inbox, web page, chat, agent transcript or file) and only says where work can run
// or plans and schedules it. Any other tool, built-in or not, keeps the card.
var grantSafeTools = map[string]bool{
	"task_plan": true, "task_plan_withdraw": true, "machines_list": true, "repositories_list": true,
	"repository_get": true, "todo_repositories_get": true, "list_followups": true, "schedule_followup": true,
	"cancel_followup": true, "schedule_state_set": true, "notify_user": true,
}

// grantSafeBuiltins are the agent's own tools that read nothing: Claude Code's to-do list and its loading of a
// deferred tool's schema (it loads task_plan's).
var grantSafeBuiltins = map[string]bool{"TodoWrite": true, "ToolSearch": true}

func grantSafeTool(name string) bool {
	if grantSafeBuiltins[name] {
		return true
	}
	rest, ok := strings.CutPrefix(name, "mcp__")
	i := strings.LastIndex(rest, "__")
	if !ok || i <= 0 || !compat.IsMCPServer(strings.TrimPrefix(rest[:i], "plugin_")) {
		return false
	}
	return grantSafeTools[compat.Tool(rest[i+2:])]
}

// grantSafeItem: it carries nothing from outside into a conversation a schedule's grant may start work from. Turn
// prompts are a scheduled task's or the owner's own words (no source, no relay, no injected notice); the rest is the
// agent's own text, its safe tools and their results.
func grantSafeItem(it proto.Item) bool {
	switch it.Role {
	case "assistant":
		switch it.Kind {
		case proto.KindText, proto.KindThinking:
			return true
		case proto.KindToolUse:
			return grantSafeTool(it.ToolName)
		}
		return false
	case "user":
		switch it.Kind {
		case proto.KindToolResult:
			return true // the result of a tool use checked above
		case proto.KindText:
			if scheduledPrompt(it) {
				return true
			}
			return it.ClientMsgID != "" && it.From == "" && it.Injected == nil && !it.Wake && it.Source == nil &&
				prompts.ItemSource(it) == nil // a message the owner sent from an app
		}
		return false
	}
	return it.Kind == proto.KindNotice // the transcript's own notices (a compaction, a stop)
}

// grantHistoryPages bounds how far back roundReadsOutside reads a conversation (session.items pages of up to 10
// turns): one longer than that keeps the card ("New chat next time" starts the schedule a new one).
const grantHistoryPages = 10

// roundReadsOutside: the round of t in session may have read untrusted outside content, or the hub cannot tell that
// it did not (latest: its turn, as scheduledRoundOf read it). True keeps the card.
func (s *Service) roundReadsOutside(ctx context.Context, t Task, machine, session string, latest []proto.Item) bool {
	if t.Facts || t.Space != "" || (t.StateNote != nil && strings.TrimSpace(t.StateNote.Text) != "") {
		return true // its prompt carries the Inbox's matters, a note an earlier run wrote, or health data
	}
	safe := func(items []proto.Item) bool {
		for _, it := range items {
			if !grantSafeItem(it) {
				return false
			}
		}
		return true
	}
	if len(latest) == 0 || !safe(latest) {
		return true
	}
	before := latest[0].Seq
	for range grantHistoryPages {
		switch {
		case before <= 0:
			return true // no seqs to page back by: unknown
		case before == 1:
			return false // read back to the conversation's start
		}
		var res struct {
			Items   []proto.Item `json:"items"`
			HasMore *bool        `json:"has_more"`
		}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := s.call(cctx, machine, DeviceSessionItems, map[string]any{"session_id": session, "turns": 10, "before_seq": before}, &res)
		cancel()
		if err != nil {
			return true
		}
		page := slices.DeleteFunc(res.Items, func(it proto.Item) bool { return it.Seq >= before })
		if !safe(page) {
			return true
		}
		more := res.HasMore == nil || *res.HasMore
		if len(page) == 0 || !more {
			return more // the start was read; a node that says there is more but gives nothing earlier: unknown
		}
		before = page[0].Seq
	}
	return true // too long to read whole
}

// scheduleGrantCover is what the grant of the round task_plan is called in lets start without the card: nil when the
// turn is no round the hub started, the schedule has no live grant, or the round may have read outside content.
func (s *Service) scheduleGrantCover(ctx context.Context, machine, session, task string, latest []proto.Item) func(PlanTask) bool {
	if task == "" {
		return nil
	}
	t, ok := s.scheduledTask(ctx, task)
	if !ok {
		return nil
	}
	g := t.liveGrant()
	if g == nil || s.roundReadsOutside(ctx, t, machine, session, latest) {
		return nil
	}
	have := map[string]bool{}
	for _, k := range g.Kinds {
		have[k] = true
	}
	homes := s.trustHomes(ctx)
	return func(p PlanTask) bool {
		return trustCovers(have, homes, trustKey(p)) || trustCovers(have, homes, trustFolderKey(p))
	}
}

// scheduledTask is task id as the schedule holds it now.
func (s *Service) scheduledTask(ctx context.Context, id string) (Task, bool) {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return Task{}, false
	}
	defer sc.mu.Unlock()
	if i := sc.indexLocked(id); i >= 0 {
		return sc.tasks[i], true
	}
	return Task{}, false
}
