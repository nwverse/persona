package assistant

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// directionAgentCounts counts conversations, independently of the open-task triage and the tree's activity filter.
type directionAgentCounts struct {
	Running int `json:"running"`
	Stopped int `json:"stopped"`
}

// countDirectionAgents runs after placement (including quiet sessions) and before filtering tree rows.
// The returned machines can change the result, even when they host only a direction or an ended task.
func (tb *treeBuild) countDirectionAgents(ctx context.Context, cfg Settings, groups map[string][]directionSession, tree directionTree, look *directionLook) map[string]bool {
	machines := map[string]bool{}
	if len(tb.leads) == 0 {
		return machines
	}
	roots := make(map[string][]agentKey, len(tb.leads))
	add := func(direction, machine, id string) {
		machine = tb.s.canonical(machine)
		if id != "" {
			roots[direction] = append(roots[direction], agentKey{machine, id})
			if machine != "" {
				machines[machine] = true
			}
		}
	}
	for _, lead := range tb.s.leadSessions(ctx, cfg) {
		id := lead.SessionID
		add(id, lead.MachineID, id)
		for _, session := range groups[id] {
			if session.MachineID != "" {
				machines[tb.s.canonical(session.MachineID)] = true
			}
		}
	}
	// Historical worker supervisors can name a computer before or after it was renamed. The task rows and
	// their triage are already built. Extend only this count's lookup, preferring the current computer name.
	for _, k := range slices.SortedFunc(maps.Keys(tb.leaderAgent), directionKeyOrder) {
		if machine := tb.s.canonical(k[0]); machine != k[0] {
			current := agentKey{machine, k[1]}
			tb.leaderAgent[current] = append(tb.leaderAgent[current], tb.leaderAgent[k]...)
		}
	}
	owners := map[string]string{}
	for id, t := range tb.todos {
		owner := tb.directionOf(t.Todo)
		if owner == "" {
			// A worker can be attached through the leader task id (before its supervisor was recorded).
			if at, ok := tb.workerAt[id]; ok {
				owner = tb.directionOf(tb.todos[at.task].Todo)
			}
		}
		if owner == "" {
			continue
		}
		owners[id] = owner
		for _, a := range t.Agents {
			if a.MachineID != "" {
				machines[tb.s.canonical(a.MachineID)] = true
			}
		}
	}
	for machine := range machines {
		look.list(ctx, machine)
	}
	index := newDirectionAgentIndex(look)
	superseded := tb.continuedDirectionTasks()
	replaced := map[string]map[agentKey]bool{}
	for id, owner := range owners {
		t := tb.todos[id].Todo
		gone := index.predecessors(t, tb.s)
		for _, a := range t.Agents {
			k := index.current(agentKey{tb.s.canonical(a.MachineID), a.SessionID})
			if superseded[id] || gone[agentKey{a.MachineID, a.SessionID}] {
				if replaced[owner] == nil {
					replaced[owner] = map[agentKey]bool{}
				}
				replaced[owner][k] = true
			} else {
				add(owner, a.MachineID, a.SessionID)
			}
		}
	}
	for i := range tree.Directions {
		d := &tree.Directions[i]
		if !d.Secretary {
			skip := replaced[d.ID]
			for _, k := range roots[d.ID] { // the direction itself or an independent, surviving task link
				delete(skip, index.current(k))
			}
			for _, session := range groups[d.ID] {
				add(d.ID, session.MachineID, session.SessionID)
			}
			count := index.count(roots[d.ID], skip)
			d.AgentCounts = &count
		}
	}
	return machines
}

// continuedDirectionTasks are the old to-dos a new Agent explicitly took over in historical multi-to-do briefs.
// A separate task on the same brief is not an ancestor.
func (tb *treeBuild) continuedDirectionTasks() map[string]bool {
	book := &tb.s.tbriefs
	book.mu.Lock()
	defer book.mu.Unlock()
	var superseded map[string]bool
	for id, link := range book.links {
		if link.Continues == "" || len(tb.todos[id].Agents) == 0 {
			continue
		}
		chain := continuationChain(id, book.links)
		if len(chain) < 2 || slices.Contains(chain, book.links[chain[0]].Continues) {
			continue
		}
		if superseded == nil {
			superseded = map[string]bool{}
		}
		for _, old := range chain[:len(chain)-1] {
			superseded[old] = true
		}
	}
	return superseded
}

type directionAgentIndex struct {
	listed   map[agentKey]bool // value: an offline transcript (for the existing takeover rule)
	running  map[agentKey]bool
	aliases  map[agentKey]agentKey
	children map[agentKey][]agentKey
}

func newDirectionAgentIndex(look *directionLook) directionAgentIndex {
	idx := directionAgentIndex{listed: map[agentKey]bool{}, running: map[agentKey]bool{},
		aliases: map[agentKey]agentKey{}, children: map[agentKey][]agentKey{}}
	// The node's public row owns earlier engine legs and runtime aliases on this computer only.
	// If successive rows name the same old id, the newest observation wins deterministically.
	updated := map[agentKey]time.Time{}
	// agentSessionLists keeps older machine spellings for task triage. The graph uses the canonical copy only.
	aliases := map[string]bool{}
	for machine := range look.listed {
		if look.s.canonical(machine) != machine {
			aliases[machine] = true
		}
	}
	for key, row := range look.rows {
		if aliases[key[0]] {
			continue
		}
		alias := func(id string) {
			if id == "" || id == key[1] {
				return
			}
			k := agentKey{key[0], id}
			if prev, ok := idx.aliases[k]; !ok || row.UpdatedAt.After(updated[k]) || row.UpdatedAt.Equal(updated[k]) && key[1] < prev[1] {
				idx.aliases[k], updated[k] = key, row.UpdatedAt
			}
		}
		for _, id := range row.Aliases() {
			alias(id)
		}
		for _, leg := range row.EngineHistory() {
			alias(leg.SessionID)
		}
	}
	for key, row := range look.rows {
		if aliases[key[0]] {
			continue
		}
		current := idx.current(key)
		if key == current {
			idx.listed[key] = row.Status == proto.StatusOffline || row.State == proto.StateOffline
			idx.running[key] = row.OwnTurnRunning()
		}
		for _, parent := range [...]string{row.ParentSessionID, row.AskingParentSessionID(), row.RootSessionID} {
			if parent != "" && parent != key[1] {
				p := idx.current(agentKey{key[0], parent})
				idx.children[p] = append(idx.children[p], current)
			}
		}
		for _, sub := range row.Subagents {
			if sub.ID == "" && sub.SessionID == "" {
				continue
			}
			k := idx.current(agentKey{key[0], cmp.Or(sub.SessionID, key[1]+"@"+sub.ID)})
			idx.children[current] = append(idx.children[current], k)
			// The separately listed child wins over a stale embedded report, whichever row is read first.
			if _, listed := idx.listed[k]; !listed && sub.Running {
				idx.running[k] = true
			}
		}
	}
	return idx
}

// current resolves a chain of known identities. A malformed alias cycle has one deterministic representative.
func (idx directionAgentIndex) current(k agentKey) agentKey {
	var path []agentKey
	for {
		next, ok := idx.aliases[k]
		if !ok {
			return k
		}
		if i := slices.Index(path, k); i >= 0 {
			return slices.MinFunc(path[i:], directionKeyOrder)
		}
		path = append(path, k)
		k = next
	}
}

// predecessors compares links within this task only. A released worker remains historical participation
// unless the task was taken over after the release. Older takeovers left an unreleased, offline/missing link.
func (idx directionAgentIndex) predecessors(t Todo, s *Service) map[agentKey]bool {
	if len(t.Agents) < 2 {
		return nil
	}
	var listed []proto.Session
	var newest time.Time
	for _, a := range t.Agents {
		if a.LinkedAt.After(newest) {
			newest = a.LinkedAt
		}
		k := idx.current(agentKey{s.canonical(a.MachineID), a.SessionID})
		if offline, ok := idx.listed[k]; ok {
			row := proto.Session{MachineID: a.MachineID, SessionID: a.SessionID}
			if offline {
				row.Status = proto.StatusOffline
			}
			listed = append(listed, row)
		}
	}
	gone := supersededAgents(t, listed)
	for _, a := range t.Agents {
		if a.ReleasedAt != nil && !a.ReleasedAt.After(newest) && a.LinkedAt.Before(newest) {
			if gone == nil {
				gone = map[agentKey]bool{}
			}
			gone[agentKey{a.MachineID, a.SessionID}] = true
		}
	}
	return gone
}

func (idx directionAgentIndex) count(roots []agentKey, skip map[agentKey]bool) (count directionAgentCounts) {
	seen := map[agentKey]bool{}
	for len(roots) != 0 {
		last := len(roots) - 1
		k := idx.current(roots[last])
		roots = roots[:last]
		if seen[k] || skip[k] {
			continue
		}
		seen[k] = true
		if idx.running[k] {
			count.Running++
		} else {
			count.Stopped++
		}
		roots = append(roots, idx.children[k]...)
	}
	return count
}
