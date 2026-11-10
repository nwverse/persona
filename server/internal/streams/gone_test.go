package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The hub gives up (the phone went away) while the node is still dialing: its stream.close reaches the node before
// the node's Open, and the node must not keep the stream, and its connection to the port, forever.
func TestStreamCloseBeforeOpen(t *testing.T) {
	gate, asked, dialed := make(chan struct{}), make(chan struct{}, 1), make(chan struct{}, 1)
	closeSeen := make(chan string, 4)
	hub, node, hp := pairWith(t, nodeHooks{
		// The node holds the dial between connecting and Open, as a slow dial or a browser starting for a preview does.
		beforeOpen: func() { asked <- struct{}{}; <-gate },
		dialed:     func() { dialed <- struct{}{} },
		onEvent: func(e string, p json.RawMessage) {
			if e == EventClose {
				var m dataMsg
				_ = json.Unmarshal(p, &m)
				closeSeen <- m.ID
			}
		},
	})
	serverSaw := make(chan error, 1)
	addr := listen(t, func(c net.Conn) { // a server that waits for the client to speak
		_, err := c.Read(make([]byte, 1))
		serverSaw <- err
		c.Close()
	})
	if err := hub.Open("p1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	callErr := make(chan error, 1)
	go func() { callErr <- hp.Call(ctx, "port.dial", map[string]any{"id": "p1", "addr": addr}, nil) }()
	<-asked
	cancel()
	hub.Abort("p1", <-callErr)
	if id := <-closeSeen; id != "p1" {
		t.Fatalf("close for %q", id)
	}
	close(gate)
	<-dialed
	if node.Len() != 0 {
		t.Fatal("the node keeps a stream the hub closed before it was opened")
	}
	select {
	case err := <-serverSaw:
		if err == nil {
			t.Fatal("the server read a byte")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the node's connection to the port stays open")
	}
}

// A stream the other side does not hold (it ended there, or that side restarted): its data gets a close back, so the
// side still holding it ends it and closes its connection.
func TestStreamDataForUnknownStreamIsClosed(t *testing.T) {
	_, node, _ := pair(t)
	serverSaw := make(chan error, 1)
	addr := listen(t, func(c net.Conn) { // a server that speaks first (VNC), then waits
		_, _ = c.Write([]byte("RFB 003.008\n"))
		_, err := c.Read(make([]byte, 1))
		serverSaw <- err
		c.Close()
	})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Open("p9"); err != nil {
		t.Fatal(err)
	}
	node.Start("p9", c) // the hub never opened p9, or forgot it
	waitFor(t, func() bool { return node.Len() == 0 }, "the node keeps a stream the hub does not hold")
	select {
	case <-serverSaw:
	case <-time.After(5 * time.Second):
		t.Fatal("the node's connection to the port stays open")
	}
}

// sentLog is a Mux's send that keeps what it sent.
type sentLog struct {
	mu   sync.Mutex
	sent []string // "event id error"
}

func (l *sentLog) send(event string, params any) error {
	m, _ := params.(dataMsg)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent = append(l.sent, event+" "+m.ID+" "+m.Err)
	return nil
}

func (l *sentLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.sent...)
}

func event(t *testing.T, m *Mux, name, id string) {
	t.Helper()
	b, _ := json.Marshal(dataMsg{ID: id, Data: []byte("x"), N: 1})
	if !m.Event(name, b) {
		t.Fatalf("%s not taken", name)
	}
}

// elapse moves the bubble's clock on by d (no real wait) and lets what it woke run.
func elapse(d time.Duration) {
	<-time.After(d)
	synctest.Wait()
}

// Data for an id this side does not hold is answered with one close, however much of it comes; an ack or a close
// for one is not answered. Data alone does not stop the id from being opened later (an older hub reuses its ids
// after a restart); a close does, for goneFor.
func TestStreamUnknownIDs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log sentLog
		m := New(log.send)
		for range 3 {
			event(t, m, EventData, "d1")
		}
		event(t, m, EventAck, "a1")
		event(t, m, EventClose, "c1")
		synctest.Wait()
		if got := log.all(); len(got) != 1 || got[0] != "stream.close d1 streams: no such stream" {
			t.Fatalf("sent %q, want one close for d1", got)
		}
		if err := m.Open("d1"); err != nil {
			t.Fatalf("an id only written to: %v", err)
		}
		if err := m.Open("a1"); err != nil {
			t.Fatalf("an id only acknowledged: %v", err)
		}
		if err := m.Open("c1"); err == nil || !strings.Contains(err.Error(), "closed before it opened") {
			t.Fatalf("an id closed before it opened: %v", err)
		}

		// Written to first, then closed: refused too.
		event(t, m, EventData, "c2")
		event(t, m, EventClose, "c2")
		if err := m.Open("c2"); err == nil {
			t.Fatal("an id written to, then closed, opened")
		}

		// After goneFor the peer is long past its wait: the id opens.
		event(t, m, EventClose, "c3")
		elapse(goneFor - time.Second)
		event(t, m, EventClose, "c4")
		elapse(time.Second)
		if err := m.Open("c3"); err != nil {
			t.Fatalf("an id closed goneFor ago: %v", err)
		}
		if err := m.Open("c4"); err == nil {
			t.Fatal("an id closed a second ago opened")
		}

		// A Mux whose connection ended remembers nothing and answers nothing.
		sent := len(log.all())
		m.CloseAll()
		event(t, m, EventData, "d2")
		synctest.Wait()
		m.mu.Lock()
		_, kept := m.gone["d2"]
		m.mu.Unlock()
		if kept || len(log.all()) != sent {
			t.Fatalf("a closed Mux took d2: kept %v, sent %q", kept, log.all())
		}
	})
}

// However many ids the peer closes, a Mux remembers at most maxGone: the oldest go first, and once their time is up
// all of them go together.
func TestStreamGoneStaysBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New((&sentLog{}).send)
		size := func() int {
			m.mu.Lock()
			defer m.mu.Unlock()
			return len(m.gone)
		}
		event(t, m, EventClose, "first")
		elapse(time.Second)
		last := ""
		for i := range maxGone + 100 {
			last = fmt.Sprintf("p%d", i)
			event(t, m, EventClose, last)
			if n := size(); n > maxGone {
				t.Fatalf("%d ids remembered, more than %d", n, maxGone)
			}
		}
		if n := size(); n != maxGone {
			t.Fatalf("%d ids remembered, want %d", n, maxGone)
		}
		if err := m.Open("first"); err != nil {
			t.Fatalf("the oldest id was kept over newer ones: %v", err)
		}
		if err := m.Open(last); err == nil {
			t.Fatal("the newest id was dropped")
		}
		event(t, m, EventClose, "refill") // full again: the next id makes room
		elapse(goneFor)
		event(t, m, EventClose, "late")
		if n := size(); n != 1 {
			t.Fatalf("%d ids remembered after goneFor, want only the new one", n)
		}
	})
}
