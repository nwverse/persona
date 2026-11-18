package rpc

import "time"

// creditWindow is how many bytes of interactive and bulk frames a framed connection may have sent and not yet seen
// acknowledged (creditLane). It adapts to the link by delay (as LEDBAT does): each acknowledgement gives a round trip
// for the newest such frame it covers, the
// smallest round trip seen lately is the link's own, and what a sample takes beyond it is time the frame spent queued
// (in the transport's buffers, behind other frames). Below queueTarget the window grows, quickly while the queue is
// short; above it the window shrinks once a round trip. On a link of 30 KB/s the window settles near what the link
// carries in a round trip, so a pong or an answer written behind bulk frames waits about queueTarget; on a fast link it
// grows to what keeps the link busy, up to windowMax.
//
// Bulk frames are cut to a quarter of the window (bulkFrameSize): on a slow link a frame takes long to go out, and an
// interactive frame behind it waits for the whole frame.
type creditWindow struct {
	cwnd     int
	inFlight int
	sent     []sentBulk // bulk sent and not acknowledged, oldest first; one entry per frame
	limited  bool       // a bulk frame waited for the window since it last grew
	base     baseRTT
	lastCut  time.Time
	rtt      time.Duration // the latest sample
}

// sentBulk is a bulk frame (or, past maxSentEntries, the frames through seq): its bytes, written from at.
type sentBulk struct {
	seq   uint64
	bytes int
	at    time.Time
}

const (
	windowInit   = 32 << 10
	windowMin    = 8 << 10
	windowMax    = 4 << 20
	bulkFrameMin = 4 << 10
	queueTarget  = 100 * time.Millisecond
	// maxSentEntries bounds sent: past this many frames in flight (tiny bulk lines on a fast link) the newest entry
	// takes the next frame's bytes too, which only blurs its round trip.
	maxSentEntries = 4096
)

func newCreditWindow() creditWindow { return creditWindow{cwnd: windowInit} }

// frameSize is the payload of a bulk frame now.
func (w *creditWindow) frameSize() int {
	return min(max(w.cwnd/4, bulkFrameMin), framePayloadMax)
}

// canSend: a bulk frame of n bytes may go out now. With nothing in flight one frame always may, whatever its size.
func (w *creditWindow) canSend(n int) bool {
	if w.inFlight == 0 || w.inFlight+n <= w.cwnd {
		return true
	}
	w.limited = true
	return false
}

// wrote records a bulk frame of n bytes, numbered seq, written from at.
func (w *creditWindow) wrote(seq uint64, n int, at time.Time) {
	w.inFlight += n
	if k := len(w.sent); k >= maxSentEntries {
		w.sent[k-1].seq, w.sent[k-1].bytes = seq, w.sent[k-1].bytes+n
		return
	}
	w.sent = append(w.sent, sentBulk{seq: seq, bytes: n, at: at})
}

// acked takes an acknowledgement of every frame through seq, read at now, and adapts the window.
func (w *creditWindow) acked(seq uint64, now time.Time) {
	var freed int
	var at time.Time
	i := 0
	for ; i < len(w.sent) && w.sent[i].seq <= seq; i++ {
		freed += w.sent[i].bytes
		at = w.sent[i].at
	}
	if i == 0 {
		return
	}
	w.sent = append(w.sent[:0], w.sent[i:]...)
	w.inFlight -= freed
	rtt := max(now.Sub(at), 0)
	w.rtt = rtt
	w.base.add(rtt, now)
	queued := rtt - w.base.min()
	switch {
	case queued > queueTarget:
		if now.Sub(w.lastCut) >= rtt {
			cut := w.cwnd / 4 // a quarter off, half when the queue is far over
			if queued > 4*queueTarget {
				cut = w.cwnd / 2
			}
			w.cwnd = max(w.cwnd-cut, windowMin)
			w.lastCut = now
		}
		w.limited = false
	case !w.limited:
		// The window was not what held bulk back: growing it would only allow a burst later.
	case queued < queueTarget/2:
		w.cwnd = min(w.cwnd+freed, windowMax) // about doubling each round trip
		w.limited = false
	default:
		w.cwnd = min(w.cwnd+max(freed*framePayloadMax/w.cwnd, 1), windowMax) // about a frame each round trip
		w.limited = false
	}
}

// baseRTT is the smallest round trip seen in the last baseSlots minutes: the link's own delay, which a new route
// can raise.
type baseRTT struct {
	slot [baseSlots]time.Duration
	at   [baseSlots]int64 // the minute each slot is for
}

const baseSlots = 5

func (b *baseRTT) add(rtt time.Duration, now time.Time) {
	rtt = max(rtt, 1) // a sample of 0 (a clock that did not move) is still a sample; 0 marks an empty slot
	minute := now.Unix() / 60
	i := int(minute % baseSlots)
	if b.at[i] != minute {
		b.slot[i], b.at[i] = rtt, minute
	} else if rtt < b.slot[i] {
		b.slot[i] = rtt
	}
}

// min is the smallest sample of the slots still current; with none, 0.
func (b *baseRTT) min() time.Duration {
	newest := int64(0)
	for _, m := range b.at {
		newest = max(newest, m)
	}
	var out time.Duration
	for i, d := range b.slot {
		if d > 0 && newest-b.at[i] < baseSlots && (out == 0 || d < out) {
			out = d
		}
	}
	return out
}
