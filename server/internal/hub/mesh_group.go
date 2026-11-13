package hub

import (
	"context"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/i18n"
)

// One center per group (docs/archive/operations/plan-hub-center.md 2, docs/server/mesh.md "Primary"): every hub of a mesh group keeps which hub
// it takes for the center and a term; the larger term wins wherever records meet, so the whole group comes to agree.
// A hub alone is its own center. Using another hub's code joins this hub (with its whole group) to that hub's group:
// the center becomes that group's, under a term larger than both had. "Set as center" in the admin page adds one to
// the term. A center that cannot be reached is never replaced by itself (two centers at once would split the state):
// members answer the apps 503 center_unreachable until someone sets another center by hand; an old center coming back
// hears the larger term and becomes a member.

type meshGroup struct {
	Center string `json:"center"`
	Term   int64  `json:"term"`
}

var errCenterOffline = i18n.Err("hub-mesh_group.center_offline")

// loadGroup reads the group's center from the database (a hub that never had one is its own, term 0).
func (h *Hub) loadGroup(ctx context.Context) {
	g := meshGroup{Center: h.hubID()}
	if cs := h.centerStore(); cs != nil {
		center, term, ok, err := cs.MeshGroup(ctx, h.hubID())
		if err != nil {
			h.logf("mesh: load the group's center: %v", err)
		} else if ok && center != "" {
			g = meshGroup{Center: center, Term: term}
		}
	}
	ms := &h.mesh
	ms.mu.Lock()
	ms.group = g
	ms.mu.Unlock()
	if g.Center != h.hubID() {
		h.logf("mesh: a member of the group whose center is %s (term %d)", shortID(g.Center), g.Term)
	}
}

// group is the center this hub has and its term.
func (h *Hub) group() meshGroup {
	ms := &h.mesh
	ms.mu.Lock()
	defer ms.mu.Unlock()
	g := ms.group
	if !ms.running || g.Center == "" {
		g.Center = h.hubID()
	}
	return g
}

// centerID is the group's center (this hub when it is alone, or the mesh is not running).
func (h *Hub) centerID() string { return h.group().Center }

// isCenter: this hub serves the apps and drives the computers (a hub without peers always does).
func (h *Hub) isCenter() bool { return h.centerID() == h.hubID() }

// isMember: another hub is the center; this one passes the apps' requests and its computers' streams on to it.
func (h *Hub) isMember() bool { return !h.isCenter() }

// centerReachable: there is a way over the links to the center (always, for the center itself).
func (h *Hub) centerReachable() bool {
	c := h.centerID()
	return c == h.hubID() || h.meshView().reachable(c)
}

// adoptGroup takes the center a record (or a pairing) says when its term is larger; at the same term the smaller
// center hub_id wins, so two hubs that raced settle the same way everywhere. It reports whether anything changed.
func (h *Hub) adoptGroup(center string, term int64, why string) bool {
	if center == "" {
		return false
	}
	ms := &h.mesh
	ms.mu.Lock()
	cur := ms.group
	if cur.Center == "" {
		cur.Center = h.hubID()
	}
	if term < cur.Term || term == cur.Term && (center == cur.Center || center > cur.Center) {
		ms.mu.Unlock()
		return false
	}
	ms.group = meshGroup{Center: center, Term: term}
	ms.mu.Unlock()
	h.logf("mesh: the center is now %s, term %d (%s)", h.hubLabel(center), term, why)
	h.saveGroup(meshGroup{Center: center, Term: term})
	h.groupChanged(cur.Center, center)
	return true
}

// setGroup makes center the center under a term past every one this hub has seen (joining, set as center).
func (h *Hub) setGroup(center string, atLeast int64, why string) meshGroup {
	ms := &h.mesh
	ms.mu.Lock()
	cur := ms.group
	if cur.Center == "" {
		cur.Center = h.hubID()
	}
	term := max(cur.Term, atLeast) + 1
	for _, r := range ms.records {
		term = max(term, r.Term+1)
	}
	g := meshGroup{Center: center, Term: term}
	ms.group = g
	ms.mu.Unlock()
	h.logf("mesh: the center is now %s, term %d (%s)", h.hubLabel(center), term, why)
	h.saveGroup(g)
	h.groupChanged(cur.Center, center)
	return g
}

func (h *Hub) saveGroup(g meshGroup) {
	if cs := h.centerStore(); cs != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cs.SaveMeshGroup(ctx, h.hubID(), g.Center, g.Term); err != nil {
			h.logf("mesh: save the group's center: %v", err)
		}
	}
}

// groupChanged acts on a new center: this hub's role may have changed (it starts or stops driving its computers and
// serving apps), every hub's record says the new center, and a member hands its credentials over.
func (h *Hub) groupChanged(oldCenter, newCenter string) {
	defer h.assistantUpdated() // a member's assistant stands by
	self := h.hubID()
	switch {
	case oldCenter == self && newCenter != self:
		h.logf("mesh: this hub is a member now: its apps go to the center, its computers too")
		h.becameMember()
	case oldCenter != self && newCenter == self:
		h.logf("mesh: this hub is the center now")
		h.becameCenter()
	case oldCenter != newCenter:
		h.dropAppConnections("the group has another center") // passing on to the old one: reconnect to the new one
	}
	h.mesh.mu.Lock()
	h.mesh.handedTo = ""
	h.mesh.mu.Unlock()
	h.meshPublish(true)
	h.meshRebuild()
	h.sendLinkEndpoints()
}

// becameMember: this hub stops driving its computers (the center connects through it) and serving apps (their
// connections are dropped: they come back and are passed on to the center).
func (h *Hub) becameMember() {
	h.dropAppConnections("this hub joined a group: the center serves the apps now")
	h.fleetRegroup() // the manager parks the machines: the center connects them through this hub
}

// becameCenter: this hub drives the computers again, its own and the members'.
func (h *Hub) becameCenter() {
	h.dropAppConnections("this hub is the center now")
	h.fleetRegroup() // the manager connects the computers again, its own and the members'
}

// dropAppConnections closes every app WebSocket this hub serves itself, and every request it is passing on to a
// center (they reconnect, and get where they should now).
func (h *Hub) dropAppConnections(why string) {
	h.dropProxiedApps()
	h.mu.Lock()
	var gone []*client
	for c := range h.clients {
		gone = append(gone, c)
	}
	h.mu.Unlock()
	for _, c := range gone {
		if c.conn != nil {
			go c.conn.Close(websocket.StatusServiceRestart, why)
		}
	}
}

// setCenter makes hub target the group's center (the admin page's "Make primary"). The center now in place hears the
// larger term and becomes a member; when it cannot be reached that needs confirm (it becomes a member when it is back).
func (h *Hub) setCenter(ctx context.Context, target string, confirm bool) (meshGroup, error) {
	g := h.group()
	if target == "" {
		target = h.hubID()
	}
	if target == g.Center {
		return g, nil
	}
	v := h.meshView()
	if target != h.hubID() && !v.reachable(target) {
		return g, i18n.Err("hub-mesh_group.target_unreachable", i18n.Args{"id": shortID(target)})
	}
	if g.Center != h.hubID() && !v.reachable(g.Center) && !confirm {
		return g, errCenterOffline
	}
	return h.setGroup(target, g.Term, "set as center in the admin page"), nil
}

// leftGroup: this hub has no peers any more: it is its own center again (its own data was kept all along).
func (h *Hub) leftGroup() {
	ms := &h.mesh
	ms.mu.Lock()
	alone := len(ms.peers) == 0
	center := ms.group.Center
	ms.mu.Unlock()
	if alone && center != "" && center != h.hubID() {
		h.setGroup(h.hubID(), 0, "no other hub is paired")
	}
}

// meshSettle: how long a hub that starts with peers waits for a link before driving its computers.
const meshSettle = 10 * time.Second

// credsHandoverRetryEvery is how soon a hub retries handing over device credentials if the center rejected or dropped the call.
var credsHandoverRetryEvery = 30 * time.Second

// handOverCredentials gives the center the device credentials this hub knows (once per center and term): the apps
// paired with this hub keep working when their requests reach the center (plan 3 "Device credentials").
func (h *Hub) handOverCredentials() {
	if !h.isMember() || !h.centerReachable() {
		return
	}
	cs := h.centerStore()
	if cs == nil {
		return
	}
	g := h.group()
	key := g.Center + "/" + itoa(int(g.Term))
	ms := &h.mesh
	ms.mu.Lock()
	if ms.handedTo == key {
		ms.mu.Unlock()
		return
	}
	ms.handedTo = key // one try at a time; a failure clears it for the next change
	ms.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		list, err := cs.AppTokens(ctx)
		if err == nil && len(list) > 0 {
			var res struct {
				Added int `json:"added"`
			}
			err = h.meshCall(ctx, g.Center, "creds.import", map[string]any{"tokens": list}, &res)
			if err == nil {
				h.logf("mesh: handed %d device credentials to the center (%d new there)", len(list), res.Added)
			}
		}
		if err != nil {
			h.logf("mesh: hand the device credentials to the center: %v", err)
			ms.mu.Lock()
			if ms.handedTo == key {
				ms.handedTo = ""
			}
			ms.mu.Unlock()
			time.AfterFunc(credsHandoverRetryEvery, h.handOverCredentials)
		}
	}()
}
