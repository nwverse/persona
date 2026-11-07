package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// What comes from the nodes is the manager's (docs/server/machines.md "Who handles the computers' events"). The hub did all of this until the
// manager connected the computers itself; it moved here as it was (fleet_*.go):
//
//   - the interactions (approval / question / plan / screen / Codex cards): made from the nodes' interaction.create
//     and what their sessions show, kept, waited for, answered, expired (fleet_ix.go);
//   - each Agent's state as the apps get it — the unified state, "Needs you", "Unread" / "Seen", the work and todos it shows —
//     and the session lists (this file);
//   - the pushes and the Live Activity's content: when a finished turn is worth a push, a card's push, what the
//     activity shows (fleet_push.go, fleet_live.go);
//   - the nodes' MCP tools and assistant.lark (fleet_tools.go).
//
// The hub is told what the apps need (assistantwire fleet.*: the lists, the pending interactions, the fleet, the
// pushes to deliver) and keeps the last of them while the manager is away; it relays the apps' answers here
// (fleetRoutes) and delivers the pushes (APNs, the relay, its apps' notify events).

// fleetBook is what the manager keeps of it.
type fleetBook struct {
	mu           sync.Mutex
	interactions map[string]*pending
	seen         map[[2]string]time.Time // (machine, session) -> when the user last looked at it
	seenLoaded   bool                    // seen holds the table's times (loadSeen succeeded once)
	stateSince   map[[2]string]stateMark // (machine, session) -> its state and when it entered it
	notified     map[string]time.Time    // assistant conversation -> when it last pushed with notify_user
	// reportSent: assistant conversation -> when a scheduled run or a task notice was sent into it (noteReportTurn):
	// the turn it starts is pushed however short it is.
	reportSent map[string]time.Time
	// codexAnnounced: Codex card id -> when it was announced (fleet_ix.go)
	codexAnnounced map[string]time.Time
	// codexEnded: Codex card id -> when the node stopped listing it (fleet_ix.go)
	codexEnded map[string]time.Time
	// screenAnswered: screen card id -> when it was answered (fleet_ix.go screenJustAnsweredLocked)
	screenAnswered map[string]answeredScreen
	// screenDetached: screen card id -> the card its node's connection ended while it was open (fleet_ix.go
	// screenBackLocked): read again off the same dialog once the node is back, it is that card, not a new question.
	screenDetached map[string]screenGone
	// screenHandedBack: screen card id -> the card handed back to the computer (fleet_ix.go screenHandedBackLocked):
	// the same dialog still on screen gets no new card until the agent moves on (#4867).
	screenHandedBack map[string]screenGone
	// upgradeEvery / upgradeFor override screenUpgradeEvery / screenUpgradeFor (0: those; upgradeScreenCard). Tests
	// shorten them on their own Service before any session list comes, so an upgrade loop another test left running
	// never reads what this one writes (#4554).
	upgradeEvery, upgradeFor time.Duration
	// hooked: Persona's hooks run in the session (a hook event came since the manager started).
	hooked map[[2]string]bool
	// stopSig: the signature of the reply each session's latest Stop ended its turn with; pushedSig: the one a
	// "finished" push was already sent for, cleared when the next prompt comes (stopDuplicate).
	stopSig, pushedSig map[[2]string]string
	turns              *turnTracker // "machine/session" -> the turn in progress, for the finished push
	clients            assistantwire.Clients
	prev               map[string][]proto.Session // each machine's list setSessions saw last

	// sent: each machine's last session list told the hub (fleet.sessions), as JSON. sentMu is held while a list is
	// decorated and told (publishList), so the lists reach the hub in the order they were made.
	sentMu sync.Mutex
	sent   map[string]string
	// shown: the list last told the hub for each machine, decorated (the reminders of agents awaiting a reply read it
	// instead of decorating every list each minute, agents_reply.go).
	shownMu sync.Mutex
	shown   map[string][]proto.Session

	// rep is the last snapshot's parts the session lists show (fleetTakeSnapshot).
	rep fleetReport

	mutesMu sync.Mutex
	mutes   *Pins
	// mutes are the hub's, as fleet.mutes handed them over last (nil: not yet).

	askMu sync.Mutex
	asked map[string]*askedOne // the assistant's own questions and cards on the phone (by interaction id)

	// reanswered: the "Change answer" messages sent to agents, by interaction and answers (fleet_delegate.go): each goes once.
	reanswered map[string]bool

	undelivered heldActs // answers the manager's own Action did not take yet

	live liveState // the Live Activity's content as last told (fleet_live.go)

	started sync.Once

	q        fleetQueue // what the nodes' events start for the assistant, in order (fleet_follow.go)
	followMu sync.Mutex
	follows  map[[2]string]bool // the conversations the channels follow (fleet_follow.go)
	// unsettled: follows the devices did not take (fleet_follow.go): the next follow asks them again
	unsettled map[[2]string]bool
}

// fleetReport is what the session lists are decorated with from the assistant's snapshot.
type fleetReport struct {
	mu       sync.RWMutex
	got      bool
	sessions map[string]SessionView // by session id
	work     map[[2]string]proto.WorkState
	workOn   bool
	links    map[[2]string]TodoLink
	quiet    map[[2]string]bool
	marks    map[[2]string][]string  // conversation → the channels its messages came from, newest first
	turns    map[[2]string]time.Time // conversation → when a channel's turn in it was answered (zero: followed now)
	inbox    map[string]bool         // the inbox agents' conversations
	tasks    []TaskView              // the scheduled tasks (nil: not read yet)
	profile  *Profile                // its settings as the hub's features need them (nil: not read yet)
}

// fleetStore is what of the store the fleet keeps (db.DB); a store without it keeps nothing.
type fleetStore interface {
	InsertInteraction(ctx context.Context, ix proto.Interaction) error
	FinishInteraction(ctx context.Context, id, status string, res *proto.Resolution) error
	InteractionOutcome(ctx context.Context, id string) (status string, res *proto.Resolution, err error)
	Interactions(ctx context.Context, machine, session string, limit int) ([]db.InteractionRecord, error)
	AllowAlways(ctx context.Context, session, tool string, keys []string) error
	AlwaysAllowed(ctx context.Context, session string, keys []string) (bool, error)
	MarkSeen(ctx context.Context, machine, session string, at time.Time) error
	SeenTimes(ctx context.Context) (map[[2]string]time.Time, error)
}

func (s *Service) fleetStore() fleetStore {
	st, ok := s.store().(fleetStore)
	if !ok {
		return nil
	}
	return guardedStore{st}
}

// guardedStore is a store whose fleet methods may not be there after all (a test's store that embeds the interface
// and keeps only some tables): one that is not answers an error, as a database that failed.
type guardedStore struct{ st fleetStore }

var errNoTable = errors.New("the store does not keep this")

func guard(err *error) {
	if recover() != nil {
		*err = errNoTable
	}
}

func (g guardedStore) InsertInteraction(ctx context.Context, ix proto.Interaction) (err error) {
	defer guard(&err)
	return g.st.InsertInteraction(ctx, ix)
}

func (g guardedStore) FinishInteraction(ctx context.Context, id, status string, res *proto.Resolution) (err error) {
	defer guard(&err)
	return g.st.FinishInteraction(ctx, id, status, res)
}

func (g guardedStore) InteractionOutcome(ctx context.Context, id string) (status string, res *proto.Resolution, err error) {
	defer guard(&err)
	return g.st.InteractionOutcome(ctx, id)
}

func (g guardedStore) Interactions(ctx context.Context, machine, session string, limit int) (out []db.InteractionRecord, err error) {
	defer guard(&err)
	return g.st.Interactions(ctx, machine, session, limit)
}

func (g guardedStore) AllowAlways(ctx context.Context, session, tool string, keys []string) (err error) {
	defer guard(&err)
	return g.st.AllowAlways(ctx, session, tool, keys)
}

func (g guardedStore) AlwaysAllowed(ctx context.Context, session string, keys []string) (ok bool, err error) {
	defer guard(&err)
	return g.st.AlwaysAllowed(ctx, session, keys)
}

func (g guardedStore) MarkSeen(ctx context.Context, machine, session string, at time.Time) (err error) {
	defer guard(&err)
	return g.st.MarkSeen(ctx, machine, session, at)
}

func (g guardedStore) SeenTimes(ctx context.Context) (out map[[2]string]time.Time, err error) {
	defer guard(&err)
	return g.st.SeenTimes(ctx)
}

// fleetOn: the manager carries the machines (its process has the devices; unit tests without them have no fleet).
func (s *Service) fleetOn() bool { return s.devs != nil }

// fleetStart starts the fleet's loops, once. The seen times are read again on each call until they could be: a
// database that failed while the manager started does not leave every session the user had looked at unread.
// restoreInteractions stays once: expiring the stale rows later would cancel the ones created since.
func (s *Service) fleetStart(ctx context.Context) {
	if !s.fleetOn() {
		return
	}
	s.fleet.started.Do(func() {
		s.restoreInteractions(ctx)
		go s.expireLoop(ctx)
		go s.liveLoop(ctx)
	})
	s.loadSeen(ctx)
}

// ---- the machines, as the devices have them ----

// fleetMachine is one machine as the moved code needs it; manager is the manager this assistant is (Service.id).
type fleetMachine struct {
	ID, Name, Home, DataDir string
	manager                 string
}

// assistantCwd is this manager's assistant directory on the machine, or "" while it has not reported its home.
func (m fleetMachine) assistantCwd() string {
	return stateDirOn(m.Home, m.DataDir, identity.AssistantDirName(m.manager))
}

// isAssistant: sess is one of this manager's assistant's conversations on the machine (isAssistantSession).
func (m fleetMachine) isAssistant(sess proto.Session) bool {
	return isAssistantSession(sess, m.assistantCwd(), m.manager)
}

// fleetMachines are the machines the devices carry.
func (s *Service) fleetMachines() []fleetMachine {
	if s.devs == nil {
		return nil
	}
	var out []fleetMachine
	for _, d := range s.devs.List() {
		out = append(out, fleetMachine{ID: d.ID, Name: d.Name, Home: d.Home, DataDir: d.DataDir, manager: s.id})
	}
	return out
}

// fleetMachine is the machine id names (an id another one absorbed: that one); false when there is none.
func (s *Service) fleetMachine(id string) (fleetMachine, bool) {
	if s.devs == nil || id == "" {
		return fleetMachine{}, false
	}
	canon := s.devs.Canonical(id)
	for _, m := range s.fleetMachines() {
		if m.ID == canon {
			return m, true
		}
	}
	return fleetMachine{}, false
}

// machineName is the machine's name, its id when there is none.
func (s *Service) machineName(id string) string {
	if m, ok := s.fleetMachine(id); ok && m.Name != "" {
		return m.Name
	}
	return id
}

// nodeCall calls a machine's node directly (the devices).
func (s *Service) nodeCall(ctx context.Context, machine, method string, params, out any) error {
	if s.devs == nil {
		return ErrNoDevice
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	res, err := s.devs.CallNode(ctx, machine, method, raw)
	if err != nil || out == nil || len(res) == 0 {
		return err
	}
	return json.Unmarshal(res, out)
}

// nodeSession is one of a machine's sessions as its node listed it.
func (s *Service) nodeSession(machine, id string) (proto.Session, bool) {
	if s.devs == nil {
		return proto.Session{}, false
	}
	return s.devs.Session(machine, id)
}

// nodeSessions are a machine's sessions as its node listed them.
func (s *Service) nodeSessions(machine string) []proto.Session {
	if s.devs == nil {
		return nil
	}
	list, _ := s.devs.Sessions(machine)
	return list
}

// isAssistantSession identifies manager's headless conversations in its directory dir, or its health space's. A host
// pane is stronger evidence than a shared working directory. Another manager's directory, left on a machine moved
// away from it, is not this one's (docs/assistant/managers.md "Directories on a computer").
func isAssistantSession(sess proto.Session, dir, manager string) bool {
	if sess.PaneID != "" || dir == "" || sess.Cwd == "" {
		return false
	}
	// The working directory may go through a symlinked home; runtimes and offline assistants have no host pane.
	return sess.Cwd == dir || identity.IsAssistantDirOf(sess.Cwd, manager)
}

// ---- the nodes' events and requests ----

// NodeEvent takes a node's event before the hub does (devices.Options.Event); false: the hub does not get it (the
// manager tells it what follows instead).
func (s *Service) NodeEvent(machine, event string, params json.RawMessage) bool {
	if !s.fleetOn() {
		return true
	}
	switch event {
	case "sessions.changed":
		// The devices took this very list just before passing the event on (Machine.onEvent, and BusUp's replay of
		// it): a copy of theirs, not a second decode of the whole list on every change. Its nested slices are shared
		// with theirs, as decorated's are: never written into.
		if s.devs != nil {
			if list, ok := s.devs.Sessions(machine); ok {
				s.setSessions(machine, list)
				return false
			}
		}
		var p struct {
			Sessions []proto.Session `json:"sessions"`
		}
		if json.Unmarshal(params, &p) == nil {
			s.setSessions(machine, p.Sessions)
		}
		return false // the hub gets the list as the apps do (fleet.sessions)
	case "sessions.revs":
		s.syncPlanInteractions(machine)
	case "session.items": // the channels' (fleet_follow.go); the apps get them from the hub
		s.followedItems(machine, params)
	case "session.delta":
		s.followedDelta(machine, params)
	case "interaction.dismiss":
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(params, &p) == nil {
			s.recordEnd(p.ID, "dismissed", nil)
			s.finishInteraction(p.ID)
		}
		return false
	case "interaction.cancel":
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(params, &p) == nil {
			s.recordEnd(p.ID, "cancelled", nil) // or the next manager would take it for one a hook may still claim
			s.finishInteraction(p.ID)
		}
		return false
	case "codex.interactions": // the Codex questions and approvals open on this machine (fleet_ix.go)
		s.syncCodexInteractions(machine, params)
		return false
	case "hook.event":
		var p map[string]any
		if json.Unmarshal(params, &p) == nil {
			if sid, _ := p["session_id"].(string); sid != "" {
				// A report-only hook (node/globalhooks.go) is not Persona's hooks running in the session: it asks for
				// no card, so it must not hold the screen's back (hasHooks). Its Stop still counts for stopDuplicate.
				if p["report_only"] != true {
					s.fleet.mu.Lock()
					if s.fleet.hooked == nil {
						s.fleet.hooked = map[[2]string]bool{}
					}
					s.fleet.hooked[[2]string{machine, sid}] = true
					s.fleet.mu.Unlock()
				}
				s.noteStop(machine, sid, p)
			}
		}
		// the hub shows it to the apps (hook_event)
	}
	return true
}

// NodeRequest answers what of a node's requests is the manager's (devices.Options.Answer, with NodeAnswer's).
func (s *Service) NodeRequest(ctx context.Context, machine, method string, params json.RawMessage) (any, bool, error) {
	if !s.fleetOn() {
		return nil, false, nil
	}
	switch method {
	case "computer.elicit": // LCU asked the person something; the card is the same one as app approval
		if s.ext == nil {
			return map[string]any{"action": "cancel"}, true, nil
		}
		out, err := s.ext.AnswerComputerElicit(ctx, machine, params)
		return out, true, err
	case "interaction.create":
		var p struct {
			Interaction proto.Interaction `json:"interaction"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, true, err
		}
		p.Interaction.MachineID = machine
		// A write through a skill's gated command (node/larkgate.go): its plugin names what the ids are.
		if sum, ok := s.assistantSummary(ctx, p.Interaction.ToolName, p.Interaction.Summary); ok {
			p.Interaction.Summary = sum
		}
		r, err := s.nodeInteraction(ctx, p.Interaction)
		return r, true, err
	case "assistant.lark":
		out, err := s.assistantLark(ctx, machine)
		return out, true, err
	case proto.MethodLarkSent: // a write the gate proxy forwarded and the API took (node/gateproxy.go)
		if s.ext == nil {
			return nil, true, errors.New("lark.sent: no extensions, there is no ledger")
		}
		out, err := s.ext.RecordNodeSent(ctx, machine, params)
		return out, true, err
	case proto.MethodMailAttachment: // mail_save_attachment's bytes for the node that saves them (fleet_tools.go)
		var p proto.MailAttachmentRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, true, err
		}
		out, err := s.mailAttachment(ctx, machine, p)
		return out, true, err
	case "mcp.tools":
		var p struct {
			Assistant bool   `json:"assistant"`
			SessionID string `json:"session_id"`
			Guest     bool   `json:"guest"` // the node's word that it is a guest conversation (guest.go)
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{"tools": s.sessionMCPTools(ctx, machine, p.SessionID, p.Assistant, p.Guest)}, true, nil
	case "mcp.call":
		var p struct {
			Name      string          `json:"name"`
			Args      json.RawMessage `json:"args"`
			SessionID string          `json:"session_id"`
			Assistant bool            `json:"assistant"`
			Guest     bool            `json:"guest"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, true, err
		}
		if res, refused := s.guestRefuses(ctx, machine, p.SessionID, p.Name, p.Guest); refused {
			return res, true, nil
		}
		return s.mcpCall(ctx, machine, p.SessionID, p.Name, p.Args, p.Assistant), true, nil
	}
	return nil, false, nil
}

// FleetUp: the connection to the hub is (again) up: it gets every pending interaction, every session list and the
// fleet again (it may have restarted and know nothing).
func (s *Service) FleetUp(ctx context.Context) {
	if !s.fleetOn() {
		return
	}
	s.fleetStart(context.WithoutCancel(ctx))
	defer s.boardFleetUp() // the pending cards are read: the board may have changed while the assistant was away
	s.fleet.sentMu.Lock()
	s.fleet.sent = nil
	s.fleet.sentMu.Unlock()
	s.hub.Tell(assistantwire.EventFleetInteractions, assistantwire.FleetInteractions{Interactions: s.pendingInteractions()})
	for _, m := range s.fleetMachines() {
		s.publishSessions(m.ID)
	}
	s.liveChanged(true)
}

// FleetEvent takes the hub's fleet.* events.
func (s *Service) FleetEvent(ctx context.Context, name string, params json.RawMessage) bool {
	switch name {
	case assistantwire.EventClients:
		var c assistantwire.Clients
		if json.Unmarshal(params, &c) == nil {
			s.fleet.mu.Lock()
			s.fleet.clients = c
			s.fleet.mu.Unlock()
		}
	case assistantwire.EventMutes:
		var p assistantwire.FleetMutes
		if json.Unmarshal(params, &p) != nil || p.Mutes == nil {
			return true // not the mutes (a hub from before they came with the event): the ones handed over last stay
		}
		s.setMutes(*p.Mutes)
		for _, m := range s.fleetMachines() {
			s.publishSessions(m.ID)
		}
		s.liveChanged(false)
	case assistantwire.EventSeen:
		var p assistantwire.FleetSeen
		if json.Unmarshal(params, &p) == nil && p.MachineID != "" && p.SessionID != "" {
			if m, ok := s.fleetMachine(p.MachineID); ok {
				s.markSeen(ctx, m.ID, p.SessionID)
			}
		}
	default:
		return false
	}
	return true
}

// ---- the snapshot's parts the lists show ----

// fleetTakeSnapshot keeps what the session lists are decorated with from the snapshot the assistant just made and tells
// the hub the lists of the machines whose sessions look different now; the assistant's own questions and cards go on
// the phone as interactions (askOnPhone).
func (s *Service) fleetTakeSnapshot(snap Snapshot) {
	if !s.fleetOn() {
		return
	}
	r := &s.fleet.rep
	r.mu.Lock()
	oldSessions, oldWork, oldOn, oldLinks, oldQuiet, oldMarks := r.sessions, r.work, r.workOn, r.links, r.quiet, r.marks
	views := make(map[string]SessionView, len(snap.Sessions))
	for _, v := range snap.Sessions {
		views[v.SessionID] = v
	}
	r.sessions, r.got = views, true
	if w := snap.Work; w != nil {
		r.work = make(map[[2]string]proto.WorkState, len(w.Sessions))
		for _, sw := range w.Sessions {
			r.work[[2]string{sw.MachineID, sw.SessionID}] = sw.Work
		}
		r.workOn = w.On
	}
	if t := snap.Todos; t != nil {
		r.links = map[[2]string]TodoLink{}
		for _, l := range t.Links {
			r.links[[2]string{l.MachineID, l.SessionID}] = l
		}
		r.quiet = map[[2]string]bool{}
		for _, q := range t.Quiet {
			r.quiet[[2]string{q.MachineID, q.SessionID}] = true
		}
	}
	if c := snap.Channels; c != nil {
		r.marks = make(map[[2]string][]string, len(c.Marks))
		for _, m := range c.Marks {
			r.marks[[2]string{m.MachineID, m.SessionID}] = m.Channels
		}
		r.turns = make(map[[2]string]time.Time, len(c.Turns))
		for _, t := range c.Turns {
			r.turns[[2]string{t.MachineID, t.SessionID}] = t.Answered
		}
	}
	if snap.Tasks != nil {
		r.tasks = snap.Tasks
	}
	if snap.Profile != nil {
		r.profile = snap.Profile
	}
	if in := snap.Inbox; in != nil {
		r.inbox = map[string]bool{}
		for _, a := range in.Agents {
			r.inbox[a.SessionID] = true
		}
	}
	changed := changedMachines(oldSessions, r.sessions)
	add := func(mid string) {
		if !slices.Contains(changed, mid) {
			changed = append(changed, mid)
		}
	}
	if oldOn != r.workOn {
		for _, m := range s.fleetMachines() {
			add(m.ID)
		}
	}
	for k, w := range r.work {
		if o, ok := oldWork[k]; !ok || !sameJSON(o, w) {
			add(k[0])
		}
	}
	for k := range oldWork {
		if _, ok := r.work[k]; !ok {
			add(k[0])
		}
	}
	for k, l := range r.links {
		if o, ok := oldLinks[k]; !ok || o != l {
			add(k[0])
		}
	}
	for k := range oldLinks {
		if _, ok := r.links[k]; !ok {
			add(k[0])
		}
	}
	for k := range r.quiet {
		if !oldQuiet[k] {
			add(k[0])
		}
	}
	for k := range oldQuiet {
		if !r.quiet[k] {
			add(k[0])
		}
	}
	for k, v := range r.marks {
		if !slices.Equal(oldMarks[k], v) {
			add(k[0])
		}
	}
	for k := range oldMarks {
		if _, ok := r.marks[k]; !ok {
			add(k[0])
		}
	}
	r.mu.Unlock()
	s.askOnPhone(snap.Interactions)
	for _, mid := range changed {
		s.sessionsChanged(mid)
	}
	go s.deliverAnswers() // answers its Action could not take yet
}

// changedMachines are the machines of the conversations whose views differ between two snapshots.
func changedMachines(old, now map[string]SessionView) []string {
	var out []string
	add := func(mid string) {
		if !slices.Contains(out, mid) {
			out = append(out, mid)
		}
	}
	for id, v := range now {
		o, ok := old[id]
		if !ok || o.Title != v.Title || o.Archived != v.Archived || o.Origin != v.Origin || o.OriginRef != v.OriginRef || !slices.Equal(o.Queued, v.Queued) || o.MachineID != v.MachineID {
			add(v.MachineID)
			if ok {
				add(o.MachineID)
			}
		}
	}
	for id, o := range old {
		if _, ok := now[id]; !ok {
			add(o.MachineID)
		}
	}
	return out
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// quietSession: mid/sid works (or worked) for a quiet task: the main conversation hears about it, nothing else does.
func (s *Service) quietSession(mid, sid string) bool {
	r := &s.fleet.rep
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.quiet[[2]string{mid, sid}]
}

// withTask names, on an interaction sent to clients, the task its agent works for (the watch and the cards show it).
func (s *Service) withTask(ix proto.Interaction) proto.Interaction {
	r := &s.fleet.rep
	r.mu.RLock()
	l, ok := r.links[[2]string{ix.MachineID, ix.SessionID}]
	r.mu.RUnlock()
	if ok && l.Task {
		ix.TaskTitle = l.Title
	}
	return ix
}

// isInboxAgent: the conversation is an inbox agent's (no "finished" push for it: the draft in the inbox is the result).
func (s *Service) isInboxAgent(sessionID string) bool {
	r := &s.fleet.rep
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.inbox[sessionID]
}

// channelAnsweredPush: the conversation's turn is (or just was) answered in a channel, so the phone's "finished" push
// leaves it alone. "Just": within the finished push's delay, which is when it looks — not much longer, or the push of
// the next turn, asked in the app, would be dropped too.
func (s *Service) channelAnsweredPush(session string) bool {
	window := 30*time.Second + s.notifyTimings().doneDelay()
	r := &s.fleet.rep
	r.mu.RLock()
	defer r.mu.RUnlock()
	for k, at := range r.turns {
		if k[1] == session && (at.IsZero() || time.Since(at) < window) {
			return true
		}
	}
	return false
}

// ---- mutes ----

// Pins is the settings mutes: muted spaces / agents get no push notifications (finished, needs you). Spaces are
// "machine/space name", agents "machine/session id"; Allowed are agents that keep theirs although their space is
// muted. The hub keeps it (GET / PUT /api/v1/mutes) and hands it to the manager (fleet.mutes).
type Pins struct {
	Spaces   []string `json:"spaces"`
	Sessions []string `json:"sessions"`
	Allowed  []string `json:"allowed,omitempty"`
}

// setMutes takes the hub's mutes (the hello's answer, fleet.mutes).
func (s *Service) setMutes(p assistantwire.Mutes) {
	m := Pins{Spaces: nonNil(p.Spaces), Sessions: nonNil(p.Sessions), Allowed: p.Allowed}
	s.fleet.mutesMu.Lock()
	s.fleet.mutes = &m
	s.fleet.mutesMu.Unlock()
}

// loadMutes is the mutes the hub handed over last (the hello's answer, fleet.mutes after every change); none before
// it did. They are the hub's settings: the manager never reads them from a database.
func (s *Service) loadMutes(context.Context) Pins {
	f := &s.fleet
	f.mutesMu.Lock()
	defer f.mutesMu.Unlock()
	if f.mutes == nil {
		return Pins{Spaces: []string{}, Sessions: []string{}}
	}
	return *f.mutes
}

// muted reports whether pushes about this session are off (the agent or its space is muted).
func (s *Service) muted(machineID, sessionID string) bool {
	space := ""
	if sess, ok := s.nodeSession(machineID, sessionID); ok {
		space = sess.SpaceName
	}
	return s.mutedAs(machineID, sessionID, space)
}

// mutedAs is muted for a machine under the id the apps know, its session in space (empty: none known).
func (s *Service) mutedAs(machineID, sessionID, space string) bool {
	p := s.loadMutes(context.Background())
	key := machineID + "/" + sessionID
	if slices.Contains(p.Sessions, key) {
		return true
	}
	if slices.Contains(p.Allowed, key) { // turned back on inside a muted space
		return false
	}
	return space != "" && slices.Contains(p.Spaces, machineID+"/"+space)
}

// mutedSessions are those of list whose pushes are off (the manager opens no card for them).
func (s *Service) mutedSessions(mid string, list []proto.Session) []string {
	out := []string{}
	for _, sess := range list {
		if s.mutedAs(mid, sess.SessionID, sess.SpaceName) {
			out = append(out, sess.SessionID)
		}
	}
	return out
}

// ---- the session lists ----

// setSessions takes a node's session list: the turns it shows starting and ending (the finished push, the
// assistant's own business), then the list the apps get.
func (s *Service) setSessions(machine string, list []proto.Session) {
	before := map[string]proto.Session{}
	for _, sess := range s.fleetPrevious(machine) {
		before[sess.SessionID] = sess
	}
	now := time.Now()
	for i := range list {
		list[i].MachineID = machine
		// A node from before titles were stripped first sends a clipped injected tag as the title.
		list[i].Title = transcript.Title(list[i].Title)
	}
	for _, sess := range list {
		// Any agent (herdr or headless, started by Persona or not) finishing a turn may notify the phone.
		was, key := before[sess.SessionID], machine+"/"+sess.SessionID
		prev := was.Status
		// The turn stopped on background work and its report woke it again within one of the node's list ticks: the
		// stop never showed, only the woken flag turning on. It is held work all the same (#4187, #4419). By the
		// background seen during the turn, not only in the last list: the list's background and its woken flag are
		// not always read together.
		if prev == proto.StatusWorking && sess.WokeByTask && !was.WokeByTask {
			s.turnsBook().holdIfBackground(key)
		}
		switch {
		case sess.Status == proto.StatusWorking && prev != proto.StatusWorking:
			s.takeTaskAnswerRetry(machine, sess.SessionID)
			s.turnsBook().working(key, now)
			s.replyWorked(machine, sess.SessionID) // answered: a question it asks next is a new one (agents_reply.go)
			ref := SessionRef{MachineID: machine, SessionID: sess.SessionID}
			s.inOrder(func() { s.TurnStarted(context.Background(), ref) }) // after the items that came before it
		case prev == proto.StatusWorking && (sess.Status == proto.StatusDone || sess.Status == proto.StatusIdle):
			go s.notifyFinished(machine, sess.SessionID, s.turnsBook().ended(key, now, backgroundRunning(sess)))
			// A new assistant conversation is named after its first turn; a task from one may be done. After the
			// items of the turn (a channel's answer is read from them).
			ref := SessionRef{MachineID: machine, SessionID: sess.SessionID}
			s.inOrder(func() { go s.TurnEnded(context.Background(), ref) })
		case (sess.Status == proto.StatusDone || sess.Status == proto.StatusIdle) &&
			(sess.Status != prev || sess.Rev != was.Rev || sess.LastText != was.LastText || !sess.UpdatedAt.Equal(was.UpdatedAt)) &&
			s.takeTaskAnswerRetry(machine, sess.SessionID):
			ref := SessionRef{MachineID: machine, SessionID: sess.SessionID}
			s.inOrder(func() { go s.taskTurnEnded(context.Background(), ref.MachineID, ref.SessionID) })
		}
		if sess.Status == proto.StatusWorking && backgroundRunning(sess) {
			s.turnsBook().sawBackground(key, now)
		}
	}
	s.fleetKeepPrevious(machine, list)
	s.sessionsChanged(machine)
	// Messages from other conversations, for those no longer running; the todos' links and herdr marks.
	var connected time.Time
	if s.devs != nil {
		connected = s.devs.ConnectedAt(machine)
	}
	d := DeviceSessions{MachineID: machine, Sessions: list, Connected: connected}
	s.inOrder(func() { s.SessionsChanged(context.Background(), d) })
}

// fleetPrevious is the list setSessions saw last for the machine (the turns' starts and ends are against it).
func (s *Service) fleetPrevious(machine string) []proto.Session {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	return s.fleet.prev[machine]
}

func (s *Service) fleetKeepPrevious(machine string, list []proto.Session) {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	if s.fleet.prev == nil {
		s.fleet.prev = map[string][]proto.Session{}
	}
	s.fleet.prev[machine] = list
}

func (s *Service) turnsBook() *turnTracker {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	if s.fleet.turns == nil {
		s.fleet.turns = newTurnTracker()
	}
	return s.fleet.turns
}

// sessionsChanged: what the apps get of the machine's sessions may have changed: the hub is told, the screen and
// plan cards follow.
func (s *Service) sessionsChanged(machine string) {
	if _, ok := s.fleetMachine(machine); !ok {
		return
	}
	list := s.publishList(machine)
	s.syncScreenInteractions(machine, list)
	s.syncPlanInteractions(machine)
}

// publishSessions tells the hub the machine's session list as the apps get it now.
func (s *Service) publishSessions(machine string) {
	if _, ok := s.fleetMachine(machine); !ok {
		return
	}
	s.publishList(machine)
}

// publishList tells the hub the machine's list as the apps get it now, when it is not what it was told last
// (fleet.sessions); the fleet follows. It returns the list.
//
// The list is decorated and told under sentMu: two publishers (FleetUp's lists, a snapshot's changed machines) would
// otherwise each decorate, then tell in either order, and an older list could land last on the hub and stay there.
func (s *Service) publishList(machine string) []proto.Session {
	f := &s.fleet
	f.sentMu.Lock()
	defer f.sentMu.Unlock()
	list := s.decorated(machine)
	msg := assistantwire.FleetSessions{MachineID: machine, Sessions: list, Muted: s.mutedSessions(machine, list)}
	b, _ := json.Marshal(msg)
	if f.sent == nil {
		f.sent = map[string]string{}
	}
	if f.sent[machine] == string(b) {
		return list
	}
	f.sent[machine] = string(b)
	f.shownMu.Lock()
	if f.shown == nil {
		f.shown = map[string][]proto.Session{}
	}
	f.shown[machine] = list
	f.shownMu.Unlock()
	s.hub.Tell(assistantwire.EventFleetSessions, json.RawMessage(b))
	s.liveChanged(false)
	return list
}

// decorated returns a machine's sessions as the apps get them: the unified states, the pending interaction counts,
// what the assistant says of its conversations, the work and todos.
func (s *Service) decoratedUnarchived(machine string) []proto.Session {
	return s.decorateInventory(machine, slices.Clone(s.nodeSessions(machine)), true)
}

// decorateInventory owns list. A subset gets the same live fields as the full
// inventory, but must not forget state marks belonging to rows it did not read.
func (s *Service) decorateInventory(machine string, list []proto.Session, complete bool) []proto.Session {
	m, _ := s.fleetMachine(machine)
	s.fleet.mu.Lock()
	counts := map[string]int{}
	asking := map[string]proto.Interaction{} // a session's card: its own before the manager's
	for _, p := range s.fleet.interactions {
		if p.ix.MachineID == machine {
			counts[p.ix.SessionID]++
			if cur, ok := asking[p.ix.SessionID]; !ok || cur.ToolName == assistantwire.WorkCardTool {
				asking[p.ix.SessionID] = p.ix
			}
		}
	}
	seen := make(map[string]time.Time, len(list))
	for _, sess := range list {
		if t, ok := s.fleet.seen[[2]string{machine, sess.SessionID}]; ok {
			seen[sess.SessionID] = t
		}
	}
	s.fleet.mu.Unlock()
	r := &s.fleet.rep
	r.mu.RLock()
	shown, via := r.sessions, r.marks
	r.mu.RUnlock()
	lang := s.lang(context.Background())
	for i := range list {
		list[i].MachineID = machine
		list[i].PendingInteractions = counts[list[i].SessionID]
		list[i].Assistant = m.isAssistant(list[i])
		if list[i].Assistant {
			list[i].Space = identity.SpaceOf(list[i].Cwd)
		}
		if t := shown[list[i].SessionID].Title; t != "" && list[i].Assistant {
			list[i].Title = t // "MMDD | type | topic", the user's own, "Persona" for the main one (titles.go)
		}
		if ch := via[[2]string{machine, list[i].SessionID}]; len(ch) > 0 {
			list[i].Channels = ch // where its messages came from (docs/extensions/channels.md "Source marks")
		}
		if list[i].Assistant {
			list[i].Archived = shown[list[i].SessionID].Archived
			list[i].Origin, list[i].OriginRef = shown[list[i].SessionID].Origin, shown[list[i].SessionID].OriginRef // origins.go
			// Messages from other conversations wait in the assistant while it runs: shown like typed-ahead input.
			if q := shown[list[i].SessionID].Queued; len(q) > 0 {
				list[i].Queued = append(slices.Clip(list[i].Queued), q...) // the node's slice is shared: never write into it
			}
		}
		list[i].SeenAt = seen[list[i].SessionID]
		localizeSession(lang, &list[i]) // the node's Chinese activity line… (fleet_text.go)
		list[i].State, list[i].BackgroundWorking = stateWithBackground(agentState(list[i], list[i].SeenAt), list[i])
		list[i].BackgroundWaiting = backgroundWaiting(list[i])
		list[i].TurnError, list[i].TurnErrorKind = turnError(list[i])
		list[i].TurnErrorCode = ""
		if list[i].TurnError != "" {
			list[i].TurnErrorCode = turnErrorCode(list[i].TurnError)
			// Not worth a "Continue": the user signs in, fixes the key or the model. Only where agents_work would not retry it
			// anyway: a gateway's 5xx that mentions the model stays transient.
			if list[i].TurnErrorKind == "" && userTurnError(list[i].TurnErrorCode) && !transientError(list[i].TurnError) {
				list[i].TurnErrorKind = proto.TurnErrorUser
			}
		}
		s.decorateWork(machine, &list[i], asking)
		decorateAwaits(&list[i])
	}
	s.decorateTodos(machine, list)
	for i := range list {
		// The todos' alone, stripped before push to client (#762).
		list[i].SpawnFrom, list[i].Chat, list[i].Topic, list[i].Dispatched = "", "", "", nil
	}
	s.fillStateMarks(machine, list, time.Now(), complete)
	return list
}

// decoratedSession is one of the machine's sessions as the apps get it.
func (s *Service) decoratedSession(machine, sid string) (proto.Session, bool) {
	for _, sess := range s.decorated(machine) {
		if sess.SessionID == sid {
			return sess, true
		}
	}
	return proto.Session{}, false
}

// decorateTodos puts on machine mid's sessions the todo each works for and whether it is quiet.
func (s *Service) decorateTodos(mid string, list []proto.Session) {
	r := &s.fleet.rep
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := range list {
		key := [2]string{mid, list[i].SessionID}
		if l, ok := r.links[key]; ok {
			list[i].Todo = &proto.TodoRef{ID: l.TodoID, Title: l.Title}
		}
		list[i].Quiet = r.quiet[key]
	}
}

// ---- seen ----

// loadSeen reads the seen times from the store, merging the ones marked meanwhile, unless that was done already. It
// answers whether this call read them; a failed read is tried again by the next call.
func (s *Service) loadSeen(ctx context.Context) bool {
	s.fleet.mu.Lock()
	loaded := s.fleet.seenLoaded
	s.fleet.mu.Unlock()
	if loaded {
		return false
	}
	st := s.fleetStore()
	if st == nil {
		return false
	}
	m, err := st.SeenTimes(ctx)
	if err != nil {
		if errors.Is(err, errNoTable) { // a store that does not keep them: nothing to read again
			s.fleet.mu.Lock()
			s.fleet.seenLoaded = true
			s.fleet.mu.Unlock()
		} else {
			s.logf("load seen times: %v", err)
		}
		return false
	}
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	if s.fleet.seenLoaded { // another call read them meanwhile
		return false
	}
	for k, v := range s.fleet.seen { // one marked meanwhile is newer
		m[k] = v
	}
	s.fleet.seen = m
	s.fleet.seenLoaded = true
	return true
}

// retrySeen reads the seen times again if they could not be read so far, and refreshes the session lists' states
// once they are.
func (s *Service) retrySeen(ctx context.Context) {
	if !s.loadSeen(ctx) {
		return
	}
	for _, m := range s.fleetMachines() {
		s.publishSessions(m.ID)
	}
}

func (s *Service) seenAt(machine, session string) time.Time {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	return s.fleet.seen[[2]string{machine, session}]
}

// markSeen records that the user looked at a session (opened it, or it finished while open) and refreshes states.
func (s *Service) markSeen(ctx context.Context, machine, session string) {
	now := time.Now().UTC()
	s.fleet.mu.Lock()
	if s.fleet.seen == nil {
		s.fleet.seen = map[[2]string]time.Time{}
	}
	s.fleet.seen[[2]string{machine, session}] = now
	s.fleet.mu.Unlock()
	s.SessionSeen(ctx, SessionSeen{MachineID: machine, SessionID: session, At: now}) // how long a triaged turn waited
	if st := s.fleetStore(); st != nil {
		if err := st.MarkSeen(context.WithoutCancel(ctx), machine, session, now); err != nil {
			s.logf("mark seen: %v", err)
		}
	}
	s.publishSessions(machine)
}

// ---- the states (docs/protocol/agent-states.md) ----

// agentState maps a host status plus Persona's own knowledge onto the unified states:
//
//	herdr blocked, or a pending question / plan / permission  → needs_you (red)
//	herdr working                                              → working
//	herdr done (finished, unseen in herdr) and not seen in the app since it finished → unread (green)
//	herdr idle (seen in herdr), or done but seen in the app    → idle (gray)
func agentState(sess proto.Session, seenAt time.Time) string {
	if sess.PendingInteractions > 0 || sess.Status == proto.StatusBlocked {
		return proto.StateNeedsYou
	}
	switch sess.Status {
	case proto.StatusWorking:
		return proto.StateWorking
	case proto.StatusDone:
		// The transcript stops changing when the turn ends, so updated_at is when it finished.
		if !seenAt.IsZero() && !seenAt.Before(sess.UpdatedAt.Add(-2*time.Second)) {
			return proto.StateIdle
		}
		return proto.StateUnread
	case proto.StatusIdle:
		return proto.StateIdle
	case proto.StatusOffline:
		return proto.StateOffline
	}
	return proto.StateUnknown
}

// backgroundState is an agent's state once its background work counts (#2228): a Claude whose turn ended while its
// background commands, subagents, Monitors or workflows still run is working, not "Unread" or "Seen" — they wake it with their results, and it
// goes on. Needs-you stays first; a stopped one (offline) has nothing running. Not the assistant's conversations:
// their Monitors and background tasks stay on, and a conversation always "Working" would skip every scheduled run.
// What waits for a turn to end (the finished push, scheduled runs, the hub's own update) reads the host's status.
func backgroundState(state string, sess proto.Session) string {
	if sess.Assistant || !backgroundRunning(sess) {
		return state
	}
	switch state {
	case proto.StateUnread, proto.StateIdle, proto.StateUnknown:
		return proto.StateWorking
	}
	return state
}

// stateWithBackground is backgroundState, and whether the background work is all that makes it working
// (Session.BackgroundWorking: "Working (background)").
func stateWithBackground(host string, sess proto.Session) (string, bool) {
	state := backgroundState(host, sess)
	return state, state == proto.StateWorking && host != proto.StateWorking
}

// turnError is the model API error that cut s's latest turn off, while it has stopped (unread or idle): not finished
// (#1516), and its kind when the agent's record gives one. The node's word comes first: the agent's own record said
// the turn failed (Codex, #2185). Otherwise its last words when they are the error, as Claude Code writes it; only an
// answer that starts with the error counts: a reply that talks about one ("fixed the crash on API Error: …") is a reply.
func turnError(sess proto.Session) (text, kind string) {
	if sess.State != proto.StateUnread && sess.State != proto.StateIdle {
		return "", ""
	}
	if text := strings.TrimSpace(sess.TurnError); text != "" {
		return text, sess.TurnErrorKind
	}
	if text := strings.TrimSpace(sess.LastText); assistantwire.CutOff(text) {
		return text, ""
	}
	return "", ""
}

// rateLimited: a turn error that says the requests came too fast or used up a limit.
var rateLimited = lazyre.New(`(?i)\b429\b|rate[ _-]?limit|too many requests|usage limit`)

// turnErrorCode is what a turn's model API error says is wrong, for a card that says what to fix (Session.TurnErrorCode,
// docs/assistant/onboarding.md 5.2); "" when it does not say.
func turnErrorCode(text string) string {
	t := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(strings.ToLower(t), "not logged in"):
		return proto.TurnErrorNotLoggedIn
	case strings.Contains(t, "There's an issue with the selected model"):
		return proto.TurnErrorModelRoute
	}
	switch classifyLauncherError(t) { // the launcher health checks' classes (launcher_health.go)
	case errKindAuth:
		return proto.TurnErrorAuthFailed
	case errKindModelRoute:
		return proto.TurnErrorModelRoute
	}
	if rateLimited.MatchString(t) {
		return proto.TurnErrorRateLimited
	}
	return ""
}

// userTurnError: a turn error with code is the user's to fix; a rate limit passes (or is the user's when the
// agent's record says so).
func userTurnError(code string) bool {
	switch code {
	case proto.TurnErrorNotLoggedIn, proto.TurnErrorAuthFailed, proto.TurnErrorModelRoute:
		return true
	}
	return false
}

type stateMark struct {
	state string
	at    time.Time
}

// markStates fills in StateSince: the time an agent's state last changed, kept until it changes again. It drops the
// marks and hook flags of the machine's sessions no longer listed.
func (s *Service) markStates(machine string, list []proto.Session, now time.Time) {
	s.fillStateMarks(machine, list, now, true)
}

func (s *Service) fillStateMarks(machine string, list []proto.Session, now time.Time, complete bool) {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	if s.fleet.stateSince == nil {
		s.fleet.stateSince = map[[2]string]stateMark{}
	}
	for i := range list {
		k := [2]string{machine, list[i].SessionID}
		mk, ok := s.fleet.stateSince[k]
		if !ok || mk.state != list[i].State {
			mk = stateMark{state: list[i].State, at: now}
			s.fleet.stateSince[k] = mk
		}
		list[i].StateSince = mk.at
	}
	if !complete {
		return
	}
	// The list is the node's whole list, kept across a disconnect (devices.Machine.setSessions): a session no longer
	// in it has ended. Forget its mark and its hook flag. A hook flag for a session never listed yet (its hook came
	// before the node's next list) has no mark and stays.
	listed := make(map[string]bool, len(list))
	for _, sess := range list {
		listed[sess.SessionID] = true
	}
	for k := range s.fleet.stateSince {
		if k[0] == machine && !listed[k[1]] {
			delete(s.fleet.stateSince, k)
			delete(s.fleet.hooked, k)
		}
	}
}

// backgroundWaiting (Session.BackgroundWaiting, #6045): working only because of background work, and that work only
// waits: the node found its background commands waiting, and no subagent or workflow runs (those are work).
func backgroundWaiting(sess proto.Session) bool {
	if !sess.BackgroundWorking || !sess.BackgroundWaiting {
		return false
	}
	for _, a := range sess.Subagents {
		if a.Running {
			return false
		}
	}
	for _, w := range sess.Workflows {
		if w.Running {
			return false
		}
	}
	return !slices.ContainsFunc(sess.Background, func(t proto.BackgroundTask) bool { return t.Kind == "agent" || t.Kind == "workflow" })
}

func backgroundRunning(sess proto.Session) bool {
	if len(sess.Background) > 0 {
		return true
	}
	for _, a := range sess.Subagents {
		if a.Running {
			return true
		}
	}
	for _, w := range sess.Workflows { // a running one is in Background too; not from every source
		if w.Running {
			return true
		}
	}
	return false
}
