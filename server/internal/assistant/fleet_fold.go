package assistant

import (
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- children folded into their parent (docs/protocol/protocol.md "Session hierarchy", #5120) ----

// sessionFold is what folding says of one machine's list: the session each folded child folds under (its topmost
// listed ancestor), and the rollup each such ancestor carries of all the children folded under it.
type sessionFold struct {
	under    map[string]string
	children map[string]*proto.SessionChildren
}

// listedRow: the row is in the list the apps show (the hub leaves archived Agents out of it, deleted_sessions.go).
func listedRow(s *proto.Session) bool { return !s.Archived || s.Assistant }

// foldSessions folds a machine's complete list (its rows as decorated, their states computed). A child (a subagent, or
// a session that names a parent or another root, proto.Session.IsChild) has as its parent the listed row its
// parent_session_id names, else the one its asking_parent_session_id names, else its root_session_id's; it folds under
// the topmost listed ancestor that parent leads to. An orphan (no listed parent) and a row whose ancestors go round in
// a cycle fold under nothing. Each ancestor that children fold under counts them all, whatever their depth, by their
// own states.
func foldSessions(list []proto.Session) sessionFold {
	at := make(map[string]int, len(list))
	for i := range list {
		if _, dup := at[list[i].SessionID]; !dup && listedRow(&list[i]) {
			at[list[i].SessionID] = i
		}
	}
	parent := func(i int) int {
		s := &list[i]
		if !s.IsChild() {
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
		if at[list[i].SessionID] != i || !listedRow(&list[i]) {
			continue
		}
		top := i
		path = append(path[:0], i)
		for {
			p := parent(top)
			if p < 0 {
				break
			}
			if containsRow(path, p) {
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
		root := list[top].SessionID
		f.under[list[i].SessionID] = root
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
	}
	return f
}

func containsRow(path []int, i int) bool {
	for _, j := range path {
		if j == i {
			return true
		}
	}
	return false
}

// apply puts the fold on the rows: what each folds under and the rollup it carries, nothing on the others. A row is
// given a new hierarchy only where it changes.
func (f sessionFold) apply(list []proto.Session) {
	for i := range list {
		id := list[i].SessionID
		list[i].SetFold(f.under[id], f.children[id])
	}
}

// foldInventory folds machine's complete list (foldSessions), puts the fold on its rows and keeps it for the rows
// decorated on their own (foldSubset) and the cards sent to the apps (withTask) until the next complete list.
func (s *Service) foldInventory(machine string, list []proto.Session) {
	f := foldSessions(list)
	f.apply(list)
	s.fleet.foldMu.Lock()
	if s.fleet.folds == nil {
		s.fleet.folds = map[string]sessionFold{}
	}
	was := s.fleet.folds[machine]
	s.fleet.folds[machine] = f
	s.fleet.foldMu.Unlock()
	s.retellFolded(machine, was, f)
}

// retellFolded tells the apps again the pending cards of machine's sessions whose folded_under changed between two
// complete lists (a card can reach the apps before the first list that folds its session): each card names the chat
// that shows it as the list does.
func (s *Service) retellFolded(machine string, was, now sessionFold) {
	if len(was.under) == 0 && len(now.under) == 0 {
		return
	}
	var again []proto.Interaction
	s.fleet.mu.Lock()
	for _, p := range s.fleet.interactions {
		if p.ix.MachineID == machine && was.under[p.ix.SessionID] != now.under[p.ix.SessionID] {
			again = append(again, p.ix)
		}
	}
	s.fleet.mu.Unlock()
	for _, ix := range again {
		s.hub.Tell(assistantwire.EventFleetInteraction, assistantwire.FleetInteraction{Interaction: s.withTask(ix)})
	}
}

// foldSubset puts on rows decorated without the rest of their machine's list what its last complete list folded them
// as: a single row pushed on its own keeps its folded_under and children.
func (s *Service) foldSubset(machine string, list []proto.Session) {
	s.fleet.foldMu.Lock()
	f := s.fleet.folds[machine]
	s.fleet.foldMu.Unlock()
	f.apply(list)
}

// foldedUnder is the session machine's session sid folded under in its last complete list; "" for none.
func (s *Service) foldedUnder(machine, sid string) string {
	s.fleet.foldMu.Lock()
	defer s.fleet.foldMu.Unlock()
	return s.fleet.folds[machine].under[sid]
}
