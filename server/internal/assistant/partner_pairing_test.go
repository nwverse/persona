package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// What the manager keeps for a pairing belongs to it alone: a hub unpaired and paired again under the same hub_id
// (PartnerHub.PairedAt later) starts with the owner asked again, and nothing approved or queued for the earlier pairing
// goes to the new one.

// partnerPairedList is the hub's list with Hub B (hub-b) online, paired at at (by the hub's clock).
func partnerPairedList(at time.Time) assistantwire.PartnerHubs {
	return assistantwire.PartnerHubs{Enabled: true, Hubs: []assistantwire.PartnerHub{{HubID: pHub, Name: "Hub B", Online: true,
		Dial: assistantwire.PartnerDialAuto, PairedAt: at}}}
}

// pairAgain has the hub say partner list (as its partner.changed does) and answer partner.hubs with it.
func (r *partnerRig) pairAgain(list assistantwire.PartnerHubs) {
	r.h.pmu.Lock()
	r.h.list = list
	r.h.pmu.Unlock()
	r.s.partnersChanged(list)
}

// prefsRoute is the owner's GET or PUT of the partner prefs routes.
func (r *partnerRig) prefsRoute(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	r.s.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (r *partnerRig) hubCards() []proto.Interaction {
	return slices.DeleteFunc(r.s.pendingQuestions(), func(q proto.Interaction) bool { return q.ToolName != "hub_send" })
}

// The owner's "don't ask" and what waits for a pairing end with it: paired again under its hub_id, the message waiting
// for a card fails (the card taken back) and the queued one too, each told once, nothing goes; hub_list, hub_send and
// the settings route ask again until the owner switches it off for this pairing, and a hub not paired has no setting.
func TestPartnerPairedAgainStartsOver(t *testing.T) {
	r := newPartnerRig(t)
	ctx := context.Background()
	if err := r.db.SetPartnerConfirm(ctx, pHub, false, partnerPairedAt); err != nil {
		t.Fatal(err)
	}
	out, _ := r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"queued for the first pairing"}`)
	queued := messageIDOf(t, out)
	if err := r.db.SetPartnerConfirm(ctx, pHub, true, partnerPairedAt); err != nil {
		t.Fatal(err)
	}
	out, _ = r.tool(pSide, "hub_send", `{"hub":"hub-b","text":"waiting for the first pairing's card"}`)
	confirming := messageIDOf(t, out)
	card := r.card(t)
	if err := r.db.SetPartnerConfirm(ctx, pHub, false, partnerPairedAt); err != nil {
		t.Fatal(err)
	}

	// Unpaired and paired again: a later pairing time under the same hub_id.
	again := time.Now().UTC().Truncate(time.Microsecond)
	r.pairAgain(assistantwire.PartnerHubs{Enabled: true, Hubs: []assistantwire.PartnerHub{{HubID: pHub, Name: "Hub B", Online: true,
		Dial: assistantwire.PartnerDialAuto, PairedAt: again}}})
	for _, id := range []string{queued, confirming} {
		if m := r.waitState(t, partnerOut, id, partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired || m.NextTry != nil {
			t.Fatalf("%s: %+v", id, m)
		}
	}
	waitFor(t, "the card taken back", func() bool { return len(r.hubCards()) == 0 })
	for _, c := range []struct{ conv, text string }{{pMain, "queued for the first pairing"}, {pSide, "waiting for the first pairing's card"}} {
		waitFor(t, "told in "+c.conv, func() bool {
			return slices.ContainsFunc(r.told(c.conv), func(n string) bool {
				return strings.Contains(n, "两个 hub 已经不再配对") && strings.Contains(n, c.text)
			})
		})
	}
	// An allow with "don't ask" that comes late, for the card taken back, changes nothing.
	r.s.partnerAnswered(ctx, r.s.partnerStore(), r.row(t, partnerOut, confirming), proto.Resolution{Allow: boolp(true), Always: true})
	if m := r.row(t, partnerOut, confirming); m.State != partnerFailed || m.CardID != card.ID {
		t.Fatalf("answered after it failed: %+v", m)
	}
	r.loop(t)

	// The setting was the first pairing's: the new one asks, and the route lists none.
	if list, _ := r.tool(pMain, "hub_list", `{}`); !strings.Contains(list, `"Hub B" (hub_id hub-b): online; a message waits for the user's OK`) {
		t.Fatalf("hub_list: %q", list)
	}
	if code, out := r.prefsRoute(t, "GET", "/api/v1/partner-messages/prefs", ""); code != http.StatusOK || len(out["prefs"].([]any)) != 0 {
		t.Fatalf("prefs of the new pairing: %d %v", code, out)
	}
	out, isErr := r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"to the new pairing"}`)
	if isErr || !strings.Contains(out, "Waiting for the user's OK") {
		t.Fatalf("hub_send to the new pairing: %q", out)
	}
	if m := r.row(t, partnerOut, messageIDOf(t, out)); m.State != partnerConfirming {
		t.Fatalf("new pairing's row: %+v", m)
	}

	// The owner switches it off for this pairing: kept with it, and messages go without a card.
	if code, out := r.prefsRoute(t, "PUT", "/api/v1/partner-messages/prefs/hub-b", `{"confirm":false}`); code != http.StatusOK || out["confirm"] != false {
		t.Fatalf("put: %d %v", code, out)
	}
	if prefs, _ := r.db.PartnerPrefs(ctx); len(prefs) != 1 || prefs[0].Confirm || !samePairing(prefs[0].PairedAt, again) {
		t.Fatalf("stored: %+v", prefs)
	}
	if code, out := r.prefsRoute(t, "GET", "/api/v1/partner-messages/prefs", ""); code != http.StatusOK ||
		!strings.Contains(string(mustJSON(out)), `{"confirm":false,"hub_id":"hub-b"}`) {
		t.Fatalf("prefs: %d %v", code, out)
	}
	out, _ = r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"without a card now"}`)
	r.waitState(t, partnerOut, messageIDOf(t, out), partnerSent)
	for _, m := range r.h.sentOf(assistantwire.PartnerKindMessage) {
		if m.Envelope.MessageID == queued || m.Envelope.MessageID == confirming {
			t.Fatalf("a message of the first pairing went to the second: %+v", m)
		}
	}

	// A hub that is not paired has no setting to change.
	if code, out := r.prefsRoute(t, "PUT", "/api/v1/partner-messages/prefs/hub-x", `{"confirm":false}`); code != http.StatusNotFound ||
		out["code"] != "partner_not_found" {
		t.Fatalf("put for a hub not paired: %d %v", code, out)
	}
}

// A hub that is no longer listed fails what waits for it as unpaired at once, card and queue alike, without a try.
func TestPartnerUnpairedFailsWhatWaits(t *testing.T) {
	r := newPartnerRig(t)
	out, _ := r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"still waiting for the card"}`)
	id := messageIDOf(t, out)
	r.card(t)
	r.pairAgain(assistantwire.PartnerHubs{Enabled: true})
	if m := r.waitState(t, partnerOut, id, partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired {
		t.Fatalf("row: %+v", m)
	}
	waitFor(t, "the card taken back", func() bool { return len(r.hubCards()) == 0 })
	waitFor(t, "told once", func() bool {
		return len(slices.DeleteFunc(r.told(pMain), func(n string) bool { return !strings.Contains(n, "两个 hub 已经不再配对") })) == 1
	})
	if got := r.h.sentOf(""); len(got) != 0 {
		t.Fatalf("sent: %+v", got)
	}
}

// holds has the manager hold list as the hub's last word without being told it (partnersChanged starts a sweep of
// what waits for a pairing that ended): what a step does with a row of another pairing is that step's alone.
func (r *partnerRig) holds(list assistantwire.PartnerHubs) {
	r.h.pmu.Lock()
	r.h.list = list
	r.h.pmu.Unlock()
	b := &r.s.partners
	b.mu.Lock()
	b.list, b.known = list, true
	b.mu.Unlock()
}

// cardsEver are the hub_send cards the manager ever showed (every snapshot it published), taken back since or not.
func (r *partnerRig) cardsEver() []string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	var out []string
	for _, snap := range r.f.snaps {
		for _, ix := range snap.Interactions {
			if ix.ToolName == "hub_send" && !slices.Contains(out, ix.ID) {
				out = append(out, ix.ID)
			}
		}
	}
	return out
}

// unpairedTold counts the notices into conversation id that say the hubs are no longer paired.
func (r *partnerRig) unpairedTold(id string) int {
	n := 0
	for _, text := range r.told(id) {
		if strings.Contains(text, "两个 hub 已经不再配对") {
			n++
		}
	}
	return n
}

// A message kept for an earlier pairing (a manager that missed the unpairing: a restart) is never sent and never asked
// about: the next manager's loop fails a queued one as unpaired before it would send it, and raises no card for one
// still waiting for the owner's OK, each told once; a status of then is dropped untold. The rows are recent by the
// manager's clock (later than the pairing there is, inside the card's 24 hours): only the pairing they were written for
// tells them apart. No sweep runs here (the manager holds the hub's list untold), so each step's own check is what
// stops its row, and without it the row goes, or its card goes up, every time.
func TestPartnerSendsNothingOfAnEarlierPairing(t *testing.T) {
	database, err := db.Open(t.Context(), newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	ctx := context.Background()
	earlier, written, due := partnerPairedAt.Add(-time.Hour), time.Now().UTC().Add(-2*time.Minute), time.Now().Add(-time.Second)
	ids := []string{"pm_e0000000000000000000000000000001", "pm_e0000000000000000000000000000002", "pm_e0000000000000000000000000000003"}
	for _, m := range []db.PartnerMessage{
		{ID: "pr_e1", Direction: partnerOut, PeerHub: pHub, PeerName: "Hub B", MessageID: ids[0], Kind: assistantwire.PartnerKindMessage,
			Hop: 1, MachineID: "server", SessionID: pSide, Text: "queued then", State: partnerQueued, NextTry: &due, PairedAt: earlier,
			CreatedAt: written},
		{ID: "pr_e2", Direction: partnerOut, PeerHub: pHub, PeerName: "Hub B", MessageID: ids[1], Kind: assistantwire.PartnerKindMessage,
			Hop: 1, MachineID: "server", SessionID: pSide, Text: "asked then", State: partnerConfirming, PairedAt: earlier, CreatedAt: written},
		{ID: "pr_e3", Direction: partnerOut, PeerHub: pHub, PeerName: "Hub B", MessageID: ids[2], Kind: assistantwire.PartnerKindStatus,
			ReplyTo: "pm_e0000000000000000000000000000009", Hop: 1, Text: assistantwire.PartnerStateDelivered, State: partnerQueued,
			NextTry: &due, PairedAt: earlier, CreatedAt: written},
	} {
		if _, _, err := database.InsertPartnerMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	again := newPartnerRigUntold(t, database)
	again.holds(again.h.list)
	again.loop(t)
	waitFor(t, "every row settled", func() bool {
		for _, id := range ids {
			if m := again.row(t, partnerOut, id); m.State == partnerQueued || m.State == partnerConfirming && m.CardID == "" {
				return false
			}
		}
		return true
	})
	for _, id := range ids {
		if m := again.row(t, partnerOut, id); m.State != partnerFailed || m.Code != assistantwire.PartnerCodeUnpaired || m.CardID != "" {
			t.Errorf("%s: %+v", id, m)
		}
	}
	if cards := again.cardsEver(); len(cards) != 0 {
		t.Fatalf("asked about a message of an earlier pairing: %v", cards)
	}
	if got := again.h.sentOf(""); len(got) != 0 {
		t.Fatalf("sent: %+v", got)
	}
	waitFor(t, "both messages told", func() bool { return again.unpairedTold(pSide) == 2 })
}

// Pairing times are the hub's, and the manager's clock never comes into what belongs to a pairing: with the hub's clock
// 90 seconds ahead, a message written right after a pairing (earlier than it by the manager's clock) still waits for
// the owner's card and goes once allowed, and the status of a message taken then goes too; paired again with the hub's
// clock set back an hour (a pairing time earlier than every row), what waits for the pairing before fails as unpaired.
func TestPartnerPairingTimesAreTheHubsOwn(t *testing.T) {
	r := newPartnerRig(t)
	r.s.partners.retry = time.Hour // capped at partnerRetryMost: a message the partner did not take is not tried again here
	r.loop(t)
	ahead := time.Now().UTC().Add(90 * time.Second).Truncate(time.Microsecond)
	r.pairAgain(partnerPairedList(ahead))
	out, isErr := r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"right after pairing"}`)
	if isErr {
		t.Fatalf("hub_send: %q", out)
	}
	id := messageIDOf(t, out)
	if m := r.row(t, partnerOut, id); !samePairing(m.PairedAt, ahead) || !m.CreatedAt.Before(ahead) {
		t.Fatalf("row: %+v", m)
	}
	r.answer(t, r.card(t), proto.Resolution{Allow: boolp(true)})
	r.waitState(t, partnerOut, id, partnerSent)
	e := assistantwire.PartnerEnvelope{MessageID: "pm_cccccccccccccccccccccccccccccccc", Kind: assistantwire.PartnerKindMessage, Hop: 1,
		Text: "Free on Saturday?", SentAt: time.Now()}
	if res := partnerGot(t, r.s, e); res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("receive: %+v", res)
	}
	waitFor(t, "the delivered status sent", func() bool {
		return slices.ContainsFunc(r.h.sentOf(assistantwire.PartnerKindStatus), func(p assistantwire.PartnerSend) bool {
			return p.Envelope.ReplyTo == e.MessageID && p.Envelope.State == assistantwire.PartnerStateDelivered
		})
	})

	// The partner goes offline with a message queued for it; the hubs are paired again, by a clock set back an hour.
	r.h.setAnswer(func(assistantwire.PartnerSend) (assistantwire.PartnerResult, error) {
		return assistantwire.PartnerResult{}, &rpc.Error{Code: assistantwire.CodePartnerOffline, Message: "no link"}
	})
	if err := r.db.SetPartnerConfirm(context.Background(), pHub, false, ahead); err != nil {
		t.Fatal(err)
	}
	out, _ = r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"queued for that pairing"}`)
	queued := messageIDOf(t, out)
	tries := func() int {
		n := 0
		for _, p := range r.h.sentOf(assistantwire.PartnerKindMessage) {
			if p.Envelope.MessageID == queued {
				n++
			}
		}
		return n
	}
	waitFor(t, "a try that found no link", func() bool { return r.row(t, partnerOut, queued).Attempts == 1 })
	r.h.setAnswer(nil)
	r.pairAgain(partnerPairedList(time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)))
	if m := r.waitState(t, partnerOut, queued, partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired {
		t.Fatalf("queued row: %+v", m)
	}
	if n := tries(); n != 1 {
		t.Fatalf("a message of the pairing before was handed to the hub %d times, the one that found no link only", n)
	}
}

// What a partner sent under a pairing that ended goes into no conversation after it: once the hub is unpaired, one
// still received fails as unpaired, one waiting in the main conversation's relay queue is dropped as it would go in,
// and one the loop would take up again fails too, none with a status for the hub it came from; paired again under the
// same hub_id, what waits in the queue from the pairing before is dropped the same way, and its messages are not
// answered.
func TestPartnerNothingOfAnEndedPairingGoesIn(t *testing.T) {
	r := newPartnerRig(t)
	ctx := context.Background()
	sessions := func(mainStatus string) []proto.Session {
		return []proto.Session{
			{SessionID: pMain, MachineID: "server", Assistant: true, Status: mainStatus, Title: "Persona"},
			{SessionID: pSide, MachineID: "server", Assistant: true, Status: proto.StatusIdle, Title: "Trip planning"},
		}
	}
	queued := func(messageID string) bool {
		b := &r.s.relays
		b.mu.Lock()
		defer b.mu.Unlock()
		return slices.ContainsFunc(b.st.Queue[pMain], func(m relayMsg) bool { return m.Partner == messageID })
	}
	statusFor := func(messageID string) bool {
		return slices.ContainsFunc(r.h.sentOf(assistantwire.PartnerKindStatus), func(p assistantwire.PartnerSend) bool {
			return p.Envelope.ReplyTo == messageID
		})
	}
	// The main conversation is working: a partner's message waits in its queue.
	r.f.setSessions("server", sessions(proto.StatusWorking)...)
	waiting := assistantwire.PartnerEnvelope{MessageID: "pm_d0000000000000000000000000000001", Kind: assistantwire.PartnerKindMessage, Hop: 1,
		Text: "Please send me the plan.", SentAt: time.Now()}
	if res := partnerGot(t, r.s, waiting); res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("receive: %+v", res)
	}
	waitFor(t, "queued behind the main conversation's turn", func() bool { return queued(waiting.MessageID) })
	r.waitState(t, partnerIn, waiting.MessageID, partnerDelivered)
	// Another was taken and is not delivered yet.
	later := time.Now().UTC().Add(time.Hour)
	if _, _, err := r.db.InsertPartnerMessage(ctx, db.PartnerMessage{ID: "pr_d2", Direction: partnerIn, PeerHub: pHub, PeerName: "Hub B",
		MessageID: "pm_d0000000000000000000000000000002", Kind: assistantwire.PartnerKindMessage, Hop: 1, Text: "And the budget.",
		State: partnerReceived, NextTry: &later, PairedAt: partnerPairedAt}); err != nil {
		t.Fatal(err)
	}

	r.pairAgain(assistantwire.PartnerHubs{Enabled: true})
	if m := r.waitState(t, partnerIn, "pm_d0000000000000000000000000000002", partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired ||
		m.NextTry != nil {
		t.Fatalf("received row: %+v", m)
	}
	r.loop(t)
	// One the loop takes up again after the unpairing (the sweep had passed it) fails at once too.
	due := time.Now().UTC().Add(-time.Second)
	if _, _, err := r.db.InsertPartnerMessage(ctx, db.PartnerMessage{ID: "pr_d3", Direction: partnerIn, PeerHub: pHub, PeerName: "Hub B",
		MessageID: "pm_d0000000000000000000000000000003", Kind: assistantwire.PartnerKindMessage, Hop: 1, Text: "And the dates.",
		State: partnerReceived, NextTry: &due, PairedAt: partnerPairedAt}); err != nil {
		t.Fatal(err)
	}
	if m := r.waitState(t, partnerIn, "pm_d0000000000000000000000000000003", partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired {
		t.Fatalf("taken up again: %+v", m)
	}
	r.f.setSessions("server", sessions(proto.StatusIdle)...)
	r.s.drainRelays("server", sessions(proto.StatusIdle))
	if m := r.waitState(t, partnerIn, waiting.MessageID, partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired {
		t.Fatalf("queued row: %+v", m)
	}
	waitFor(t, "dropped from the queue", func() bool { return !queued(waiting.MessageID) })
	if got := r.partnerSends(pMain); len(got) != 0 {
		t.Fatalf("went into the main conversation after the unpairing: %+v", got)
	}
	for _, id := range []string{"pm_d0000000000000000000000000000002", "pm_d0000000000000000000000000000003"} {
		if statusFor(id) {
			t.Fatalf("a status went for %s", id)
		}
	}

	// Paired again under the same hub_id, and once more: what the pairing before left in the queue is dropped as it
	// would go in.
	r.pairAgain(partnerPairedList(time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)))
	r.f.setSessions("server", sessions(proto.StatusWorking)...)
	before := assistantwire.PartnerEnvelope{MessageID: "pm_d0000000000000000000000000000004", Kind: assistantwire.PartnerKindMessage, Hop: 1,
		Text: "One more thing.", SentAt: time.Now()}
	if res := partnerGot(t, r.s, before); res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("receive: %+v", res)
	}
	waitFor(t, "queued again", func() bool { return queued(before.MessageID) })
	r.waitState(t, partnerIn, before.MessageID, partnerDelivered)
	r.pairAgain(partnerPairedList(time.Now().UTC().Truncate(time.Microsecond)))
	if out, isErr := r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"done","reply_to":"`+before.MessageID+`"}`); !isErr ||
		!strings.Contains(out, "earlier pairing") {
		t.Fatalf("an answer to a message of the pairing before: %q", out)
	}
	r.f.setSessions("server", sessions(proto.StatusIdle)...)
	r.s.drainRelays("server", sessions(proto.StatusIdle))
	if m := r.waitState(t, partnerIn, before.MessageID, partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired {
		t.Fatalf("queued row of the pairing before: %+v", m)
	}
	waitFor(t, "dropped from the queue", func() bool { return !queued(before.MessageID) })
	if got := r.partnerSends(pMain); len(got) != 0 {
		t.Fatalf("went into the main conversation after the pairing it came under: %+v", got)
	}
}

// A partner names itself, so the owner's card names it with the start of its hub_id too, and hub_list says when two
// partners have one name: one named like another is told apart.
func TestPartnerLookalikeNames(t *testing.T) {
	r := newPartnerRig(t)
	const twin = "7c1e4a2b-9d3f-4c2a-8b1e-0123456789ab"
	r.pairAgain(assistantwire.PartnerHubs{Enabled: true, Hubs: []assistantwire.PartnerHub{
		{HubID: pHub, Name: "Hub B", Online: true, PairedAt: partnerPairedAt},
		{HubID: twin, Name: "hub b", Online: true, PairedAt: partnerPairedAt},
	}})
	list, _ := r.tool(pMain, "hub_list", `{}`)
	for _, want := range []string{`"Hub B" (hub_id hub-b): online`, `"hub b" (hub_id ` + twin + `): online`} {
		i := strings.Index(list, want)
		if i < 0 || !strings.Contains(strings.SplitN(list[i:], "\n", 2)[0], "another partner hub has this name too") {
			t.Fatalf("hub_list does not tell %q apart: %q", want, list)
		}
	}
	if out, isErr := r.tool(pMain, "hub_send", `{"hub":"Hub B","text":"which one?"}`); !isErr || !strings.Contains(out, "give its hub_id") {
		t.Fatalf("a name two partners have: %q", out)
	}
	out, isErr := r.tool(pMain, "hub_send", `{"hub":"`+twin+`","text":"to the twin"}`)
	if isErr {
		t.Fatalf("hub_send by hub_id: %q", out)
	}
	ix := r.card(t)
	if !strings.Contains(ix.Summary, "「hub b」（7c1e4a2b）") || !strings.Contains(ix.AlwaysLabel, "「hub b」（7c1e4a2b）") {
		t.Fatalf("the card: %q / %q", ix.Summary, ix.AlwaysLabel)
	}
	if got := partnerShortID(twin); got != "7c1e4a2b" {
		t.Fatalf("short id: %q", got)
	}
}

// With partner hubs switched off (hub.json partners.enabled false, the emergency switch), what a partner sent and is
// not in a conversation yet stays out: a message waiting in the main conversation's queue is dropped as it would go
// in, one taken but not delivered fails; no status goes back (nothing goes out either).
func TestPartnerNothingGoesInWhilePartnersAreOff(t *testing.T) {
	r := newPartnerRig(t)
	ctx := context.Background()
	sessions := func(mainStatus string) []proto.Session {
		return []proto.Session{
			{SessionID: pMain, MachineID: "server", Assistant: true, Status: mainStatus, Title: "Persona"},
			{SessionID: pSide, MachineID: "server", Assistant: true, Status: proto.StatusIdle, Title: "Trip planning"},
		}
	}
	queued := func(messageID string) bool {
		b := &r.s.relays
		b.mu.Lock()
		defer b.mu.Unlock()
		return slices.ContainsFunc(b.st.Queue[pMain], func(m relayMsg) bool { return m.Partner == messageID })
	}
	r.f.setSessions("server", sessions(proto.StatusWorking)...)
	waiting := assistantwire.PartnerEnvelope{MessageID: "pm_e0000000000000000000000000000001", Kind: assistantwire.PartnerKindMessage, Hop: 1,
		Text: "Please send me the plan.", SentAt: time.Now()}
	if res := partnerGot(t, r.s, waiting); res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("receive: %+v", res)
	}
	waitFor(t, "queued behind the main conversation's turn", func() bool { return queued(waiting.MessageID) })
	later := time.Now().UTC().Add(time.Hour)
	if _, _, err := r.db.InsertPartnerMessage(ctx, db.PartnerMessage{ID: "pr_e2", Direction: partnerIn, PeerHub: pHub, PeerName: "Hub B",
		MessageID: "pm_e0000000000000000000000000000002", Kind: assistantwire.PartnerKindMessage, Hop: 1, Text: "And the budget.",
		State: partnerReceived, NextTry: &later, PairedAt: partnerPairedAt}); err != nil {
		t.Fatal(err)
	}

	r.pairAgain(assistantwire.PartnerHubs{Enabled: false})
	if m := r.waitState(t, partnerIn, "pm_e0000000000000000000000000000002", partnerFailed); m.Code != assistantwire.PartnerCodeUnpaired {
		t.Fatalf("received row: %+v", m)
	}
	r.f.setSessions("server", sessions(proto.StatusIdle)...)
	r.s.drainRelays("server", sessions(proto.StatusIdle))
	r.waitState(t, partnerIn, waiting.MessageID, partnerFailed)
	waitFor(t, "dropped from the queue", func() bool { return !queued(waiting.MessageID) })
	if got := r.partnerSends(pMain); len(got) != 0 {
		t.Fatalf("went into the main conversation with partner hubs off: %+v", got)
	}
}
