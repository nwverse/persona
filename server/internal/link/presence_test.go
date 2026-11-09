package link

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestParsePresenceLine(t *testing.T) {
	for line, want := range map[string]Presence{
		`{"presence":{"active":true,"notifies":true}}`:  {Active: true, Notifies: true},
		`{"presence":{"active":false,"notifies":true}}`: {Notifies: true},
		`{"presence":{}}`: {},
	} {
		if got, ok := ParsePresenceLine([]byte(line)); !ok || got != want {
			t.Errorf("%s: %+v %v, want %+v", line, got, ok, want)
		}
	}
	for _, line := range []string{``, `{}`, `{"presence":null}`, `{"other":{"active":true}}`} {
		if p, ok := ParsePresenceLine([]byte(line)); ok {
			t.Errorf("%q read as presence %+v", line, p)
		}
	}
}

// What the manager is told: nothing before the app said anything, what is left of the TTL while an active line is
// fresh, inactive once it is PresenceTTL old (the app stopped saying it).
func TestPresenceBoxLatest(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	if _, ok := (*PresenceBox)(nil).Latest(t0); ok {
		t.Fatal("a nil box (no app) has a presence")
	}
	b := NewPresenceBox()
	if _, ok := b.Latest(t0); ok {
		t.Fatal("a presence before the app said one")
	}
	b.Set(Presence{Active: true, Notifies: true}, t0)
	for after, want := range map[time.Duration]PresenceInfo{
		0:                                {Active: true, Notifies: true, TTLMS: 120_000},
		30 * time.Second:                 {Active: true, Notifies: true, TTLMS: 90_000},
		PresenceTTL - time.Millisecond:   {Active: true, Notifies: true, TTLMS: 1},
		PresenceTTL - time.Millisecond/2: {Notifies: true},
		PresenceTTL:                      {Notifies: true},
		PresenceTTL + 10*time.Minute:     {Notifies: true},
	} {
		if got, ok := b.Latest(t0.Add(after)); !ok || got != want {
			t.Errorf("%s after: %+v, want %+v", after, got, want)
		}
	}
	b.Set(Presence{Notifies: true}, t0)
	if got, _ := b.Latest(t0.Add(time.Second)); got != (PresenceInfo{Notifies: true}) {
		t.Errorf("inactive: %+v", got)
	}
}

// Active mode: the latest presence goes as the link comes up, again for every line the app writes (the same one
// included: it renews the TTL), and again on the next link.
func TestLinkSendsPresence(t *testing.T) {
	type sent struct {
		link int32
		m    Message
	}
	box := NewPresenceBox()
	box.Set(Presence{Active: true, Notifies: true}, time.Now().Add(-time.Minute))
	got := make(chan sent, 64)
	var links atomic.Int32
	drop := make(chan struct{})
	srv, _ := fakeHubHello(t, func(c *websocket.Conn, _ Message) {
		n := links.Add(1)
		if n == 1 {
			go func() {
				<-drop
				c.CloseNow()
			}()
		}
		for {
			_, b, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m Message
			if json.Unmarshal(b, &m) == nil && m.Type == "presence" {
				got <- sent{n, m}
			}
		}
	})
	runAgainst(t, srv, "jd_x", Options{Presence: box})
	wait := func(what string, link int32, ok func(Message) bool) {
		t.Helper()
		timeout := time.After(10 * time.Second)
		for {
			select {
			case s := <-got:
				if s.link == link && ok(s.m) {
					return
				}
			case <-timeout:
				t.Fatalf("no presence on link %d: %s", link, what)
			}
		}
	}
	// Said a minute ago: half the TTL is left.
	wait("as the link comes up", 1, func(m Message) bool {
		return m.Active && m.Notifies && m.TTLMS > 0 && m.TTLMS <= PresenceTTL.Milliseconds()-time.Minute.Milliseconds()
	})
	box.Set(Presence{Active: true, Notifies: true}, time.Now())
	wait("the same line again, with the whole TTL", 1, func(m Message) bool {
		return m.Active && m.TTLMS > PresenceTTL.Milliseconds()-time.Minute.Milliseconds()
	})
	box.Set(Presence{Notifies: true}, time.Now())
	wait("a change", 1, func(m Message) bool { return !m.Active && m.Notifies && m.TTLMS == 0 })
	close(drop)
	wait("on the next link", 2, func(m Message) bool { return !m.Active && m.Notifies })
}

// Passive mode: the info answer carries the app's latest presence once it said one.
func TestPassiveInfoCarriesPresence(t *testing.T) {
	box := NewPresenceBox()
	c := startConnector(t, newFakeNode(t).path, Options{Presence: box})
	fp, code := c.invite(t)
	h := hubFor(t, c.addr, fp)
	if r := h.pair(code, tokenA); r.status != http.StatusOK {
		t.Fatalf("pair: %+v", r)
	}
	if info := h.infoOf(tokenA); info.Presence != nil {
		t.Fatalf("presence before the app said any: %+v", info.Presence)
	}
	box.Set(Presence{Active: true, Notifies: true}, time.Now())
	info := h.infoOf(tokenA)
	if p := info.Presence; p == nil || !p.Active || !p.Notifies || p.TTLMS <= 0 || p.TTLMS > PresenceTTL.Milliseconds() {
		t.Fatalf("info %+v", info.Presence)
	}
}
