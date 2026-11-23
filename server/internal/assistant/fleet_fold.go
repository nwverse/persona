package assistant

import (
	"context"
	"maps"
	"slices"

	"github.com/nwverse/persona/server/internal/proto"
)

// ---- children folded into their parent (#5120), a tab's workers into its lead (#5180), side sessions into the
// session they were started from (#7944), and cross-machine A2A / dispatch subagents across the fleet ----
// docs/protocol/protocol.md "Session hierarchy"

// sessionFold is what folding says of one machine's list: the session each folded row folds under (its topmost
// listed ancestor, with underMachine set when that ancestor lives on another computer), why for a row that is no child
// (as: proto.FoldedAsWorker or proto.FoldedAsSide), the side session a row folded through one belongs to (side, not
// set on that side itself), and the rollup each such ancestor carries of all the rows folded under it (including
// cross-machine children).
type sessionFold struct {
	under        map[string]string
	underMachine map[string]string
	as           map[string]string
	side         map[string]string
	children     map[string]*proto.SessionChildren
}

// tabKey is a host tab of one machine's list (a herdr tab): its space kind, space and slot.
type tabKey struct{ kind, space, slot string }

// foldEntry holds only the lightweight hierarchy metadata needed to fold across machines without retaining full
// ~1.5 KB proto.Session copies.
type foldEntry struct {
	sessionID             string
	state                 string
	isSubagent            bool
	parentMachineID       string
	parentSessionID       string
	askingParentSessionID string
	rootSessionID         string
	parentAgentID         string
	assistant             bool
	archived              bool
	hiddenByDirectory     bool
	hidden                bool
	spaceKind             string
	spaceID               string
	slotID                string
	hasPaneIndex          bool
	paneIndex             int
}

func (e *foldEntry) listed() bool {
	return (!e.archived || e.assistant) && !e.hiddenByDirectory && !e.hidden
}

func (e *foldEntry) isChild() bool {
	return e.isSubagent || e.parentSessionID != "" || e.askingParentSessionID != "" ||
		(e.rootSessionID != "" && e.rootSessionID != e.sessionID)
}

func (e *foldEntry) tabSeat() bool {
	return !e.assistant && e.sessionID != "" && e.spaceID != "" && e.slotID != "" && !e.isChild() && e.state != proto.StateOffline
}

func toFoldEntries(list []proto.Session, hidden map[string]bool) []foldEntry {
	out := make([]foldEntry, len(list))
	for i := range list {
		s := &list[i]
		e := foldEntry{
			sessionID:             s.SessionID,
			state:                 s.State,
			isSubagent:            s.IsSubagent,
			parentMachineID:       s.ParentMachineID(),
			parentSessionID:       s.ParentSessionID,
			askingParentSessionID: s.AskingParentSessionID(),
			rootSessionID:         s.RootSessionID,
			parentAgentID:         s.ParentAgentID(),
			assistant:             s.Assistant,
			archived:              s.Archived,
			hiddenByDirectory:     s.HiddenByDirectory(),
			hidden:                hidden[s.SessionID],
			spaceKind:             s.SpaceKind,
			spaceID:               s.SpaceID,
			slotID:                s.SlotID,
		}
		if s.PaneIndex != nil {
			e.hasPaneIndex, e.paneIndex = true, *s.PaneIndex
		}
		out[i] = e
	}
	return out
}

// foldSessions folds a machine's complete list (its rows as decorated, their states computed). Only the rows the apps
// are shown are listed: not an archived Agent (the hub leaves those out, deleted_sessions.go), nor one in hidden (a
// review's reviewer, which publishList leaves out). A child (a subagent, or a session that names a parent or another
// root, proto.Session.IsChild) has as its parent the listed row its parent_session_id names, else the one its
// asking_parent_session_id names, else its root_session_id's; it folds under the topmost listed ancestor that parent
// leads to. A side session (sideParents, #7944) has as its parent the row its parent_agent_id names and is marked
// proto.FoldedAsSide; a row that folds through a side further up names the nearest one (folded_side). A tab's worker
// (tabLeads, #5180) has its tab's lead as its parent and is marked proto.FoldedAsWorker; a side is in no tab team. An
// orphan (no listed parent) and a row whose ancestors go round in a cycle fold under nothing. Each ancestor that rows
// fold under counts them all, whatever their depth, by their own states, how many of them are its workers, and how
// many are side sessions, by their own states.
func foldSessions(list []proto.Session, hidden map[string]bool) sessionFold {
	listed := func(s *proto.Session) bool {
		return (!s.Archived || s.Assistant) && !s.HiddenByDirectory() && !hidden[s.SessionID]
	}
	if !mayFold(list, listed) {
		return sessionFold{} // what the walk below finds for such a list, without the index it builds
	}
	at := make(map[string]int, len(list))
	for i := range list {
		if _, dup := at[list[i].SessionID]; !dup && listed(&list[i]) {
			at[list[i].SessionID] = i
		}
	}
	counted := func(i int) bool { j, ok := at[list[i].SessionID]; return ok && j == i }
	sides := sideParents(list, at, counted)
	lead := tabLeads(list, func(i int) bool { _, side := sides[i]; return counted(i) && !side })
	parent := func(i int) int {
		s := &list[i]
		if !s.IsChild() {
			if j, ok := sides[i]; ok {
				return j
			}
			if j, ok := lead[i]; ok {
				return j
			}
			return -1
		}
		// A row whose parent lives on another computer cannot fold inside a single-machine list.
		if pm := s.ParentMachineID(); pm != "" && (s.MachineID == "" || pm != s.MachineID) {
			return -1
		}
		root := s.RootSessionID
		if root == s.SessionID {
			root = ""
		}
		for _, id := range [...]string{s.ParentSessionID, s.AskingParentSessionID(), root} {
			if j, ok := at[id]; ok && id != "" && j != i {
				return j
			}
		}
		return -1
	}
	var f sessionFold
	var path []int // the rows walked up from one child, to find a cycle
	for i := range list {
		if at[list[i].SessionID] != i || !listed(&list[i]) {
			continue
		}
		top := i
		path = append(path[:0], i)
		for {
			p := parent(top)
			if p < 0 {
				break
			}
			if slices.Contains(path, p) {
				top = i // a cycle: no topmost ancestor
				break
			}
			path = append(path, p)
			top = p
		}
		if top == i {
			continue
		}
		if f.under == nil {
			f.under, f.children = map[string]string{}, map[string]*proto.SessionChildren{}
		}
		id, root := list[i].SessionID, list[top].SessionID
		f.under[id] = root
		c := f.children[root]
		if c == nil {
			c = &proto.SessionChildren{}
			f.children[root] = c
		}
		c.Total++
		switch list[i].State {
		case proto.StateWorking:
			c.Working++
		case proto.StateNeedsYou:
			c.NeedsYou++
		}
		_, side := sides[i]
		if _, worker := lead[i]; worker || side { // a worker or a side itself, not a row folded through one
			if f.as == nil {
				f.as = map[string]string{}
			}
			if side {
				f.as[id] = proto.FoldedAsSide
				c.Sides++
				switch list[i].State {
				case proto.StateWorking:
					c.SidesWorking++
				case proto.StateNeedsYou:
					c.SidesNeedsYou++
				case proto.StateUnread:
					c.SidesUnread++
				}
			} else {
				f.as[id] = proto.FoldedAsWorker
				c.Workers++
			}
			continue
		}
		// The nearest side on the way up, the topmost row aside (it folds under nothing, so it is no side here).
		for _, p := range path[1 : len(path)-1] {
			if _, ok := sides[p]; ok {
				if f.side == nil {
					f.side = map[string]string{}
				}
				f.side[id] = list[p].SessionID
				break
			}
		}
	}
	return f
}

// foldFleet folds decorated lists across every machine in lists: same-machine children, tab teams and side sessions
// fold as foldSessions does, and cross-machine children (ParentMachineID != machine) walk up to their topmost listed
// ancestor across machines, setting underMachine on the child's fold and rolling their state into the ancestor's
// SessionChildren on the parent machine.
func foldFleet(lists map[string][]proto.Session, hidden map[string]map[string]bool) map[string]sessionFold {
	entries := make(map[string][]foldEntry, len(lists))
	for m, list := range lists {
		entries[m] = toFoldEntries(list, hidden[m])
	}
	return foldFleetEntries(entries)
}

func foldFleetEntries(machines map[string][]foldEntry) map[string]sessionFold {
	type nodeRef struct {
		machine string
		idx     int
	}
	at := make(map[string]map[string]int, len(machines))
	sides := make(map[string]map[int]int, len(machines))
	leads := make(map[string]map[int]int, len(machines))
	for m, list := range machines {
		idxMap := make(map[string]int, len(list))
		for i := range list {
			if _, dup := idxMap[list[i].sessionID]; !dup && list[i].listed() {
				idxMap[list[i].sessionID] = i
			}
		}
		at[m] = idxMap
		counted := func(i int) bool { j, ok := idxMap[list[i].sessionID]; return ok && j == i }
		sides[m] = sideParentsEntries(list, idxMap, counted)
		leads[m] = tabLeadsEntries(list, func(i int) bool { _, side := sides[m][i]; return counted(i) && !side })
	}
	parentOf := func(ref nodeRef) (nodeRef, bool) {
		list := machines[ref.machine]
		e := &list[ref.idx]
		if !e.isChild() {
			if j, ok := sides[ref.machine][ref.idx]; ok {
				return nodeRef{ref.machine, j}, true
			}
			if j, ok := leads[ref.machine][ref.idx]; ok {
				return nodeRef{ref.machine, j}, true
			}
			return nodeRef{}, false
		}
		pm := e.parentMachineID
		if pm == "" {
			pm = ref.machine
		}
		if e.parentSessionID != "" {
			if j, ok := at[pm][e.parentSessionID]; ok && (pm != ref.machine || j != ref.idx) {
				return nodeRef{pm, j}, true
			}
		}
		if e.askingParentSessionID != "" {
			if j, ok := at[pm][e.askingParentSessionID]; ok && (pm != ref.machine || j != ref.idx) {
				return nodeRef{pm, j}, true
			}
		}
		if pm == ref.machine {
			root := e.rootSessionID
			if root == e.sessionID {
				root = ""
			}
			if root != "" {
				if j, ok := at[ref.machine][root]; ok && j != ref.idx {
					return nodeRef{ref.machine, j}, true
				}
			}
		}
		return nodeRef{}, false
	}

	folds := make(map[string]sessionFold, len(machines))
	for m := range machines {
		folds[m] = sessionFold{}
	}
	var path []nodeRef
	for m, list := range machines {
		for i := range list {
			if at[m][list[i].sessionID] != i || !list[i].listed() {
				continue
			}
			start := nodeRef{m, i}
			top := start
			path = append(path[:0], start)
			for {
				p, ok := parentOf(top)
				if !ok {
					break
				}
				if slices.Contains(path, p) {
					top = start // a cycle: no topmost ancestor
					break
				}
				path = append(path, p)
				top = p
			}
			if top == start {
				continue
			}
			id := list[i].sessionID
			rootID := machines[top.machine][top.idx].sessionID

			mf := folds[m]
			if mf.under == nil {
				mf.under = map[string]string{}
			}
			mf.under[id] = rootID
			if top.machine != m {
				if mf.underMachine == nil {
					mf.underMachine = map[string]string{}
				}
				mf.underMachine[id] = top.machine
			}
			folds[m] = mf

			rf := folds[top.machine]
			if rf.children == nil {
				rf.children = map[string]*proto.SessionChildren{}
			}
			c := rf.children[rootID]
			if c == nil {
				c = &proto.SessionChildren{}
				rf.children[rootID] = c
			}
			c.Total++
			switch list[i].state {
			case proto.StateWorking:
				c.Working++
			case proto.StateNeedsYou:
				c.NeedsYou++
			}
			_, side := sides[m][i]
			if _, worker := leads[m][i]; worker || side {
				mf = folds[m]
				if mf.as == nil {
					mf.as = map[string]string{}
				}
				if side {
					mf.as[id] = proto.FoldedAsSide
					c.Sides++
					switch list[i].state {
					case proto.StateWorking:
						c.SidesWorking++
					case proto.StateNeedsYou:
						c.SidesNeedsYou++
					case proto.StateUnread:
						c.SidesUnread++
					}
				} else {
					mf.as[id] = proto.FoldedAsWorker
					c.Workers++
				}
				folds[m] = mf
				folds[top.machine] = rf
				continue
			}
			folds[top.machine] = rf
			if top.machine == m {
				for _, p := range path[1 : len(path)-1] {
					if p.machine == m {
						if _, ok := sides[m][p.idx]; ok {
							mf = folds[m]
							if mf.side == nil {
								mf.side = map[string]string{}
							}
							mf.side[id] = machines[m][p.idx].sessionID
							folds[m] = mf
							break
						}
					}
				}
			}
		}
	}
	return folds
}

func sideParentsEntries(list []foldEntry, at map[string]int, counted func(int) bool) map[int]int {
	var out map[int]int
	for i := range list {
		e := &list[i]
		id := e.parentAgentID
		if id == "" || id == e.sessionID || e.assistant || e.isChild() || !counted(i) {
			continue
		}
		if j, ok := at[id]; ok && j != i && !list[j].assistant {
			if out == nil {
				out = map[int]int{}
			}
			out[i] = j
		}
	}
	return out
}

func tabLeadsEntries(list []foldEntry, counted func(int) bool) map[int]int {
	member := func(i int) (tabKey, bool) {
		e := &list[i]
		if !e.tabSeat() || !counted(i) {
			return tabKey{}, false
		}
		return tabKey{e.spaceKind, e.spaceID, e.slotID}, true
	}
	var leads map[tabKey]int
	for i := range list {
		if !list[i].hasPaneIndex || list[i].paneIndex != 0 {
			continue
		}
		if k, ok := member(i); ok {
			if leads == nil {
				leads = map[tabKey]int{}
			}
			if _, two := leads[k]; two {
				leads[k] = -1
			} else {
				leads[k] = i
			}
		}
	}
	var out map[int]int
	for i := range list {
		k, ok := member(i)
		if !ok {
			continue
		}
		if j, has := leads[k]; has && j >= 0 && j != i {
			if out == nil {
				out = map[int]int{}
			}
			out[i] = j
		}
	}
	return out
}

// mayFold: a row of list may fold, so foldSessions has to walk it: a listed row is a child, a listed row may be a side
// (sideSeat), or listed rows that can be of a tab's team (tabSeat) sit in a first pane (a lead) and in another pane (a
// worker; a second row in its tab's first pane would leave it without a lead). None of these, and no row has a parent,
// so nothing folds. Each test asks less than foldSessions does (a side's parent_agent_id need name no listed row here),
// and rows listed twice count each time: that can only say a fold is possible where foldSessions finds none.
func mayFold(list []proto.Session, listed func(*proto.Session) bool) bool {
	var first, other bool
	for i := range list {
		s := &list[i]
		switch {
		case !listed(s):
		case s.IsChild(), sideSeat(s):
			return true
		case tabSeat(s):
			if p := s.PaneIndex; p != nil && *p == 0 {
				first = true
			} else {
				other = true
			}
			if first && other {
				return true
			}
		}
	}
	return false
}

// sideSeat: s can be a side session as far as the row alone tells (sideParents): no child and not the assistant's,
// naming a parent_agent_id other than itself.
func sideSeat(s *proto.Session) bool {
	id := s.ParentAgentID()
	return id != "" && id != s.SessionID && !s.Assistant && !s.IsChild()
}

// tabSeat: s can be of a tab's team as far as the row alone tells (tabLeads): a session, not the assistant's, in a
// named space and slot, no child and not offline.
func tabSeat(s *proto.Session) bool {
	return !s.Assistant && s.SessionID != "" && s.SpaceID != "" && s.SlotID != "" && !s.IsChild() && s.State != proto.StateOffline
}

// sideParents finds the side sessions of a machine's list (#7944): for each, by its index, the index of the row its
// parent_agent_id names. A side is a counted row (the listed rows, each id once) that is no child and no assistant row
// and names another counted row that is no assistant row either; that row need not run. A child keeps folding by its
// own parent fields.
func sideParents(list []proto.Session, at map[string]int, counted func(int) bool) map[int]int {
	var out map[int]int
	for i := range list {
		s := &list[i]
		id := s.ParentAgentID()
		if id == "" || id == s.SessionID || s.Assistant || s.IsChild() || !counted(i) {
			continue
		}
		if j, ok := at[id]; ok && j != i && !list[j].Assistant {
			if out == nil {
				out = map[int]int{}
			}
			out[i] = j
		}
	}
	return out
}

// tabLeads finds the tab teams of a machine's list (#5180): for each worker, by its index, its lead's index. A tab is
// the rows of one space kind, space and slot, both named; a row of the assistant's is in none. Its lead is the one row
// counted (the listed rows, each id once) in its first pane (pane_index 0) that is no child and not offline; a tab with
// none or more than one such row has no lead, its Agents peers. Every other counted row of a tab with a lead that is
// no child (a child folds by its own parent) and not offline (a stopped pane has left the team) is its worker.
func tabLeads(list []proto.Session, counted func(int) bool) map[int]int {
	member := func(i int) (tabKey, bool) {
		s := &list[i]
		if !tabSeat(s) || !counted(i) {
			return tabKey{}, false
		}
		return tabKey{s.SpaceKind, s.SpaceID, s.SlotID}, true
	}
	var leads map[tabKey]int // the lead's index; -1 for a tab with more than one row in its first pane
	for i := range list {
		if p := list[i].PaneIndex; p == nil || *p != 0 {
			continue
		}
		if k, ok := member(i); ok {
			if leads == nil {
				leads = map[tabKey]int{}
			}
			if _, two := leads[k]; two {
				leads[k] = -1
			} else {
				leads[k] = i
			}
		}
	}
	var out map[int]int
	for i := range list {
		k, ok := member(i)
		if !ok {
			continue
		}
		if j, has := leads[k]; has && j >= 0 && j != i {
			if out == nil {
				out = map[int]int{}
			}
			out[i] = j
		}
	}
	return out
}

// apply puts the fold on the rows: what each folds under (including cross-machine parent machine), why, and the rollup
// it carries, nothing on the others. A row is given a new hierarchy only where it changes.
func (f sessionFold) apply(list []proto.Session) {
	for i := range list {
		id := list[i].SessionID
		list[i].SetFoldWithMachine(f.underMachine[id], f.under[id], f.as[id], f.side[id], f.children[id])
	}
}

func sameSessionFold(a, b sessionFold) bool {
	if !maps.Equal(a.under, b.under) || !maps.Equal(a.underMachine, b.underMachine) ||
		!maps.Equal(a.as, b.as) || !maps.Equal(a.side, b.side) || len(a.children) != len(b.children) {
		return false
	}
	for k, va := range a.children {
		vb, ok := b.children[k]
		if !ok || (va == nil) != (vb == nil) || (va != nil && *va != *vb) {
			return false
		}
	}
	return true
}

func crossParentMachines(machine string, list []proto.Session) map[string]bool {
	var out map[string]bool
	for i := range list {
		if pm := list[i].ParentMachineID(); pm != "" && pm != machine {
			if out == nil {
				out = map[string]bool{}
			}
			out[pm] = true
		}
	}
	return out
}

func (s *Service) reviewerHiddenFor(machine string) map[string]bool {
	var hidden map[string]bool
	for k := range s.reviewerSessions() {
		if k[0] == machine {
			if hidden == nil {
				hidden = map[string]bool{}
			}
			hidden[k[1]] = true
		}
	}
	return hidden
}

// foldSeedMachine builds lightweight fold entries for another machine that has not yet entered foldMeta (for instance
// when a cross-machine child arrives after its parent machine was already folded via the single-machine fast path).
func (s *Service) foldSeedMachine(machine string) []foldEntry {
	if !s.fleetOn() {
		return nil
	}
	raw := s.nodeSessions(machine)
	if len(raw) == 0 {
		return nil
	}
	rows := s.decorateInventory(machine, raw, false)
	if snap := s.agentArchives.snapshot.Load(); snap != nil && len(*snap) > 0 {
		markAgentArchives(machine, rows, *snap, nil, 0)
	}
	for i := range rows {
		rows[i].SetHiddenByDirectory(s.hiddenByDirectory(machine, rows[i]))
	}
	return toFoldEntries(rows, s.reviewerHiddenFor(machine))
}

// foldInventory folds machine's complete list (foldSessions for purely single-machine fleets; foldFleetEntries when
// any cross-machine subagent is active), puts the fold on its rows and keeps it for the rows decorated on their own
// (foldSubset) and the cards sent to the apps (withTask) until the next complete list. When a cross-machine child's
// state changes another machine's parent rollup, that machine is republished asynchronously.
func (s *Service) foldInventory(machine string, list []proto.Session) {
	// Include remembered archive rows added after live inventory decoration.
	s.decorateSubagents(context.Background(), machine, list)
	for i := range list {
		list[i].SetHiddenByDirectory(s.hiddenByDirectory(machine, list[i]))
	}
	hidden := s.reviewerHiddenFor(machine)
	crossRefs := crossParentMachines(machine, list)

	s.fleet.foldMu.Lock()
	hasCross := len(crossRefs) > 0 || len(s.fleet.foldMeta) > 0
	if !hasCross {
		s.fleet.foldMu.Unlock()
		f := foldSessions(list, hidden)
		f.apply(list)
		s.fleet.foldMu.Lock()
		if s.fleet.folds == nil {
			s.fleet.folds = map[string]sessionFold{}
		}
		s.fleet.folds[machine] = f
		s.fleet.foldMu.Unlock()
		return
	}
	var missing []string
	for pm := range crossRefs {
		if pm != machine && s.fleet.foldMeta[pm] == nil {
			missing = append(missing, pm)
		}
	}
	s.fleet.foldMu.Unlock()

	var seeded map[string][]foldEntry
	if len(missing) > 0 {
		seeded = make(map[string][]foldEntry, len(missing))
		for _, pm := range missing {
			if rows := s.foldSeedMachine(pm); rows != nil {
				seeded[pm] = rows
			}
		}
	}

	entries := toFoldEntries(list, hidden)
	s.fleet.foldMu.Lock()
	if s.fleet.foldMeta == nil {
		s.fleet.foldMeta = map[string][]foldEntry{}
	}
	for pm, rows := range seeded {
		if s.fleet.foldMeta[pm] == nil {
			s.fleet.foldMeta[pm] = rows
		}
	}
	s.fleet.foldMeta[machine] = entries
	anyCross := false
	for m, rows := range s.fleet.foldMeta {
		for i := range rows {
			if rows[i].parentMachineID != "" && rows[i].parentMachineID != m {
				anyCross = true
				break
			}
		}
		if anyCross {
			break
		}
	}
	allFolds := foldFleetEntries(s.fleet.foldMeta)
	if !anyCross {
		s.fleet.foldMeta = nil
	}
	if s.fleet.folds == nil {
		s.fleet.folds = map[string]sessionFold{}
	}
	var changedOthers []string
	for m, next := range allFolds {
		prev := s.fleet.folds[m]
		s.fleet.folds[m] = next
		if m != machine && !sameSessionFold(prev, next) {
			changedOthers = append(changedOthers, m)
		}
	}
	cur := allFolds[machine]
	s.fleet.foldMu.Unlock()
	cur.apply(list)
	if len(changedOthers) > 0 && s.fleetOn() {
		for _, other := range changedOthers {
			other := other
			go s.publishSessions(other)
		}
	}
}

// foldSubset puts on rows decorated without the rest of their machine's list what its last complete list folded them
// as: a single row pushed on its own keeps its folded_under, folded_under_machine_id, folded_as, folded_side and children.
func (s *Service) foldSubset(machine string, list []proto.Session) {
	s.fleet.foldMu.Lock()
	f := s.fleet.folds[machine]
	s.fleet.foldMu.Unlock()
	f.apply(list)
}

// foldedUnder is the session machine's session sid folded under in its last complete list, why when it is no child
// (proto.FoldedAsWorker, proto.FoldedAsSide) and the side session it belongs to when it folds through one; "" for
// none.
func (s *Service) foldedUnder(machine, sid string) (under, as, side string) {
	_, under, as, side = s.foldedUnderWithMachine(machine, sid)
	return under, as, side
}

// foldedUnderWithMachine is foldedUnder plus the cross-machine parent machine ID ("" when same machine or none).
func (s *Service) foldedUnderWithMachine(machine, sid string) (underMachine, under, as, side string) {
	s.fleet.foldMu.Lock()
	defer s.fleet.foldMu.Unlock()
	f := s.fleet.folds[machine]
	return f.underMachine[sid], f.under[sid], f.as[sid], f.side[sid]
}
