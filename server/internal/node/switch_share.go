package node

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/nwverse/persona/server/internal/proto"
)

// What a node keeps of launcher switches (launcher_switch.go) outside a session's state record, in one place a
// resident node's Nodes share (attach.go, as they carry background scans, carry.go): a resident node makes a new Node
// for every hub connection, and a switch outlives the connection it began on (from the first Ctrl+C it runs on its own
// clock). Shared, the next connection's Node sees it: it lists the session as starting, has messages and the user's
// Resume wait for the switch and picks and stops refused, instead of racing it in the agent's pane; it gets the records
// the switch writes once the old Node's connection is gone; and the switches of sessions with no record (kept in
// memory) survive the reconnect. A node that serves one connection has a share of its own.

// switchShare is what a node's Nodes share of launcher switches. Its lock is never held while calling into a Node.
type switchShare struct {
	mu sync.Mutex
	// pending: the switches waiting for a message, of sessions with no state record (a recorded session keeps its own
	// in the record).
	pending map[string]proto.PendingSwitch
	// marked: the sessions a message has taken a switch up for, from before its checks until it ends.
	marked map[string]bool
	// flights: the switches past their checks, from just before the quit until they end: the row listed meanwhile, and
	// a channel closed at the end.
	flights map[string]*switchFlight
	// held: the records a switch wrote that its Node could no longer send its hub (its connection had ended), until a
	// Node whose state has loaded takes them (to).
	held map[string]sessionState
	to   *Node
}

type switchFlight struct {
	row  proto.Session
	done chan struct{}
}

// switches is n's share: the resident node's when attach set one, else one of n's own.
func (n *Node) switches() *switchShare {
	if s := n.share.Load(); s != nil {
		return s
	}
	n.share.CompareAndSwap(nil, &switchShare{})
	return n.share.Load()
}

// pendingOf is session id's switch kept here, nil when none.
func (s *switchShare) pendingOf(id string) *proto.PendingSwitch {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps, ok := s.pending[id]; ok {
		return &ps
	}
	return nil
}

// setPending keeps ps as session id's switch here (nil: none).
func (s *switchShare) setPending(id string, ps *proto.PendingSwitch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps == nil {
		delete(s.pending, id)
		return
	}
	if s.pending == nil {
		s.pending = map[string]proto.PendingSwitch{}
	}
	s.pending[id] = *ps
}

// mark marks session id as having a switch taken up by a message until done is called.
func (s *switchShare) mark(id string) (done func()) {
	s.mu.Lock()
	if s.marked == nil {
		s.marked = map[string]bool{}
	}
	s.marked[id] = true
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.marked, id)
		s.mu.Unlock()
	}
}

// begin registers session id's switch as under way, listed as row meanwhile; false when one is already.
func (s *switchShare) begin(id string, row proto.Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flights[id] != nil {
		return false
	}
	if s.flights == nil {
		s.flights = map[string]*switchFlight{}
	}
	s.flights[id] = &switchFlight{row: row, done: make(chan struct{})}
	return true
}

// end ends session id's switch under way: those waiting for it go on, and its records are no longer held for it.
func (s *switchShare) end(id string) {
	s.mu.Lock()
	f := s.flights[id]
	delete(s.flights, id)
	s.mu.Unlock()
	if f != nil {
		close(f.done)
	}
}

// inFlight: session id's switch is under way (past its checks).
func (s *switchShare) inFlight(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flights[id] != nil
}

// busy: a message has taken a switch of session id up, or one is under way.
func (s *switchShare) busy(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.marked[id] || s.flights[id] != nil
}

// wait blocks while a switch of session id is under way.
func (s *switchShare) wait(ctx context.Context, id string) error {
	s.mu.Lock()
	f := s.flights[id]
	s.mu.Unlock()
	if f == nil {
		return nil
	}
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("the agent is moving to another launcher and was not ready in time: %w", ctx.Err())
	}
}

// listed are the rows of the switches under way that the list has not seen (the agent is quit, its new one not up).
func (s *switchShare) listed(seen map[string]bool) []proto.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []proto.Session
	for id, f := range s.flights {
		if !seen[id] {
			out = append(out, f.row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// keep takes the record v of session id that Node from could not send its hub, when a switch of it is under way
// (written by the switch, which outlived from's connection): to the Node whose state has loaded now, or held until one
// has. false: not a switch's, not taken.
func (s *switchShare) keep(from *Node, id string, v sessionState) bool {
	s.mu.Lock()
	if s.flights[id] == nil {
		s.mu.Unlock()
		return false
	}
	to := s.to
	if to == nil || to == from {
		if s.held == nil {
			s.held = map[string]sessionState{}
		}
		s.held[id] = v
		s.mu.Unlock()
		return true
	}
	s.mu.Unlock()
	to.state.put(id, v) // the latest word of that session: over what that Node loaded, and written through its hub
	return true
}

// receive makes n, whose state has loaded, the Node that takes the records switches write from now on, and gives it
// those held meanwhile.
func (s *switchShare) receive(n *Node) {
	s.mu.Lock()
	s.to = n
	held := s.held
	s.held = nil
	s.mu.Unlock()
	for id, v := range held {
		n.state.put(id, v)
	}
}

// leave: n's connection has ended; records go to the next Node from now on.
func (s *switchShare) leave(n *Node) {
	s.mu.Lock()
	if s.to == n {
		s.to = nil
	}
	s.mu.Unlock()
}
