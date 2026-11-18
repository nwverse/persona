package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/netshape"
)

// A framed connection gives the heartbeat (devices.pingAnswered) the signals a connection of lines gives for the same
// traffic: these feed a manager-side peer the same lines both ways and compare PingActivity.

// framedHub is a manager-side peer that asked for lanes and read the node's answer: its input is framed from here. write
// hands it one raw read and returns once Serve has asked for the next. base is the activity the answer itself gave.
func framedHub(t *testing.T) (p *Peer, write func([]byte), base time.Time) {
	t.Helper()
	r, w := io.Pipe()
	p = New("h", io.Discard)
	p.EnableLanes(testControl, testBulk)
	served := make(chan error, 1)
	go func() { served <- p.Serve(context.Background(), r) }()
	t.Cleanup(func() {
		_ = w.Close()
		_ = r.Close()
		p.Close(io.EOF)
		<-served
	})
	up := make(chan error, 1)
	go func() { up <- p.UpgradeLanes(context.Background()) }()
	eventually(t, func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.lanesCall != "" })
	write = func(b []byte) {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			_, err := w.Write(b)
			if err == nil {
				_, err = w.Write(nil) // returns once Serve read again: what came before is taken
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not take the write")
		}
	}
	write([]byte(`{"id":"h1","result":{"version":1}}` + "\n"))
	if err := <-up; err != nil {
		t.Fatal(err)
	}
	base = p.PingActivity()
	for time.Since(base) < time.Millisecond { // later reads must be told apart from the answer's
		runtime.Gosched()
	}
	return p, write, base
}

// linesActivity is whether a peer of lines fed reads counts any activity.
func linesActivity(t *testing.T, reads ...string) bool {
	t.Helper()
	p := New("h", io.Discard)
	write := servePipe(t, p)
	for _, r := range reads {
		write(r)
	}
	return !p.PingActivity().IsZero()
}

func TestLanesActivityAsLines(t *testing.T) {
	small := []byte(`{"event":"session.screen","params":{"session_id":"s1"}}`)
	twoFrames := []byte(`{"event":"sessions.changed","params":{"list":"` + strings.Repeat("x", 40<<10) + `"}}`)
	port := []byte(`{"event":"stream.data","params":{"id":"p1","data":"` + strings.Repeat("A", 20<<10) + `"}}`)
	deadline := []byte(`{"id":"h9","error":{"message":"context deadline exceeded"}}`)
	reply := []byte(`{"id":"h7","result":{}}`)
	type part struct {
		lane  Lane
		piece int
		line  []byte
	}
	for _, c := range []struct {
		name  string
		parts []part
		moves bool
	}{
		// Lines the node starts itself never count, however they are cut and whatever follows in the same read.
		{"an event, then a whole event of two frames", []part{{LaneEvents, framePayloadMax, small}, {LaneEvents, framePayloadMax, twoFrames}}, false},
		{"two port lines cut small", []part{{LaneBulk, 8 << 10, port}, {LaneBulk, 8 << 10, port}}, false},
		{"a deadline, then a whole event of two frames", []part{{LaneInteractive, framePayloadMax, deadline}, {LaneEvents, framePayloadMax, twoFrames}}, false},
		{"small events", []part{{LaneEvents, framePayloadMax, small}, {LaneEvents, framePayloadMax, small}}, false},
		{"an event of one frame behind an event", []part{{LaneEvents, framePayloadMax, small}, {LaneEvents, framePayloadMax, port}}, false},
		// A reply does, ahead of the line before it.
		{"an event, then a reply", []part{{LaneEvents, framePayloadMax, small}, {LaneInteractive, framePayloadMax, reply}}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var lines string
			for _, p := range c.parts {
				lines += string(p.line) + "\n"
			}
			if got := linesActivity(t, lines); got != c.moves {
				t.Fatalf("lines: activity %t", got)
			}
			p, write, base := framedHub(t)
			var wire []byte
			seq := uint64(0)
			for _, part := range c.parts {
				var w []byte
				w, seq = frames(seq, part.lane, part.piece, part.line)
				wire = append(wire, w...)
			}
			write(wire)
			if got := p.PingActivity().After(base); got != c.moves {
				t.Fatalf("framed: activity %t, lines %t", got, c.moves)
			}
		})
	}
}

// The middle piece of a line whose first piece is in the same read is never read as a line of its own: params with a
// nested id and result look like a reply's envelope.
func TestLanesActivityMiddlePieceIsNotALine(t *testing.T) {
	inner := strings.Repeat(`{"id":"s1","result":"ok","x":"`+strings.Repeat("y", 200)+`"},`, 200)
	big := []byte(`{"event":"tool.results","params":{"items":[` + inner + `{}]}}`)
	p, write, base := framedHub(t)
	wire, seq := frames(0, LaneEvents, framePayloadMax, []byte(`{"event":"session.screen","params":{}}`))
	for i := 0; i < 3; i++ { // the first three pieces of big, not its last
		seq++
		var h [frameHeader]byte
		putHead(h[:], frameHead{kind: frameData, lane: LaneBulk, slot: 1, size: 4 << 10, seq: seq})
		wire = append(append(wire, h[:]...), big[i*4<<10:(i+1)*4<<10]...)
	}
	write(wire)
	if p.PingActivity().After(base) {
		t.Fatal("a middle piece of a node event counted")
	}
}

// The middle of a long line counts read for read, as with lines: a line of two frames read 4 KiB at a time.
func TestLanesActivityMiddleReadForRead(t *testing.T) {
	line := `{"event":"sessions.changed","params":"` + strings.Repeat("e", 50<<10) + `"}`
	var reads []string
	for i := 0; i < len(line)-1000; i += 4 << 10 {
		reads = append(reads, line[i:min(i+4<<10, len(line)-1000)])
	}
	if !linesActivity(t, reads...) {
		t.Fatal("lines: the middle of a 50 KiB line did not count")
	}
	p, write, base := framedHub(t)
	wire, _ := frames(0, LaneEvents, framePayloadMax, []byte(line))
	for i := 0; i < len(wire)-1000; i += 4 << 10 {
		write(wire[i:min(i+4<<10, len(wire)-1000)])
	}
	if !p.PingActivity().After(base) {
		t.Fatal("framed: the middle of a 50 KiB line did not count")
	}
}

// A short line finishing while a long one still arrives leaves the long one's middle counting until it is whole; with
// lines nothing finishes in between.
func TestLanesActivityMiddleStandsUntilTheLineIsWhole(t *testing.T) {
	p, write, base := framedHub(t)
	long := []byte(`{"event":"sessions.changed","params":"` + strings.Repeat("e", 4*framePayloadMax) + `"}`)
	wire, _ := frames(0, LaneEvents, framePayloadMax, long) // frames 1..5
	piece := frameHeader + framePayloadMax
	write(wire[:piece]) // a line begun in a read does not count in it, as with lines
	write(wire[piece : 2*piece])
	mid := p.PingActivity()
	if !mid.After(base) {
		t.Fatal("the middle did not count")
	}
	short := []byte(`{"event":"session.screen","params":{}}`)
	var h [frameHeader]byte
	putHead(h[:], frameHead{kind: frameData, lane: LaneEvents, fin: true, slot: 1, size: len(short), seq: 3})
	write(append(h[:], short...))
	if p.PingActivity().Before(mid) {
		t.Fatal("a short line took the activity back while the long one still arrived")
	}
}

// A large request on a slow link with a deep transport buffer does not hold a ping behind it: interactive lines wait
// for credit too, so the buffer holds about a window of them, not all of it (with lines the ping would wait to be
// written, which pingAnswered counts neither way).
func TestLanesPingPassesALargeRequest(t *testing.T) {
	slow := netshape.Config{Rate: 64 << 10, Queue: 256 << 10} // 4 s of buffer one way
	a, b := netshape.Pipe(slow, netshape.Config{})
	h, n := New("h", a), New("n", b)
	h.EnableLanes(testControl, testBulk)
	n.EnableLanes(testControl, testBulk)
	n.OnRequest = func(context.Context, string, json.RawMessage) (any, error) { return map[string]bool{"ok": true}, nil }
	go func() { _ = h.Serve(context.Background(), a) }()
	go func() { _ = n.Serve(context.Background(), b) }()
	defer func() { a.Close(); b.Close() }()
	if err := h.UpgradeLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = h.Call(context.Background(), "model.http", map[string]string{"body": strings.Repeat("b", 1<<20)}, nil)
	}()
	eventually(t, func() bool { return h.LaneStats().Lanes[LaneInteractive].SentFrames > 4 })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := h.Call(ctx, "ping", map[string]any{}, nil); err != nil {
		t.Fatalf("ping behind a 1 MiB request: %v after %s (%s)", err, time.Since(start), h.LaneStats())
	}
}

// Lines in progress hold at most twice the line limit together: a peer opening many never finishes none of them.
func TestFrameReaderBoundsLinesInProgress(t *testing.T) {
	var wire []byte
	seq := uint64(0)
	for slot := uint16(0); slot < 8; slot++ {
		for range 4 { // 4 KiB a line so far
			seq++
			var h [frameHeader]byte
			putHead(h[:], frameHead{kind: frameData, lane: LaneEvents, slot: slot, size: 1 << 10, seq: seq})
			wire = append(append(wire, h[:]...), bytes.Repeat([]byte("x"), 1<<10)...)
		}
	}
	fr := newFrameReader(bytes.NewReader(wire), 10<<10) // eight lines of 4 KiB: over twice the limit together
	for fr.Scan() {
	}
	if !errors.Is(fr.Err(), bufio.ErrTooLong) {
		t.Fatalf("%v", fr.Err())
	}
}

// Lines that never started are NotSent when the writer stops, however it stops.
func TestLaneWriterStopIsNotSentForLinesNotStarted(t *testing.T) {
	g := newGate()
	closed := make(chan struct{})
	lw := newLaneWriter(g, closed, func(error) {}, nil)
	lw.start()
	busy := queue(t, lw, LaneEvents, strings.Repeat("x", 2*framePayloadMax))
	g.waitWrite(t)
	waiting := queue(t, lw, LaneEvents, `{"event":"e"}`)
	lw.stop(ErrClosed)
	if err := <-waiting.done; !errors.Is(err, ErrNotSent) || !errors.Is(err, ErrClosed) {
		t.Fatalf("not started: %v", err)
	}
	if err := <-busy.done; errors.Is(err, ErrNotSent) || !errors.Is(err, ErrClosed) {
		t.Fatalf("begun: %v", err)
	}
	close(closed)
	g.release(10)
}

// The reader asks for an acknowledgement after frames with credit (interactive, bulk), never after events or control
// alone.
func TestFrameReaderAsksAcksForCreditOnly(t *testing.T) {
	for lane, wants := range map[Lane]bool{LaneControl: false, LaneEvents: false, LaneInteractive: true, LaneBulk: true} {
		wire, _ := frames(0, lane, framePayloadMax, []byte(`{"event":"x"}`))
		fr := newFrameReader(bytes.NewReader(wire), 1<<20)
		asked := false
		fr.ack = func(uint64) { asked = true }
		for fr.Scan() {
		}
		if asked != wants {
			t.Errorf("%s: asked %t", lane, asked)
		}
	}
}

// A line whose write is under way when the peer closes is told how the write went, as a line on the one writer is: an
// accepted update's answer that went out whole is still seen by LineWritten.
func TestLanesLineWrittenAcrossClose(t *testing.T) {
	g := newGate()
	n := New("n", g)
	n.EnableLanes(testControl, testBulk)
	written := make(chan []byte, 1)
	n.LineWritten = func(b []byte) { written <- b }
	lw := n.frameWriter()
	lw.start()
	n.out.Store(lw)
	errc := make(chan error, 1)
	go func() { errc <- n.Notify("evt", map[string]string{"x": "y"}) }()
	g.waitWrite(t) // its only frame is being written
	n.Close(errors.New("gone"))
	g.release(10)
	if err := <-errc; err != nil {
		t.Fatalf("written whole across the close: %v", err)
	}
	if b := <-written; !bytes.Contains(b, []byte(`"evt"`)) {
		t.Fatalf("LineWritten saw %q", b)
	}
}
