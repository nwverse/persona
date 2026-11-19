package devices

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// The hub's one-way events for a node (devices.notify: terminal input, a forwarded port's bytes) come in on the bus's
// read loop, which must not wait for any one computer: they are queued per machine and written by one writer each, in
// the order they came, so a computer slow to take them holds up only its own. One whose write stalls noteStall, or
// that lets maxNoteBytes pile up, is connected again (what ran on the connection ends, as with any dropped one). A
// forwarded port's streams queue apart from the rest (portNote): on a framed connection their bytes wait for credit,
// and terminal input behind them would wait with them.
var (
	noteStall    = 30 * time.Second
	maxNoteBytes = 32 << 20
)

type nodeNotes struct {
	mu      sync.Mutex
	q       []nodeNote
	bytes   int
	writing bool
}

type nodeNote struct {
	event  string
	params json.RawMessage
}

// portNote: event belongs to a forwarded port's stream and stays in order with its bytes: its data and its close
// (streams.EventData, EventClose), which once ran apart and closed a stream ahead of its last bytes. Its
// acknowledgements (stream.ack) go with the other events: they wait for no credit, and nothing waits behind them.
func portNote(event string) bool { return strings.HasPrefix(event, "stream.") && event != "stream.ack" }

// queueNote queues an event for the node.
func (m *Machine) queueNote(event string, params json.RawMessage) {
	n := &m.notes
	if portNote(event) {
		n = &m.bulkNotes
	}
	n.mu.Lock()
	if n.bytes+len(params) > maxNoteBytes {
		dropped := len(n.q)
		n.q, n.bytes = nil, 0
		n.mu.Unlock()
		m.d.logf("[%s] its node takes no events (%d waiting): connecting it again", m.cfg.ID, dropped)
		m.reconnect()
		return
	}
	n.q = append(n.q, nodeNote{event: event, params: params})
	n.bytes += len(params)
	start := !n.writing
	n.writing = true
	n.mu.Unlock()
	if start {
		go m.writeNotes(n)
	}
}

// writeNotes writes the events queued on n to the node's connection of the moment until none is left.
func (m *Machine) writeNotes(n *nodeNotes) {
	for {
		n.mu.Lock()
		if len(n.q) == 0 {
			n.q, n.writing = nil, false
			n.mu.Unlock()
			return
		}
		e := n.q[0]
		n.q[0] = nodeNote{}
		n.q = n.q[1:]
		n.bytes -= len(e.params)
		n.mu.Unlock()
		m.mu.Lock()
		p := m.peer
		m.mu.Unlock()
		if p == nil {
			continue
		}
		stalled := time.AfterFunc(noteStall, func() {
			m.d.logf("[%s] its node took nothing for %s: connecting it again", m.cfg.ID, noteStall)
			p.Close(errors.New("the node stopped taking events"))
		})
		_ = p.Notify(e.event, e.params)
		stalled.Stop()
	}
}
