package hub

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/streams"
)

// The computers are the manager's (docs/server/machines.md "Who connects the computers", devicewire): the manager that carries them — the
// chief, the assistant's process, during the migration to managers — connects every one and holds what is known of it.
// The hub hands it hub.json's machines as defaults (the hello's devices section, devices.config when they change),
// relays the apps' calls and streams to it (busNode: devices.call, devices.notify), and keeps what it reports
// (devices.state, devices.event) to show the apps and drive its own features. It never dials or accepts a computer;
// a Persona Link that dials the hub's address is relayed to the manager as it is (devices_relay.go).

// devicesOwner is the manager that carries the machines.
const devicesOwner = assistantwire.AssistantID

// errManagerOffline: the manager that carries the machine is not connected; an app's call is answered 503
// assistant_offline. It counts as offline (apierr.ErrOffline) for the code that only asks that.
var errManagerOffline error = managerOfflineError{}

type managerOfflineError struct{}

func (managerOfflineError) Error() string        { return errAssistantOffline.Error() }
func (managerOfflineError) Is(target error) bool { return target == apierr.ErrOffline }

// devicesState is the hub's side of the manager's machines.
type devicesState struct {
	mu sync.Mutex
	// entries are hub.json's machines and those the admin page added since (handed to the manager).
	entries []MachineConfig
	// absorbed: a hub.json entry whose computer another entry has → that machine's id; recEntries: this hub's
	// computers as its mesh record lists them (what each one's manager says of reaching it), by manager.
	absorbed   map[string]string
	recEntries map[string][]devicewire.RecMachine
	// reported: what each manager last reported of its own machines (machine_entries.go), kept in hub_settings;
	// reportedRead: the setting was read into it (at start, or before a write since); reportedForgot, entriesForgot:
	// the managers removed since, the machines removed since, not taken back from the setting read late; refused:
	// the ids each manager's last report named that are not its own (logged once); reportedDue: a write is to go;
	// reportedSaved: the last write; reportedSave keeps writes one at a time.
	reported       reportedMachines
	reportedSince  map[string]bool // the managers that reported since the hub started
	reportedRead   bool
	reportedForgot map[string]bool
	entriesForgot  map[string]bool
	refused        map[string]map[string]bool
	reportedDue    bool
	reportedSaved  time.Time
	reportedSave   sync.Mutex
	// up is closed while the manager is connected and has reported; warmUntil: until then calls wait for it (a hub
	// just restarted, the machines shown as before).
	up        chan struct{}
	upOpen    bool
	warmUntil time.Time
	// fresh: the manager's connection is new (by manager); at its first report the hub asks it to watch what the
	// apps watch.
	fresh map[string]bool
	// sent is the config last handed over; due: a devices.config is to go out.
	sent map[string][]byte // by manager
	// assign: hub.json's machines handed to another manager than the carrier (ownership.go), machine → manager.
	assign map[string]string
	// assignRead: assign is what machineOwnersKey holds (read, or nothing to read); a move writes only over a read one.
	assignRead   bool
	assignFailed bool // ownership read failed: dedicated local machines must not fall back to the carrier
	// moving keeps moves one at a time: each writes the whole of assign, from the one before it (moveMachine).
	moving  sync.Mutex
	due     bool
	started bool
	// reqs are the nodes' requests being answered (devices.request) by the manager's key; reqEnded the keys whose
	// devices.request_end came before the request did (an event is read at once, a call in its own goroutine).
	reqs     map[string]context.CancelFunc
	reqEnded map[string]time.Time
}

func init() {
	handleBusEvent(devicewire.EventState, func(h *Hub, c *busConn, params json.RawMessage) {
		var st devicewire.States
		if err := json.Unmarshal(params, &st); err != nil {
			h.logf("bus: [%s] %s: %v", c.ident.ID, devicewire.EventState, err)
			return
		}
		h.applyDeviceStatesFrom(c.managerID(), st)
	})
	handleBusEvent(devicewire.EventNode, func(h *Hub, c *busConn, params json.RawMessage) {
		var ev devicewire.NodeEvent
		if json.Unmarshal(params, &ev) != nil || h.ownsMachine(c, ev.MachineID) != nil {
			return // only its manager speaks for a machine's node (ownership.go)
		}
		m := h.machineByID(ev.MachineID)
		if m == nil {
			return
		}
		if mux := m.portStreams(); mux != nil && mux.Event(ev.Event, ev.Params) {
			return
		}
		m.onEvent(ev.Event, ev.Params)
	})
	handleBus(devicewire.MethodRequest, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var p devicewire.NodeCall
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: devicewire.MethodRequest + ": " + err.Error()}
		}
		if err := h.ownsMachine(c, p.MachineID); err != nil {
			return nil, err
		}
		m := h.machineByID(p.MachineID)
		if m == nil {
			return nil, &rpc.Error{Code: devicewire.CodeNoDevice, Message: fmt.Sprintf("no machine %q", p.MachineID)}
		}
		args := p.Params
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		ctx, done := h.requestScope(ctx, requestKey(c, p.Request))
		defer done()
		return m.onRequest(ctx, p.Method, args)
	})
	handleBusEvent(devicewire.EventRequestEnd, func(h *Hub, c *busConn, params json.RawMessage) {
		var p devicewire.RequestEnd
		if json.Unmarshal(params, &p) != nil || p.Request == "" {
			return
		}
		// Its own requests only (the keys are its own); an end mark kept ahead of the request only for its own machine.
		h.requestEnded(requestKey(c, p.Request), h.ownsMachine(c, p.MachineID) == nil)
	})
	handleBus(devicewire.MethodConnected, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var p devicewire.Connected
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: devicewire.MethodConnected + ": " + err.Error()}
		}
		if err := h.ownsMachine(c, p.MachineID); err != nil {
			return nil, err
		}
		m := h.machineByID(p.MachineID)
		if m == nil {
			return devicewire.ConnectedReply{}, &rpc.Error{Code: devicewire.CodeNoDevice, Message: fmt.Sprintf("no machine %q", p.MachineID)}
		}
		return h.machineConnected(ctx, m, p.ConnectedAt), nil
	})
	// A service machine's credential goes only to the manager that carries it (ownership.go).
	handleBus(devicewire.MethodConnectorCred, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var p devicewire.ConnectorCred
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: err.Error()}
		}
		if err := h.ownsMachine(c, p.MachineID); err != nil {
			return nil, err
		}
		return h.connectorCred(ctx, p.MachineID)
	})
	handleBus(devicewire.MethodConnectorPaired, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var p devicewire.ConnectorCred
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: err.Error()}
		}
		if err := h.ownsMachine(c, p.MachineID); err != nil {
			return nil, err
		}
		return map[string]any{}, h.keepConnectorCred(ctx, p)
	})
}

// ---- the nodes' requests ----

// requestScope is a node's request's context: the bus connection's, ended too when the manager says the node's
// connection is gone (devices.request_end), as a node's own connection ending ends what the hub does for it.
func (h *Hub) requestScope(ctx context.Context, key string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	if key == "" {
		return ctx, cancel
	}
	d := &h.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ended := d.reqEnded[key]; ended {
		delete(d.reqEnded, key)
		cancel()
		return ctx, cancel
	}
	if d.reqs == nil {
		d.reqs = map[string]context.CancelFunc{}
	}
	d.reqs[key] = cancel
	return ctx, func() {
		d.mu.Lock()
		delete(d.reqs, key)
		d.mu.Unlock()
		cancel()
	}
}

// requestKey is manager c's request key: one manager's keys never name another's requests.
func requestKey(c *busConn, key string) string {
	if key == "" {
		return ""
	}
	return c.managerID() + "\x00" + key
}

// requestEnded: devices.request_end. A request that is not running yet is marked ended for when it comes, if mark.
func (h *Hub) requestEnded(key string, mark bool) {
	d := &h.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	if cancel := d.reqs[key]; cancel != nil {
		delete(d.reqs, key)
		cancel()
		return
	}
	if !mark {
		return
	}
	now := time.Now()
	for k, at := range d.reqEnded { // its request never came (it was answered, or the bus dropped): not kept long
		if now.Sub(at) > time.Minute {
			delete(d.reqEnded, k)
		}
	}
	if d.reqEnded == nil {
		d.reqEnded = map[string]time.Time{}
	}
	d.reqEnded[key] = now
}

// ---- the handover ----

// devicesConfig is what the hub hands the manager now.
func (h *Hub) devicesConfig() devicewire.Config { return h.devicesConfigFor(devicesOwner) }

// devicesConfigFor is what the hub hands manager id: the machines it carries (ownership.go); the mesh's members
// only to the carrier.
func (h *Hub) devicesConfigFor(id string) devicewire.Config {
	out := devicewire.Config{Machines: []json.RawMessage{}, HubID: h.hubID(), HubName: h.hubName(), HubVersion: h.version}
	for _, mc := range h.cfgEntries() {
		if h.entryOwner(mc.ID) != id {
			continue
		}
		if b, err := json.Marshal(mc); err == nil {
			out.Machines = append(out.Machines, b)
		}
	}
	if h.cfg != nil {
		out.AutoUpdateNodes = h.cfg.AutoUpdateNodes
		out.TailcatDERPMapURL = h.cfg.Tailcat.DERPMapURL
	}
	out.NodePackagesURL, out.NodePackagesProxy = h.nodePackages()
	if every, timeout, set := h.linkPingTimes(); set {
		out.LinkPingMS, out.LinkPingTimeoutMS = int(every.Milliseconds()), int(timeout.Milliseconds())
	}
	msg := h.linkEndpoints()
	out.MeshHubs = msg.MeshHubs
	for _, e := range msg.Endpoints {
		out.Endpoints = append(out.Endpoints, devicewire.LinkEndpoint{URL: e.URL, Kind: e.Kind})
	}
	if id == devicesOwner {
		out.Mesh = h.devicesMesh()
	}
	out.PendingUnknown = h.trustUnknown()
	for _, mid := range h.pendingMachineIDs() {
		if h.entryOwner(mid) == id {
			out.Pending = append(out.Pending, mid)
		}
	}
	return out
}

// nodePackages is where the manager fetches node builds (hub.json's node_packages): its url, else this build's update
// URL (a release hub's, a Docker image built with one); "off" none.
func (h *Hub) nodePackages() (url, proxy string) {
	if h.cfg != nil {
		url, proxy = h.cfg.NodePackages.URL, h.cfg.NodePackages.Proxy
	}
	switch strings.TrimSpace(url) {
	case "off":
		return "", ""
	case "":
		url = h.build.UpdateURL
	}
	return strings.TrimRight(strings.TrimSpace(url), "/"), proxy
}

// devicesMesh is the mesh as the manager needs it (nil without one).
func (h *Hub) devicesMesh() *devicewire.Mesh {
	ms := &h.mesh
	ms.mu.Lock()
	running := ms.running
	var settle time.Time
	if running && len(ms.peers) > 0 && len(ms.links) == 0 {
		settle = ms.started.Add(meshSettle)
	}
	ms.mu.Unlock()
	if !running {
		return nil
	}
	out := &devicewire.Mesh{Member: h.isMember(), SettleUntil: settle, Hubs: map[string]devicewire.MeshHub{}}
	if h.cfg != nil {
		out.PathSwitchMS = h.cfg.Mesh.pathSwitch().Milliseconds()
	}
	if out.Member {
		out.CenterName = h.hubNameOf(h.centerID())
	}
	v := h.meshView()
	if v != nil {
		for id, vh := range v.hubs {
			if id == h.hubID() {
				continue
			}
			out.Hubs[id] = devicewire.MeshHub{Name: h.hubNameOf(id), Reachable: vh.reachable, RTTMS: v.rttTo(id)}
		}
	}
	if !out.Member {
		for _, r := range h.memberRecords() {
			mr := devicewire.MemberRecord{HubID: r.HubID}
			for _, rm := range r.Machines {
				cfg, _ := json.Marshal(rm.Cfg)
				mr.Machines = append(mr.Machines, devicewire.RecMachine{ID: rm.ID, Cfg: cfg, Kind: rm.Kind, Avail: rm.Avail, RTTMS: rm.RTTMS})
			}
			out.Members = append(out.Members, mr)
		}
	}
	return out
}

// devicesConfigChanged hands the manager the config again if it changed (a regroup of the mesh, an entry added, the
// hub's addresses), at most every 200 ms.
func (h *Hub) devicesConfigChanged() {
	d := &h.devices
	d.mu.Lock()
	if d.due {
		d.mu.Unlock()
		return
	}
	d.due = true
	d.mu.Unlock()
	time.AfterFunc(200*time.Millisecond, func() {
		d.mu.Lock()
		d.due = false
		d.mu.Unlock()
		for _, p := range h.busRoster() {
			if p.Role != bus.RoleManager {
				continue
			}
			b, _ := json.Marshal(h.devicesConfigFor(p.ID))
			d.mu.Lock()
			same := string(b) == string(d.sent[p.ID])
			d.mu.Unlock()
			if same {
				continue
			}
			if err := h.notifyBus(p.ID, devicewire.EventConfig, json.RawMessage(b)); err == nil {
				d.mu.Lock()
				if d.sent == nil {
					d.sent = map[string][]byte{}
				}
				d.sent[p.ID] = b
				d.mu.Unlock()
			}
		}
	})
}

// devicesHello is the hello's devices section for manager id: the machines it carries (none, for most).
func (h *Hub) devicesHello(id string) *devicewire.Config {
	cfg := h.devicesConfigFor(id)
	b, _ := json.Marshal(cfg)
	d := &h.devices
	d.mu.Lock()
	if d.sent == nil {
		d.sent = map[string][]byte{}
	}
	d.sent[id] = b
	d.mu.Unlock()
	return &cfg
}

// cfgEntries is hub.json's machines and those added since it started.
func (h *Hub) cfgEntries() []MachineConfig {
	d := &h.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.started && h.cfg != nil {
		return slices.Clone(h.cfg.Machines)
	}
	return slices.Clone(d.entries)
}

// cfgEntry is the hub.json entry with this id (nil: none).
func (h *Hub) cfgEntry(id string) *MachineConfig {
	for _, mc := range h.cfgEntries() {
		if mc.ID == id {
			return &mc
		}
	}
	return nil
}

// addMachineLive hands the manager a hub.json entry the admin page just added: it connects it now. A hub that is not
// running (tests) reads it at its next start.
func (h *Hub) addMachineLive(mc MachineConfig) {
	d := &h.devices
	d.mu.Lock()
	if !d.started {
		d.mu.Unlock()
		return
	}
	for _, e := range d.entries {
		if e.ID == mc.ID {
			d.mu.Unlock()
			return
		}
	}
	d.entries = append(d.entries, mc)
	d.mu.Unlock()
	h.setLeaving(mc.ID, false) // added again after a reject (machine_trust.go)
	h.logf("[%s] added (%s): the manager connects it", mc.ID, kindOf(mc))
	h.devicesConfigChanged()
	h.meshTouch()
}

// removeMachineLive takes a hub.json entry out of what the manager connects: it lets the computer go now. A hub that
// is not running (tests) does not read it again.
func (h *Hub) removeMachineLive(id string) {
	d := &h.devices
	d.mu.Lock()
	if !d.started {
		d.mu.Unlock()
		return
	}
	n := len(d.entries)
	d.entries = slices.DeleteFunc(d.entries, func(e MachineConfig) bool { return e.ID == id })
	gone := len(d.entries) != n
	d.mu.Unlock()
	if !gone {
		return
	}
	h.logf("[%s] removed: the manager lets it go", id)
	h.forgetMachineOwner(context.Background(), id) // added again, it starts at the carrier
	h.forgetMachineEntry(id)
	h.devicesConfigChanged()
	h.meshTouch()
}

// addedLive is the entries added while the hub runs (pendingRestart).
func (h *Hub) addedLive() []MachineConfig {
	d := &h.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.started || h.cfg == nil {
		return nil
	}
	var out []MachineConfig
	for _, e := range d.entries {
		if !slices.ContainsFunc(h.cfg.Machines, func(c MachineConfig) bool { return c.ID == e.ID }) {
			out = append(out, e)
		}
	}
	return out
}

func kindOf(c MachineConfig) string {
	if c.Kind == "" {
		return "ssh"
	}
	return c.Kind
}

// forMesh is a hub.json entry as a member tells the center about it: no ssh host, tailcat address or binary path.
func (c MachineConfig) forMesh() MachineConfig {
	c.SSHHost, c.TailcatAddr, c.RemoteBin, c.Address, c.Proxy = "", "", "", "", ""
	return c
}

// ---- what the manager reports ----

// startDevices makes the machines of hub.json before the managers report them: shown as before a restart while they
// come back (what the last hub process saw them report, saveDevicesWarm), offline otherwise, as their manager last
// reported them (machine_entries.go); a call waits for the carrier meanwhile.
func (h *Hub) startDevices(ctx context.Context) {
	d := &h.devices
	d.mu.Lock()
	d.entries, d.started = slices.Clone(h.cfg.Machines), true
	if d.up == nil {
		d.up = make(chan struct{})
	}
	d.mu.Unlock()
	if err := h.loadMachineOwners(ctx); err != nil {
		h.logf("devices: ownership unavailable; dedicated local machines stay unassigned: %v", err)
	}
	h.loadMachineEntries(ctx) // what each manager last reported of them (machine_entries.go)
	var saved devicesWarm
	if h.store != nil {
		if ok, err := h.store.TakeSetting(ctx, devicesWarmKey, &saved); err != nil || !ok || time.Since(saved.SavedAt) > devicesWarmStale {
			saved.Machines = nil
		}
	}
	until := time.Now().Add(devicesWarmGrace)
	n := 0
	h.fleet.mu.Lock()
	for _, mc := range h.cfg.Machines {
		if h.byID[mc.ID] != nil {
			continue
		}
		m := newMachine(h, mc)
		m.managed, m.peer = true, &busNode{h: h, id: mc.ID, done: make(chan struct{})}
		m.owner = h.entryOwner(mc.ID)
		if e, ok := h.reportedEntryOf(m.owner, mc.ID); ok { // as its manager last reported it, until it reports again
			m.info, m.lastSeen = e.Info, e.LastSeen
		}
		m.engine = saved.Engines[mc.ID]
		if c, ok := saved.EngineHosts[mc.ID]; ok {
			m.engineHosts = &c
		}
		if w, ok := saved.Machines[mc.ID]; ok && (w.Until.IsZero() || w.Until.After(time.Now())) {
			m.info, m.sessions, m.warmUntil = w.Info, w.Sessions, until
			if !w.Until.IsZero() && w.Until.Before(until) {
				m.warmUntil = w.Until
			}
			n++
		}
		h.machines = append(h.machines, m)
		h.byID[mc.ID] = m
	}
	h.fleet.mu.Unlock()
	d.mu.Lock()
	d.warmUntil = until
	d.mu.Unlock()
	if n > 0 {
		h.logf("warm start: %d machines shown as before while the manager reconnects them", n)
	}
	time.AfterFunc(devicesWarmGrace, h.devicesWarmEnded)
}

const (
	// devicesWarmKey is the hub's own (hub_settings): what a stopping hub process saw the managers report, for the
	// next one. The manager hands its own machines to its next process apart (internal/devices warm.go).
	devicesWarmKey   = "hub.devices_warm"
	devicesWarmGrace = 30 * time.Second
	devicesWarmStale = 2 * time.Minute
)

// devicesWarm is the hand-over: the online machines as reported, every machine's engine, when.
type devicesWarm struct {
	Machines map[string]devicesWarmMachine `json:"machines"`
	Engines  map[string]string             `json:"engines,omitempty"`
	// EngineHosts are their sets of hosts (#2576), as the managers reported them.
	EngineHosts map[string]proto.EngineChoice `json:"engine_hosts,omitempty"`
	SavedAt     time.Time                     `json:"saved_at"`
}

type devicesWarmMachine struct {
	Info     proto.NodeInfo  `json:"info"`
	Sessions []proto.Session `json:"sessions"`
	// Until: a machine only still shown from an earlier hand-over keeps that one's deadline (quick restarts in a row
	// do not keep it shown for ever); zero for one online.
	Until time.Time `json:"until,omitzero"`
}

// saveDevicesWarm hands the next hub process the hub.json machines as the managers last reported them (beginStop,
// while they are still connected): the apps see them as they were while the managers come back.
func (h *Hub) saveDevicesWarm(ctx context.Context) {
	if h.store == nil {
		return
	}
	saved := devicesWarm{Machines: map[string]devicesWarmMachine{}, Engines: map[string]string{},
		EngineHosts: map[string]proto.EngineChoice{}, SavedAt: time.Now()}
	for _, m := range h.machinesNow() {
		if h.cfgEntry(m.cfg.ID) == nil {
			continue
		}
		m.mu.Lock()
		if m.managed && !m.derived {
			// Online, or still shown from the last hand-over (a second restart within the grace keeps it).
			shown := m.online || m.warmLocked(saved.SavedAt)
			if m.snap != nil && m.snap.Engine != "" {
				saved.Engines[m.cfg.ID] = m.snap.Engine
			} else if m.engine != "" {
				saved.Engines[m.cfg.ID] = m.engine
			}
			if m.snap != nil && m.snap.EngineHosts != nil {
				saved.EngineHosts[m.cfg.ID] = proto.EngineChoice{Hosts: m.snap.EngineHosts, Default: m.snap.EngineDefault}
			} else if m.engineHosts != nil {
				saved.EngineHosts[m.cfg.ID] = *m.engineHosts
			}
			if shown {
				w := devicesWarmMachine{Info: m.info, Sessions: slices.Clone(m.sessions)}
				if !m.online {
					w.Until = m.warmUntil
				}
				saved.Machines[m.cfg.ID] = w
			}
		}
		m.mu.Unlock()
	}
	if err := h.store.PutSetting(ctx, devicesWarmKey, saved); err != nil {
		h.logf("warm start: hand over the machines: %v", err)
	}
}

// devicesWarmEnded: the warm start is over; the machines no manager reported since show offline, whichever manager
// they are (one manager back does not keep the others' shown as before).
func (h *Hub) devicesWarmEnded() {
	changed := false
	for _, m := range h.machinesNow() {
		m.mu.Lock()
		if m.managed && !m.warmUntil.IsZero() && m.snap == nil {
			m.endWarmLocked()
			markUnknownLocked(m.sessions)
			changed = true
		}
		m.mu.Unlock()
	}
	if changed {
		h.broadcastMachines()
		for _, m := range h.machinesNow() {
			h.sessionsChanged(m)
		}
	}
}

// warmLocked: shown as before a restart — the hub's, the manager not back yet, or the manager's (State.WarmUntil),
// its node not back yet; m.mu is held.
func (m *Machine) warmLocked(now time.Time) bool { return !m.online && now.Before(m.warmUntil) }

func (m *Machine) endWarmLocked() { m.warmUntil = time.Time{} }

func markUnknownLocked(list []proto.Session) {
	for i := range list {
		if list[i].Status != proto.StatusOffline {
			list[i].Status = proto.StatusUnknown
			if list[i].PendingInteractions == 0 { // the manager's states as it would show them: a card still waits
				list[i].State = proto.StateUnknown
			}
		}
	}
}

// devicesUp: the manager that carries the machines is connected and has reported them.
func (h *Hub) devicesUp() bool {
	d := &h.devices
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.upOpen
}

// waitDevices returns nil once the manager is up; errManagerOffline at once while it is not, unless the hub just
// started (its machines shown as before): then a call waits for it that long.
func (h *Hub) waitDevices(ctx context.Context) error {
	d := &h.devices
	d.mu.Lock()
	if d.upOpen {
		d.mu.Unlock()
		return nil
	}
	if d.up == nil {
		d.up = make(chan struct{})
	}
	up, until := d.up, d.warmUntil
	d.mu.Unlock()
	if !time.Now().Before(until) {
		return errManagerOffline
	}
	t := time.NewTimer(time.Until(until))
	defer t.Stop()
	select {
	case <-up:
		return nil
	case <-t.C:
	case <-ctx.Done():
		return ctx.Err()
	}
	return errManagerOffline
}

// awaitManager is waitDevices for what cannot wait long (a WebSocket handshake, a peer's request): at most limit.
// True once the manager is up.
func (h *Hub) awaitManager(ctx context.Context, limit time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	return h.waitDevices(ctx) == nil
}

// applyDeviceStates is applyDeviceStatesFrom the manager that carries hub.json's machines.
func (h *Hub) applyDeviceStates(st devicewire.States) { h.applyDeviceStatesFrom(devicesOwner, st) }

// applyDeviceStatesFrom takes devices.state from manager owner: every machine it carries, in the apps' order. The
// other managers' machines stay as they were, and only the machines owner carries are taken (ownership.go
// machineOwner: another manager's, and a mesh member's from any manager but the carrier, are dropped). The apps get
// the managers' machines grouped: devicesOwner's first, then the others' by id.
func (h *Hub) applyDeviceStatesFrom(owner string, st devicewire.States) {
	now := time.Now()
	type change struct {
		m              *Machine
		ended, offline bool
		began          bool // the node's connection is a new one (or the hub just met it): it knows no fs.watch (fswatch.go)
		oldMux         *streams.Mux
	}
	var changes []change
	h.fleet.mu.Lock()
	prev := h.byID
	order := make([]*Machine, 0, len(st.Machines))
	byID := map[string]*Machine{}
	for _, s := range st.Machines {
		id := s.Machine.ID
		if id == "" || byID[id] != nil {
			continue
		}
		m := prev[id]
		// Only its manager reports a machine (ownership.go): hub.json's, its entry's, derived or not (another one is
		// letting it go, or claims what is not its own); any other (a mesh member's), the carrier's only.
		if h.cfgEntry(id) != nil && h.entryOwner(id) != owner || h.cfgEntry(id) == nil && owner != devicesOwner {
			continue
		}
		if m == nil || !m.managed {
			// Its entry stays as it was made (it is read without m.mu everywhere, as when the hub connected it).
			m = newMachine(h, h.mirrorConfig(s))
			m.managed, m.peer = true, &busNode{h: h, id: id, done: make(chan struct{})}
		}
		m.mu.Lock()
		m.owner = owner
		was := m.online || m.warmLocked(now)
		wasConn, wasAt := m.connected, m.connectedAt
		snap := s.Machine
		// online is the node connected now, as it was when the hub connected it (self-update waits for it, the node
		// updates go to it); the manager's warm start is warmUntil, as the hub's own was (the apps see both as online).
		m.snap, m.info, m.online, m.err = &snap, s.Info, s.Connected, s.Machine.Error
		if s.Machine.LastSeen.After(m.lastSeen) {
			m.lastSeen = s.Machine.LastSeen
		}
		m.derived, m.paths = s.Derived, s.Paths
		m.connected, m.connectedAt = s.Connected, s.ConnectedAt
		m.warmUntil = s.WarmUntil
		c := change{m: m}
		c.ended = wasConn && (!s.Connected || !s.ConnectedAt.Equal(wasAt))
		c.began = s.Connected && (!wasConn || !s.ConnectedAt.Equal(wasAt))
		if c.ended || s.Connected && !wasConn {
			c.oldMux, m.streams = m.streams, nil
		}
		if s.Connected && m.streams == nil {
			m.streams = streams.New(m.notify) // forwarded ports (ports.go)
		}
		if was && !m.online && !m.warmLocked(now) {
			markUnknownLocked(m.sessions)
			c.offline = true
		}
		m.mu.Unlock()
		order = append(order, m)
		byID[id] = m
		changes = append(changes, c)
	}
	var gone []*Machine
	groups := map[string][]*Machine{owner: order}
	var unmanaged []*Machine
	for _, m := range h.machines { // the previous order
		if byID[m.cfg.ID] != nil {
			continue
		}
		if !m.managed {
			unmanaged = append(unmanaged, m) // a test's machine stays
			continue
		}
		if o := m.ownerOrCarrier(); o != owner {
			groups[o] = append(groups[o], m)
		} else {
			gone = append(gone, m)
		}
	}
	for id, m := range prev { // one not in the order (none, but a map is the truth)
		if byID[id] == nil && !slices.Contains(h.machines, m) && !m.managed {
			unmanaged = append(unmanaged, m)
		}
	}
	owners := make([]string, 0, len(groups))
	for o := range groups {
		if o != devicesOwner {
			owners = append(owners, o)
		}
	}
	sort.Strings(owners)
	all := slices.Clone(groups[devicesOwner])
	for _, o := range owners {
		all = append(all, groups[o]...)
	}
	all = append(all, unmanaged...)
	for _, m := range all {
		byID[m.cfg.ID] = m
	}
	h.machines, h.byID = all, byID
	h.fleet.mu.Unlock()

	d := &h.devices
	d.mu.Lock()
	if d.recEntries == nil {
		d.recEntries = map[string][]devicewire.RecMachine{}
	}
	d.recEntries[owner] = st.Entries
	if owner == devicesOwner { // hub.json's machines, the hub's start: the carrier's
		d.absorbed = st.Absorbed
		if d.up == nil {
			d.up = make(chan struct{})
		}
		if !d.upOpen {
			close(d.up)
			d.upOpen = true
		}
		d.warmUntil = time.Time{} // the hub's start is over: from now on a call without the manager fails at once
	}
	fresh := d.fresh[owner]
	delete(d.fresh, owner)
	d.mu.Unlock()
	h.takeMachineEntries(owner, st) // kept for when it is away (machine_entries.go)
	// The rejected computers the manager let go are not kept out any more (machine_trust.go).
	h.fleetLeft(func(id string) bool { return byID[id] != nil || st.Absorbed[id] != "" })
	if fresh {
		go h.watchAgain(order)
	}

	for _, c := range changes {
		if c.oldMux != nil {
			c.oldMux.CloseAll()
		}
		if c.ended {
			h.connectionEnded(c.m, i18n.T(h.userLang(), "hub-devices_link.computer_offline"))
		}
	}
	for _, m := range gone {
		h.connectionEnded(m, i18n.T(h.userLang(), "hub-devices_link.computer_offline"))
		m.mu.Lock()
		mux := m.streams
		m.streams = nil
		m.mu.Unlock()
		if mux != nil {
			mux.CloseAll()
		}
	}
	h.broadcastMachines()
	for _, c := range changes {
		if c.offline {
			h.sessionsChanged(c.m)
		}
		if c.began {
			go h.fsWatchReconnected(c.m) // the web panels' open folders (#2439)
		}
	}
}

// mirrorConfig is a reported machine's entry: hub.json's for one of its own, else the member's, with the id the
// manager gave it.
func (h *Hub) mirrorConfig(s devicewire.State) MachineConfig {
	if mc := h.cfgEntry(s.Machine.ID); mc != nil && !s.Derived {
		return *mc
	}
	var mc MachineConfig
	_ = json.Unmarshal(s.Config, &mc)
	mc.ID, mc.Kind = s.Machine.ID, s.Machine.Kind
	if s.Machine.Name != "" {
		mc.Name = s.Machine.Name
	}
	return mc
}

// devicesConnected: the manager that carries the machines connected (its handshake). The port streams and terminals
// that ran through its last connection ended with it, as they do when a node's own connection ends (the manager ends
// them on the nodes: internal/devices hubopen.go); a terminal whose first watch waits for this connection stays.
func (h *Hub) devicesConnected(owner string) {
	d := &h.devices
	d.mu.Lock()
	delete(d.reportedForgot, owner) // a manager in the roster again: what it reports is kept
	if d.fresh == nil {
		d.fresh = map[string]bool{}
	}
	d.fresh[owner] = true
	d.mu.Unlock()
	for _, m := range h.machinesNow() {
		m.mu.Lock()
		mux := m.streams
		if !m.managed || mux == nil || cmp.Or(m.owner, devicesOwner) != owner {
			m.mu.Unlock()
			continue
		}
		m.streams = streams.New(m.notify)
		m.mu.Unlock()
		mux.CloseAll()
	}
	t := &h.terms
	t.mu.Lock()
	ran := map[[2]string]*termRelay{}
	for k, r := range t.relays {
		if r.synced != nil || r.observing {
			ran[k] = r
		}
	}
	t.mu.Unlock()
	for k, r := range ran {
		if m := h.machineByID(k[0]); m != nil && m.managed && m.ownerOrCarrier() == owner {
			h.terminalEnded(k[0], k[1], errAssistantOffline.Error(), r)
		}
	}
}

// watchAgain asks a manager that is new to watch the sessions the apps watch (it watches them again itself whenever
// a node reconnects). A few at a time: one slow computer does not hold up the others' items.
func (h *Hub) watchAgain(list []*Machine) {
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, m := range list {
		m.mu.Lock()
		managed := m.managed
		// The sessions the apps watch, and those the old manager was told to watch: the new one watches none of them
		// yet (watchAgainFresh).
		ids := map[string]bool{}
		for id, n := range m.watchers {
			if n > 0 {
				ids[id] = true
			}
		}
		for id := range m.nodeWatch {
			ids[id] = true
		}
		m.mu.Unlock()
		if !managed {
			continue
		}
		for id := range ids {
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-sem; wg.Done() }()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				m.watchAgainFresh(ctx, id)
			}()
		}
	}
	wg.Wait()
}

// connectionEnded ends what ran on a node's connection that is gone: its terminals, the text being generated.
func (h *Hub) connectionEnded(m *Machine, why string) {
	h.forgetLiveText(m.cfg.ID) // the node that was generating it is gone: the transcript has what was finished
	h.terminalsEnded(m.cfg.ID, why)
	h.fullsOffline(m.cfg.ID) // the full herdr views ran on that connection too
	// The questions waiting on it are the manager's (assistant/fleet_ix.go NodeDetached).
}

// devicesGone: the manager that carries the machines disconnected. The machines show offline (why: it is not
// there), their terminals and streams close, their questions end; when it is back it reports them again.
func (h *Hub) devicesGone(owner string) {
	d := &h.devices
	d.mu.Lock()
	if d.upOpen && owner == devicesOwner {
		d.up = make(chan struct{})
		d.upOpen = false
	}
	d.mu.Unlock()
	// The hub's own stop (the manager gets its SIGTERM at the same time): the next hub shows them as they were
	// (startDevices). A moment's wait lets the hub's stop begin first.
	time.Sleep(500 * time.Millisecond)
	if h.stopping.Load() || h.busConnOf(owner) != nil || owner == devicesOwner && h.devicesUp() {
		return
	}
	d.mu.Lock()
	delete(d.recEntries, owner) // it reaches none of them now (the mesh record says so)
	d.mu.Unlock()
	var off []*Machine
	for _, m := range h.machinesNow() {
		m.mu.Lock()
		if !m.managed || cmp.Or(m.owner, devicesOwner) != owner {
			m.mu.Unlock()
			continue
		}
		m.mu.Unlock()
		h.offlineLocal(m, i18n.Localize(errAssistantOffline, h.userLang()))
		off = append(off, m)
	}
	for _, m := range off {
		h.connectionEnded(m, i18n.Localize(errAssistantOffline, h.userLang()))
	}
	if len(off) > 0 {
		h.machineEntriesChanged() // seen until now (offlineLocal)
		h.logf("devices: [%s] the manager is gone: %d machines offline until it is back", owner, len(off))
		h.broadcastMachines()
		for _, m := range off {
			h.sessionsChanged(m)
		}
	}
}

// offlineLocal shows m offline with error why until its manager reports it again: its streams close, its sessions'
// states are unknown (the caller ends what ran on its connection and tells the apps).
func (h *Hub) offlineLocal(m *Machine, why string) {
	m.mu.Lock()
	if m.connected { // seen until now
		m.lastSeen = time.Now().UTC()
	}
	mux := m.streams
	m.streams, m.connected = nil, false
	m.online, m.err = false, why
	m.endWarmLocked()
	if m.snap != nil {
		s := *m.snap
		s.Online, s.Error, s.Transport, s.TailcatPath, s.Link = false, m.err, "", "", nil
		m.snap = &s
	}
	markUnknownLocked(m.sessions)
	m.mu.Unlock()
	if mux != nil {
		mux.CloseAll()
	}
}

// machineConnected is the hub's part of a node's new connection (devices.connected): what it adds to the node's
// configuration and the calls right after it, and what it starts for the computer.
func (h *Hub) machineConnected(ctx context.Context, m *Machine, at time.Time) devicewire.ConnectedReply {
	m.mu.Lock()
	m.hookedAt = at
	m.mu.Unlock()
	out := devicewire.ConnectedReply{Configure: map[string]json.RawMessage{}}
	if b, err := json.Marshal(m.codexConfig(ctx)); err == nil {
		out.Configure["codex"] = b
	}
	if list := m.echoesToSend(ctx); len(list) > 0 {
		if b, err := json.Marshal(map[string]any{"echoes": list}); err == nil {
			out.Calls = append(out.Calls, devicewire.NodeCall{MachineID: m.cfg.ID, Method: "session.echoes", Params: b})
		}
	}
	return out
}

// updateNodes has each manager that carries machines redeploy its own whose node is not this build (selfupdate.go):
// the carrier always, the others connected now.
func (h *Hub) updateNodes(ctx context.Context) {
	owners := []string{devicesOwner}
	for _, mc := range h.cfgEntries() {
		if o := h.machineOwner(mc.ID); !slices.Contains(owners, o) && h.busConnOf(o) != nil {
			owners = append(owners, o)
		}
	}
	for _, o := range owners {
		if err := h.callBus(ctx, o, devicewire.MethodUpdateNodes, map[string]any{}, nil); err != nil {
			h.logf("update: [%s] redeploys its machines: %v", o, err)
		}
	}
}

// ---- calls through the manager ----

// busNode is a machine's node as the hub reaches it: through the manager.
type busNode struct {
	h    *Hub
	id   string
	done chan struct{} // never closed: the manager says when the node is gone
}

func (b *busNode) Call(ctx context.Context, method string, params, out any) error {
	owner := b.h.machineOwner(b.id)
	if caller, ok := callerOf(ctx); ok && caller != owner { // a manager's device.call, the machine moved since its check
		return &rpc.Error{Code: devicewire.CodeNoDevice, Message: fmt.Sprintf("%s on machine %q: another manager's now", clip(method, 64), clip(b.id, 64))}
	}
	if owner == devicesOwner {
		if err := b.h.waitDevices(ctx); err != nil {
			return fmt.Errorf("%s: %w", b.id, err)
		}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	p := devicewire.NodeCall{MachineID: b.id, Method: method, Params: raw}
	if dl, ok := ctx.Deadline(); ok {
		p.TimeoutMS = managerTimeout(time.Until(dl)).Milliseconds()
	}
	var res json.RawMessage
	if err := b.h.callBusAsIs(ctx, owner, devicewire.MethodCall, p, &res); err != nil {
		return deviceError(b.id, err)
	}
	if out != nil && len(res) > 0 {
		return json.Unmarshal(res, out)
	}
	return nil
}

// managerTimeout is how long the manager gives a call the hub waits left for: a little less, so its answer (the
// node's, or its deadline: CodeDeadline) is back before the hub gives up itself.
func managerTimeout(left time.Duration) time.Duration {
	margin := min(left/10, 500*time.Millisecond)
	return max(time.Millisecond, left-margin)
}

func (b *busNode) Notify(event string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := b.h.notifyBus(b.h.machineOwner(b.id), devicewire.EventNotify, devicewire.NodeEvent{MachineID: b.id, Event: event, Params: raw}); err != nil {
		return fmt.Errorf("%s: %w", b.id, errManagerOffline)
	}
	return nil
}

func (b *busNode) Done() <-chan struct{} { return b.done }
func (b *busNode) Close(error)           {}

// managerDroppedError: the manager's connection dropped with the call out. The app is told the manager is not there
// (503 assistant_offline, callErr), but it is not "offline" (nothing sent): the node may have done it, so a send's
// outcome is unknown (sendOutcome) and the same message is not sent again.
type managerDroppedError struct{ id string }

func (e managerDroppedError) Error() string {
	return e.id + ": the connection to the manager dropped during the call"
}

func (managerDroppedError) Is(target error) bool {
	return target == errManagerOffline || target == rpc.ErrDropped || target == rpc.ErrClosed
}

// nodeError is an error the manager coded, given back its kind.
type nodeError struct {
	msg  string
	kind error
}

func (e *nodeError) Error() string { return e.msg }
func (e *nodeError) Unwrap() error { return e.kind }

// deviceError turns the manager's coded errors back into the hub's own: the node offline, its connection dropped,
// the call's time up, the manager gone; a node's own coded refusal stays as it is. What may have reached the node —
// the bus dropping with the call out, the connection to the node dropping, the time running out on the manager's
// side — reads as a node connection that dropped or timed out (a send's outcome unknown, send.go), never as offline.
func deviceError(id string, err error) error {
	switch {
	case errors.Is(err, errBusDropped):
		return managerDroppedError{id: id}
	case errors.Is(err, errBusOffline):
		return fmt.Errorf("%s: %w", id, errManagerOffline)
	}
	var re *rpc.Error
	if !errors.As(err, &re) {
		return err
	}
	switch re.Code {
	case devicewire.CodeMachineOffline, devicewire.CodeNoDevice:
		return &nodeError{msg: re.Message, kind: errOffline}
	case devicewire.CodeNodeClosed:
		return &nodeError{msg: re.Message, kind: rpc.ErrDropped}
	case devicewire.CodeDeadline:
		return &nodeError{msg: re.Message, kind: context.DeadlineExceeded}
	}
	return err
}

// ---- kind service's credentials (service.go there) ----

// connectorCred is a service machine's pin and credential, opened for the manager.
func (h *Hub) connectorCred(ctx context.Context, machine string) (devicewire.ConnectorCred, error) {
	cs := h.centerStore()
	if cs == nil {
		return devicewire.ConnectorCred{}, errors.New("no database")
	}
	row, err := cs.ConnectorCredential(ctx, machine)
	if err != nil {
		return devicewire.ConnectorCred{}, err
	}
	if row == nil {
		return devicewire.ConnectorCred{MachineID: machine}, nil
	}
	tok, err := h.openKey(row.Token)
	if err != nil {
		return devicewire.ConnectorCred{}, fmt.Errorf("读不出连接器的凭据：%w", err)
	}
	return devicewire.ConnectorCred{MachineID: machine, FP: row.FP, Token: tok, Version: row.Version}, nil
}

// keepConnectorCred seals and keeps what pairing made.
func (h *Hub) keepConnectorCred(ctx context.Context, c devicewire.ConnectorCred) error {
	cs := h.centerStore()
	if cs == nil {
		return errors.New("no database")
	}
	if mc := h.cfgEntry(c.MachineID); mc == nil || mc.Kind != kindService {
		// Pairing for an entry being added: it is written to hub.json right after.
		if mc != nil {
			return fmt.Errorf("%s is not of kind service", c.MachineID)
		}
	}
	sealed, err := h.sealKey(c.Token)
	if err != nil {
		return err
	}
	return cs.SaveConnectorCredential(ctx, db.ConnectorCredential{MachineID: c.MachineID, FP: c.FP, Token: sealed,
		Version: clip(oneLine(c.Version), 80)})
}

// pairConnector has the machine's manager (the carrier for one being added) pair a service machine's connector (the
// admin page); it keeps the credential (devices.connector_paired, its own machine's only).
func (h *Hub) pairConnector(ctx context.Context, pairLink, id, address, proxyURL string) (string, string, error) {
	_ = h.waitDevices(ctx) // the hub just started: the manager is on its way
	var out struct {
		Address string `json:"address"`
		Version string `json:"version"`
	}
	err := h.callBus(ctx, h.machineOwner(id), devicewire.MethodPairConnector, devicewire.PairConnector{Link: pairLink, MachineID: id,
		Address: address, Proxy: proxyURL}, &out)
	if errors.Is(err, errBusOffline) {
		return "", "", errAssistantOffline
	}
	if err != nil {
		return "", "", err
	}
	return out.Address, out.Version, nil
}

const kindService = "service"

// validProxy checks a socks5:// proxy for a service machine (the manager dials through it).
func validProxy(proxyURL string) error {
	if proxyURL == "" {
		return nil
	}
	if !strings.HasPrefix(proxyURL, "socks5://") && !strings.HasPrefix(proxyURL, "socks5h://") ||
		strings.TrimPrefix(strings.TrimPrefix(proxyURL, "socks5h://"), "socks5://") == "" {
		return i18n.Err("hub-devices_link.bad_proxy")
	}
	return nil
}

// linkPingTimes are the heartbeats of the Persona Links the hub relays: hub.json's link section (config_link.go), else
// link.PingEvery and PingTimeout; tests set their own (h.linkPing). set: not the defaults, the manager is told them
// (devicesConfig) and tells Persona Link.
func (h *Hub) linkPingTimes() (every, timeout time.Duration, set bool) {
	if h.linkPing[0] > 0 {
		return h.linkPing[0], h.linkPing[1], true
	}
	if h.cfg != nil {
		if every, timeout, ok := h.cfg.Link.pingTimes(); ok {
			return every, timeout, true
		}
	}
	return link.PingEvery, link.PingTimeout, false
}

// metricsRoutesRelayed relays the monitoring's routes (devicewire.MetricsRoutes: the machines' samples, their history,
// the token usage) to the manager, which keeps them.
func (h *Hub) metricsRoutesRelayed(api *http.ServeMux) {
	for _, pattern := range devicewire.MetricsRoutes {
		by := h.byRequest
		if strings.Contains(pattern, "/machines/{id}/") {
			by = h.byMachine
		}
		api.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			_ = h.waitDevices(r.Context()) // the hub just started: the manager is on its way (bounded); else 503 at once
			h.forwardOwned(w, r, by)
		})
	}
}

// usageRoutesRelayed relays the subscription usage windows' route (devicewire.UsageRoutes) to the manager, which
// asks the machines it connects and the account-pool gateways (docs/server/usage-windows.md); 503 assistant_offline while
// it is away.
func (h *Hub) usageRoutesRelayed(api *http.ServeMux) {
	for _, pattern := range devicewire.UsageRoutes {
		api.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			_ = h.waitDevices(r.Context()) // the hub just started: the manager is on its way (bounded); else 503 at once
			h.forwardOwned(w, r, h.byRequest)
		})
	}
}

// adminUsageRoutesRelayed relays the admin UI's usage sources page (devicewire.AdminUsageRoutes) to the manager. mux
// is the admin's signed-in routes: the hub has checked the session (and the CSRF token of a write) before, and tells
// the manager so (assistantwire.AdminHeader); the apps' API has no such route.
func (h *Hub) adminUsageRoutesRelayed(mux *http.ServeMux) {
	for _, pattern := range devicewire.AdminUsageRoutes {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			_ = h.waitDevices(r.Context())
			if id, ok := h.owner(w, r, h.byRequest); ok { // the manager it names, else the default: its usage sources
				h.forwardAdminHTTP(w, r, id)
			}
		})
	}
}

// deviceRoutesRelayed relays the apps' requests on the routes that are the manager's (devicewire.Routes: its own
// settings of each machine and what only it can do) to it, as the assistant's (forwardHTTP); while it is not
// connected they are answered 503 assistant_offline.
func (h *Hub) deviceRoutesRelayed(api *http.ServeMux) {
	for _, pattern := range devicewire.Routes {
		api.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if h.localMachine(r.PathValue("mid")) == nil {
				writeErr(w, http.StatusNotFound, i18n.Err("devices-api.unknown_machine"))
				return
			}
			_ = h.waitDevices(r.Context()) // the hub just started: the manager is on its way (bounded); else 503 at once
			h.forwardOwned(w, r, h.byMachine)
		})
	}
}

// memberRecords are the records of the hubs whose computers the center reaches through them: the other hubs that
// take this hub for their center, not seen for too long excluded.
func (h *Hub) memberRecords() []*hubRecord {
	self := h.hubID()
	ms := &h.mesh
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if !ms.running {
		return nil
	}
	var out []*hubRecord
	for id, r := range ms.records {
		if id == self || r.Center != self || time.Since(ms.seenAt[id]) > meshRecordKeep {
			continue
		}
		if _, paired := ms.peers[id]; !paired && !h.meshView().reachable(id) {
			continue // left the group (no longer paired with anyone here)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HubID < out[j].HubID })
	return out
}

// managerHTTP asks manager id (the machine's, for a machine's route) one of its routes (devicewire.Routes) as an app
// would, for the hub's own features (persona_settings); a status that is not 2xx is an error with the body's message.
func (h *Hub) managerHTTP(ctx context.Context, id, method, path string, body any, out any) error {
	req := assistantwire.HTTPRequest{Method: method, Path: path, Header: map[string][]string{"Content-Type": {"application/json"}}}
	req.Header[assistantwire.RoleHeader] = []string{"full"}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		req.Body = b
	}
	var res assistantwire.HTTPResponse
	if err := h.callBus(ctx, id, assistantwire.MethodHTTP, req, &res); err != nil {
		if errors.Is(err, errBusOffline) {
			return errAssistantOffline
		}
		return err
	}
	if res.Status >= 300 {
		if why := apierr.Describe(res.Body); why != "" { // with the cause a localized summary moved to detail (#5728)
			return errors.New(why)
		}
		return fmt.Errorf("%s %s: %d", method, path, res.Status)
	}
	if out != nil {
		return json.Unmarshal(res.Body, out)
	}
	return nil
}

// claudeDirsState is what GET /api/v1/machines/{mid}/claude-dirs answers (the manager's, claudedirs.go there).
type claudeDirsState struct {
	Dirs    []proto.ClaudeDir `json:"dirs"`
	Blocked []string          `json:"blocked"`
	Home    string            `json:"home,omitempty"`
	Live    bool              `json:"live"`
}

func (m *Machine) claudeDirsState(ctx context.Context) claudeDirsState {
	var st claudeDirsState
	if err := m.hub.managerHTTP(ctx, m.hub.machineOwner(m.cfg.ID), http.MethodGet, "/api/v1/machines/"+m.cfg.ID+"/claude-dirs", nil, &st); err != nil {
		m.hub.logf("[%s] claude dirs: %v", m.cfg.ID, err)
	}
	return st
}

func (m *Machine) blockedClaudeDirs(ctx context.Context) []string {
	return m.claudeDirsState(ctx).Blocked
}

// readBlockedClaudeDirs is blockedClaudeDirs with the error, for changing the list: a failed read is not "none
// blocked", or the PUT that follows would replace every pattern with just the one changed.
func (m *Machine) readBlockedClaudeDirs(ctx context.Context) ([]string, error) {
	var st claudeDirsState
	if err := m.hub.managerHTTP(ctx, m.hub.machineOwner(m.cfg.ID), http.MethodGet, "/api/v1/machines/"+m.cfg.ID+"/claude-dirs", nil, &st); err != nil {
		return nil, fmt.Errorf("read blocked claude dirs: %w", err)
	}
	return st.Blocked, nil
}

func (m *Machine) setBlockedClaudeDirs(ctx context.Context, patterns []string) error {
	return m.hub.managerHTTP(ctx, m.hub.machineOwner(m.cfg.ID), http.MethodPut, "/api/v1/machines/"+m.cfg.ID+"/claude-dirs/blocked",
		map[string]any{"blocked": patterns}, nil)
}
