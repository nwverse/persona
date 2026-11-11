package assistant

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The background supervisor (#4927, docs/assistant/unified-entry.md "Background supervisor"). One built-in assistant
// conversation follows up every supervised task no leader supervises, so the main conversation stays free for the
// owner: the supervision messages the main conversation got for a task's own signals go to it (digestTo), and it
// reviews the tasks with task_review as a leader does. It is made on first use on the assistant's machine, stays
// listed (keptSessions), is no leader (not in assistant.leaders, no direction, no slot of the 20) and cannot be
// archived. On by default; assistant.main_supervises_own_tasks turns it off, and signals raised from then on go to the
// main conversation again.
//
// Nothing is written on the tasks for it: a task's ledger keeps the main conversation as its supervisor and the task's
// origin stays where it was planned. So task cards, the dock, receipts, the to-do events and the direction tree (tasks
// without a leader sit under Persona) are what they were, the main conversation's own right to review is the rule it
// always had, a switch of the setting moves every open task at once, and a background supervisor that is gone leaves
// no task naming a conversation that does not exist. Who gets a task's signals is decided when they are sent
// (ownSupervisor), and what the background supervisor may do to a task follows the same rule (backgroundSupervises).

// purposeSupervise is the model purpose the background supervisor runs under; it inherits the main chat's.
const purposeSupervise = "assistant.supervise"

// backgroundGrace is how long after its start the background supervisor counts as there without its machine listing
// it yet; backgroundRetry how long after a start that failed the next one is tried (its tasks' signals go to the main
// conversation meanwhile).
var backgroundGrace, backgroundRetry = 2 * time.Minute, 10 * time.Minute

// backgroundBook holds the background supervisor's start still: one at a time, and not again right after a failure.
type backgroundBook struct {
	mu       sync.Mutex
	failedAt time.Time
}

// backgroundOn: the background supervisor is on with settings cfg (assistant.main_supervises_own_tasks is not on).
func (s *Service) backgroundOn(cfg Settings) bool {
	return !s.noBackground && (cfg.MainSupervisesOwnTasks == nil || !*cfg.MainSupervisesOwnTasks)
}

// mainSupervisesOwnTasks is assistant.main_supervises_own_tasks in effect: the main conversation follows up its own
// tasks itself (off by default: the background supervisor does).
func (s *Service) mainSupervisesOwnTasks(ctx context.Context) bool {
	return !s.backgroundOn(s.settings(ctx))
}

// ownSupervisor is the conversation that gets the signals of the tasks no leader supervises, with settings cfg: the
// background supervisor while it is on and there is one on the assistant's machine, else the main conversation.
func (s *Service) ownSupervisor(cfg Settings) string {
	if id := cfg.BackgroundSupervisor; s.backgroundOn(cfg) && id != "" && id != cfg.Main && cfg.BackgroundSupervisorMachineID == cfg.MachineID {
		return id
	}
	return cfg.Main
}

// isBackground: conversation id is the one kept as the background supervisor, on or off. It stays listed, is neither
// archived nor made a leader, and may finish what it was sent before the setting was switched off.
func (s *Service) isBackground(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	cfg := s.settings(ctx)
	return id != cfg.Main && cfg.BackgroundSupervisor == id
}

// backgroundSupervises: conversation self is the background supervisor and task t is one whose signals it gets: a
// supervised task whose ledger names no current leader conversation and no task leader's agent. It may review it,
// change its scope, answer and escalate its cards and speak to its agent as the task's own supervisor does.
func (s *Service) backgroundSupervises(ctx context.Context, self string, t Todo) bool {
	if t.Origin == "" || t.Supervision == nil || !s.isBackground(ctx, self) {
		return false
	}
	sup := t.Supervision.Supervisor
	return !s.isLeader(ctx, sup) && !s.isLeaderRef(sup)
}

// ensureBackground is the background supervisor to send to now, made if there is none yet or the one there was is
// gone; "" while it is off, there is no main conversation or machine, or it cannot start (its tasks' signals then go
// to the main conversation, and the start is tried again after backgroundRetry). gone is the one it found gone (on
// another machine than the assistant's now, or no longer listed), whose queued message the caller takes back
// (backgroundLost).
func (s *Service) ensureBackground(ctx context.Context) (id, gone string) {
	b := &s.background
	b.mu.Lock()
	defer b.mu.Unlock()
	cfg := s.settings(ctx)
	if !s.backgroundOn(cfg) || cfg.Main == "" || cfg.MachineID == "" {
		return "", ""
	}
	if id := s.ownSupervisor(cfg); id != cfg.Main {
		if s.backgroundThere(ctx, cfg, id) {
			return id, ""
		}
		s.logf("supervise: the background supervisor %s is gone from %s", short(id), cfg.MachineID)
	}
	if cfg.BackgroundSupervisor != cfg.Main {
		gone = cfg.BackgroundSupervisor
	}
	if !b.failedAt.IsZero() && time.Since(b.failedAt) < backgroundRetry {
		return "", gone
	}
	id, err := s.startBackground(ctx)
	if err != nil {
		b.failedAt = time.Now()
		s.logf("supervise: the background supervisor did not start: %v; its tasks' signals go to the main conversation", err)
		return "", gone
	}
	b.failedAt = time.Time{}
	return id, gone
}

// backgroundLost takes back what was still queued for background supervisor old, which is gone: its relay queue is
// dropped, and the signals of the supervision message that waited there are returned with their deduplication entries
// forgotten, so they are told again where they go now. dedupMu is held.
func (s *Service) backgroundLost(ctx context.Context, old, web string) []superviseSignal {
	if old == "" {
		return nil
	}
	b := &s.supervise
	b.mu.Lock()
	lost := b.queued[old]
	b.setQueuedLocked(old, nil)
	b.mu.Unlock()
	r := &s.relays
	r.mu.Lock()
	if s.relayLocked(ctx) && len(r.st.Queue[old]) > 0 && !r.draining[old] {
		delete(r.st.Queue, old)
		s.saveRelayLocked()
	}
	r.mu.Unlock()
	if len(lost) > 0 {
		s.logf("supervise: %d signal(s) queued for the background supervisor %s, which is gone, are told again", len(lost), short(old))
		s.forgetSupervision(ctx, lost, web)
	}
	return lost
}

// backgroundStranded, from the watch loop: a supervision message still queued for a background supervisor that is
// gone would wait for ever when no new signal comes for it. Its signals go back among the pending ones, and the next
// digest sends them to a new background supervisor, or to the main conversation.
func (s *Service) backgroundStranded(ctx context.Context) {
	cfg := s.settings(ctx)
	id := cfg.BackgroundSupervisor
	b := &s.supervise
	b.mu.Lock()
	waiting := id != "" && len(b.queued[id]) > 0
	b.mu.Unlock()
	if !waiting || cfg.BackgroundSupervisorMachineID == cfg.MachineID && s.backgroundThere(ctx, cfg, id) {
		return
	}
	b.dedupMu.Lock()
	defer b.dedupMu.Unlock()
	lost := s.backgroundLost(ctx, id, s.webBase(ctx))
	if len(lost) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	for _, g := range lost {
		if b.sent[g.id()] == g.Key {
			delete(b.sent, g.id())
		}
		if _, newer := b.pending[g.id()]; !newer {
			b.pending[g.id()] = g
		}
	}
	s.retryDigestLocked(s.superviseTimings().urgent)
}

// backgroundThere: background supervisor id is still a conversation of the assistant's machine. One started within
// backgroundGrace is (its machine may not list it yet), and so is one whose machine cannot be asked now: what is sent
// to it queues until the machine is back. Only a machine that answers without it says it is gone.
func (s *Service) backgroundThere(ctx context.Context, cfg Settings, id string) bool {
	if s.backgroundFresh(cfg, id) {
		return true
	}
	var res struct {
		Session proto.Session `json:"session"`
		OK      bool          `json:"ok"`
	}
	if err := s.call(ctx, cfg.MachineID, DeviceSessionGet, map[string]any{"session_id": id}, &res); err != nil {
		return true
	}
	return res.OK && res.Session.Assistant
}

// backgroundFresh: conversation id is the background supervisor, started within backgroundGrace.
func (s *Service) backgroundFresh(cfg Settings, id string) bool {
	at := cfg.BackgroundSupervisorAt
	return id != "" && id == cfg.BackgroundSupervisor && at != nil && time.Since(*at) < backgroundGrace
}

// startBackground starts the background supervisor on the assistant's machine with what it is for
// (prompts/assistant/supervisor_start), under its own model purpose, and keeps its id in the settings (replacing one
// that is gone). It is listed with the scheduled tasks' conversations (origin task), titled in the owner's language,
// and its turns are held from the pushes as a supervision message's are.
func (s *Service) startBackground(ctx context.Context) (string, error) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Second)
	defer cancel()
	c, err := s.startConversation(sctx, startParams{Text: prompts.RenderSupervisorStart(prompts.SupervisorStart{Owner: s.owner(ctx)}),
		Purpose: purposeSupervise, Origin: OriginTask, Source: &proto.MessageSource{Kind: proto.SourceSupervision}})
	if err != nil {
		return "", err
	}
	s.holdPushes(sctx, c.SessionID)
	now := time.Now().UTC()
	if _, err := s.updateSettings(sctx, func(v *Settings) error {
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID, v.BackgroundSupervisorAt = c.SessionID, c.MachineID, &now
		return nil
	}); err != nil {
		return "", err
	}
	s.titleSet(c.MachineID, c.SessionID, i18n.T(s.lang(ctx), "assistant-conversations.background_supervisor"), titleByAssistant)
	s.logf("supervise: started the background supervisor %s on %s", short(c.SessionID), c.MachineID)
	s.settingsChanged(sctx)
	return c.SessionID, nil
}

// backgroundNeeded: one of the signals list would go to the background supervisor if there were one (digestTo).
func (s *Service) backgroundNeeded(cfg Settings, list []superviseSignal) bool {
	const probe = "\x00background"
	cfg.BackgroundSupervisor, cfg.BackgroundSupervisorMachineID = probe, cfg.MachineID
	for _, g := range list {
		if _, handled, _ := s.leaderRoute(g); !handled && s.digestTo(cfg, g) == probe {
			return true
		}
	}
	return false
}

// backgroundModel puts what the background supervisor runs on (purposeSupervise) on params, a message to it: a change
// of that purpose's model applies from its next message, as the main chat's does. params are left as they are when
// the purpose cannot be resolved: the conversation then goes on with what it ran last.
func (s *Service) backgroundModel(ctx context.Context, params map[string]any) {
	p := maps.Clone(params)
	if err := s.applyModel(ctx, purposeSupervise, p); err != nil {
		s.logf("supervise: the background supervisor's model: %v; it keeps what it ran last", err)
		return
	}
	maps.Copy(params, p)
}

// announceBackground: the main conversation is told once that the background supervisor follows up its tasks (a
// default never changes without a word): the background supervisor's id the first time it is asked once there is one,
// "" before and after.
func (s *Service) announceBackground(ctx context.Context) string {
	cfg := s.settings(ctx)
	id := s.ownSupervisor(cfg)
	if id == cfg.Main || cfg.BackgroundSupervisorAnnounced {
		return ""
	}
	if _, err := s.updateSettings(ctx, func(v *Settings) error { v.BackgroundSupervisorAnnounced = true; return nil }); err != nil {
		s.logf("supervise: note the background supervisor's announcement: %v", err)
		return "" // told once it can be noted, never twice
	}
	return id
}
