package devices

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/link"
)

func presenceMsg(active, notifies bool, ttlMS int64) link.Message {
	return link.Message{Type: "presence", Active: active, Notifies: notifies, TTLMS: ttlMS}
}

// sameDesktop: a and b say the same, whatever the clocks' readings (one went through JSON).
func sameDesktop(a, b devicewire.Desktop) bool {
	return a.MachineID == b.MachineID && a.Since.Equal(b.Since) && a.ActiveUntil.Equal(b.ActiveUntil) && a.Notifies == b.Notifies
}

// sentDesktops is how many devices.desktops d sent, and the last one.
func sentDesktops(d *Devices) (int, devicewire.Desktops) {
	f := d.host()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.desktops) == 0 {
		return 0, devicewire.Desktops{}
	}
	return len(f.desktops), f.desktops[len(f.desktops)-1]
}

// Active mode: a presence message makes the machine a desktop until its TTL runs out, every one renews it and is
// reported (the hub needs each new active_until), an inactive one ends it, and so does the link going away.
func TestLinkPresenceIsReported(t *testing.T) {
	d := newTestDevices(t)
	c, l := attentionLink(t, d, link.FeatureAttention)
	m := l.m
	c.next(t, "endpoints")
	desktop := func() *devicewire.Desktop { return m.desktop(time.Now()) }
	if desktop() != nil {
		t.Fatal("a desktop before its Persona Link said so")
	}

	before := time.Now()
	c.send(t, presenceMsg(true, true, 90_000))
	eventually(t, 5*time.Second, "the presence", func() bool { return desktop() != nil })
	first := desktop()
	if !first.Notifies || first.Since.Before(before) || first.ActiveUntil.Before(before.Add(90*time.Second)) ||
		first.ActiveUntil.After(time.Now().Add(90*time.Second)) {
		t.Fatalf("desktop %+v", first)
	}
	eventually(t, 5*time.Second, "the presence reported", func() bool {
		_, last := sentDesktops(d)
		return len(last.Desktops) == 1 && sameDesktop(last.Desktops[0], *first) && first.MachineID == "lap"
	})
	if m.desktop(first.ActiveUntil) != nil {
		t.Fatal("still a desktop once its TTL ran out")
	}

	c.send(t, presenceMsg(true, true, 120_000)) // the app's repeat
	eventually(t, 5*time.Second, "the renewed presence", func() bool {
		d := desktop()
		return d != nil && d.ActiveUntil.After(first.ActiveUntil)
	})
	if again := desktop(); !again.Since.Equal(first.Since) {
		t.Fatalf("a repeat moved since: %v → %v", first.Since, again.Since)
	}
	eventually(t, 5*time.Second, "the repeat reported (the hub would let it lapse)", func() bool {
		_, last := sentDesktops(d)
		return len(last.Desktops) == 1 && last.Desktops[0].ActiveUntil.After(first.ActiveUntil)
	})

	c.send(t, presenceMsg(false, true, 0))
	eventually(t, 5*time.Second, "the end of the presence", func() bool { return desktop() == nil })
	eventually(t, 5*time.Second, "its end reported", func() bool { _, last := sentDesktops(d); return len(last.Desktops) == 0 })

	c.send(t, presenceMsg(true, true, 0)) // no ttl_ms: the default
	eventually(t, 5*time.Second, "the presence again", func() bool { return desktop() != nil })
	if p := desktop(); p.Since.Equal(first.Since) || p.ActiveUntil.After(time.Now().Add(link.PresenceTTL)) {
		t.Fatalf("after an inactive one %+v", p)
	}
	_ = c.conn.CloseNow()
	eventually(t, 5*time.Second, "the link's end", func() bool { return desktop() == nil })
	// The next link starts with nothing: what the last one said ended with it.
	c, l = dialTestLinkHello(t, d, link.Message{Type: "hello", Machine: "lap", Version: "test", Address: testHubAddress,
		Features: []string{link.FeatureAttention}})
	c.next(t, "endpoints")
	if l.m != m || desktop() != nil {
		t.Fatalf("a new link took the last one's presence: %+v", desktop())
	}
}

// What a Persona Link may ask for is capped: no presence keeps phones quiet for long without being said again; and one
// that says its notifications do not show is kept as it is said (the hub does not count it).
func TestLinkPresenceTTLIsCapped(t *testing.T) {
	d := newTestDevices(t)
	c, l := attentionLink(t, d, link.FeatureAttention)
	m := l.m
	for _, ttl := range []int64{1 << 62, -5} {
		c.send(t, presenceMsg(false, false, 0))
		eventually(t, 5*time.Second, "no presence", func() bool { return m.desktop(time.Now()) == nil })
		c.send(t, presenceMsg(true, true, ttl))
		eventually(t, 5*time.Second, "the presence", func() bool { return m.desktop(time.Now()) != nil })
		limit := presenceTTLMax
		if ttl < 0 {
			limit = link.PresenceTTL
		}
		if until := m.desktop(time.Now()).ActiveUntil; until.After(time.Now().Add(limit)) {
			t.Fatalf("ttl_ms %d: until %v", ttl, until)
		}
	}
	c.send(t, presenceMsg(true, false, 60_000))
	eventually(t, 5*time.Second, "notifications off", func() bool { p := m.desktop(time.Now()); return p != nil && !p.Notifies })
}

// Only a computer that gets the summary as it is counts (attentionFor's rule): its Persona Link asked for it, it is not
// waiting to be confirmed, not a work computer. While the hub cannot say which computers wait (the summary freezes, so
// no new card reaches it) it does not count; a work computer is known all the same.
func TestPresenceCountsOnlyWhereTheSummaryGoes(t *testing.T) {
	d := newTestDevices(t)
	c, l := attentionLink(t, d) // no attention in its hello
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence taken", func() bool {
		l.m.mu.Lock()
		defer l.m.mu.Unlock()
		return l.m.presence != nil
	})
	if l.m.desktop(time.Now()) != nil {
		t.Fatal("a desktop whose Persona Link takes no summary")
	}

	d = newTestDevices(t)
	cfg := devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{linkEntry("Laptop")}, Pending: []string{"lap"}}
	c, l = attentionLinkWith(t, d, cfg, link.FeatureAttention)
	m := l.m
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence taken", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.presence != nil
	})
	if m.desktop(time.Now()) != nil {
		t.Fatal("a desktop waiting to be confirmed")
	}
	cfg.Pending = nil // confirmed
	d.Configure(cfg)
	if m.desktop(time.Now()) == nil {
		t.Fatal("not a desktop once confirmed")
	}
	cfg.PendingUnknown = true
	d.Configure(cfg)
	if m.desktop(time.Now()) != nil {
		t.Fatal("a desktop while the hub cannot say: its summary is frozen, a new card would reach neither")
	}
	cfg.PendingUnknown = false
	d.Configure(cfg)
	if m.desktop(time.Now()) == nil {
		t.Fatal("not a desktop once the hub can say again")
	}
	cfg.Pending = []string{"lap"}
	d.Configure(cfg)
	if m.desktop(time.Now()) != nil {
		t.Fatal("still a desktop once waiting to be confirmed again")
	}

	d = newTestDevices(t)
	work, _ := json.Marshal(MachineConfig{ID: "lap", Name: "Laptop", Kind: kindLink, Trust: "work"})
	c, l = attentionLinkWith(t, d, devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{work},
		PendingUnknown: true}, link.FeatureAttention)
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence taken", func() bool {
		l.m.mu.Lock()
		defer l.m.mu.Unlock()
		return l.m.presence != nil
	})
	if l.m.desktop(time.Now()) != nil {
		t.Fatal("a work computer counts while the hub cannot say")
	}
}

// Passive mode: each info read takes the presence the connector says (and reports it); one without it ends it, and so
// does a read that fails.
func TestConnectorPresence(t *testing.T) {
	ac := newAttConnector(t)
	ac.set(func(ac *attConnector) {
		ac.features = []string{link.FeatureAttention}
		ac.presence = &link.PresenceInfo{Active: true, Notifies: true, TTLMS: 100_000}
	})
	m := serviceMachineAt(t, ac.srv, ac.fp, "jc_test")
	probe(m)
	p := m.desktop(time.Now())
	if p == nil || !p.Notifies || p.MachineID != "example-service" || p.ActiveUntil.After(time.Now().Add(100*time.Second)) {
		t.Fatalf("desktop %+v", p)
	}
	n, last := sentDesktops(m.d)
	if len(last.Desktops) != 1 || !sameDesktop(last.Desktops[0], *p) {
		t.Fatalf("reported %+v", last)
	}
	probe(m)
	if again, _ := sentDesktops(m.d); again != n+1 {
		t.Fatal("a renewed presence was not reported")
	}

	ac.set(func(ac *attConnector) { ac.presence = nil })
	probe(m)
	if m.desktop(time.Now()) != nil {
		t.Fatal("still a desktop once the info says nothing")
	}
	n, last = sentDesktops(m.d)
	if len(last.Desktops) != 0 {
		t.Fatalf("its end was not reported: %+v", last)
	}
	probe(m)
	if again, _ := sentDesktops(m.d); again != n {
		t.Fatal("reported with no presence before or after")
	}

	ac.set(func(ac *attConnector) { ac.presence = &link.PresenceInfo{Active: true, Notifies: true, TTLMS: 100_000} })
	probe(m)
	if m.desktop(time.Now()) == nil {
		t.Fatal("no desktop")
	}
	ac.srv.Close()
	probe(m)
	if m.desktop(time.Now()) != nil {
		t.Fatal("still a desktop once its connector stopped answering")
	}
}

// After a handshake the hub may have lost what it had: the full report carries the desktops again, the same list
// included; an ordinary report carries them only when they changed.
func TestDesktopsGoOutOnHandshake(t *testing.T) {
	d := newTestDevices(t)
	c, l := attentionLink(t, d, link.FeatureAttention)
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence reported", func() bool {
		_, last := sentDesktops(d)
		return len(last.Desktops) == 1
	})
	n, _ := sentDesktops(d)
	d.report(false)
	if again, _ := sentDesktops(d); again != n {
		t.Fatalf("an ordinary report resent unchanged desktops (%d → %d)", n, again)
	}
	d.report(true)
	again, last := sentDesktops(d)
	if again != n+1 || len(last.Desktops) != 1 || last.Desktops[0].MachineID != l.m.cfg.ID {
		t.Fatalf("after a handshake: %d sent, last %+v", again-n, last)
	}
}

// A Persona Link entry that another hub.json entry absorbed (the same computer) is reported by that one's id: the hub
// knows the computer only by it.
func TestAbsorbedLinkPresenceUsesTheComputersID(t *testing.T) {
	d := newTestDevices(t)
	cfg := devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{linkEntryOf("box"), linkEntry("Laptop")}}
	c, l := attentionLinkWith(t, d, cfg, link.FeatureAttention)
	f := &d.fleet
	f.mu.Lock()
	f.init()
	f.absorbed[l.m] = d.cfgMachine("box")
	f.mu.Unlock()
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence reported", func() bool {
		_, last := sentDesktops(d)
		return len(last.Desktops) == 1 && last.Desktops[0].MachineID == "box"
	})
}
