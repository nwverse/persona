package hub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// reportLinkDesktop is the manager's devices.state with machine "lap" (named Laptop), then its devices.desktops with
// that computer's presence (nil: none), through the bus handler as the manager's message comes.
func reportLinkDesktop(t *testing.T, h *Hub, desktop *devicewire.Desktop, derived bool) {
	t.Helper()
	h.applyDeviceStates(devicewire.States{Machines: []devicewire.State{{
		Machine: proto.Machine{ID: "lap", Name: "Laptop", Kind: kindLink}, Derived: derived}}})
	list := devicewire.Desktops{Desktops: []devicewire.Desktop{}}
	if desktop != nil {
		list.Desktops = append(list.Desktops, *desktop)
	}
	raw, _ := json.Marshal(list)
	busEvents[devicewire.EventDesktops](h, &busConn{ident: db.BusIdentity{ID: devicesOwner}}, raw)
}

func activeUntil(since time.Time, d time.Duration) *devicewire.Desktop {
	return &devicewire.Desktop{MachineID: "lap", Since: since, ActiveUntil: time.Now().Add(d), Notifies: true}
}

// noEventOfType fails when c has an event of type typ queued.
func noEventOfType(t *testing.T, c *client, typ string) {
	t.Helper()
	for {
		select {
		case b := <-c.out:
			var m map[string]any
			if json.Unmarshal(b, &m) == nil && m["type"] == typ {
				t.Fatalf("unexpected %s event: %s", typ, b)
			}
		default:
			return
		}
	}
}

func linkEntries(ev map[string]any) []map[string]any {
	list, _ := ev["desktops"].([]any)
	var out []map[string]any
	for _, d := range list {
		out = append(out, d.(map[string]any))
	}
	return out
}

// A computer whose Persona Link says the user is at it is listed, with the wire's fields, to the clients that asked
// for desktops_link only; a later active_until alone says nothing new; notifications that do not show, or a mesh
// member's computer, do not count; and it goes when its manager no longer reports it.
func TestLinkDesktopListedToClientsThatAsk(t *testing.T) {
	h := newTestHub(t)
	old := &client{out: make(chan []byte, 16), subs: map[[2]string]bool{}, role: roleFull}
	asks := &client{out: make(chan []byte, 16), subs: map[[2]string]bool{}, role: roleFull,
		urlFeatures: map[string]bool{featureDesktopsLink: true}}
	h.mu.Lock()
	h.clients[old], h.clients[asks] = struct{}{}, struct{}{}
	h.mu.Unlock()

	since := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	reportLinkDesktop(t, h, activeUntil(since, time.Minute), false)
	got := linkEntries(nextOfType(t, asks, "desktops"))
	want := map[string]any{"kind": "link", "machine_id": "lap", "device": "", "name": "Laptop", "since": "2026-11-09T12:00:00Z"}
	if len(got) != 1 || len(got[0]) != len(want) {
		t.Fatalf("desktops %v", got)
	}
	for k, v := range want {
		if got[0][k] != v {
			t.Fatalf("desktops %v: %s is %v, want %v", got, k, got[0][k], v)
		}
	}
	noEventOfType(t, old, "desktops")
	if !h.linkDesktopOnline(devicesOwner) || h.desktopOnline() {
		t.Fatal("a Persona Link desktop is not the desktop app")
	}
	if l := h.desktops(false); len(l) != 0 {
		t.Fatalf("the list older clients get %v", l)
	}

	reportLinkDesktop(t, h, activeUntil(since, 2*time.Minute), false) // the app's repeat
	noEventOfType(t, asks, "desktops")

	reportLinkDesktop(t, h, &devicewire.Desktop{MachineID: "lap", Since: since, ActiveUntil: time.Now().Add(time.Minute)}, false)
	if got := linkEntries(nextOfType(t, asks, "desktops")); len(got) != 0 || h.linkDesktopOnline(devicesOwner) {
		t.Fatalf("notifications that do not show: %v", got)
	}
	// lap is this hub's own computer, not derived, here: only its manager may say it is a desktop.
	raw, _ := json.Marshal(devicewire.Desktops{Desktops: []devicewire.Desktop{*activeUntil(since, time.Minute)}})
	busEvents[devicewire.EventDesktops](h, &busConn{ident: db.BusIdentity{ID: "m-other"}}, raw)
	noEventOfType(t, asks, "desktops")
	if h.linkDesktopOnline(devicesOwner) {
		t.Fatal("another manager's word made this one's computer a desktop")
	}

	reportLinkDesktop(t, h, activeUntil(since, time.Minute), true)
	noEventOfType(t, asks, "desktops")
	if h.linkDesktopOnline(devicesOwner) {
		t.Fatal("a mesh member's computer counts")
	}

	reportLinkDesktop(t, h, activeUntil(since, time.Minute), false)
	nextOfType(t, asks, "desktops")
	reportLinkDesktop(t, h, nil, false)
	if got := linkEntries(nextOfType(t, asks, "desktops")); len(got) != 0 || h.linkDesktopOnline(devicesOwner) {
		t.Fatalf("after the manager stopped reporting it: %v", got)
	}
	noEventOfType(t, old, "desktops")
}

// A Persona Link desktop lapses at its active_until even when no manager says anything more: the mute ends when it is
// asked, and the clients hear it.
func TestLinkDesktopLapses(t *testing.T) {
	h := newTestHub(t)
	asks := &client{out: make(chan []byte, 16), subs: map[[2]string]bool{}, role: roleFull,
		urlFeatures: map[string]bool{featureDesktopsLink: true}}
	h.mu.Lock()
	h.clients[asks] = struct{}{}
	h.mu.Unlock()
	reportLinkDesktop(t, h, activeUntil(time.Now(), 300*time.Millisecond), false)
	if got := linkEntries(nextOfType(t, asks, "desktops")); len(got) != 1 || !h.linkDesktopOnline(devicesOwner) {
		t.Fatalf("desktops %v", got)
	}
	for {
		select {
		case b := <-asks.out:
			var m map[string]any
			if json.Unmarshal(b, &m) == nil && m["type"] == "desktops" {
				if got := linkEntries(m); len(got) != 0 {
					t.Fatalf("after it lapsed: %v", got)
				}
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the clients never heard it lapsed")
		}
	}
}

// Whether a Persona Link desktop mutes is read when an alert goes out, not left to the timer: one past its active_until
// does not count even before the timer took it out.
func TestLinkDesktopCountsOnlyUntilItsTime(t *testing.T) {
	h := newTestHub(t)
	h.mu.Lock()
	h.linkDesktops.list = []linkDesktop{{owner: devicesOwner, machine: "lap", name: "Laptop", since: time.Now().Add(-time.Minute),
		until: time.Now().Add(-time.Second)}}
	h.mu.Unlock()
	if h.linkDesktopOnline(devicesOwner) {
		t.Fatal("a lapsed Persona Link desktop mutes the phone")
	}
	if l := h.desktops(true); len(l) != 0 {
		t.Fatalf("a lapsed Persona Link desktop is listed: %v", l)
	}
}

// A client that asks for desktops_link in its client_hello (after the list it got on connecting) gets the list again,
// with the Persona Link desktops.
func TestClientHelloAskingForLinkDesktopsGetsThem(t *testing.T) {
	h := newTestHub(t)
	reportLinkDesktop(t, h, activeUntil(time.Now(), time.Minute), false)
	c := &client{out: make(chan []byte, 16), subs: map[[2]string]bool{}, role: roleFull}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	h.sendDesktops(c)
	if got := linkEntries(nextOfType(t, c, "desktops")); len(got) != 0 {
		t.Fatalf("before asking %v", got)
	}
	h.onClientMessage(context.Background(), c, []byte(`{"type":"client_hello","features":["desktops_link"]}`))
	if got := linkEntries(nextOfType(t, c, "desktops")); len(got) != 1 || got[0]["kind"] != "link" {
		t.Fatalf("after asking %v", got)
	}
	h.onClientMessage(context.Background(), c, []byte(`{"type":"client_hello","features":["desktops_link"]}`))
	noEventOfType(t, c, "desktops")
	// Asking no more: the list it has holds a Persona Link desktop it would not hear end.
	h.onClientMessage(context.Background(), c, []byte(`{"type":"client_hello","features":[]}`))
	if got := linkEntries(nextOfType(t, c, "desktops")); len(got) != 0 {
		t.Fatalf("after no longer asking %v", got)
	}
}

// "Phone Muted" with only a Persona Link desktop holds back the "Needs you" cards on the WebSocket, the one alert
// Persona Link shows; finished turns, reply reminders and other alerts still come.
func TestWSNotifyMuteWhileLinkDesktop(t *testing.T) {
	h := newTestHub(t)
	phone := &client{device: "test-device", out: make(chan []byte, 16)}
	h.mu.Lock()
	h.clients[phone] = struct{}{}
	h.mu.Unlock()
	h.onClientMessage(context.Background(), phone, []byte(`{"type":"client_hello","features":["ws_notify"]}`))
	if err := h.push.setPrefs(phone.device, NotifyPrefs{Input: true, Done: DoneAlways, MuteWhileDesktop: true}); err != nil {
		t.Fatal(err)
	}
	kinds := func() []string {
		var out []string
		for {
			select {
			case raw := <-phone.out:
				var m map[string]any
				_ = json.Unmarshal(raw, &m)
				if m["type"] == "notify" {
					out = append(out, m["kind"].(string))
				}
			default:
				return out
			}
		}
	}
	ix := proto.Interaction{ID: "ix1", MachineID: "m", SessionID: "s", Kind: proto.IxQuestion, Summary: "Pick one"}
	everything := func() {
		h.notifyInteraction(assistantwire.AssistantID, ix, "box")
		h.notifyDone(assistantwire.AssistantID, "done", "m", "box", "s", "Done", "", pushWants{Kind: "done", Took: time.Hour, Long: time.Minute})
		h.notifyReply(assistantwire.AssistantID, "m", "box", "s", "Still waiting", "")
		alert := notifyPayload(notifyRequest{Title: "t", Level: levelActive}, "", "")
		h.notifyWSFrom("", kindAlert, func(i18n.Lang) map[string]any { return alert }, pushWants{Kind: "input"}.fn(), quietNone)
	}
	kinds()
	everything()
	all := kinds()
	if len(all) != 4 || all[0] != "interaction" {
		t.Fatalf("no desktop: %v", all)
	}

	reportLinkDesktop(t, h, activeUntil(time.Now(), time.Minute), false)
	everything()
	if got := kinds(); len(got) != 3 || got[0] == "interaction" || got[1] == "interaction" || got[2] == "interaction" {
		t.Fatalf("with a Persona Link desktop: %v, want all but the card", got)
	}
	// Another manager's card: that Persona Link shows only its own manager's summary.
	h.notifyInteraction("m-other", ix, "box")
	if got := kinds(); len(got) != 1 || got[0] != "interaction" {
		t.Fatalf("another manager's card with a Persona Link desktop: %v", got)
	}

	if err := h.push.setPrefs(phone.device, NotifyPrefs{Input: true, Done: DoneAlways}); err != nil {
		t.Fatal(err)
	}
	everything()
	if got := kinds(); len(got) != 4 {
		t.Fatalf("Phone Muted off: %v", got)
	}
}

// The same for APNs: a card is held back from a muted phone while a Persona Link desktop is there, a finished turn is
// not; and with no desktop the card goes.
func TestPushMuteWhileLinkDesktop(t *testing.T) {
	h, f := testLive(t)
	h.push.desktopOnline, h.push.desktopOpen = h.desktopOnline, h.desktopOpen
	if err := h.push.setPrefs("phone", NotifyPrefs{Input: true, Done: DoneAlways, MuteWhileDesktop: true}); err != nil {
		t.Fatal(err)
	}
	input := pushWants{Kind: "input"}
	card := func(id string) {
		h.notifyInteraction(assistantwire.AssistantID, proto.Interaction{ID: id, MachineID: "m1", SessionID: "s1", Kind: proto.IxPermission, Summary: "ls"}, "Laptop")
	}
	// Straight to the hub's list: testLive's store keeps no machine entries, which a devices.state would write.
	setLinkDesktops := func(list []linkDesktop) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.setLinkDesktopsLocked(list, time.Now())
	}
	setLinkDesktops([]linkDesktop{{owner: assistantwire.AssistantID, machine: "lap", name: "Laptop", since: time.Now(),
		until: time.Now().Add(time.Minute)}})
	card("ix1")
	h.push.notifyDone("m1", "Laptop", "s1", "Done", "", input.fn())
	if got := pushedTo(t, f, 0, 1); !same(got, "devtok") {
		t.Fatalf("with a Persona Link desktop: %v, want the finished turn only", got)
	}
	setLinkDesktops(nil)
	card("ix2")
	if got := pushedTo(t, f, 1, 1); !same(got, "devtok") {
		t.Fatalf("with no desktop the card went to %v", got)
	}
}
