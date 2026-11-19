package rpc

import (
	"testing"
	"time"
)

// fill sends bulk frames of the window's size until it is full, all from at; it returns the last seq.
func (w *creditWindow) fillFrom(seq uint64, at time.Time) uint64 {
	for w.canSend(w.frameSize()) {
		seq++
		w.wrote(seq, w.frameSize(), at, true)
	}
	return seq
}

func TestBulkWindowGrowsOnAnIdleLink(t *testing.T) {
	w := newCreditWindow()
	now := time.Unix(1_000_000, 0)
	seq := uint64(0)
	for range 20 {
		seq = w.fillFrom(seq, now)
		if !w.limited {
			t.Fatal("a full window was not noted")
		}
		now = now.Add(10 * time.Millisecond) // a round trip with no queue
		w.acked(seq, clockMS(now), now)
	}
	if w.cwnd != windowMax {
		t.Fatalf("window %d after 20 quiet round trips, want %d", w.cwnd, windowMax)
	}
	if w.inFlight != 0 || len(w.sent) != 0 {
		t.Fatalf("in flight %d, %d entries", w.inFlight, len(w.sent))
	}
	if w.frameSize() != framePayloadMax {
		t.Fatalf("frame %d", w.frameSize())
	}
}

// Round trips that grow past the link's own by more than queueTarget shrink the window, at most once a round trip,
// never below windowMin; frames shrink with it.
func TestBulkWindowShrinksWhenFramesQueue(t *testing.T) {
	w := newCreditWindow()
	w.cwnd = 1 << 20
	now := time.Unix(1_000_000, 0)
	w.wrote(1, 1000, now, true)
	w.acked(1, clockMS(now.Add(400*time.Millisecond)), now.Add(400*time.Millisecond)) // the link's own round trip
	before := w.cwnd
	seq := uint64(1)
	for i := 0; i < 3; i++ {
		seq++
		w.wrote(seq, 1000, now, true)
	}
	now = now.Add(time.Second)
	w.acked(seq-2, clockMS(now), now) // 1 s: 600 ms queued
	if w.cwnd >= before {
		t.Fatalf("window %d did not shrink", w.cwnd)
	}
	cut := w.cwnd
	w.acked(seq-1, clockMS(now.Add(10*time.Millisecond)), now.Add(10*time.Millisecond)) // the same round trip: no second cut
	if w.cwnd != cut {
		t.Fatalf("cut twice in a round trip: %d → %d", cut, w.cwnd)
	}
	for range 100 {
		seq++
		w.wrote(seq, 1000, now, true)
		now = now.Add(3 * time.Second)
		w.acked(seq, clockMS(now), now)
	}
	if w.cwnd != windowMin || w.frameSize() != bulkFrameMin {
		t.Fatalf("window %d, frame %d", w.cwnd, w.frameSize())
	}
}

// A window bulk did not fill does not grow: a burst later would find it larger than the link has shown it can take.
func TestBulkWindowGrowsOnlyWhenItWasTheLimit(t *testing.T) {
	w := newCreditWindow()
	now := time.Unix(1_000_000, 0)
	for i := uint64(1); i <= 50; i++ {
		w.wrote(i, 1000, now, true)
		now = now.Add(5 * time.Millisecond)
		w.acked(i, clockMS(now), now)
	}
	if w.cwnd != windowInit {
		t.Fatalf("window %d, want %d", w.cwnd, windowInit)
	}
}

func TestBulkWindowCanSend(t *testing.T) {
	w := newCreditWindow()
	if !w.canSend(10 * windowInit) {
		t.Fatal("nothing in flight: one frame of any size may go")
	}
	w.wrote(1, windowInit-100, time.Now(), true)
	if w.canSend(200) || !w.limited {
		t.Fatal("over the window")
	}
	if !w.canSend(100) {
		t.Fatal("exactly the window")
	}
}

func TestBulkWindowEntriesStayBounded(t *testing.T) {
	w := newCreditWindow()
	start := time.Unix(1_000_000, 0)
	for i := uint64(1); i <= 3*maxSentEntries; i++ {
		w.wrote(i, 10, start.Add(time.Duration(i)), true)
	}
	if len(w.sent) > maxSentEntries {
		t.Fatalf("%d entries", len(w.sent))
	}
	w.acked(3*maxSentEntries, clockMS(start.Add(time.Second)), start.Add(time.Second))
	if w.inFlight != 0 || len(w.sent) != 0 {
		t.Fatalf("in flight %d after acknowledging everything", w.inFlight)
	}
}

func TestBaseDelayForgetsOldMinutesAndWraps(t *testing.T) {
	var b baseDelay
	now := time.Unix(1_000_000*60, 0)
	b.add(50, now)
	b.add(300, now.Add(time.Minute))
	if b.min() != 50 {
		t.Fatalf("%d", b.min())
	}
	b.add(300, now.Add(baseSlots*time.Minute))
	if b.min() != 300 {
		t.Fatalf("a sample %d minutes old still counts: %d", baseSlots, b.min())
	}
	// Two clocks far apart: delays near the top of the range and past its wrap compare by their difference.
	var w baseDelay
	w.add(1<<32-10, now)
	w.add(5, now) // 15 ms later than the first, past the wrap
	if w.min() != 1<<32-10 {
		t.Fatalf("wrapped: %d", w.min())
	}
}

// Short frames (short lines, a long line's last piece) and full ones are judged against bases of their own kind: next
// to a base short frames set, full frames' own time on the wire read as queueing and the window never opened on a slow
// link (13 KB/s on 30 KB/s in #10660's lane experiment), and with no samples from short frames a window of short lines
// alone never grew (a review: 238 KiB/s where lines did 1920).
func TestCreditWindowJudgesFramesByTheirKind(t *testing.T) {
	t.Run("full frames next to short ones", func(t *testing.T) {
		w := newCreditWindow()
		now := time.Unix(1_000_000, 0)
		w.wrote(1, 50, now, false)
		w.acked(1, clockMS(now.Add(400*time.Millisecond)), now.Add(400*time.Millisecond)) // a short frame: the link's delay alone
		seq := w.fillFrom(1, now)
		w.acked(seq, clockMS(now.Add(540*time.Millisecond)), now.Add(540*time.Millisecond)) // full frames: the delay and their own time on the wire
		if w.base[1].min() != 540 || w.cwnd <= windowInit {
			t.Fatalf("full frames: base %d ms, window %d", w.base[1].min(), w.cwnd)
		}
	})
	t.Run("short frames alone", func(t *testing.T) {
		w := newCreditWindow()
		now := time.Unix(1_000_000, 0)
		seq := uint64(0)
		for range 10 { // a window of 6 KiB lines each round trip, no queue
			for w.canSend(6 << 10) {
				seq++
				w.wrote(seq, 6<<10, now, false)
			}
			now = now.Add(200 * time.Millisecond)
			w.acked(seq, clockMS(now), now)
		}
		if w.cwnd < 16*windowInit {
			t.Fatalf("window %d after 10 quiet round trips of short frames", w.cwnd)
		}
	})
}

// Past maxSentEntries the newest entry takes the next frames, and their time: its round trip is the newest frame's,
// not the time since the first frame it took.
func TestCreditWindowMergedEntryTakesTheNewestTime(t *testing.T) {
	w := newCreditWindow()
	start := time.Unix(1_000_000, 0)
	for i := uint64(1); i <= maxSentEntries; i++ {
		w.wrote(i, 10, start, true)
	}
	later := start.Add(10 * time.Second)
	w.wrote(maxSentEntries+1, 10, later, true)
	w.acked(maxSentEntries+1, clockMS(later.Add(100*time.Millisecond)), later.Add(100*time.Millisecond))
	if w.rtt != 100*time.Millisecond {
		t.Fatalf("round trip %s, want the newest frame's 100ms", w.rtt)
	}
}

// Acknowledgements bunched behind what the other side sends make the round trip long, longer and longer here; the
// window is judged by the delay one way, and keeps what the other side reads in a round trip in flight, so this
// direction stays busy (judged by the round trip, the growing wait on the way back read as queueing).
func TestCreditWindowAcrossABusyWayBack(t *testing.T) {
	const rate = 30_000 // bytes a second the link carries this way
	w := newCreditWindow()
	now := time.Unix(1_000_000, 0)
	seq := uint64(0)
	var back time.Duration
	for round := range 8 {
		back = 2*time.Second + time.Duration(round)*1500*time.Millisecond // acknowledgements wait this long on the way back
		start := now
		var sent []sentFrame
		for w.canSend(w.frameSize()) {
			seq++
			n := w.frameSize()
			w.wrote(seq, n, start, true)
			sent = append(sent, sentFrame{seq: seq, bytes: n, at: start})
		}
		read := start.Add(200 * time.Millisecond) // one way, then each frame in its turn at the link's rate
		now = start.Add(back)
		for _, f := range sent {
			read = read.Add(time.Duration(f.bytes) * time.Second / rate)
			w.acked(f.seq, clockMS(read), now)
		}
	}
	if w.rate < rate*8/10 || w.rate > rate*12/10 {
		t.Fatalf("delivery rate %.0f, the link's %d", w.rate, rate)
	}
	if need := int(rate * back.Seconds() * 8 / 10); w.cwnd < need {
		t.Fatalf("window %d, under the %d the link carries in a round trip", w.cwnd, need)
	}
	if w.frameSize() > bulkFrameMin*2 {
		t.Fatalf("frames of %d on a %d B/s link", w.frameSize(), rate)
	}
}

// simBusyWayBack runs w for d against a link of rate bytes a second and oneWay delay each way whose other direction is
// kept full (queue bytes waiting, frames of backFrame bytes): an acknowledgement goes out when the other side's frame
// being written ends, and waits behind queue bytes; with backFrame 0 it goes out at once. The window always has bulk
// to send. It returns what the other side read after warmUp, a second, and the longest a frame waited to go on the
// link after warmUp.
func simBusyWayBack(w *creditWindow, rate float64, oneWay time.Duration, backFrame, queue int, d, warmUp time.Duration) (float64, time.Duration) {
	type inFlight struct {
		seq  uint64
		read time.Time // the other side reads it
	}
	type ack struct {
		seq  uint64
		read time.Time
		at   time.Time // it arrives
	}
	perByte := float64(time.Second) / rate
	frameTime := time.Duration(float64(backFrame) * perByte)
	backQueue := time.Duration(float64(queue) * perByte)
	start := time.Unix(1_000_000, 0)
	free := start // when the link is free to start the next frame
	var frames []inFlight
	var acks []ack
	seq := uint64(0)
	read := 0
	var worst time.Duration
	for now := start; now.Before(start.Add(d)); now = now.Add(time.Millisecond) {
		for len(acks) > 0 && !acks[0].at.After(now) {
			w.acked(acks[0].seq, clockMS(acks[0].read), now)
			acks = acks[1:]
		}
		for len(frames) > 0 && !frames[0].read.After(now) {
			f := frames[0]
			frames = frames[1:]
			// Acknowledged when the other side's frame being written ends, behind the queue.
			leave := f.read
			if frameTime > 0 {
				leave = start.Add((f.read.Sub(start)/frameTime + 1) * frameTime)
			}
			acks = append(acks, ack{seq: f.seq, read: f.read, at: leave.Add(backQueue + oneWay)})
		}
		for {
			n := w.frameSize()
			if !w.canSend(n) {
				break
			}
			seq++
			w.wrote(seq, n, now, true)
			begin := maxTime(free, now)
			if now.Sub(start) >= warmUp {
				worst = max(worst, begin.Sub(now))
			}
			free = begin.Add(time.Duration(float64(n) * perByte))
			frames = append(frames, inFlight{seq: seq, read: free.Add(oneWay)})
			if free.Sub(start) >= warmUp && free.Sub(start) < d {
				read += n
			}
		}
	}
	return float64(read) / (d - warmUp).Seconds(), worst
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// TestCreditWindowKeepsTheLinkBusyWhenTheWayBackIsFull: a port's bytes go one way while the other side's events fill
// the way back, so acknowledgements wait behind them and come in bunches (#10660's review: the window, judged by round
// trips, let through 5.5 KB/s of a 30 KB/s link). Frames still keep the link busy, and wait on their way about as
// long as the other side's frames hold acknowledgements back, not for a whole queue.
func TestCreditWindowKeepsTheLinkBusyWhenTheWayBackIsFull(t *testing.T) {
	const rate = 30_000
	for _, queue := range []int{0, 64 << 10, 256 << 10} {
		w := newCreditWindow()
		got, worst := simBusyWayBack(&w, rate, 200*time.Millisecond, 32<<10, queue, 2*time.Minute, 30*time.Second)
		if got < rate*9/10 {
			t.Errorf("%d KiB queued on the way back: %.0f B/s of a %d B/s link", queue>>10, got, rate)
		}
		if worst > 1500*time.Millisecond {
			t.Errorf("%d KiB queued on the way back: a frame waited %s to go out", queue>>10, worst)
		}
	}
}

// TestCreditWindowGrowsLittlePastTheLink: once the window knows what the link carries, it grows no more than a quarter
// past it. Grown by doubling until the queue shows, a round trip later, it had queued a second of frames at 30 KB/s
// on the way (a pong behind them waited that long).
func TestCreditWindowGrowsLittlePastTheLink(t *testing.T) {
	for _, c := range []struct {
		rate  float64
		worst time.Duration
	}{{30_000, 600 * time.Millisecond}, {1_000_000, 200 * time.Millisecond}} {
		w := newCreditWindow()
		got, worst := simBusyWayBack(&w, c.rate, 200*time.Millisecond, 0, 0, 2*time.Minute, 30*time.Second)
		if got < c.rate*95/100 {
			t.Errorf("%.0f B/s link: %.0f B/s", c.rate, got)
		}
		if worst > c.worst {
			t.Errorf("%.0f B/s link: a frame waited %s to go out, want under %s", c.rate, worst, c.worst)
		}
	}
}
