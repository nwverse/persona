package rpc

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// laneWriter writes a framed connection (lanes.go). The peer's writers queue their lines on it and wait; one goroutine
// (run) cuts them into frames and writes them, each write a batch of up to frameMax bytes: the acknowledgement due
// first, then frames by lane in strict priority, a frame for each line of a lane in turn. A line once started is
// written to its end, as a line on the one writer was; one not started yet can still be withdrawn (NotSent).
type laneWriter struct {
	w      io.Writer
	closed <-chan struct{} // the peer's
	fail   func(error)     // closes the peer: a write failed, or the other side broke the protocol
	filter func([]byte) []byte

	mu      sync.Mutex
	wake    chan struct{} // 1: lines queued, credit or an acknowledgement due
	running bool          // run started: output is framed
	busy    bool          // a write is under way
	err     error         // why the writer stopped; nil while it goes on
	lanes   [laneCount]laneQueue
	seq     uint64 // the last data frame numbered
	ackDue  uint64 // the reader has read every frame through this one, and wants it acknowledged
	ackSent uint64
	win     bulkWindow
	totals  [laneCount]laneTotals
}

// laneSlots is how many lines of one lane may be in progress at once (laneQueue.slots); a frame's slot says which.
const laneSlots = 16

var newline = []byte("\n")

type laneQueue struct {
	lines []*outLine // waiting or in progress, in turn order
	next  int        // whose turn it is
	slots uint16     // slots in use, one bit each
}

type laneTotals struct {
	frames, bytes int64
	maxWait       time.Duration
}

// outLine is a line queued on a laneWriter.
type outLine struct {
	b       []byte // the line (its newline dropped once it starts, after filter)
	off     int    // written so far
	lane    Lane
	slot    uint16
	started bool
	queued  time.Time
	done    chan error // told once: nil when the last frame is written, or why it never will be
}

func newLaneWriter(w io.Writer, closed <-chan struct{}, fail func(error), filter func([]byte) []byte) *laneWriter {
	return &laneWriter{w: w, closed: closed, fail: fail, filter: filter, wake: make(chan struct{}, 1), win: newBulkWindow()}
}

func (lw *laneWriter) poke() {
	select {
	case lw.wake <- struct{}{}:
	default:
	}
}

// start begins writing: from now on everything this side sends is frames.
func (lw *laneWriter) start() {
	lw.mu.Lock()
	lw.running = true
	lw.mu.Unlock()
	go lw.run()
}

// enqueue queues l; delayed: something was being written or waiting already.
func (lw *laneWriter) enqueue(l *outLine) (delayed bool, err error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if lw.err != nil {
		return false, lw.err
	}
	delayed = lw.busy
	for i := range lw.lanes {
		delayed = delayed || len(lw.lanes[i].lines) > 0
	}
	q := &lw.lanes[l.lane]
	q.lines = append(q.lines, l)
	lw.poke()
	return delayed, nil
}

// withdraw takes l back if none of it went out yet.
func (lw *laneWriter) withdraw(l *outLine) bool {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if l.started {
		return false
	}
	q := &lw.lanes[l.lane]
	for i, x := range q.lines {
		if x == l {
			q.remove(i)
			return true
		}
	}
	return true // the writer stopped and let it go
}

func (q *laneQueue) remove(i int) {
	q.lines = append(q.lines[:i], q.lines[i+1:]...)
	if i < q.next {
		q.next--
	}
	if q.next >= len(q.lines) {
		q.next = 0
	}
}

// ack asks for an acknowledgement of every frame read through seq.
func (lw *laneWriter) ack(seq uint64) {
	lw.mu.Lock()
	if seq > lw.ackDue {
		lw.ackDue = seq
		lw.poke()
	}
	lw.mu.Unlock()
}

// acked takes the other side's acknowledgement of every frame through seq.
func (lw *laneWriter) acked(seq uint64, now time.Time) error {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if seq > lw.seq {
		return fmt.Errorf("%w: acknowledges frame %d, %d sent", errFrame, seq, lw.seq)
	}
	lw.win.acked(seq, now)
	lw.poke()
	return nil
}

func (lw *laneWriter) run() {
	buf := make([]byte, 0, frameMax)
	var finished []*outLine
	for {
		lw.mu.Lock()
		if lw.err != nil {
			lw.mu.Unlock()
			return
		}
		var batch []byte
		batch, finished = lw.fillLocked(buf[:0], finished[:0], time.Now())
		if len(batch) == 0 {
			lw.mu.Unlock()
			select {
			case <-lw.wake:
			case <-lw.closed:
				lw.stop(ErrClosed)
				return
			}
			continue
		}
		lw.busy = true
		lw.mu.Unlock()
		n, err := lw.w.Write(batch)
		if err == nil && n != len(batch) {
			err = io.ErrShortWrite
		}
		lw.mu.Lock()
		lw.busy = false
		for _, l := range finished {
			l.done <- err
		}
		lw.mu.Unlock()
		if err != nil {
			lw.stop(err)
			lw.fail(err)
			return
		}
	}
}

// stop ends the writer: every line still queued is told err.
func (lw *laneWriter) stop(err error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if lw.err == nil {
		lw.err = err
	}
	for i := range lw.lanes {
		q := &lw.lanes[i]
		for _, l := range q.lines {
			l.done <- lw.err
		}
		*q = laneQueue{}
	}
}

// fillLocked makes the next write: the acknowledgement due, then frames in priority order while they fit in frameMax
// (the first always does). finished is the lines whose last frame it holds.
func (lw *laneWriter) fillLocked(batch []byte, finished []*outLine, now time.Time) ([]byte, []*outLine) {
	if lw.ackDue > lw.ackSent {
		var h [frameHeader]byte
		putHead(h[:], frameHead{kind: frameAck, seq: lw.ackDue})
		batch = append(batch, h[:]...)
		lw.ackSent = lw.ackDue
	}
	for {
		l, n, ok := lw.pickLocked(frameMax-len(batch), len(batch) == 0, now)
		if !ok {
			break
		}
		lw.seq++
		fin := l.off+n == len(l.b)
		var h [frameHeader]byte
		putHead(h[:], frameHead{kind: frameData, lane: l.lane, fin: fin, slot: l.slot, size: n, seq: lw.seq})
		batch = append(append(batch, h[:]...), l.b[l.off:l.off+n]...)
		l.off += n
		t := &lw.totals[l.lane]
		t.frames++
		t.bytes += int64(n)
		if l.lane == LaneBulk {
			lw.win.wrote(lw.seq, n, now)
		}
		q := &lw.lanes[l.lane]
		if fin {
			q.slots &^= 1 << l.slot
			for i, x := range q.lines {
				if x == l {
					q.remove(i)
					break
				}
			}
			finished = append(finished, l)
		}
	}
	return batch, finished
}

// pickLocked is the next frame: its line, advanced to its turn, and its size; ok false when there is none, or when
// the next one would not fit in room (and the batch is not empty: first).
func (lw *laneWriter) pickLocked(room int, first bool, now time.Time) (*outLine, int, bool) {
	for lane := LaneControl; lane < laneCount; lane++ {
		q := &lw.lanes[lane]
		if len(q.lines) == 0 {
			continue
		}
		most := framePayloadMax
		if lane == LaneBulk {
			most = lw.win.frameSize()
		}
		for k := 0; k < len(q.lines); k++ {
			i := (q.next + k) % len(q.lines)
			l := q.lines[i]
			if !l.started && q.slots == 1<<laneSlots-1 {
				continue // every slot is busy with a line in progress: this one waits for its turn
			}
			left := len(l.b) - l.off
			if !l.started {
				left = len(bytes.TrimSuffix(l.b, newline)) // filter may change it a little: a hint is enough here
			}
			n := min(left, most)
			if !first && frameHeader+n > room {
				return nil, 0, false
			}
			if lane == LaneBulk && !lw.win.canSend(n) {
				break // no credit for bulk: lower lanes have nothing more to send
			}
			if !l.started {
				// Filtered as it goes on the wire, as a line on the one writer was (the node hides values handed over
				// meanwhile too), and once.
				if lw.filter != nil {
					l.b = lw.filter(l.b)
				}
				l.b = bytes.TrimSuffix(l.b, newline)
				n = min(len(l.b), most)
				for s := uint16(0); s < laneSlots; s++ {
					if q.slots&(1<<s) == 0 {
						l.slot = s
						break
					}
				}
				q.slots |= 1 << l.slot
				l.started = true
				lw.totals[lane].maxWait = max(lw.totals[lane].maxWait, now.Sub(l.queued))
			}
			q.next = (i + 1) % len(q.lines)
			return l, n, true
		}
	}
	return nil, 0, false
}

// LaneStats is a framed connection's writer as it is now (Peer.LaneStats).
type LaneStats struct {
	Framed   bool // this side's output is framed
	Lanes    [laneCount]LaneCounters
	Window   int           // bulk bytes that may be in flight
	InFlight int           // bulk bytes sent and not acknowledged
	RTT      time.Duration // the latest bulk round trip
	BaseRTT  time.Duration // the smallest lately
}

// LaneCounters is one lane's: what waits now, what went out, and the longest a line waited for its first frame.
type LaneCounters struct {
	QueuedLines, QueuedFrames int
	QueuedBytes               int64
	SentFrames, SentBytes     int64
	MaxWait                   time.Duration
	OldestWait                time.Duration // how long the oldest line still waiting has
}

// String is the counters on one line, for a log, each field per lane in priority order (control/interactive/events/
// bulk): "lanes=on queued=0/1/0/3 bytes=0B/1KiB/0B/96KiB maxwait=1ms/40ms/2ms/1.2s sent=12/340/88/51 window=64KiB
// inflight=32KiB rtt=410ms base=380ms"; "lanes=off" for a connection of lines.
func (s LaneStats) String() string {
	if !s.Framed {
		return "lanes=off"
	}
	var queued, size, wait, sent [laneCount]string
	for i, c := range s.Lanes {
		queued[i] = fmt.Sprint(c.QueuedFrames)
		size[i] = sizeText(c.QueuedBytes)
		wait[i] = c.MaxWait.Round(time.Millisecond).String()
		sent[i] = fmt.Sprint(c.SentFrames)
	}
	return fmt.Sprintf("lanes=on queued=%s bytes=%s maxwait=%s sent=%s window=%s inflight=%s rtt=%s base=%s",
		strings.Join(queued[:], "/"), strings.Join(size[:], "/"), strings.Join(wait[:], "/"), strings.Join(sent[:], "/"),
		sizeText(int64(s.Window)), sizeText(int64(s.InFlight)), s.RTT.Round(time.Millisecond), s.BaseRTT.Round(time.Millisecond))
}

func sizeText(n int64) string {
	switch {
	case n >= 10<<20:
		return fmt.Sprintf("%dMiB", n>>20)
	case n >= 10<<10:
		return fmt.Sprintf("%dKiB", n>>10)
	}
	return fmt.Sprintf("%dB", n)
}

func (lw *laneWriter) stats(now time.Time) LaneStats {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	s := LaneStats{Framed: lw.running, Window: lw.win.cwnd, InFlight: lw.win.inFlight, RTT: lw.win.rtt, BaseRTT: lw.win.base.min()}
	for i := range lw.lanes {
		c := &s.Lanes[i]
		c.SentFrames, c.SentBytes, c.MaxWait = lw.totals[i].frames, lw.totals[i].bytes, lw.totals[i].maxWait
		most := framePayloadMax
		if Lane(i) == LaneBulk {
			most = lw.win.frameSize()
		}
		for _, l := range lw.lanes[i].lines {
			left := len(l.b) - l.off
			c.QueuedLines++
			c.QueuedBytes += int64(left)
			c.QueuedFrames += max((left+most-1)/most, 1)
			if !l.started {
				c.OldestWait = max(c.OldestWait, now.Sub(l.queued))
			}
		}
		c.MaxWait = max(c.MaxWait, c.OldestWait)
	}
	return s
}
