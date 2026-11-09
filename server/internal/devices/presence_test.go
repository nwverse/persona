package devices

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/link"
)

func presenceMsg(active, notifies bool, ttlMS int64) link.Message {
	return link.Message{Type: "presence", Active: active, Notifies: notifies, TTLMS: ttlMS}
}

// notifying is a summary listing each card as its own agent's, notifying; more: agents past the cap.
func notifying(more int, cards ...string) link.Attention {
	a := link.Attention{NeedsYou: len(cards) + more, More: more, Items: []link.AttentionItem{}}
	for i, ix := range cards {
		a.Items = append(a.Items, link.AttentionItem{MachineID: "lap", SessionID: fmt.Sprintf("s-%d", i), Kind: "permission",
			Notify: true, InteractionID: ix, Route: fmt.Sprintf("/s/lap/s-%d", i)})
	}
	return a
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

// heldLink is a link machine's Persona Link connected to d with the hub's handover cfg, asking for the summary, and the
// summary it took (d has one before it connects, so it holds it).
func heldLink(t *testing.T, d *Devices, cfg devicewire.Config) (*testLinkClient, *mgrLink) {
	t.Helper()
	if d.attention.Load() == nil {
		d.SetAttention(notifying(0))
	}
	c, l := attentionLinkWith(t, d, cfg, link.FeatureAttention)
	c.next(t, "attention")
	return c, l
}

func laptopCfg() devicewire.Config {
	return devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{linkEntry("Laptop")}}
}

// isDesktop: m is a desktop now.
func isDesktop(m *Machine) bool { return m.desktop(time.Now()) != nil }

// hasPresence: what m's Persona Link said was taken.
func hasPresence(m *Machine) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.presence != nil
}

// Active mode: a presence message makes the machine a desktop until its TTL runs out, every one renews it and is
// reported (the hub needs each renewal), an inactive one ends it, and so does the link going away.
func TestLinkPresenceIsReported(t *testing.T) {
	d := newTestDevices(t)
	c, l := heldLink(t, d, laptopCfg())
	m := l.m
	if isDesktop(m) {
		t.Fatal("a desktop before its Persona Link said so")
	}

	before := time.Now()
	c.send(t, presenceMsg(true, true, 90_000))
	eventually(t, 5*time.Second, "the presence", func() bool { return isDesktop(m) })
	first := m.desktop(time.Now())
	if !first.Notifies || first.Since.Before(before) || first.until.Before(before.Add(90*time.Second)) ||
		first.until.After(time.Now().Add(90*time.Second)) || first.TTLMS > 90_000 || first.TTLMS < 80_000 {
		t.Fatalf("desktop %+v", first)
	}
	eventually(t, 5*time.Second, "the presence reported", func() bool {
		_, last := sentDesktops(d)
		return len(last.Desktops) == 1 && last.Desktops[0].MachineID == "lap" && last.Desktops[0].Since.Equal(first.Since) &&
			last.Desktops[0].TTLMS > 80_000
	})
	if m.desktop(first.until) != nil {
		t.Fatal("still a desktop once its TTL ran out")
	}
	// What is said is the TTL left: a report later (a handshake) does not fill it up again.
	if later := m.desktop(time.Now().Add(30 * time.Second)); later.TTLMS > 60_000 {
		t.Fatalf("30 s later the TTL left is %d ms", later.TTLMS)
	}

	n, _ := sentDesktops(d)
	c.send(t, presenceMsg(true, true, 120_000)) // the app's repeat
	eventually(t, 5*time.Second, "the renewed presence", func() bool {
		p := m.desktop(time.Now())
		return p != nil && p.until.After(first.until)
	})
	if again := m.desktop(time.Now()); !again.Since.Equal(first.Since) {
		t.Fatalf("a repeat moved since: %v → %v", first.Since, again.Since)
	}
	eventually(t, 5*time.Second, "the repeat reported (the hub would let it lapse)", func() bool {
		k, last := sentDesktops(d)
		return k > n && len(last.Desktops) == 1 && last.Desktops[0].TTLMS > 110_000
	})

	c.send(t, presenceMsg(false, true, 0))
	eventually(t, 5*time.Second, "the end of the presence", func() bool { return !isDesktop(m) })
	eventually(t, 5*time.Second, "its end reported", func() bool { _, last := sentDesktops(d); return len(last.Desktops) == 0 })

	c.send(t, presenceMsg(true, true, 0)) // no ttl_ms: the default
	eventually(t, 5*time.Second, "the presence again", func() bool { return isDesktop(m) })
	if p := m.desktop(time.Now()); p.Since.Equal(first.Since) || p.until.After(time.Now().Add(link.PresenceTTL)) {
		t.Fatalf("after an inactive one %+v", p)
	}
	_ = c.conn.CloseNow()
	eventually(t, 5*time.Second, "the link's end", func() bool { return !isDesktop(m) })
	// The next link starts with nothing: what the last one said ended with it.
	c, l = dialTestLinkHello(t, d, link.Message{Type: "hello", Machine: "lap", Version: "test", Address: testHubAddress,
		Features: []string{link.FeatureAttention}})
	c.next(t, "attention")
	if l.m != m || isDesktop(m) {
		t.Fatalf("a new link took the last one's presence: %+v", m.desktop(time.Now()))
	}
}

// A new link that takes over while the old one is still open (a command-line Link, an app restarting) starts with no
// presence: the old link's is not the new one's.
func TestNewLinkDropsTheOldLinksPresence(t *testing.T) {
	d := newTestDevices(t)
	c, l := heldLink(t, d, laptopCfg())
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence", func() bool { return isDesktop(l.m) })
	c2, _ := dialTestLinkHello(t, d, link.Message{Type: "hello", Machine: "lap", Version: "test", Address: testHubAddress,
		Features: []string{link.FeatureAttention}})
	c2.next(t, "attention") // sent once the new link is the machine's
	if l2 := l.m.currentLink(); l2 == nil || l2 == l {
		t.Fatal("the new link did not take over")
	}
	if hasPresence(l.m) || isDesktop(l.m) {
		t.Fatal("the new link took the old link's presence")
	}
}

// What a Persona Link may ask for is capped: no presence keeps phones quiet for long without being said again; and one
// that says its notifications do not show is kept as it is said (the hub does not count it).
func TestLinkPresenceTTLIsCapped(t *testing.T) {
	d := newTestDevices(t)
	c, l := heldLink(t, d, laptopCfg())
	m := l.m
	for _, ttl := range []int64{1 << 62, -5} {
		c.send(t, presenceMsg(false, false, 0))
		eventually(t, 5*time.Second, "no presence", func() bool { return !isDesktop(m) })
		c.send(t, presenceMsg(true, true, ttl))
		eventually(t, 5*time.Second, "the presence", func() bool { return isDesktop(m) })
		limit := devicewire.DesktopTTLMax
		if ttl < 0 {
			limit = link.PresenceTTL
		}
		if p := m.desktop(time.Now()); p.until.After(time.Now().Add(limit)) || p.TTLMS > limit.Milliseconds() {
			t.Fatalf("ttl_ms %d: %+v until %v", ttl, p.Desktop, p.until)
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
	d.SetAttention(notifying(0))
	c, l := attentionLink(t, d) // no attention in its hello
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence taken", func() bool { return hasPresence(l.m) })
	if isDesktop(l.m) {
		t.Fatal("a desktop whose Persona Link takes no summary")
	}

	d = newTestDevices(t)
	cfg := laptopCfg()
	cfg.Pending = []string{"lap"}
	c, l = heldLink(t, d, cfg)
	m := l.m
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence taken", func() bool { return hasPresence(m) })
	if isDesktop(m) {
		t.Fatal("a desktop waiting to be confirmed")
	}
	cfg.Pending = nil // confirmed: the summary as it is goes out
	d.Configure(cfg)
	c.next(t, "attention")
	eventually(t, 5*time.Second, "a desktop once confirmed", func() bool { return isDesktop(m) })
	cfg.PendingUnknown = true
	d.Configure(cfg)
	if isDesktop(m) {
		t.Fatal("a desktop while the hub cannot say: its summary is frozen, a new card would reach neither")
	}
	cfg.PendingUnknown = false
	d.Configure(cfg)
	if !isDesktop(m) {
		t.Fatal("not a desktop once the hub can say again")
	}
	cfg.Pending = []string{"lap"}
	d.Configure(cfg)
	if isDesktop(m) {
		t.Fatal("still a desktop once waiting to be confirmed again")
	}

	d = newTestDevices(t)
	work, _ := json.Marshal(MachineConfig{ID: "lap", Name: "Laptop", Kind: kindLink, Trust: "work"})
	c, l = heldLink(t, d, devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{work}, PendingUnknown: true})
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence taken", func() bool { return hasPresence(l.m) })
	if isDesktop(l.m) {
		t.Fatal("a work computer counts while the hub cannot say")
	}
}

// A Persona Link desktop must hold the summary as it is now: none yet, no desktop; once it took it, a desktop.
func TestLinkDesktopHoldsTheSummary(t *testing.T) {
	d := newTestDevices(t)
	c, l := attentionLinkWith(t, d, laptopCfg(), link.FeatureAttention) // the manager has no summary yet
	c.next(t, "endpoints")
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the presence taken", func() bool { return hasPresence(l.m) })
	if isDesktop(l.m) {
		t.Fatal("a desktop that holds no summary")
	}
	d.SetAttention(notifying(0, "ix-1"))
	c.next(t, "attention")
	eventually(t, 5*time.Second, "a desktop once it holds it", func() bool { return isDesktop(l.m) })
}

// What a computer holds: nothing until it took one; the first summary taken is the baseline (it rings for nothing); a
// later one rings for the cards it lists as notifying for the first time; a newer summary not delivered yet still
// counts once one was taken; a failure ends it until one is taken again. A summary that is not capped forgets the cards it leaves out, as Persona Link forgets
// their sessions: one listed again notifies again, the baseline's too; a capped one keeps them.
func TestSummaryHeld(t *testing.T) {
	var h summaryHeld
	a1, a2, a3 := notifying(0, "ix-1"), notifying(0, "ix-1", "ix-2"), notifying(0, "ix-3")
	a1.Rev, a2.Rev, a3.Rev = "r1", "r2", "r3"
	if h.holds("r1") {
		t.Fatal("held before any summary was taken")
	}
	h.done(&a1, true)
	if !h.holds("r1") || h.holds("") || h.rang["ix-1"] {
		t.Fatalf("after the baseline: %+v", h)
	}
	if !h.holds("r2") {
		t.Fatal("a newer summary on its way after the baseline does not count")
	}
	h.done(&a2, true)
	if !h.rang["ix-2"] || h.rang["ix-1"] {
		t.Fatalf("rang %v: ix-2 rang, ix-1 was notifying in the baseline already", h.rang)
	}
	h.done(&a3, false)
	if h.holds("r3") || h.holds("r2") || h.rang["ix-3"] {
		t.Fatalf("after a failed delivery: %+v", h)
	}
	h.done(&a3, true)
	if !h.holds("r3") || !h.rang["ix-3"] {
		t.Fatalf("after it was taken: %+v", h)
	}
	if _, kept := h.rang["ix-1"]; kept {
		t.Fatalf("rang %v: ix-1 left a summary that is not capped, yet it is remembered", h.rang)
	}
	h.done(&a2, true)
	if !h.rang["ix-1"] {
		t.Fatalf("rang %v: ix-1 listed again as notifying, Persona Link rings for it again", h.rang)
	}
	capped := notifying(1, "ix-9")
	capped.Rev = "r9"
	h.done(&capped, true)
	if !h.rang["ix-1"] || !h.rang["ix-2"] {
		t.Fatalf("rang %v: a capped summary forgot the cards past its cap", h.rang)
	}
}

// Passive mode: each info read takes the presence the connector says (and reports it); one without it ends it, and so
// does a read that fails. The connector must hold the summary as it is: a post that fails (500), that is refused (429)
// or that times out ends it until a post is taken again; an info read that goes through does not bring it back.
func TestConnectorPresence(t *testing.T) {
	ac := newAttConnector(t)
	ac.set(func(ac *attConnector) {
		ac.features = []string{link.FeatureAttention}
		ac.presence = &link.PresenceInfo{Active: true, Notifies: true, TTLMS: 100_000}
	})
	m := serviceMachineAt(t, ac.srv, ac.fp, "jc_test")
	a := notifying(0, "ix-1")
	m.d.attention.Store(&a)
	probe(m) // info, then the summary posted
	if !isDesktop(m) {
		probe(m) // the post came after the info read's report: the next read reports it
	}
	p := m.desktop(time.Now())
	if p == nil || !p.Notifies || p.MachineID != "example-service" || p.TTLMS > 100_000 {
		t.Fatalf("desktop %+v", p)
	}
	eventually(t, 5*time.Second, "the presence reported", func() bool {
		_, last := sentDesktops(m.d)
		return len(last.Desktops) == 1 && last.Desktops[0].MachineID == "example-service"
	})
	n, _ := sentDesktops(m.d)
	probe(m)
	if again, _ := sentDesktops(m.d); again <= n {
		t.Fatal("a renewed presence was not reported")
	}

	ac.set(func(ac *attConnector) { ac.fail = 1 }) // GET 200, POST 500
	b := notifying(0, "ix-1", "ix-2")
	m.d.SetAttention(b)
	probe(m)
	if isDesktop(m) {
		t.Fatal("a desktop whose connector did not take the summary (500)")
	}
	eventually(t, 5*time.Second, "the hub told it is no desktop", func() bool {
		_, last := sentDesktops(m.d)
		return len(last.Desktops) == 0
	})
	m.probeService(context.Background()) // GET 200 alone, no post
	if isDesktop(m) {
		t.Fatal("an info read alone made it a desktop again")
	}
	probe(m) // the post is taken
	if !isDesktop(m) {
		t.Fatal("not a desktop once a post was taken")
	}
	if !m.rang("ix-2") || m.rang("ix-1") {
		t.Fatal("ix-2 was rung for, ix-1 was in the baseline")
	}

	ac.set(func(ac *attConnector) { ac.refuse = 429 })
	m.d.SetAttention(notifying(0, "ix-3"))
	probe(m)
	if isDesktop(m) {
		t.Fatal("a desktop whose connector refused the summary (429)")
	}

	ac.set(func(ac *attConnector) { ac.presence = nil; ac.refuse = 0 })
	m2 := serviceMachineAt(t, ac.srv, ac.fp, "jc_test")
	ac.set(func(ac *attConnector) { ac.presence = &link.PresenceInfo{Active: true, Notifies: true, TTLMS: 100_000} })
	m2.d.attention.Store(&a)
	probe(m2)
	probe(m2)
	if !isDesktop(m2) {
		t.Fatal("no desktop")
	}
	ac.set(func(ac *attConnector) { ac.stall = true })
	m2.d.SetAttention(b)
	m2.probeService(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	m2.postAttention(ctx) // times out
	cancel()
	if isDesktop(m2) {
		t.Fatal("a desktop whose connector did not answer the post in time")
	}
	ac.set(func(ac *attConnector) { ac.stall = false; ac.held = m2.d.attentionFor(m2.cfg.ID, "").Rev }) // it took it after all
	m2.probeService(context.Background())
	if !isDesktop(m2) {
		t.Fatal("not a desktop once its info says it holds the summary as it is")
	}

	ac.set(func(ac *attConnector) { ac.presence = nil })
	probe(m2)
	if isDesktop(m2) {
		t.Fatal("still a desktop once the info says nothing")
	}
	ac.set(func(ac *attConnector) { ac.presence = &link.PresenceInfo{Active: true, Notifies: true, TTLMS: 100_000} })
	probe(m2)
	probe(m2)
	if !isDesktop(m2) {
		t.Fatal("no desktop again")
	}
	ac.srv.Close()
	probe(m2)
	if isDesktop(m2) {
		t.Fatal("still a desktop once its connector stopped answering")
	}
}

// After a handshake the hub may have lost what it had: the full report carries the machines first, then the desktops
// again, the same list included; an ordinary report carries them only when they changed.
func TestDesktopsGoOutOnHandshake(t *testing.T) {
	d := newTestDevices(t)
	c, l := heldLink(t, d, laptopCfg())
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
	f := d.host()
	f.mu.Lock()
	from := len(f.notified)
	f.mu.Unlock()
	d.report(true)
	again, last := sentDesktops(d)
	if again != n+1 || len(last.Desktops) != 1 || last.Desktops[0].MachineID != l.m.cfg.ID {
		t.Fatalf("after a handshake: %d sent, last %+v", again-n, last)
	}
	f.mu.Lock()
	order := slices.Clone(f.notified[from:])
	f.mu.Unlock()
	if i, j := slices.Index(order, devicewire.EventState), slices.Index(order, devicewire.EventDesktops); i < 0 || j < i {
		t.Fatalf("a handshake's events %v: the machines must come before the desktops", order)
	}
}

// A Persona Link entry that another hub.json entry absorbed (the same computer) is reported by that one's id: the hub
// knows the computer only by it.
func TestAbsorbedLinkPresenceUsesTheComputersID(t *testing.T) {
	d := newTestDevices(t)
	cfg := devicewire.Config{HubID: "hub-test", HubName: "test", Machines: []json.RawMessage{linkEntryOf("box"), linkEntry("Laptop")}}
	c, l := heldLink(t, d, cfg)
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

// LinkRang: false at once without a desktop; a card in the baseline, or listed only past the cap, rang for nothing; a
// card a later summary the desktop took lists as notifying rang, also when that summary comes while LinkRang waits.
func TestLinkRang(t *testing.T) {
	d := newTestDevices(t)
	start := time.Now()
	if d.LinkRang("ix-1", 5*time.Second) || time.Since(start) > time.Second {
		t.Fatal("rang (or waited) with no desktop")
	}
	d.SetAttention(notifying(0, "ix-base"))
	c, l := heldLink(t, d, laptopCfg())
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the desktop", func() bool { return isDesktop(l.m) })
	if d.LinkRang("ix-base", 100*time.Millisecond) {
		t.Fatal("a card of the baseline rang")
	}
	d.SetAttention(notifying(1, "ix-new")) // ix-old is past the cap: in More only
	c.next(t, "attention")
	if !d.LinkRang("ix-new", 5*time.Second) {
		t.Fatal("a card a later summary listed as notifying did not ring")
	}
	if d.LinkRang("ix-old", 100*time.Millisecond) {
		t.Fatal("a card in More rang")
	}
	got := make(chan bool, 1)
	go func() { got <- d.LinkRang("ix-late", 5*time.Second) }()
	time.Sleep(50 * time.Millisecond)
	d.SetAttention(notifying(0, "ix-new", "ix-late"))
	if !<-got {
		t.Fatal("a card that rang while LinkRang waited")
	}
}

// LinkRang waits for a desktop that stops counting while it waits (another delivery ends before the newer summary
// went out to it) instead of taking it as gone; a desktop whose notifications do not show rings for nothing.
func TestLinkRangWaitsForTheNewerSummary(t *testing.T) {
	d := newTestDevices(t)
	d.SetAttention(notifying(0))
	c, l := heldLink(t, d, laptopCfg())
	c.send(t, presenceMsg(true, true, 60_000))
	eventually(t, 5*time.Second, "the desktop", func() bool { return isDesktop(l.m) })
	got := make(chan bool, 1)
	go func() { got <- d.LinkRang("ix-1", 5*time.Second) }()
	time.Sleep(50 * time.Millisecond) // LinkRang found the desktop
	a := notifying(0, "ix-1")
	d.attention.Store(&a) // the newer summary, not on its way yet: the desktop does not count meanwhile
	d.delivered()         // another computer's delivery ends
	time.Sleep(50 * time.Millisecond)
	d.spreadAttention()
	c.next(t, "attention")
	if !<-got {
		t.Fatal("a desktop that did not count for a moment was taken as gone")
	}

	c.send(t, presenceMsg(true, false, 60_000))
	eventually(t, 5*time.Second, "notifications that do not show", func() bool {
		p := l.m.desktop(time.Now())
		return p != nil && !p.Notifies
	})
	d.SetAttention(notifying(0, "ix-1", "ix-2"))
	c.next(t, "attention")
	start := time.Now()
	if d.LinkRang("ix-2", 5*time.Second) || time.Since(start) > time.Second {
		t.Fatal("a desktop whose notifications do not show rang (or was waited for)")
	}
}

// A connector that says it holds no summary (it restarted, its summary went stale) takes the next one as Persona Link's
// baseline again: the cards in it rang for nothing.
func TestConnectorRestartTakesABaseline(t *testing.T) {
	ac := newAttConnector(t)
	ac.set(func(ac *attConnector) {
		ac.features = []string{link.FeatureAttention}
		ac.presence = &link.PresenceInfo{Active: true, Notifies: true, TTLMS: 100_000}
	})
	m := serviceMachineAt(t, ac.srv, ac.fp, "jc_test")
	a := notifying(0)
	m.d.attention.Store(&a)
	probe(m) // the baseline
	m.d.SetAttention(notifying(0, "ix-1"))
	probe(m)
	if !m.rang("ix-1") {
		t.Fatal("a card after the baseline did not ring")
	}
	ac.set(func(ac *attConnector) { ac.held = "" }) // it restarted
	m.d.SetAttention(notifying(0, "ix-1", "ix-2"))
	probe(m)
	if m.rang("ix-2") {
		t.Fatal("a card in the restarted connector's first summary rang")
	}
}

// A post the answer of which names another run of the connector (it restarted between two info reads) went to its menu
// bar app as a baseline: the cards in it rang for nothing. Another run named by an info read is the same.
func TestConnectorNewRunTakesABaseline(t *testing.T) {
	ac := newAttConnector(t)
	ac.set(func(ac *attConnector) {
		ac.features, ac.instance = []string{link.FeatureAttention}, "run-1"
		ac.presence = &link.PresenceInfo{Active: true, Notifies: true, TTLMS: 100_000}
	})
	m := serviceMachineAt(t, ac.srv, ac.fp, "jc_test")
	a := notifying(0)
	m.d.attention.Store(&a)
	probe(m) // the baseline
	m.d.SetAttention(notifying(0, "ix-1"))
	sendWoken(context.Background(), m)
	if !m.rang("ix-1") {
		t.Fatal("a card after the baseline did not ring")
	}
	ac.set(func(ac *attConnector) { ac.instance = "run-2" }) // it restarted (and still says the old rev, as it may)
	m.d.SetAttention(notifying(0, "ix-1", "ix-2"))
	sendWoken(context.Background(), m)
	m.postAttention(context.Background())
	if m.rang("ix-2") {
		t.Fatal("a card in the new run's first summary rang")
	}
	// A new run holds no summary yet; the info read names it, and the summary posted then is its baseline.
	ac.set(func(ac *attConnector) { ac.instance, ac.held = "run-3", "" })
	probe(m)
	if m.rang("ix-1") || m.rang("ix-2") {
		t.Fatal("a card of the new run's baseline counts as rung")
	}
	m.d.SetAttention(notifying(0, "ix-1", "ix-2", "ix-3"))
	sendWoken(context.Background(), m)
	m.postAttention(context.Background())
	if !m.rang("ix-3") {
		t.Fatal("a card after the new run's baseline did not ring")
	}
	// Another run that took a summary whose answer never came: the info read names it, and what it holds was its
	// baseline: nothing this manager saw rung counts for it.
	ac.set(func(ac *attConnector) { ac.instance = "run-4" })
	m.probeService(context.Background())
	if m.rang("ix-3") {
		t.Fatal("a card rung on the earlier run counts for the new one")
	}
}
