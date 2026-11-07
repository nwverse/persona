package devices

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
)

// The computers the center drives and the ways to each (docs/archive/operations/plan-hub-center.md 5, docs/server/mesh.md "Several ways"). A way —
// a path — is a hub.json entry of this hub (local, service, link, ssh, tailcat) or one of a member hub's (the center
// opens the node's stream through that hub, peer_node.go). Paths that turn out to reach the same computer (the same
// machine_uid in node.info) are one machine: listed once, connected through one path at a time (a resident node takes
// one attach and drops the one before). Which: this machine's own > a direct path > through a member, and the
// smaller round trip among equals. Sticky: the path in use stays while it works; a broken one is left at once for the
// next; a better one must stay better for mesh.path_switch_seconds before the switch, which connects the new one
// before letting the old one go.

const (
	pathClassLocal  = 0
	pathClassDirect = 1
	pathClassHub    = 2
	viaHub          = "hub" // a path through a member hub
	kindHub         = "hub" // the kind of a machine only a member has in hub.json
)

// pathCheckEvery: how often the path in use is compared with the others (a var: tests shorten it).
var pathCheckEvery = 2 * time.Second

// nodePath is one way to a computer's node.
type nodePath struct {
	key string   // "cfg:<id>" for this hub's hub.json entry, "hub:<hub_id>/<id>" for a member's
	via string   // the entry's kind (local | service | link | ssh | tailcat), or "hub"
	src *Machine // cfg: the entry whose way it is (its transports, its link, its connector)
	hub string   // hub: the member
	mid string   // hub: the computer's id in the member's hub.json
	rec devicewire.RecMachine
}

func (p *nodePath) class() int {
	switch {
	case p.via == viaHub:
		return pathClassHub
	case p.via == "local":
		return pathClassLocal
	}
	return pathClassDirect
}

// pathHealth is how a path did lately.
type pathHealth struct {
	failUntil time.Time // not tried again before this (backoff after a failure)
	fails     int
	err       string
	okAt      time.Time // last connected through it
	rtt       int       // ms: the node's round trip the last time it was in use
}

// pathInfo is a path as the admin page and the machine list show it (plan 5 "paths").
type pathInfo = devicewire.PathInfo

type fleet struct {
	mu       sync.Mutex
	saveIDs  sync.Mutex            // one write of "mesh.machine_ids" at a time, of the newest (saveMachineIDs)
	saveUIDs sync.Mutex            // one write of "mesh.path_uids" at a time, of the newest (savePathUIDs)
	derived  map[string]*Machine   // group key -> a machine only members have
	absorbed map[*Machine]*Machine // a hub.json entry whose computer another entry has -> that one
	paths    map[*Machine][]*nodePath
	ids      map[string]string // group key -> the id given to a derived machine (setting "mesh.machine_ids")
	uids     map[string]string // path key -> the machine_uid seen through it (setting "mesh.path_uids")
	health   map[string]*pathHealth
	cancel   map[*Machine]context.CancelFunc // derived machines' run loops
	changed  chan struct{}                   // closed when a regroup changed something
	order    []*Machine                      // the machines listed: hub.json's (not absorbed), then derived by id
}

func (f *fleet) init() {
	if f.paths == nil {
		f.derived, f.absorbed, f.paths = map[string]*Machine{}, map[*Machine]*Machine{}, map[*Machine][]*nodePath{}
		f.ids, f.uids, f.health = map[string]string{}, map[string]string{}, map[string]*pathHealth{}
		f.cancel, f.changed = map[*Machine]context.CancelFunc{}, make(chan struct{})
	}
}

// machinesNow is every machine the apps see, in order: hub.json's (but entries for a computer another entry has),
// then the members' computers. Before the first regroup (and in unit tests) it is hub.json's.
func (d *Devices) machinesNow() []*Machine {
	f := &d.fleet
	f.mu.Lock()
	order := f.order
	f.mu.Unlock()
	if order == nil {
		return d.cfgMachines()
	}
	return append([]*Machine(nil), order...)
}

// fleetAll is every machine object with a run loop: hub.json's, absorbed ones included, and the derived ones.
func (d *Devices) fleetAll() []*Machine {
	out := d.cfgMachines()
	f := &d.fleet
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.derived {
		out = append(out, m)
	}
	return out
}

// machineByID is the machine an id the apps use stands for: a hub.json entry (or the entry that has its computer),
// or a member's computer.
func (d *Devices) machineByID(id string) *Machine {
	if id == "" {
		return nil
	}
	entry := d.cfgMachine(id)
	f := &d.fleet
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := entry; m != nil {
		if to := f.absorbed[m]; to != nil {
			return to
		}
		return m
	}
	for _, m := range f.derived {
		if m.cfg.ID == id {
			return m
		}
	}
	return nil
}

// pathsOf is a machine's paths, best first by kind (the choice also looks at health and round trips).
func (d *Devices) pathsOf(m *Machine) []*nodePath {
	f := &d.fleet
	f.mu.Lock()
	defer f.mu.Unlock()
	if ps, ok := f.paths[m]; ok {
		return append([]*nodePath(nil), ps...)
	}
	if !m.derived {
		return []*nodePath{cfgPath(m)}
	}
	return nil
}

func cfgPath(m *Machine) *nodePath {
	return &nodePath{key: "cfg:" + m.cfg.ID, via: kindOf(m.cfg), src: m}
}

func kindOf(c MachineConfig) string {
	if c.Kind == "" {
		return "ssh"
	}
	return c.Kind
}

// fleetChanged returns a channel closed at the next change of the machines or their paths.
func (d *Devices) fleetChanged() <-chan struct{} {
	f := &d.fleet
	f.mu.Lock()
	defer f.mu.Unlock()
	f.init()
	return f.changed
}

// loadPathUIDs reads which computer each path reached last time, and the ids given to members' computers.
func (d *Devices) loadPathUIDs(ctx context.Context) {
	f := &d.fleet
	f.mu.Lock()
	f.init()
	f.mu.Unlock()
	if d.opts.Store == nil {
		return
	}
	var uids, ids map[string]string
	if _, err := d.opts.Store.Setting(ctx, "mesh.path_uids", &uids); err != nil {
		d.logf("mesh: load path uids: %v", err)
	}
	if _, err := d.opts.Store.Setting(ctx, "mesh.machine_ids", &ids); err != nil {
		d.logf("mesh: load machine ids: %v", err)
	}
	f.mu.Lock()
	for k, v := range uids {
		f.uids[k] = v
	}
	for k, v := range ids {
		f.ids[k] = v
	}
	f.mu.Unlock()
}

// learnUID keeps the computer a path reached; when it is news, the machines are grouped again.
func (d *Devices) learnUID(p *nodePath, uid string) {
	if uid == "" || p == nil {
		return
	}
	f := &d.fleet
	f.mu.Lock()
	f.init()
	if f.uids[p.key] == uid {
		f.mu.Unlock()
		return
	}
	f.uids[p.key] = uid
	f.mu.Unlock()
	if d.opts.Store != nil {
		go d.savePathUIDs()
	}
	d.fleetRegroup()
}

// savePathUIDs writes which computer each path reached as it is now. The copy is taken after the previous write,
// under the lock: a write started earlier never lands after a newer one, so a restart does not read a map that
// misses a path learnt last (and list that path's computer twice).
func (d *Devices) savePathUIDs() {
	f := &d.fleet
	f.saveUIDs.Lock()
	defer f.saveUIDs.Unlock()
	f.mu.Lock()
	uids := maps.Clone(f.uids)
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.opts.Store.PutSetting(ctx, "mesh.path_uids", uids); err != nil {
		d.logf("mesh: save path uids: %v", err)
	}
}

// memberRecords are the members' computers the center reaches through them (the hub's mesh, devicewire.Mesh).
func (d *Devices) memberRecords() []devicewire.MemberRecord {
	mesh := d.config().Mesh
	if mesh == nil || mesh.Member {
		return nil
	}
	out := slices.Clone(mesh.Members)
	sort.Slice(out, func(i, j int) bool { return out[i].HubID < out[j].HubID })
	return out
}

// fleetRegroup works out the machines and their paths again: hub.json's entries, and (on the center) the members'
// computers, grouped by the computer each path reached.
func (d *Devices) fleetRegroup() {
	members := d.memberRecords()
	cfgs := d.cfgMachines()
	f := &d.fleet
	f.mu.Lock()
	f.init()

	var all []*nodePath
	for _, m := range cfgs {
		all = append(all, cfgPath(m))
	}
	for _, r := range members {
		for _, rm := range r.Machines {
			all = append(all, &nodePath{key: "hub:" + r.HubID + "/" + rm.ID, via: viaHub, hub: r.HubID, mid: rm.ID, rec: rm})
		}
	}
	// Group by computer: one group per uid, a path whose computer is not known yet alone.
	type group struct {
		key   string
		paths []*nodePath
	}
	var groups []*group
	byKey := map[string]*group{}
	for _, p := range all {
		k := "p:" + p.key
		if uid := f.uids[p.key]; uid != "" {
			k = "u:" + uid
		}
		g := byKey[k]
		if g == nil {
			g = &group{key: k}
			byKey[k] = g
			groups = append(groups, g)
		}
		g.paths = append(g.paths, p)
	}

	newPaths := map[*Machine][]*nodePath{}
	newAbsorbed := map[*Machine]*Machine{}
	newDerived := map[string]*Machine{}
	taken := map[string]bool{}
	for _, m := range cfgs {
		taken[m.cfg.ID] = true
	}
	var listed []*Machine
	var pendingDerived []*group
	for _, g := range groups {
		var owner *Machine
		for _, p := range g.paths {
			if p.src == nil {
				continue
			}
			if owner == nil {
				owner = p.src
			} else {
				newAbsorbed[p.src] = owner
			}
		}
		if owner == nil {
			pendingDerived = append(pendingDerived, g)
			continue
		}
		newPaths[owner] = orderPaths(g.paths)
		listed = append(listed, owner)
	}
	// The members' computers: the machine made earlier for any of the group's paths is kept (its id with it).
	prevOf := map[string]*Machine{}
	for _, m := range f.derived {
		for _, p := range f.paths[m] {
			prevOf[p.key] = m
		}
	}
	for _, m := range f.derived {
		taken[m.cfg.ID] = true // ids in use stay with their machines
	}
	used := map[*Machine]bool{}
	var derivedList []*Machine
	renamed := false
	for _, g := range pendingDerived {
		var m *Machine
		for _, p := range g.paths {
			if prev := prevOf[p.key]; prev != nil && !used[prev] {
				m = prev
				break
			}
		}
		ps := orderPaths(g.paths)
		if m == nil {
			id := f.ids[g.key]
			if id == "" || taken[id] && !derivedHas(f.derived, id) {
				id = derivedID(ps[0], taken, d.hubNameOf(ps[0].hub))
			}
			taken[id] = true
			var cfg MachineConfig
			_ = json.Unmarshal(ps[0].rec.Cfg, &cfg)
			cfg.ID, cfg.Kind = id, kindHub
			if cfg.Name == "" {
				cfg.Name = ps[0].mid
			}
			m = newMachine(d, cfg)
			m.raw = ps[0].rec.Cfg
			m.derived = true
		} else if m.setName(derivedName(ps[0])) {
			renamed = true // the member renamed it: the same machine, connected as it is
		}
		used[m] = true
		if f.ids[g.key] != m.cfg.ID {
			f.ids[g.key] = m.cfg.ID
		}
		newDerived[g.key] = m
		newPaths[m] = ps
		derivedList = append(derivedList, m)
	}
	sort.Slice(derivedList, func(i, j int) bool { return derivedList[i].cfg.ID < derivedList[j].cfg.ID })

	// What changed: machines gone, machines new, paths moved.
	var gone, started []*Machine
	for _, m := range f.derived {
		if !used[m] {
			gone = append(gone, m)
		}
	}
	for _, m := range newDerived {
		if f.cancel[m] == nil {
			started = append(started, m)
		}
	}
	changed := len(gone) > 0 || len(started) > 0 || len(newAbsorbed) != len(f.absorbed) || !samePaths(f.paths, newPaths)
	for m, to := range newAbsorbed {
		if f.absorbed[m] != to {
			changed = true
		}
	}
	f.derived, f.absorbed, f.paths = newDerived, newAbsorbed, newPaths
	f.order = append(listed, derivedList...)
	d.mu.Lock()
	ctx := d.ctx
	d.mu.Unlock()
	cancels := map[*Machine]context.CancelFunc{}
	for _, m := range gone {
		cancels[m] = f.cancel[m]
		delete(f.cancel, m)
	}
	for _, m := range started {
		if ctx == nil {
			continue
		}
		mctx, cancel := context.WithCancel(ctx)
		f.cancel[m] = cancel
		m.ctx = mctx // set before its run loop starts: onEvent reads it
		d.workers.Add(1)
		go func() {
			defer d.workers.Done()
			m.run(mctx)
		}()
	}
	ids := make(map[string]string, len(f.ids))
	for k, v := range f.ids {
		if newDerived[k] != nil {
			ids[k] = v
		}
	}
	f.ids = ids
	var wake []*Machine
	if changed {
		close(f.changed)
		f.changed = make(chan struct{})
		for m := range newPaths {
			wake = append(wake, m)
		}
		wake = append(wake, cfgs...)
	}
	f.mu.Unlock()

	for m, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
		d.logf("[%s] no longer reachable through any hub of the group: removed", m.cfg.ID)
		d.forgetMachine(m.cfg.ID)
	}
	for _, m := range started {
		d.logf("[%s] a member's computer: reached through %s", m.cfg.ID, d.hubNameOf(d.pathsOf(m)[0].hub))
	}
	if !changed {
		if renamed {
			d.changed()
		}
		return
	}
	for _, m := range wake {
		m.wake()
	}
	if d.opts.Store != nil {
		go d.saveMachineIDs()
	}
	d.changed()
}

// saveMachineIDs writes the ids given to members' computers as they are now. The copy is taken after the previous
// write, under the lock: a write started earlier never lands after a newer one, and the map encoded is not the one
// the next regroup writes to.
func (d *Devices) saveMachineIDs() {
	f := &d.fleet
	f.saveIDs.Lock()
	defer f.saveIDs.Unlock()
	f.mu.Lock()
	ids := maps.Clone(f.ids)
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = d.opts.Store.PutSetting(ctx, "mesh.machine_ids", ids)
}

func derivedHas(d map[string]*Machine, id string) bool {
	for _, m := range d {
		if m.cfg.ID == id {
			return true
		}
	}
	return false
}

// derivedName is what the member calls the computer of p: its hub.json name, else its id there (as a new derived
// machine is named).
func derivedName(p *nodePath) string {
	var cfg struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(p.rec.Cfg, &cfg)
	return cmp.Or(cfg.Name, p.mid)
}

// derivedID is the id a member's computer gets: its id in that member's hub.json, unless the center (or another
// member) has it already; then @<the member's name>, then the member's hub_id too, then a number.
func derivedID(p *nodePath, taken map[string]bool, hubName string) string {
	base := p.mid
	if !taken[base] {
		return base
	}
	suffix := hubSlug(hubName, p.hub)
	id := base + "@" + suffix
	if taken[id] {
		id = base + "@" + suffix + "-" + shortID(p.hub)
	}
	for n := 2; taken[id]; n++ {
		id = base + "@" + suffix + "-" + shortID(p.hub) + "-" + itoa(n)
	}
	return id
}

// orderPaths: hub.json entries in their order, then members' by hub_id and id.
func orderPaths(ps []*nodePath) []*nodePath {
	out := append([]*nodePath(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.src != nil) != (b.src != nil) {
			return a.src != nil
		}
		if a.src != nil {
			return false // hub.json order
		}
		if a.hub != b.hub {
			return a.hub < b.hub
		}
		return a.mid < b.mid
	})
	return out
}

func samePaths(a, b map[*Machine][]*nodePath) bool {
	if len(a) != len(b) {
		return false
	}
	for m, pa := range a {
		pb, ok := b[m]
		if !ok || len(pa) != len(pb) {
			return false
		}
		for i := range pa {
			if pa[i].key != pb[i].key || pa[i].rec.Avail != pb[i].rec.Avail {
				return false
			}
		}
	}
	return true
}

// wakeOwnerOf wakes the machine a hub.json entry's path belongs to (the entry itself, or the one that has its computer).
func (d *Devices) wakeOwnerOf(m *Machine) {
	f := &d.fleet
	f.mu.Lock()
	to := f.absorbed[m]
	f.mu.Unlock()
	if to != nil {
		to.wake()
	}
}

// parkReason says why a machine's run loop does not connect now ("" when it should): a member leaves its computers to
// the center; an entry whose computer another entry has is reached through that one.
func (d *Devices) parkReason(m *Machine) string {
	if mesh := d.config().Mesh; mesh != nil && mesh.Member {
		return "由中心 hub「" + mesh.CenterName + "」连着"
	}
	f := &d.fleet
	f.mu.Lock()
	defer f.mu.Unlock()
	if to := f.absorbed[m]; to != nil {
		return "和「" + to.name() + "」是同一台电脑"
	}
	if m.derived {
		if _, ok := f.paths[m]; !ok {
			return "removed"
		}
	}
	return ""
}

// pathState is what is known of a path now: whether it can be tried, its round trip (ms, 0 unknown).
type pathState struct {
	avail     bool
	rtt       int
	failUntil time.Time
	err       string
}

func (d *Devices) pathState(p *nodePath) pathState {
	f := &d.fleet
	f.mu.Lock()
	var hl pathHealth
	if x := f.health[p.key]; x != nil {
		hl = *x
	}
	f.mu.Unlock()
	st := pathState{failUntil: hl.failUntil, err: hl.err, rtt: hl.rtt}
	switch p.via {
	case viaHub:
		var hub devicewire.MeshHub
		if mesh := d.config().Mesh; mesh != nil {
			hub = mesh.Hubs[p.hub]
		}
		st.avail = hub.Reachable && p.rec.Avail
		if !hub.Reachable {
			st.err = "「" + d.hubNameOf(p.hub) + "」现在连不上"
		}
		if hub.Reachable && hub.RTTMS >= 0 && p.rec.RTTMS > 0 {
			st.rtt = hub.RTTMS + p.rec.RTTMS
		}
	default:
		avail, rtt := p.src.reachableHere()
		st.avail = avail
		if rtt > 0 {
			st.rtt = rtt
		}
		if !avail {
			if why := p.src.unavailableWhy(); why != "" {
				st.err = why // what keeps it from being tried says more than how the last try ended
			}
		}
	}
	return st
}

// pathBefore orders two available paths: class, then a known round trip before an unknown one, then the smaller.
func pathBefore(a *nodePath, as pathState, b *nodePath, bs pathState) bool {
	if a.class() != b.class() {
		return a.class() < b.class()
	}
	if (as.rtt > 0) != (bs.rtt > 0) {
		return as.rtt > 0
	}
	return as.rtt < bs.rtt
}

// clearlyBetter: q is worth leaving p for — a better kind of path, or the same kind with a clearly smaller round trip.
func clearlyBetter(q *nodePath, qs pathState, p *nodePath, ps pathState) bool {
	if q.class() != p.class() {
		return q.class() < p.class()
	}
	return qs.rtt > 0 && ps.rtt > 0 && qs.rtt*10 < ps.rtt*7 && ps.rtt-qs.rtt >= 5
}

// choosePath is the path a machine should connect through now; nil and how long to wait when none can be tried.
func (d *Devices) choosePath(m *Machine) (*nodePath, time.Duration) {
	var best *nodePath
	var bestSt pathState
	wait := time.Duration(0)
	now := time.Now()
	for _, p := range d.pathsOf(m) {
		st := d.pathState(p)
		if !st.avail {
			continue
		}
		if now.Before(st.failUntil) {
			if d := st.failUntil.Sub(now); wait == 0 || d < wait {
				wait = d
			}
			continue
		}
		if best == nil || pathBefore(p, st, best, bestSt) {
			best, bestSt = p, st
		}
	}
	return best, wait
}

// betterPath is a path clearly better than the one in use that has stayed so for mesh.path_switch_seconds (since
// tracks when each became better); nil for none.
func (d *Devices) betterPath(m *Machine, cur *nodePath, since map[string]time.Time) *nodePath {
	curSt := d.pathState(cur)
	now := time.Now()
	var pick *nodePath
	var pickSt pathState
	seen := map[string]bool{}
	for _, p := range d.pathsOf(m) {
		if p.key == cur.key {
			continue
		}
		st := d.pathState(p)
		if !st.avail || now.Before(st.failUntil) || !clearlyBetter(p, st, cur, curSt) {
			continue
		}
		seen[p.key] = true
		if since[p.key].IsZero() {
			since[p.key] = now
		}
		if now.Sub(since[p.key]) < d.pathSwitch() {
			continue
		}
		if pick == nil || pathBefore(p, st, pick, pickSt) {
			pick, pickSt = p, st
		}
	}
	for k := range since {
		if !seen[k] {
			delete(since, k)
		}
	}
	return pick
}

// pathEnded records how a connection through p went: a path that held for a while is fine again at once; one that
// failed waits, longer each time in a row (2 s doubling to a minute).
func (d *Devices) pathEnded(p *nodePath, online bool, err error, held time.Duration, planned bool) {
	f := &d.fleet
	f.mu.Lock()
	f.init()
	hl := f.health[p.key]
	if hl == nil {
		hl = &pathHealth{}
		f.health[p.key] = hl
	}
	switch {
	case planned:
		hl.fails, hl.failUntil, hl.err = 0, time.Time{}, ""
	case online && held > time.Minute:
		hl.fails = 0
		hl.failUntil = time.Now().Add(2 * time.Second)
	default:
		hl.fails = min(hl.fails+1, 6)
		hl.failUntil = time.Now().Add(min(time.Second<<hl.fails, time.Minute))
	}
	if err != nil && !planned {
		hl.err = err.Error()
	}
	f.mu.Unlock()
}

// pathConnected records that p reached the node.
func (d *Devices) pathConnected(p *nodePath) {
	f := &d.fleet
	f.mu.Lock()
	f.init()
	hl := f.health[p.key]
	if hl == nil {
		hl = &pathHealth{}
		f.health[p.key] = hl
	}
	hl.okAt, hl.err = time.Now(), ""
	f.mu.Unlock()
}

// pathRTT records the node's round trip through p (the path in use, measureNodes).
func (d *Devices) pathRTT(p *nodePath, rtt int) {
	f := &d.fleet
	f.mu.Lock()
	f.init()
	hl := f.health[p.key]
	if hl == nil {
		hl = &pathHealth{}
		f.health[p.key] = hl
	}
	if hl.rtt == 0 || abs(rtt-hl.rtt) > max(2, hl.rtt/5) { // noise does not count as a change
		hl.rtt = rtt
	}
	f.mu.Unlock()
}

// pathInfos is a machine's paths for the admin page and the machine list.
func (d *Devices) pathInfos(m *Machine) []pathInfo {
	active := m.activePathKey()
	self := d.config().HubID
	var out []pathInfo
	for _, p := range d.pathsOf(m) {
		st := d.pathState(p)
		pi := pathInfo{Via: p.via, HubID: self, OK: st.avail && time.Now().After(st.failUntil), RTTMS: st.rtt,
			Active: p.key == active, Error: st.err}
		if p.via == viaHub {
			pi.HubID, pi.ID = p.hub, p.mid
		} else {
			pi.ID = p.src.cfg.ID
			if pi.Active {
				if t := m.transportNow(); t != "" {
					pi.Via = t // ssh that fell back to tailcat
				}
			}
		}
		if pi.Active {
			pi.OK, pi.Error = true, ""
		}
		out = append(out, pi)
	}
	return out
}

// measureNodes measures the round trip to every computer this hub drives, over the path in use.
func (d *Devices) measureNodes(ctx context.Context) {
	for _, m := range d.machinesNow() {
		p := m.activePath()
		if p == nil || !m.isOnline() {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		start := time.Now()
		err := m.call(cctx, "ping", map[string]any{}, nil)
		cancel()
		if err == nil {
			d.pathRTT(p, max(1, int(time.Since(start).Milliseconds())))
		}
	}
}

// decorate fills in what the apps see of how a machine is reached: the hub it is reached through, its own id, the
// route, its paths.
func (d *Devices) decorate(m *Machine, s proto.Machine) proto.Machine {
	cfg := d.config()
	s.HubID, s.HubName = cfg.HubID, cfg.HubName
	f := &d.fleet
	f.mu.Lock()
	for _, p := range f.paths[m] {
		if uid := f.uids[p.key]; uid != "" {
			s.MachineUID = uid
			break
		}
	}
	f.mu.Unlock()
	if uid := m.nodeUID(); uid != "" {
		s.MachineUID = uid
	}
	infos := d.pathInfos(m)
	route := &proto.MachineRoute{Via: kindOf(m.cfg)}
	if m.derived {
		route.Via = viaHub
	}
	for i, pi := range infos {
		if pi.Active || i == 0 && !s.Online {
			route = &proto.MachineRoute{Via: pi.Via}
			if pi.Via == viaHub {
				route.Hops = 1
				s.HubID, s.HubName = pi.HubID, d.hubNameOf(pi.HubID)
			}
			if s.Online && pi.RTTMS > 0 {
				rtt := pi.RTTMS
				route.RTTMS = &rtt
			}
			if pi.Active {
				break
			}
		}
	}
	s.Route = route
	if len(infos) > 1 || m.derived {
		for _, pi := range infos {
			s.Paths = append(s.Paths, proto.MachinePath{Via: pi.Via, HubID: pi.HubID, OK: pi.OK, RTTMS: pi.RTTMS, Active: pi.Active})
		}
	}
	return s
}

// hubNameOf is what a hub of the group is called ("" for an unknown one: its id).
func (d *Devices) hubNameOf(id string) string {
	cfg := d.config()
	if id == cfg.HubID {
		return cfg.HubName
	}
	if cfg.Mesh != nil {
		if hub, ok := cfg.Mesh.Hubs[id]; ok && hub.Name != "" {
			return hub.Name
		}
	}
	return id
}

// meshSettling is how long to wait before connecting anything: just started with peers, the group may have another
// center by now.
func (d *Devices) meshSettling() time.Duration {
	if mesh := d.config().Mesh; mesh != nil && time.Now().Before(mesh.SettleUntil) {
		return time.Until(mesh.SettleUntil)
	}
	return 0
}

// meshWait is closed at the next change of the hub's handover (the mesh among it).
func (d *Devices) meshWait() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfgChanged == nil {
		d.cfgChanged = make(chan struct{})
	}
	return d.cfgChanged
}

// pathSwitch is how long a better path must stay better before the switch (mesh.path_switch_seconds).
func (d *Devices) pathSwitch() time.Duration {
	if mesh := d.config().Mesh; mesh != nil && mesh.PathSwitchMS > 0 {
		return time.Duration(mesh.PathSwitchMS) * time.Millisecond
	}
	return 30 * time.Second
}

// measureLoop measures the round trip to every computer now and then (the choice between paths uses it).
func (d *Devices) measureLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		d.measureNodes(ctx)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// hubSlug is a hub's name made fit for a machine id suffix: lowercase letters, digits and dashes; a name with none
// of those (say, a Chinese one) gives the start of the hub_id.
func hubSlug(name, hubID string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return shortID(hubID)
	}
	return s
}

func shortID(hubID string) string {
	if len(hubID) > 6 {
		return hubID[:6]
	}
	return hubID
}

func itoa(n int) string { return strconv.Itoa(n) }

// ShortenTimings makes the checks of tests elsewhere (the hub's end-to-end tests) quick: how often the path in use is
// compared with the others, a connector's info is read, and how long one that refused is left alone.
func ShortenTimings(pathCheck, serviceProbe, refusedWait time.Duration) {
	pathCheckEvery, serviceProbeEvery, connectorRefusedWait = pathCheck, serviceProbe, refusedWait
}
