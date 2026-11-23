package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	approvalops "github.com/nwverse/persona/server/internal/approval"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Direction leads supervise the tasks they plan; main supervises its own work (#13362). Explicit transfers
// override that initial assignment (#13344). Ordinary signals and start remedies stay with that supervisor;
// owner-decision escalations go to main. Task leaders' workers keep their leader Agent until it closes.
// Workers cannot hand out Persona tasks and use their runtime's in-session subagents instead.

// isLeader: conversation id is one the user made a leader (Settings.Leaders), and not the main conversation.
func (s *Service) isLeader(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	cfg := s.settings(ctx)
	return id != cfg.Main && slices.Contains(cfg.Leaders, id)
}

// supervises: conversation id may hand out tasks rather than being a worker: the main conversation or a leader.
// Planning direction leads own their tasks from dispatch through review and cancellation.
func (s *Service) supervises(ctx context.Context, id string) bool {
	return s.isMain(ctx, id) || s.isLeader(ctx, id)
}

// defaultPlanSupervisor assigns a direction lead its own plans, and otherwise uses main. A task leader's
// workers and active reporting conversations retain their specialized topology.
func (s *Service) defaultPlanSupervisor(ctx context.Context, conversation, parent string) (string, bool) {
	if parent != "" || conversation == "" {
		return "", false
	}
	if s.isLeader(ctx, conversation) {
		return conversation, true
	}
	if _, reports := s.reporterOf(ctx, conversation); reports {
		return "", false
	}
	main := s.settings(ctx).Main
	return main, main != ""
}

// planTaskSupervisor resolves ownership before dispatch, including failed starts. Retrying or continuing an
// assigned task must not overwrite a later transfer with the saved plan's original conversation.
func (s *Service) planTaskSupervisor(ctx context.Context, conversation string, task PlanTask) (string, error) {
	cfg := s.settings(ctx)
	supervisor := ""
	for _, id := range []string{task.TodoID, task.ReuseTodo} {
		if t, ok := s.todoNow(id); ok && t.Supervision.assigned() {
			supervisor = t.Supervision.Supervisor
			break
		}
	}
	if supervisor == "" {
		if task.Parent != "" {
			return conversation, nil
		}
		supervisor, _ = s.defaultPlanSupervisor(ctx, conversation, "")
		if task.Supervisor != "" && s.isLeader(ctx, task.Supervisor) {
			supervisor = task.Supervisor
		} else if conversation != cfg.Main {
			if c, ok := s.session(ctx, cfg.MachineID, conversation); ok && c.Assistant && runtimeSessionSupportsSupervisionPolicy(c) && !s.isBackground(ctx, conversation) {
				supervisor = conversation
			}
		}
	}
	if supervisor == "" {
		return "", nil
	}
	machine := cfg.MachineID
	if supervisor == cfg.Main {
		machine = cfg.mainRouteMachine()
	}
	if !s.runtimeWithoutSupervisionPolicy(ctx, machine, supervisor) {
		return supervisor, nil
	}
	if s.isLeader(ctx, supervisor) {
		// A lead that cannot receive supervision must not silently hand its work to main.
		return "", planInvalid{i18n.Err("assistant-conversations.supervision_reviewer_required")}
	}
	if !s.runtimeWithoutSupervisionPolicy(ctx, cfg.mainRouteMachine(), cfg.Main) {
		return cfg.Main, nil
	}
	if background := s.runtimeSupervisorFallback(ctx, cfg); background != cfg.Main {
		return cfg.Main, nil
	}
	return "", planInvalid{i18n.Err("assistant-conversations.supervision_reviewer_required")}
}

// legacyLeaderPlanSupervision reports whether t's saved pre-#5732 plan explicitly assigned its current leader.
// That topology is not the manual-Agent exception and rehomes to main. An unavailable or non-matching plan proves
// nothing, so an explicit leader assignment is left alone rather than guessed away.
func (s *Service) legacyLeaderPlanSupervision(ctx context.Context, t Todo) bool {
	if t.PlanID == "" || t.Supervision == nil || t.Supervision.assigned() || !s.isLeader(ctx, t.Supervision.Supervisor) {
		return false
	}
	p, err := s.loadPlan(ctx, t.PlanID)
	if err != nil {
		return false
	}
	for _, task := range p.Tasks {
		if task.TodoID == t.ID && (task.Supervisor == t.Supervision.Supervisor || p.Conversation == t.Supervision.Supervisor) {
			return true
		}
	}
	return false
}

// effectiveSupervisor resolves current plan/transfer ownership before legacy migration policy, including needs_user.
func (s *Service) effectiveSupervisor(ctx context.Context, cfg Settings, t Todo) string {
	if t.Supervision == nil || t.Supervision.Supervisor == "" {
		return cfg.Main
	}
	if t.Supervision.assigned() {
		return t.Supervision.Supervisor
	}
	if s.legacyLeaderPlanSupervision(ctx, t) {
		return cfg.Main
	}
	return t.Supervision.Supervisor
}

// rehomeSupervisedTodos brings open legacy side-supervised tasks under main without rewriting their review state or
// criteria. Explicit leader assignments and task-leader workers are the two specialized topologies to keep (#5732).
// A legacy background ledger also stays: that conversation may still accept the old task it already supervised.
func (s *Service) rehomeSupervisedTodos(ctx context.Context) {
	main := s.settings(ctx).Main
	if main == "" {
		return
	}
	todos := s.todosOf(ctx, "*")
	web := s.webBase(ctx)
	// A crash may have persisted Supervisor=main without reaching the queue reconciliation below. Recover those
	// orphaned queued signals first; the candidate loop alone would now skip the task as already rehomed.
	s.rehomeMainQueuedSignals(ctx, main, todos, web)
	rehomed := map[string]map[string]bool{} // old conversation → the tasks taken from its queued supervision
	for _, t := range todos {
		if t.DoneAt != nil || t.Supervision == nil {
			continue
		}
		sup := t.Supervision.Supervisor
		if t.Supervision.assigned() {
			continue
		}
		_, _, worker := parseLeaderRef(sup)
		manualLeader := s.isLeader(ctx, sup) && s.effectiveSupervisor(ctx, s.settings(ctx), t) == sup
		capableRuntime := sup != "" && t.Origin == sup && s.runtimeWithSupervisionPolicy(ctx, s.settings(ctx).MachineID, sup)
		if sup == main || worker || manualLeader || capableRuntime || s.isBackground(ctx, sup) {
			continue
		}
		if _, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
			if v.Supervisor == main {
				return false
			}
			v.Supervisor = main
			return true
		}); err != nil {
			s.logf("task %s: rehome supervision: %v", t.ID, err)
			continue
		}
		if rehomed[sup] == nil {
			rehomed[sup] = map[string]bool{}
		}
		rehomed[sup][t.ID] = true
		if t.Origin != main {
			s.taskCard(ctx, main, t.ID, "")
		}
	}
	if len(rehomed) > 0 {
		for sup, ids := range rehomed {
			s.rehomeQueuedSupervision(ctx, sup, ids, todos, web)
		}
	}
}

// rehomeMainQueuedSignals recovers queue entries left behind when a previous process stopped after the supervisor write.
// Structured signals name their tasks directly; a pre-structured message uses the same text-id fallback as normal rehome.
func (s *Service) rehomeMainQueuedSignals(ctx context.Context, main string, todos []Todo, web string) {
	mainTasks := map[string]bool{}
	for _, t := range todos {
		if t.DoneAt == nil && t.Supervision != nil && t.Supervision.Supervisor == main {
			mainTasks[t.ID] = true
		}
	}
	if len(mainTasks) == 0 {
		return
	}
	r := &s.relays
	r.mu.Lock()
	if !s.relayLocked(ctx) {
		r.mu.Unlock()
		return
	}
	byConversation := map[string]map[string]bool{}
	for conversation, queue := range r.st.Queue {
		if conversation == main || s.isBackground(ctx, conversation) {
			// An entry explicitly queued for the configured legacy background is intentional compatibility delivery,
			// not an orphaned side signal left by the supervisor write.
			continue
		}
		for _, msg := range queue {
			if msg.Tool != superviseTool {
				continue
			}
			if signals, structured := decodeDigestSignals(msg.Signals); structured {
				for _, g := range signals {
					if mainTasks[g.Todo] {
						if byConversation[conversation] == nil {
							byConversation[conversation] = map[string]bool{}
						}
						byConversation[conversation][g.Todo] = true
					}
				}
				continue
			}
			for id := range mainTasks {
				if strings.Contains(msg.Text, id) {
					if byConversation[conversation] == nil {
						byConversation[conversation] = map[string]bool{}
					}
					byConversation[conversation][id] = true
				}
			}
		}
	}
	r.mu.Unlock()
	for old, ids := range byConversation {
		s.rehomeQueuedSupervision(ctx, old, ids, todos, web)
	}
}

// rehomeQueuedSupervision takes the rehomed tasks' signals out of an old side conversation's durable queue and gathers
// them again under their new supervisor. Queue admission already acknowledged their outbox entries, and their old dedup
// reservation would otherwise acknowledge the replay without delivering it. Other tasks in a mixed digest are regathered
// too, exactly as dropQueuedSupervisionTasks preserves them for /stop (#5732).
func (s *Service) rehomeQueuedSupervision(ctx context.Context, old string, ids map[string]bool, known []Todo, web string) {
	b := &s.supervise
	b.dedupMu.Lock()
	defer b.dedupMu.Unlock()
	b.mu.Lock()
	b.init()
	var memory, remaining []superviseSignal
	for _, g := range b.queued[old] {
		if ids[g.Todo] {
			memory = append(memory, g)
		} else {
			remaining = append(remaining, g)
		}
	}
	var replay []superviseSignal
	_, _ = s.dropQueuedSupervisionTasksAtomic(old, ids, known, func(pending map[string]superviseSignal, queued, regather []superviseSignal) bool {
		replay = mergeSignals(regather, remaining)
		replay = mergeSignals(replay, queued)
		replay = mergeSignals(replay, memory)
		for _, g := range pending { // an earlier attempt may already have committed the replay before its supervisor write
			if ids[g.Todo] {
				replay = mergeSignals(replay, []superviseSignal{g})
			}
		}
		dirty := false
		for _, g := range replay {
			cur, ok := b.pending[g.id()]
			if !ok {
				// First-load rehoming precedes the normal outbox restore. A newer durable stretch with the same id
				// must win over this older queued replay rather than being overwritten before that restore reads it.
				cur, ok = pending[g.id()]
			}
			if !ok {
				cur = g
			}
			if old, exists := pending[g.id()]; !exists || old.Key != cur.Key {
				dirty = true
			}
			b.pending[g.id()] = cur
			pending[g.id()] = cur
		}
		return dirty
	})
	if len(memory) > 0 {
		b.setQueuedLocked(old, nil)
	}
	for _, g := range replay {
		delete(b.sent, g.id())
	}
	b.mu.Unlock()

	if len(replay) > 0 {
		s.forgetSupervision(ctx, replay, web)
		b.mu.Lock()
		s.scheduleDigestLocked(time.Now())
		b.mu.Unlock()
	}
}

// digestTo routes supervision to the persisted planning/transfer assignment. Explicit escalations and card rungs
// keep their special target; unmarked legacy tasks retain their compatibility policy. Task
// leaders' Agent-supervised workers are handled by leaderRoute before this function.
func (s *Service) digestTo(ctx context.Context, cfg Settings, g superviseSignal) string {
	// Explicit escalations keep their destination; needs_user alone is not an escalation.
	if t, ok := s.todoNow(g.Todo); ok && t.Supervision != nil && t.Supervision.assigned() &&
		g.Kind != sigAsk && g.Kind != sigEscalated {
		return s.effectiveSupervisor(ctx, cfg, t)
	}
	own := s.ownSupervisor(cfg)
	if g.Kind == sigTodoReady || g.Kind == sigParentCloseout {
		// Dependent readiness and parent closeout wakes go to the owning supervisor conversation (Leader or Main chat),
		// never to the background supervisor.
		if g.To != "" && g.To != cfg.BackgroundSupervisor {
			return g.To
		}
		return cfg.Main
	}
	if g.To != "" {
		switch {
		case (g.Kind == sigDecide || g.Kind == sigCheck) && g.To == cfg.Main:
			// A task leader's decision, or the owner's answer to a check's decision on its worker, goes to the conversation
			// above it. ownSupervisor is main for new work (#5732); only legacy queued work keeps a background route.
			return own
		case g.To == cfg.BackgroundSupervisor && g.To != own &&
			(cfg.MainSupervisesOwnTasks != nil && *cfg.MainSupervisesOwnTasks || cfg.BackgroundSupervisorMachineID != cfg.MachineID):
			return cfg.Main // a legacy card named a background supervisor that was switched off or moved
		}
		return g.To
	}
	switch g.Kind {
	case sigEscalated, sigStarted, sigLeaderStuck:
		return cfg.Main
	}
	if t, ok := s.todoNow(g.Todo); ok && t.Supervision != nil {
		sup := s.effectiveSupervisor(ctx, cfg, t)
		if sup != cfg.Main && slices.Contains(cfg.Leaders, sup) {
			if t.Supervision.state(t) == reviewNeedsUser {
				return cfg.Main
			}
			return sup
		}
		if g.Kind == sigFailed {
			// Only an explicitly assigned manual leader keeps this route. New task_plan work, including a capable-runtime
			// side chat's, still uses main's failed-start remedies (#5732).
			return cfg.Main
		}
		// The explicit capable-runtime topology is the one side-chat exception to universal main supervision.
		if sup != cfg.Main && sup == t.Origin && !s.isLeader(ctx, sup) && s.runtimeWithSupervisionPolicy(ctx, cfg.MachineID, sup) {
			return sup
		}
	}
	return cfg.Main
}

// runtimeSupervisorFallback is the existing background/main compatibility route for work whose runtime supervisor
// cannot receive policy. It is not the supervisor for ordinary new tasks: ownSupervisor deliberately keeps those on main.
func (s *Service) runtimeSupervisorFallback(ctx context.Context, cfg Settings) string {
	if id := cfg.BackgroundSupervisor; s.backgroundOn(cfg) && id != "" && id != cfg.Main &&
		cfg.BackgroundSupervisorMachineID == cfg.MachineID && !s.runtimeWithoutSupervisionPolicy(ctx, cfg.MachineID, id) {
		return id
	}
	return cfg.Main
}

// digestRuntimeTarget applies the runtime policy boundary to a target chosen by digestTo or recheckDigestTo. A main
// target moves to the compatibility route only for a task handed out by an incompatible runtime; ordinary main work
// never inherits the legacy background route.
func (s *Service) digestRuntimeTarget(ctx context.Context, cfg Settings, g superviseSignal, to string) string {
	if to == "" || to == cfg.BackgroundSupervisor {
		return to
	}
	if t, ok := s.todoNow(g.Todo); ok && t.Supervision.assigned() && to == t.Supervision.Supervisor && to != cfg.Main && s.runtimeWithoutSupervisionPolicy(ctx, cfg.MachineID, to) {
		return "" // retain the assigned lead's work until it can receive policy; never route it to main
	}
	runtimeOrigin := false
	if t, ok := s.todoNow(g.Todo); ok {
		runtimeOrigin = s.runtimeWithoutSupervisionPolicy(ctx, cfg.MachineID, t.Origin)
	}
	if to == cfg.Main && !runtimeOrigin {
		return to
	}
	if to != cfg.Main && !s.runtimeWithoutSupervisionPolicy(ctx, cfg.MachineID, to) {
		return to
	}
	if g.Kind == sigFailed || g.Kind == sigTodoReady || g.Kind == sigParentCloseout {
		return cfg.Main // the background supervisor has no start remedies and never receives todo_ready or parent_closeout wakes
	}
	own := s.runtimeSupervisorFallback(ctx, cfg)
	s.logf("supervise: %s runs on a runtime that cannot take supervision policy: its signal goes to %s", short(to), short(own))
	return own
}

// digestTarget is digestTo with the runtime policy boundary applied at delivery time.
func (s *Service) digestTarget(ctx context.Context, cfg Settings, g superviseSignal) string {
	return s.digestRuntimeTarget(ctx, cfg, g, s.digestTo(ctx, cfg, g))
}

// escalatedIn: one of the entries is a leader's needs_user.
func escalatedIn(list []prompts.SuperviseEntry) bool {
	return slices.ContainsFunc(list, func(e prompts.SuperviseEntry) bool { return e.Kind == sigEscalated })
}

// askedIn: one of the entries is a card that came to the supervisor before the user (ask_route.go).
func askedIn(list []prompts.SuperviseEntry) bool {
	return slices.ContainsFunc(list, func(e prompts.SuperviseEntry) bool { return e.Kind == sigAsk })
}

// escalateToMain tells the main conversation that leader task t needs the user (task_review needs_user from its leader):
// why, and the option the leader recommends. The user's decision card goes out as for any needs_user.
func (s *Service) escalateToMain(t Todo, round int, why, recommend string) {
	s.signal(superviseSignal{Todo: t.ID, Kind: sigEscalated, Key: fmt.Sprintf("needs_user@%d@%s", round, time.Now().UTC().Format(time.RFC3339Nano)),
		Detail: clip(oneLine(why), 200), Ask: clip(oneLine(recommend), 200)})
}

// workerRefusal refuses task_plan and agent_start from an agent handed out as a task's agent: it does the work itself,
// with its own in-session subagents, so tasks do not spawn tasks without end. The main conversation and leaders hand
// out freely.
func (s *Service) workerRefusal(ctx context.Context, machine, self string) error {
	if self == "" || s.supervises(ctx, self) {
		return nil
	}
	if s.devs != nil {
		machine = s.devs.Canonical(machine) // as the task's agents are kept
	}
	t, ok := s.taskOf(ctx, machine, self)
	if !ok {
		return nil
	}
	return fmt.Errorf("you are the agent of task %s (%s): an agent handed out as a task does not hand out Persona tasks "+
		"(task_plan, agent_start). Do the work here, with your own in-session subagents if it splits; if it needs another "+
		"task, say so to your supervisor (report_to_supervisor)", t.ID, t.Title)
}

// ---- assistant.leaders ----

func (s *Service) getLeaders(ctx context.Context) (string, error) {
	list := s.settings(ctx).Leaders
	if len(list) == 0 {
		return "[] (none: only the main conversation supervises)", nil
	}
	raw, _ := json.Marshal(list)
	return string(raw), nil
}

// setLeaders takes the whole list of leader conversations: each an assistant conversation of the assistant's machine
// other than the main one.
func (s *Service) setLeaders(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	var in []string
	if err := json.Unmarshal(v, &in); err != nil {
		return nil, errors.New("value must be a list of conversation ids ([] for none)")
	}
	cfg := s.settings(ctx)
	list, err := s.leaderList(ctx, cfg, in)
	if err != nil {
		return nil, err
	}
	before := cfg.Leaders
	if sameLeaders(list, before) {
		return &ownChange{summary: "assistant.leaders did not change"}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	shown := func(l i18n.Lang, ids []string) string { return clipped(l, strings.Join(ids, ", ")) }
	// The approval compares the whole list the card was raised against with the current one, not the card's text
	// (#11392): the card clips both lists to 120 characters, three ids and a bit, so with four or more leaders the
	// owner's pin or unpin after them left the text as it was, and approving wrote the card's list over it.
	return &ownChange{applyChecksProposal: true, cardText: func(l i18n.Lang) string {
		return i18n.T(l, "assistant-settings_tool.change_leaders", i18n.Args{"before": shown(l, before), "after": shown(l, list)})
	},
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.leaderList(ctx, s.settings(ctx), in); err != nil {
				return "", fmt.Errorf("%w: validate the approved operation against current state: %v", approvalops.ErrNotApplied, err)
			}
			if _, err := s.updateSettings(ctx, func(v *Settings) error {
				if !sameLeaders(v.Leaders, before) {
					return fmt.Errorf("assistant.leaders changed after the card was shown, so nothing was saved: get it and set it again for a new card (%w)", approvalops.ErrStale)
				}
				v.Leaders = list
				return nil
			}); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			return fmt.Sprintf("assistant.leaders is %q: a manually started Agent the owner explicitly puts under one keeps its ordinary "+
				"supervision there. A direction lead supervises its own task_plan work, including failed starts, retry and review; "+
				"main supervises its own tasks and receives the lead's owner-decision escalations.", list), nil
		}}, nil
}

// leaderList is in as assistant.leaders would keep it with settings cfg: trimmed, without blanks or repeats, each a
// Persona conversation on the assistant's machine other than the main one and the background supervisor, at most
// leadersMost.
func (s *Service) leaderList(ctx context.Context, cfg Settings, in []string) ([]string, error) {
	list := []string{}
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(list, id) {
			continue
		}
		if id == cfg.Main {
			return nil, errors.New("the main conversation supervises already: a leader is a side conversation")
		}
		if id == cfg.BackgroundSupervisor {
			return nil, errors.New("the background supervisor is built in and follows up the tasks no leader supervises: a leader is another side conversation")
		}
		if c, ok := s.session(ctx, cfg.MachineID, id); !ok || !c.Assistant {
			return nil, fmt.Errorf("%s is not a Persona conversation on the assistant's machine (conversations_list has their ids)", id)
		}
		list = append(list, id)
	}
	if len(list) > leadersMost {
		return nil, fmt.Errorf("at most %d leader conversations", leadersMost)
	}
	return list, nil
}

// sameLeaders: lists a and b name the same leaders in the same order (nil and [] are both none).
func sameLeaders(a, b []string) bool {
	return slices.Equal(a, b) || len(a) == 0 && len(b) == 0
}

// ---- a cancelled task's agents ----

// cancelStopWait is how long a cancelled task's closed agent may still read busy before it is left unarchived;
// cancelStopPoll how often it is looked at meanwhile.
var cancelStopWait, cancelStopPoll = 30 * time.Second, 500 * time.Millisecond

// stopTaskAgents stops what task t's agents still ask of the user once t is cancelled: each agent on it is closed on
// its computer (session.close: its tab and process go, its transcript stays, and where its engine can resume it a
// message does), its
// cards still waiting end, and it is archived (reversible, nothing deleted). It runs in the background: closing can
// take a while and the review is answered first.
func (s *Service) stopTaskAgents(ctx context.Context, t Todo) {
	var agents []TodoAgent
	for _, a := range t.Agents {
		if a.ReleasedAt == nil && a.DismissedAt == nil && a.SessionID != "" {
			agents = append(agents, a)
		}
	}
	if len(agents) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		for _, a := range agents {
			s.stopCancelledAgent(ctx, t.ID, a.MachineID, a.SessionID)
		}
	}()
}

// stopCancelledAgent stops, ends the cards of and archives one agent of cancelled task id; one that works for another
// open task now, an assistant conversation (the user's to stop) or a session its machine does not list is left alone.
// The agent quits and its terminal stays (session.stop, #4754); a session with no terminal (another tool's, an engine
// that runs agents itself) is closed as before.
func (s *Service) stopCancelledAgent(ctx context.Context, id, machine, session string) {
	s.todos.mu.RLock()
	other, linked := s.todos.activeBySession[[2]string{machine, session}]
	s.todos.mu.RUnlock()
	if linked && other != id {
		s.logf("task %s cancelled: agent %s/%s works for %s now, left alone", id, machine, short(session), other)
		return
	}
	c, err := s.agentArchiveTarget(ctx, machine, session) // refuses an assistant conversation and an unknown session
	if err != nil {
		s.logf("task %s cancelled: agent %s/%s left alone: %v", id, machine, short(session), err)
		return
	}
	machine = c.MachineID
	closed := false
	if !agentCloseBusy(c) && s.closeEndedTerminals(ctx) {
		// A cancelled task ends its idle Agent's session: with the owner's close-on-end setting its terminal goes
		// too (#10355); a busy one is only stopped below and keeps its terminal.
		var err error
		if closed, err = s.closeEndedTerminal(ctx, machine, session, proto.StopUser); err != nil {
			s.logf("task %s cancelled: close agent %s/%s: %v", id, machine, short(session), err)
		}
	}
	if c.Status != proto.StatusOffline && !closed {
		cctx, cancel := context.WithTimeout(ctx, agentArchiveCloseTimeout)
		err := s.call(cctx, machine, deviceSessionStop, map[string]any{"session_id": session}, nil)
		if re := (*rpc.Error)(nil); errors.As(err, &re) && re.Code == "no_terminal" {
			err = s.call(cctx, machine, "session.close", map[string]any{"session_id": session}, nil)
		}
		if err != nil {
			s.logf("task %s cancelled: stop agent %s/%s: %v", id, machine, short(session), err)
		}
		cancel()
	}
	s.endSessionCards(machine, session)
	// The archive waits for the list to show it closed: a row still busy there (working, a card waiting) would bring
	// it straight back (archivedSessions).
	var row proto.Session
	for deadline := time.Now().Add(cancelStopWait); ; time.Sleep(cancelStopPoll) {
		r, err := s.agentArchiveTarget(ctx, machine, session)
		if err != nil {
			s.logf("task %s cancelled: archive agent %s/%s: %v", id, machine, short(session), err)
			return
		}
		if !agentArchiveBusy(r) {
			row = r
			break
		}
		if time.Now().After(deadline) {
			s.logf("task %s cancelled: agent %s/%s still busy, left unarchived", id, machine, short(session))
			return
		}
	}
	changedAt, err := s.noteAgentArchiveAt(ctx, row, true)
	if err != nil {
		s.logf("task %s cancelled: archive agent %s/%s: %v", id, machine, short(session), err)
		return
	}
	row.ArchiveChangedAt = archiveChangedAt(changedAt)
	publishMark, err := s.noteAgentArchiveClosed(ctx, row, time.Now().UTC(), false) // what it writes as it goes is not new work
	if err != nil {
		s.logf("task %s cancelled: archive agent %s/%s: %v", id, machine, short(session), err)
		s.publishList(row.MachineID) // the archive itself committed; secondary metadata failure must not hide it
	} else if publishMark != 0 {
		s.publishListAtArchiveMark(row.MachineID, publishMark)
	}
	s.logf("task %s cancelled: closed and archived agent %s/%s", id, machine, short(session))
}

// endSessionCards ends the cards machine/session still waits on (cancelled in the history), as when its node goes away.
func (s *Service) endSessionCards(machine, session string) {
	s.fleet.mu.Lock()
	var ids []string
	for id, p := range s.fleet.interactions {
		if p.ix.SessionID == session && s.sameMachine(p.ix.MachineID, machine) && (p.prepared != nil || !p.claimed) {
			if p.prepared == nil {
				p.claimed = true
			}
			ids = append(ids, id)
		}
	}
	s.fleet.mu.Unlock()
	s.feishuDetach(ids)
	for _, id := range ids {
		if s.endPreparedByID(id, "cancelled", "The conversation that owned this approval ended; nothing changed.") {
			continue
		}
		s.recordEnd(id, "cancelled", nil)
		s.finishInteraction(id)
	}
}
