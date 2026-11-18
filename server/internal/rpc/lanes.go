package rpc

// Lanes (#10660, docs/protocol/protocol.md "Lanes"): a connection both of whose ends opt in (EnableLanes) can switch
// from JSON lines to frames, each carrying a piece of one line on one of four lanes. The writer sends the lanes in
// strict priority (control, interactive, events, bulk) and takes turns between the lines of a lane, so a pong or an
// answer to a tap waits at most for one frame already on its way, not for a 1 MiB file chunk ahead of it on the one
// writer. Bulk frames also wait for credit (bulkWindow): the other side acknowledges the frames it has read, and only
// so many bulk bytes may be sent and not yet acknowledged, so what sits in the transport's own buffers (ssh, a relay,
// TCP) ahead of a pong stays bounded too, and a peer that stops reading holds the bulk writer back.
//
// The switch is negotiated on the connection itself, so old peers on either end, and every byte pipe in between,
// see nothing new:
//
//	→ {"id":"h3","method":"rpc.lanes","params":{"versions":[1]}}     (a line)
//	← {"id":"h3","result":{"version":1}}                              (the answering side's last line; frames follow)
//	→ {"event":"rpc.lanes","params":{"version":1}}                    (the asking side's last line; frames follow)
//
// A peer without lanes answers the request with an error (an unknown method) and both sides keep lines. Each side
// switches its output under its writer's lock, right after the line that announces it, and its input in the read
// loop, right after the line that announces the other side's switch, so no byte in flight is read the wrong way.
//
// A frame is a 16-byte header and up to framePayloadMax bytes of a line (without its newline):
//
//	[0]     kind: 'D' a piece of a line, 'A' an acknowledgement
//	[1]     lane (D), 0 (A)
//	[2]     flags: 1 the line's last piece (D), 0 (A); the other bits 0
//	[3]     0
//	[4:6]   slot: which of the lane's lines in progress the piece belongs to (D), 0 (A)
//	[6:8]   payload length, big endian (D), 0 (A)
//	[8:16]  D: the frame's sequence number in this direction, 1 for the first, big endian; A: the last sequence
//	        number the sender has read, every frame before it included
//
// Sequence numbers and the cumulative acknowledgement are what a resumed connection (#10663) is built on: it keeps the
// frames not yet acknowledged and sends them again after a reconnect. Version 1 uses them for the bulk window only.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Lane is one of the classes a framed connection sends in strict priority.
type Lane uint8

const (
	LaneControl     Lane = iota // ping and its pong, rpc.cancel, flow control
	LaneInteractive             // requests and their answers that are not bulk
	LaneEvents                  // events that are not bulk
	LaneBulk                    // file chunks, uploads, node update packages, port bytes: limited by the bulk window
	laneCount
)

var laneNames = [laneCount]string{"control", "interactive", "events", "bulk"}

func (l Lane) String() string {
	if l < laneCount {
		return laneNames[l]
	}
	return fmt.Sprintf("lane%d", l)
}

// lanesMethod is the request that asks for frames and the event that marks the asking side's switch.
const lanesMethod = "rpc.lanes"

// lanesVersion is the frame format this code speaks.
const lanesVersion = 1

// CodeLanesRefused is the code of an answer to rpc.lanes from a peer with lanes enabled that will not switch.
const CodeLanesRefused = "lanes_refused"

type lanesRequest struct {
	Versions []int `json:"versions"`
}

type lanesAnswer struct {
	Version int `json:"version"`
}

// lanesAccepted: result, the answer to this side's rpc.lanes, switches the other side to frames this code reads.
func lanesAccepted(m message) bool {
	if m.Error != nil || len(m.Result) == 0 {
		return false
	}
	var a lanesAnswer
	return json.Unmarshal(m.Result, &a) == nil && a.Version == lanesVersion
}

// laneTable says which lane a message goes on. A request and its answer go on the same lane (the method's).
type laneTable struct {
	control, bulk map[string]bool
}

func newLaneTable(control, bulk []string) *laneTable {
	t := &laneTable{control: map[string]bool{}, bulk: map[string]bool{}}
	for _, n := range control {
		t.control[n] = true
	}
	for _, n := range bulk {
		t.bulk[n] = true
	}
	return t
}

// method is the lane of a request for method, and of its answer.
func (t *laneTable) method(method string) Lane {
	switch {
	case t == nil:
		return LaneInteractive
	case t.control[method]:
		return LaneControl
	case t.bulk[method]:
		return LaneBulk
	}
	return LaneInteractive
}

// event is the lane of an event.
func (t *laneTable) event(event string) Lane {
	switch {
	case event == cancelEvent:
		return LaneControl
	case t == nil:
		return LaneEvents
	case t.control[event]:
		return LaneControl
	case t.bulk[event]:
		return LaneBulk
	}
	return LaneEvents
}

// Frame sizes: a whole frame, header included, is at most frameMax.
const (
	frameHeader     = 16
	frameMax        = 32 << 10
	framePayloadMax = frameMax - frameHeader
)

const (
	frameData byte = 'D'
	frameAck  byte = 'A'
	flagFin   byte = 1
)

// frameHead is a frame's header.
type frameHead struct {
	kind byte
	lane Lane
	fin  bool
	slot uint16
	size int    // payload bytes
	seq  uint64 // D: this frame's number; A: the last one read
}

func putHead(b []byte, h frameHead) {
	_ = b[frameHeader-1]
	b[0], b[1], b[2], b[3] = h.kind, byte(h.lane), 0, 0
	if h.fin {
		b[2] = flagFin
	}
	binary.BigEndian.PutUint16(b[4:6], h.slot)
	binary.BigEndian.PutUint16(b[6:8], uint16(h.size))
	binary.BigEndian.PutUint64(b[8:16], h.seq)
}

var errFrame = errors.New("rpc: malformed frame")

// parseHead reads and checks a header: anything this version does not write is an error, so a connection whose two
// ends disagree about where frames are fails at once instead of reading garbage as lines.
func parseHead(b []byte) (frameHead, error) {
	_ = b[frameHeader-1]
	h := frameHead{kind: b[0], lane: Lane(b[1]), fin: b[2] == flagFin, slot: binary.BigEndian.Uint16(b[4:6]),
		size: int(binary.BigEndian.Uint16(b[6:8])), seq: binary.BigEndian.Uint64(b[8:16])}
	switch h.kind {
	case frameData:
		if h.lane >= laneCount || b[2]&^flagFin != 0 || b[3] != 0 || h.size > framePayloadMax {
			return h, fmt.Errorf("%w: data header % x", errFrame, b[:8])
		}
	case frameAck:
		if b[1]|b[2]|b[3]|b[4]|b[5]|b[6]|b[7] != 0 {
			return h, fmt.Errorf("%w: ack header % x", errFrame, b[:8])
		}
	default:
		return h, fmt.Errorf("%w: kind 0x%02x", errFrame, h.kind)
	}
	return h, nil
}
