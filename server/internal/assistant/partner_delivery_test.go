package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// rowOf is the envelope direction / messageID kept for partner peer (false: none).
func (r *partnerRig) rowOf(t *testing.T, peer, direction, messageID string) (db.PartnerMessage, bool) {
	t.Helper()
	m, err := r.db.PartnerMessageOf(context.Background(), direction, peer, messageID)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		return db.PartnerMessage{}, false
	}
	return *m, true
}

func (r *partnerRig) waitStateOf(t *testing.T, peer, direction, messageID, state string) db.PartnerMessage {
	t.Helper()
	var m db.PartnerMessage
	waitFor(t, peer+" "+direction+" "+messageID+" "+state, func() bool {
		m, _ = r.rowOf(t, peer, direction, messageID)
		return m.State == state
	})
	return m
}

// noCards: messages to hubs go without the owner's OK.
func (r *partnerRig) noCards(t *testing.T, hubs ...string) {
	t.Helper()
	for _, hub := range hubs {
		if err := r.db.SetPartnerConfirm(context.Background(), hub, false, partnerPairedAt); err != nil {
			t.Fatal(err)
		}
	}
}

// statusesFor are the status envelopes sent about message id, in the order they went.
func (r *partnerRig) statusesFor(id string) []assistantwire.PartnerEnvelope {
	var out []assistantwire.PartnerEnvelope
	for _, p := range r.h.sentOf(assistantwire.PartnerKindStatus) {
		if p.Envelope.ReplyTo == id {
			out = append(out, p.Envelope)
		}
	}
	return out
}

// refuseSends has the node refuse for good every assistant.send into conversation id; the others go in.
func (r *partnerRig) refuseSends(id string) {
	f := r.f
	f.answerNode(DeviceAssistantSend, func(_ string, params json.RawMessage) (any, error) {
		var a map[string]any
		_ = json.Unmarshal(params, &a)
		if a["session_id"] == id {
			return nil, &rpc.Error{Code: "conversation_deleted", Message: "the conversation was deleted"}
		}
		f.sends = append(f.sends, a) // under the fake's lock, as answers run
		return map[string]any{"session_id": a["session_id"]}, nil
	})
}

// A partner that keeps its link up but never answers holds up only its own envelopes: while a call to it waits, the
// messages to another partner go, in the order they were written, and only one call to the silent one is in flight.
func TestPartnerSilentPartnerHoldsUpOnlyItself(t *testing.T) {
	r := newPartnerRig(t)
	const other = "hub-c"
	r.pairAgain(assistantwire.PartnerHubs{Enabled: true, Hubs: []assistantwire.PartnerHub{
		{HubID: pHub, Name: "Hub B", Online: true, PairedAt: partnerPairedAt},
		{HubID: other, Name: "Hub C", Online: true, PairedAt: partnerPairedAt},
	}})
	r.noCards(t, pHub, other)
	release := make(chan struct{})
	var once sync.Once
	let := func() { once.Do(func() { close(release) }) }
	var mu sync.Mutex
	inFlight, most := 0, 0
	r.h.setAnswer(func(p assistantwire.PartnerSend) (assistantwire.PartnerResult, error) {
		if p.To != pHub {
			return assistantwire.PartnerResult{Status: assistantwire.PartnerAccepted}, nil
		}
		mu.Lock()
		inFlight++
		most = max(most, inFlight)
		mu.Unlock()
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
		return assistantwire.PartnerResult{}, &rpc.Error{Code: assistantwire.CodePartnerOffline, Message: "no answer in time"}
	})
	r.loop(t)
	t.Cleanup(let) // before the loop's cleanup waits for its senders
	var silent []string
	for i := range 3 {
		out, _ := r.tool(pMain, "hub_send", fmt.Sprintf(`{"hub":"hub-b","text":"to the silent one %d"}`, i))
		silent = append(silent, messageIDOf(t, out))
	}
	waitFor(t, "a call to the silent partner in flight", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return inFlight == 1
	})
	var others []string
	for i := range 2 {
		out, _ := r.tool(pMain, "hub_send", fmt.Sprintf(`{"hub":"hub-c","text":"to the other one %d"}`, i))
		others = append(others, messageIDOf(t, out))
	}
	for _, id := range others {
		r.waitStateOf(t, other, partnerOut, id, partnerSent)
	}
	var order []string
	for _, p := range r.h.sentOf(assistantwire.PartnerKindMessage) {
		if p.To == other {
			order = append(order, p.Envelope.MessageID)
		}
	}
	if !slices.Equal(order, others) {
		t.Fatalf("to the other partner in the order %v, written %v", order, others)
	}
	for _, id := range silent {
		if m, _ := r.rowOf(t, pHub, partnerOut, id); m.State != partnerQueued {
			t.Fatalf("to the silent one: %+v", m)
		}
	}
	let()
	waitFor(t, "the silent one's first tried", func() bool {
		m, _ := r.rowOf(t, pHub, partnerOut, silent[0])
		return m.Attempts >= 1
	})
	mu.Lock()
	defer mu.Unlock()
	if most != 1 {
		t.Fatalf("%d calls to the silent partner at once", most)
	}
}

// The rate limits are the loop guard beside the hop limit, and hold however many come at once: hub_send refuses past
// partnerOutRate messages to one partner in the window (keeping no row), and partner.receive refuses past
// partnerInRate messages from one partner with rate_limited, while statuses have their own budget.
func TestPartnerRateLimits(t *testing.T) {
	r := newPartnerRig(t)
	ctx := context.Background()
	r.noCards(t, pHub)
	var mu sync.Mutex
	var ok, refused int
	var wg sync.WaitGroup
	for i := range partnerOutRate + 5 {
		wg.Go(func() {
			out, isErr := r.tool(pMain, "hub_send", fmt.Sprintf(`{"hub":"hub-b","text":"message %d"}`, i))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case !isErr:
				ok++
			case strings.Contains(out, "wait, or tell the user"):
				refused++
			default:
				t.Errorf("hub_send %d: %q", i, out)
			}
		})
	}
	wg.Wait()
	if ok != partnerOutRate || refused != 5 {
		t.Fatalf("hub_send: %d went, %d refused", ok, refused)
	}
	if rows := r.outRows(t, assistantwire.PartnerKindMessage); len(rows) != partnerOutRate {
		t.Fatalf("%d rows kept, want %d", len(rows), partnerOutRate)
	}
	if out, isErr := r.tool(pMain, "hub_send", `{"hub":"hub-b","text":"one more"}`); !isErr || !strings.Contains(out, "wait, or tell the user") {
		t.Fatalf("past the limit: %q", out)
	}

	setSettings(t, r.s, func(v *Settings) { v.Main = "" }) // what comes in fails no_main: nothing to deliver here
	results := make([]assistantwire.PartnerResult, 2*partnerInRate)
	errs := make([]error, len(results))
	for i := range results {
		wg.Go(func() { // as a link runs a partner's calls: side by side
			raw, _ := json.Marshal(assistantwire.PartnerReceive{FromHub: pHub, FromName: "Hub B", Envelope: assistantwire.PartnerEnvelope{
				MessageID: fmt.Sprintf("pm_%032x", 0xa000+i), Kind: assistantwire.PartnerKindMessage, Hop: 1, Text: fmt.Sprintf("flood %d", i)}})
			var out any
			out, _, errs[i] = r.s.partnerRequest(ctx, assistantwire.MethodPartnerReceive, raw)
			results[i], _ = out.(assistantwire.PartnerResult)
		})
	}
	wg.Wait()
	accepted := 0
	for i, res := range results {
		switch {
		case errs[i] != nil:
			t.Errorf("envelope %d: %v", i, errs[i])
		case res.Status == assistantwire.PartnerAccepted:
			accepted++
		case res.Status != assistantwire.PartnerRefused || res.Code != assistantwire.PartnerCodeRateLimited:
			t.Errorf("envelope %d: %+v", i, res)
		}
	}
	if accepted != partnerInRate {
		t.Fatalf("%d accepted at once, want %d", accepted, partnerInRate)
	}
	sent := r.outRows(t, assistantwire.PartnerKindMessage)[0]
	if res := partnerGot(t, r.s, assistantwire.PartnerEnvelope{MessageID: "pm_b0000000000000000000000000000001", Kind: assistantwire.PartnerKindStatus,
		ReplyTo: sent.MessageID, State: assistantwire.PartnerStateDelivered}); res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("a status past the messages' limit: %+v", res)
	}
	if n, err := r.db.CountPartnerMessages(ctx, partnerIn, pHub, false, time.Now().Add(-partnerRateWindow)); err != nil || n != partnerInRate {
		t.Fatalf("%d kept, %v", n, err)
	}
	// The statuses' own budget holds as the messages' does, side by side: partnerStatusRate in all (the one above too).
	statuses := make([]assistantwire.PartnerResult, partnerStatusRate+10)
	for i := range statuses {
		wg.Go(func() {
			raw, _ := json.Marshal(assistantwire.PartnerReceive{FromHub: pHub, FromName: "Hub B", Envelope: assistantwire.PartnerEnvelope{
				MessageID: fmt.Sprintf("pm_%032x", 0xb100+i), Kind: assistantwire.PartnerKindStatus, ReplyTo: sent.MessageID,
				State: assistantwire.PartnerStateDelivered}})
			out, _, err := r.s.partnerRequest(ctx, assistantwire.MethodPartnerReceive, raw)
			if err != nil {
				t.Errorf("status %d: %v", i, err)
			}
			statuses[i], _ = out.(assistantwire.PartnerResult)
		})
	}
	wg.Wait()
	accepted = 0
	for i, res := range statuses {
		switch {
		case res.Status == assistantwire.PartnerAccepted:
			accepted++
		case res.Status != assistantwire.PartnerRefused || res.Code != assistantwire.PartnerCodeRateLimited:
			t.Errorf("status %d: %+v", i, res)
		}
	}
	if accepted != partnerStatusRate-1 {
		t.Fatalf("%d statuses accepted at once after one, want %d", accepted, partnerStatusRate-1)
	}
	if n, err := r.db.CountPartnerMessages(ctx, partnerIn, pHub, true, time.Now().Add(-partnerRateWindow)); err != nil || n != partnerStatusRate {
		t.Fatalf("%d statuses kept, %v", n, err)
	}
}

// A partner's reply the conversation that asked refuses for good goes to the main conversation and counts as delivered
// there: the partner hears so, the message it answers is answered, and nothing is left for the loop to deliver again.
func TestPartnerReplyRefusedGoesToMainOnce(t *testing.T) {
	r := newPartnerRig(t)
	r.noCards(t, pHub)
	r.loop(t)
	out, _ := r.tool(pSide, "hub_send", `{"hub":"hub-b","text":"Is the build green?"}`)
	asked := messageIDOf(t, out)
	r.waitState(t, partnerOut, asked, partnerSent)
	r.refuseSends(pSide)
	reply := assistantwire.PartnerEnvelope{MessageID: "pm_c0000000000000000000000000000001", Kind: assistantwire.PartnerKindReply,
		ReplyTo: asked, Hop: 2, Text: "It is green.", SentAt: time.Now()}
	if res := partnerGot(t, r.s, reply); res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("reply: %+v", res)
	}
	m := r.waitState(t, partnerIn, reply.MessageID, partnerDelivered)
	if m.SessionID != pMain {
		t.Fatalf("in row: %+v", m)
	}
	r.waitState(t, partnerOut, asked, partnerAnswered)
	waitFor(t, "the reply in the main conversation", func() bool { return len(r.partnerSends(pMain)) == 1 })
	if words := relayWords(stringOf(r.partnerSends(pMain)[0]["text"])); words != "It is green." {
		t.Fatalf("the main conversation's message: %q", words)
	}
	waitFor(t, "its delivered status, and nothing left to do", func() bool {
		m, _ := r.rowOf(t, pHub, partnerIn, reply.MessageID)
		st := r.statusesFor(reply.MessageID)
		return m.NextTry == nil && len(st) == 1 && st[0].State == assistantwire.PartnerStateDelivered
	})
	if got := len(r.partnerSends(pMain)); got != 1 {
		t.Fatalf("%d copies in the main conversation", got)
	}
}

// An incoming message's status is queued by the loop when the manager stopped (or the database failed) after the
// message was delivered or failed but before its status was kept: once, with the state and code the row has.
func TestPartnerReconcilerQueuesALostStatus(t *testing.T) {
	r := newPartnerRig(t)
	ctx := context.Background()
	due := time.Now().Add(-time.Second)
	rows := []db.PartnerMessage{
		{ID: "pr_d1", Direction: partnerIn, PeerHub: pHub, PeerName: "Hub B", MessageID: "pm_d0000000000000000000000000000001",
			Kind: assistantwire.PartnerKindMessage, Hop: 1, MachineID: "server", SessionID: pMain, Text: "in", State: partnerDelivered, NextTry: &due},
		{ID: "pr_d2", Direction: partnerIn, PeerHub: pHub, PeerName: "Hub B", MessageID: "pm_d0000000000000000000000000000002",
			Kind: assistantwire.PartnerKindMessage, Hop: 1, Text: "out of luck", State: partnerFailed, Code: assistantwire.PartnerCodeNoMain, NextTry: &due},
		{ID: "pr_d3", Direction: partnerIn, PeerHub: pHub, PeerName: "Hub B", MessageID: "pm_d0000000000000000000000000000003",
			Kind: assistantwire.PartnerKindMessage, Hop: 1, MachineID: "server", SessionID: pMain, Text: "answered", State: partnerReplied, NextTry: &due},
	}
	for _, m := range rows {
		if _, _, err := r.db.InsertPartnerMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	r.loop(t)
	want := map[string]assistantwire.PartnerEnvelope{
		rows[0].MessageID: {State: assistantwire.PartnerStateDelivered},
		rows[1].MessageID: {State: assistantwire.PartnerStateFailed, Code: assistantwire.PartnerCodeNoMain},
		rows[2].MessageID: {State: assistantwire.PartnerStateDelivered},
	}
	for id, w := range want {
		waitFor(t, "the status of "+id, func() bool {
			st := r.statusesFor(id)
			m, _ := r.rowOf(t, pHub, partnerIn, id)
			return len(st) == 1 && st[0].State == w.State && st[0].Code == w.Code && m.NextTry == nil
		})
	}
	for id := range want {
		if m, ok := r.rowOf(t, pHub, partnerOut, partnerStatusID(r.row(t, partnerIn, id), want[id].State)); !ok || m.State != partnerSent {
			t.Fatalf("the status row of %s: %+v", id, m)
		}
	}
	if got := r.partnerSends(pMain); len(got) != 0 {
		t.Fatalf("delivered again: %+v", got)
	}
}

// partnerPair wires two rigs as each other's partner: what a sends to hub-b, b's manager takes from hub-a, and back.
func partnerPair(t *testing.T, a, b *partnerRig) {
	t.Helper()
	b.pairAgain(assistantwire.PartnerHubs{Enabled: true, Hubs: []assistantwire.PartnerHub{{HubID: "hub-a", Name: "Hub A", Online: true,
		PairedAt: partnerPairedAt}}})
	carry := func(to *partnerRig, from, name string) func(assistantwire.PartnerSend) (assistantwire.PartnerResult, error) {
		return func(p assistantwire.PartnerSend) (assistantwire.PartnerResult, error) {
			raw, _ := json.Marshal(assistantwire.PartnerReceive{FromHub: from, FromName: name, Envelope: p.Envelope})
			out, _, err := to.s.partnerRequest(context.Background(), assistantwire.MethodPartnerReceive, raw)
			if err != nil {
				return assistantwire.PartnerResult{}, err
			}
			return out.(assistantwire.PartnerResult), nil
		}
	}
	a.h.setAnswer(carry(b, "hub-a", "Hub A"))
	b.h.setAnswer(carry(a, pHub, "Hub B"))
}

// A message the partner's main conversation refuses for good comes back failed: the partner's manager marks it
// undelivered and says so in a status, this hub's message fails with that code, and the conversation that sent it is told.
func TestPartnerRefusedThereIsFailedHere(t *testing.T) {
	a, b := newPartnerRig(t), newPartnerRig(t)
	partnerPair(t, a, b)
	a.noCards(t, pHub)
	b.refuseSends(pMain)
	a.loop(t)
	b.loop(t)
	out, _ := a.tool(pSide, "hub_send", `{"hub":"hub-b","text":"Can you run the nightly tests?"}`)
	id := messageIDOf(t, out)
	if m := b.waitStateOf(t, "hub-a", partnerIn, id, partnerFailed); m.Code != assistantwire.PartnerCodeUndelivered {
		t.Fatalf("there: %+v", m)
	}
	if m := a.waitState(t, partnerOut, id, partnerFailed); m.Code != assistantwire.PartnerCodeUndelivered {
		t.Fatalf("here: %+v", m)
	}
	waitFor(t, "the sending conversation told", func() bool {
		return slices.ContainsFunc(a.told(pSide), func(n string) bool {
			return strings.Contains(n, "没能送进那边的对话（undelivered）") && strings.Contains(n, "Can you run the nightly tests?")
		})
	})
	if got := b.partnerSends(pMain); len(got) != 0 {
		t.Fatalf("delivered there after all: %+v", got)
	}
}

// A message lost on the way in (its main conversation gone while it waited in the queue) and one with the main
// conversation on a work computer fail undelivered, and the partner hears it.
func TestPartnerLostOrWorkComputerFailsUndelivered(t *testing.T) {
	r := newPartnerRig(t)
	sessions := func(status string) []proto.Session {
		return []proto.Session{{SessionID: pMain, MachineID: "server", Assistant: true, Status: status, Title: "Persona"}}
	}
	r.f.setSessions("server", sessions(proto.StatusWorking)...)
	e := assistantwire.PartnerEnvelope{MessageID: "pm_f0000000000000000000000000000001", Kind: assistantwire.PartnerKindMessage, Hop: 1,
		Text: "Waiting behind a turn"}
	if res := partnerGot(t, r.s, e); res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("receive: %+v", res)
	}
	r.waitState(t, partnerIn, e.MessageID, partnerDelivered) // queued behind the turn: in, durably
	r.loop(t)
	waitFor(t, "its delivered status", func() bool { return len(r.statusesFor(e.MessageID)) == 1 })
	r.f.mu.Lock()
	r.f.known[pMain] = false // the conversation is gone
	r.f.mu.Unlock()
	r.f.setSessions("server", sessions(proto.StatusIdle)...)
	r.s.drainRelays("server", sessions(proto.StatusIdle))
	if m := r.waitState(t, partnerIn, e.MessageID, partnerFailed); m.Code != assistantwire.PartnerCodeUndelivered {
		t.Fatalf("lost: %+v", m)
	}
	waitFor(t, "its failed status", func() bool {
		st := r.statusesFor(e.MessageID)
		return len(st) == 2 && st[1].State == assistantwire.PartnerStateFailed && st[1].Code == assistantwire.PartnerCodeUndelivered
	})

	w := newPartnerRig(t)
	w.f.mu.Lock()
	w.f.hello.Assistant.WorkMachines = []string{"server"}
	w.f.mu.Unlock()
	setSettings(t, w.s, func(v *Settings) { v.MainMachineID = "server" }) // the main conversation is bound to it
	w.loop(t)
	e.MessageID = "pm_f0000000000000000000000000000002"
	if res := partnerGot(t, w.s, e); res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("receive on a work computer's hub: %+v", res)
	}
	if m := w.waitState(t, partnerIn, e.MessageID, partnerFailed); m.Code != assistantwire.PartnerCodeUndelivered {
		t.Fatalf("work computer: %+v", m)
	}
	waitFor(t, "the work computer's failed status", func() bool {
		st := w.statusesFor(e.MessageID)
		return len(st) == 1 && st[0].State == assistantwire.PartnerStateFailed && st[0].Code == assistantwire.PartnerCodeUndelivered
	})
	if got := w.partnerSends(pMain); len(got) != 0 {
		t.Fatalf("into a work computer's conversation: %+v", got)
	}
}
