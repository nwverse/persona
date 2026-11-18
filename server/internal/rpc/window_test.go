package rpc

import (
	"testing"
	"time"
)

// fill sends bulk frames of the window's size until it is full, all from at; it returns the last seq.
func (w *bulkWindow) fillFrom(seq uint64, at time.Time) uint64 {
	for w.canSend(w.frameSize()) {
		seq++
		w.wrote(seq, w.frameSize(), at)
	}
	return seq
}

func TestBulkWindowGrowsOnAnIdleLink(t *testing.T) {
	w := newBulkWindow()
	now := time.Unix(1_000_000, 0)
	seq := uint64(0)
	for range 20 {
		seq = w.fillFrom(seq, now)
		if !w.limited {
			t.Fatal("a full window was not noted")
		}
		now = now.Add(10 * time.Millisecond) // a round trip with no queue
		w.acked(seq, now)
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
	w := newBulkWindow()
	w.cwnd = 1 << 20
	now := time.Unix(1_000_000, 0)
	w.wrote(1, 1000, now)
	w.acked(1, now.Add(400*time.Millisecond)) // the link's own round trip
	before := w.cwnd
	seq := uint64(1)
	for i := 0; i < 3; i++ {
		seq++
		w.wrote(seq, 1000, now)
	}
	now = now.Add(time.Second)
	w.acked(seq-2, now) // 1 s: 600 ms queued
	if w.cwnd >= before {
		t.Fatalf("window %d did not shrink", w.cwnd)
	}
	cut := w.cwnd
	w.acked(seq-1, now.Add(10*time.Millisecond)) // the same round trip: no second cut
	if w.cwnd != cut {
		t.Fatalf("cut twice in a round trip: %d → %d", cut, w.cwnd)
	}
	for range 100 {
		seq++
		w.wrote(seq, 1000, now)
		now = now.Add(3 * time.Second)
		w.acked(seq, now)
	}
	if w.cwnd != windowMin || w.frameSize() != bulkFrameMin {
		t.Fatalf("window %d, frame %d", w.cwnd, w.frameSize())
	}
}

// A window bulk did not fill does not grow: a burst later would find it larger than the link has shown it can take.
func TestBulkWindowGrowsOnlyWhenItWasTheLimit(t *testing.T) {
	w := newBulkWindow()
	now := time.Unix(1_000_000, 0)
	for i := uint64(1); i <= 50; i++ {
		w.wrote(i, 1000, now)
		now = now.Add(5 * time.Millisecond)
		w.acked(i, now)
	}
	if w.cwnd != windowInit {
		t.Fatalf("window %d, want %d", w.cwnd, windowInit)
	}
}

func TestBulkWindowCanSend(t *testing.T) {
	w := newBulkWindow()
	if !w.canSend(10 * windowInit) {
		t.Fatal("nothing in flight: one frame of any size may go")
	}
	w.wrote(1, windowInit-100, time.Now())
	if w.canSend(200) || !w.limited {
		t.Fatal("over the window")
	}
	if !w.canSend(100) {
		t.Fatal("exactly the window")
	}
}

func TestBulkWindowEntriesStayBounded(t *testing.T) {
	w := newBulkWindow()
	start := time.Unix(1_000_000, 0)
	for i := uint64(1); i <= 3*maxSentEntries; i++ {
		w.wrote(i, 10, start.Add(time.Duration(i)))
	}
	if len(w.sent) > maxSentEntries {
		t.Fatalf("%d entries", len(w.sent))
	}
	w.acked(3*maxSentEntries, start.Add(time.Second))
	if w.inFlight != 0 || len(w.sent) != 0 {
		t.Fatalf("in flight %d after acknowledging everything", w.inFlight)
	}
}

func TestBaseRTTForgetsOldMinutes(t *testing.T) {
	var b baseRTT
	now := time.Unix(1_000_000*60, 0)
	b.add(50*time.Millisecond, now)
	b.add(300*time.Millisecond, now.Add(time.Minute))
	if b.min() != 50*time.Millisecond {
		t.Fatalf("%s", b.min())
	}
	b.add(300*time.Millisecond, now.Add(baseSlots*time.Minute))
	if b.min() != 300*time.Millisecond {
		t.Fatalf("a sample %d minutes old still counts: %s", baseSlots, b.min())
	}
}
