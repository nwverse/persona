package hub

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/rpc"
)

func partnerEnvelope(kind, text string) assistantwire.PartnerEnvelope {
	return assistantwire.PartnerEnvelope{MessageID: "pm_" + randomHex(16), Kind: kind, Hop: 1, Text: text, SentAt: time.Now().UTC().Truncate(time.Second)}
}

// An envelope goes from A's manager over the link to B's, stamped with who sent it (the link's hub and the name B kept
// at pairing, never the envelope's word), and B's manager's answer comes back to A's; the list reaches each manager
// after its hello (partner.changed) and on asking (partner.hubs), and the feature is listed only while the default
// manager takes partner messages.
func TestPartnerEnvelopeRoundTrip(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	caps := []string{assistantwire.CapabilityPartnerHubs}
	ma := connectPartnerManager(t, a, assistantwire.AssistantID, caps, nil)
	mb := connectPartnerManager(t, b, assistantwire.AssistantID, caps, func(p assistantwire.PartnerReceive) (any, error) {
		if strings.Contains(p.Envelope.Text, "again") {
			return assistantwire.PartnerResult{Status: assistantwire.PartnerDuplicate}, nil
		}
		return assistantwire.PartnerResult{Status: assistantwire.PartnerAccepted}, nil
	})
	eventually(t, 5*time.Second, "partner.changed after the hello", func() bool {
		l, n := ma.lastChanged()
		return n > 0 && l.Enabled && len(l.Hubs) == 0
	})
	if !slices.Contains(a.h.features(), featurePartnerHubs) {
		t.Fatalf("features without partner_hubs: %v", a.h.features())
	}
	pairPartners(t, a, b)
	eventually(t, 5*time.Second, "partner.changed with B online", func() bool {
		l, _ := ma.lastChanged()
		return len(l.Hubs) == 1 && l.Hubs[0].HubID == hubB && l.Hubs[0].Name == "Office" && l.Hubs[0].Online && l.Hubs[0].Dial == "auto"
	})
	var list assistantwire.PartnerHubs
	if err := ma.call(assistantwire.MethodPartnerHubs, map[string]any{}, &list); err != nil || !list.Enabled || len(list.Hubs) != 1 || !list.Hubs[0].Online {
		t.Fatalf("partner.hubs %+v %v", list, err)
	}

	env := partnerEnvelope(assistantwire.PartnerKindMessage, "can you check the build?")
	var res assistantwire.PartnerResult
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: env}, &res); err != nil ||
		res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("send: %+v %v", res, err)
	}
	got := mb.received()
	if len(got) != 1 || got[0].FromHub != hubA || got[0].FromName != "家里" || got[0].Envelope.MessageID != env.MessageID ||
		got[0].Envelope.Text != env.Text || got[0].Envelope.Kind != env.Kind || !got[0].Envelope.SentAt.Equal(env.SentAt) {
		t.Fatalf("B's manager got %+v", got)
	}
	// The other way, a reply, and B's manager's own answer (duplicate) passed back as it is.
	reply := partnerEnvelope(assistantwire.PartnerKindReply, "again")
	reply.ReplyTo, reply.Hop = env.MessageID, 2
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: reply}, &res); err != nil ||
		res.Status != assistantwire.PartnerDuplicate {
		t.Fatalf("send again: %+v %v", res, err)
	}
	if err := mb.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubA, Envelope: partnerEnvelope("status", "")}, &res); err != nil ||
		res.Status != assistantwire.PartnerAccepted {
		t.Fatalf("B to A: %+v %v", res, err)
	}
	if got := ma.received(); len(got) != 1 || got[0].FromHub != hubB || got[0].FromName != "Office" {
		t.Fatalf("A's manager got %+v", got)
	}

	// Refusals: an envelope that does not read, a hub that is not a partner, another manager than the default.
	bad := env
	bad.MessageID = "pm_1"
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: bad}, &res); rpcCode(err) != bus.CodeInvalidParams {
		t.Fatalf("a bad message id: %v", err)
	}
	bad = partnerEnvelope("gossip", "x")
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: bad}, &res); rpcCode(err) != bus.CodeInvalidParams {
		t.Fatalf("a bad kind: %v", err)
	}
	bad = partnerEnvelope(assistantwire.PartnerKindReply, "x")
	bad.ReplyTo = "not a message id"
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: bad}, &res); rpcCode(err) != bus.CodeInvalidParams {
		t.Fatalf("a bad reply_to: %v", err)
	}
	bad = partnerEnvelope("message", strings.Repeat("x", partnerTextCap+1))
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: bad}, &res); rpcCode(err) != bus.CodeInvalidParams {
		t.Fatalf("a text over the cap: %v", err)
	}
	if err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubC, Envelope: env}, &res); rpcCode(err) != assistantwire.CodePartnerUnknown {
		t.Fatalf("an unknown hub: %v", err)
	}
	other := connectPartnerManager(t, a, "other", caps, nil)
	if err := other.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: env}, &res); rpcCode(err) != codeOtherManager {
		t.Fatalf("a manager that is not the default: %v", err)
	}
	// Nor does it get the list: partner hubs are the default manager's.
	var none assistantwire.PartnerHubs
	if err := other.call(assistantwire.MethodPartnerHubs, map[string]any{}, &none); err != nil || none.Enabled || len(none.Hubs) != 0 {
		t.Fatalf("partner.hubs for another manager: %+v %v", none, err)
	}
	if len(mb.received()) != 2 {
		t.Fatalf("a refused envelope reached B: %+v", mb.received())
	}
}

// B's manager is away, or does not take partner messages: A's send fails partner_unavailable (it stays queued at A);
// with no link it fails partner_offline; with partner hubs off on A partner_unknown.
func TestPartnerSendWhenItCannotGo(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	caps := []string{assistantwire.CapabilityPartnerHubs}
	ma := connectPartnerManager(t, a, assistantwire.AssistantID, caps, nil)
	pairPartners(t, a, b)
	env := partnerEnvelope(assistantwire.PartnerKindMessage, "hello")
	var res assistantwire.PartnerResult
	send := func() error {
		return ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: env}, &res)
	}
	if err := send(); rpcCode(err) != assistantwire.CodePartnerUnavailable { // no manager on B
		t.Fatalf("B without a manager: %v", err)
	}
	connectPartnerManager(t, b, assistantwire.AssistantID, nil, nil) // one that does not name the capability
	eventually(t, 5*time.Second, "B's manager connected", func() bool { return b.h.busConnOf(assistantwire.AssistantID) != nil })
	if err := send(); rpcCode(err) != assistantwire.CodePartnerUnavailable {
		t.Fatalf("B's manager without the capability: %v", err)
	}
	if slices.Contains(b.h.features(), featurePartnerHubs) {
		t.Fatal("partner_hubs listed with a manager that does not take them")
	}
	// No link: both set to never, the link cut.
	fullA, _ := a.deviceToken(t, roleFull)
	fullB, _ := b.deviceToken(t, roleFull)
	a.do(t, "PATCH", "/api/v1/partner-hubs/"+hubB, fullA, `{"dial":"never"}`)
	b.do(t, "PATCH", "/api/v1/partner-hubs/"+hubA, fullB, `{"dial":"never"}`)
	cutPartnerLink(t, a, b)
	eventually(t, 10*time.Second, "the link gone", func() bool { return a.h.partnerLinkTo(hubB) == nil })
	if err := send(); rpcCode(err) != assistantwire.CodePartnerOffline {
		t.Fatalf("no link: %v", err)
	}
	if _, err := a.h.unpairPartner(context.Background(), hubB, false); err != nil {
		t.Fatal(err)
	}
	if err := send(); rpcCode(err) != assistantwire.CodePartnerUnknown {
		t.Fatalf("unpaired: %v", err)
	}
}

// A partner with more calls running here than partnerLinkCalls (its manager would send one at a time) has its link
// closed 4013; it dials again and the link comes back.
func TestPartnerTooManyCalls(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	release := make(chan struct{})
	defer close(release)
	connectPartnerManager(t, b, assistantwire.AssistantID, []string{assistantwire.CapabilityPartnerHubs},
		func(assistantwire.PartnerReceive) (any, error) {
			<-release // B's manager is busy: every call stays running on B
			return assistantwire.PartnerResult{Status: assistantwire.PartnerAccepted}, nil
		})
	pairPartners(t, a, b)
	la, lb := waitPartnerLink(t, a, b)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ended := make(chan error, partnerLinkCalls+1)
	for range partnerLinkCalls + 1 {
		go func() {
			var res assistantwire.PartnerResult
			ended <- la.rpc.Call(ctx, "msg", partnerEnvelope(assistantwire.PartnerKindMessage, "hi"), &res)
		}()
	}
	select {
	case <-lb.ctx.Done():
	case <-ctx.Done():
		t.Fatal("B kept a link with too many calls running")
	}
	if why := context.Cause(lb.ctx); why == nil || why.Error() != "too many calls at once" {
		t.Fatalf("B's link ended with %v", why)
	}
	if err := <-ended; err == nil {
		t.Fatal("a call on the closed link answered")
	}
	eventually(t, 20*time.Second, "the link back", func() bool {
		l := b.h.partnerLinkTo(hubA)
		return l != nil && l != lb && a.h.partnerLinkTo(hubB) != nil
	})
}

// What fails on one hub stays there: B's manager failing with words that name its database gives A's manager
// partner_unavailable in the hub's own words; what a partner answers a msg with reaches this hub's manager (and its
// log) only cleaned and bounded, its code kept only when it is one the manager acts on.
func TestPartnerErrorsStayHome(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	caps := []string{assistantwire.CapabilityPartnerHubs}
	ma := connectPartnerManager(t, a, assistantwire.AssistantID, caps, nil)
	const secret = "failed to connect to user=persona database=persona_test host=192.0.2.7" // B's own business
	connectPartnerManager(t, b, assistantwire.AssistantID, caps, func(assistantwire.PartnerReceive) (any, error) {
		return nil, &rpc.Error{Code: assistantwire.CodePartnerUnavailable, Message: "partner messages can't be kept now: " + secret}
	})
	pairPartners(t, a, b)
	var res assistantwire.PartnerResult
	err := ma.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: partnerEnvelope("message", "hi")}, &res)
	if rpcCode(err) != assistantwire.CodePartnerUnavailable || strings.Contains(err.Error(), "192.0.2.7") || strings.Contains(err.Error(), "database") {
		t.Fatalf("A's manager got %v", err)
	}
	long := "line one\nforged: line two\x1b[31m‮" + strings.Repeat("x", 1<<20)
	for _, tc := range []struct {
		in   error
		code string
	}{
		{&rpc.Error{Code: "something_else", Message: long}, assistantwire.CodePartnerUnavailable},
		{&rpc.Error{Code: assistantwire.CodePartnerUnknown, Message: long}, assistantwire.CodePartnerUnknown},
		{&rpc.Error{Code: assistantwire.CodePartnerUnavailable, Message: long}, assistantwire.CodePartnerUnavailable},
		{errors.New("closed\nby the partner: " + long), assistantwire.CodePartnerOffline},
	} {
		got := partnerSendError(tc.in)
		if got.Code != tc.code || len(got.Message) > 300 || strings.ContainsAny(got.Message, "\n\x1b‮") {
			t.Errorf("%.40q: %s %.80q (%d bytes)", tc.in.Error(), got.Code, got.Message, len(got.Message))
		}
	}
}

// The pairings not read at start (the database not answering yet): read again until they are. Meanwhile nothing
// says the partner is not paired: a send waits (partner_offline, not partner_unknown), partner.hubs has no list to
// give (and no manager is told an empty one, which would take its tools away), pairing and the list's changes wait
// (not_ready). Once read, the manager hears the list.
func TestPartnerLoadRetried(t *testing.T) {
	ctx := context.Background()
	st := newMeshStore()
	paired := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if err := st.SavePartnerHub(ctx, db.PartnerHub{HubID: hubA, Name: "家里", FP: "fp", Endpoints: []string{partnerUnroutable}, Token: "sealed",
		CredentialID: "dev_a", Dial: assistantwire.PartnerDialNever, CreatedAt: paired}); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.partnerLoadFails = 1 << 30 // until the test says
	st.mu.Unlock()
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{store: st})
	mb := connectPartnerManager(t, b, assistantwire.AssistantID, []string{assistantwire.CapabilityPartnerHubs}, nil)
	var res assistantwire.PartnerResult
	send := func(to string) error {
		return mb.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: to, Envelope: partnerEnvelope("message", "hi")}, &res)
	}
	if err := send(hubA); rpcCode(err) != assistantwire.CodePartnerOffline {
		t.Fatalf("a send before the pairings were read: %v", err)
	}
	if err := send(hubC); rpcCode(err) != assistantwire.CodePartnerOffline {
		t.Fatalf("a send to any hub before the pairings were read: %v", err)
	}
	var list assistantwire.PartnerHubs
	if err := mb.call(assistantwire.MethodPartnerHubs, map[string]any{}, &list); rpcCode(err) != bus.CodeStoreUnavailable {
		t.Fatalf("partner.hubs before the pairings were read: %+v %v", list, err)
	}
	full, _ := b.deviceToken(t, roleFull)
	for _, r := range [][2]string{{"POST", "/api/v1/partner-hubs/code"}, {"DELETE", "/api/v1/partner-hubs/" + hubA}} {
		if code, body := b.do(t, r[0], r[1], full, ""); code != http.StatusServiceUnavailable || !strings.Contains(string(body), `"not_ready"`) {
			t.Fatalf("%s %s before the pairings were read: %d %s", r[0], r[1], code, body)
		}
	}
	if code, body := b.do(t, "PATCH", "/api/v1/partner-hubs/"+hubA, full, `{"dial":"auto"}`); code != http.StatusServiceUnavailable {
		t.Fatalf("the dial setting before the pairings were read: %d %s", code, body)
	}
	st.mu.Lock()
	st.partnerLoadFails = 0
	st.mu.Unlock()
	eventually(t, 10*time.Second, "the list told once read", func() bool { return mb.listed(hubA) })
	if l, _ := mb.lastChanged(); !l.Hubs[0].PairedAt.Equal(paired) || l.Hubs[0].Name != "家里" {
		t.Fatalf("the list: %+v", l)
	}
	for _, l := range mb.allChanged() {
		if l.Enabled && len(l.Hubs) == 0 {
			t.Fatalf("told an empty list while the pairings were not read: %+v", mb.allChanged())
		}
	}
	if err := send(hubA); rpcCode(err) != assistantwire.CodePartnerOffline { // paired, no link (dial never)
		t.Fatalf("a send once read: %v", err)
	}
	if err := send(hubC); rpcCode(err) != assistantwire.CodePartnerUnknown {
		t.Fatalf("a send to a hub that is not a partner, once read: %v", err)
	}
	if code, body := b.do(t, "PATCH", "/api/v1/partner-hubs/"+hubA, full, `{"dial":"never"}`); code != http.StatusOK {
		t.Fatalf("the dial setting once read: %d %s", code, body)
	}
}

// The list is the default manager's: made the default, another manager hears it (partner.changed) and the one before
// hears the empty list, so only the default offers the hub tools; and back again.
func TestPartnerListFollowsTheDefaultManager(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{managers: true})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	caps := []string{assistantwire.CapabilityPartnerHubs}
	const other = "m-0b0b0b"
	m1 := connectPartnerManager(t, a, assistantwire.AssistantID, caps, nil)
	m2 := connectPartnerManager(t, a, other, caps, nil)
	pairPartners(t, a, b)
	eventually(t, 10*time.Second, "the default manager told the list", func() bool { return m1.listed(hubB) })
	if l, n := m2.lastChanged(); n > 0 && l.Enabled {
		t.Fatalf("a manager that is not the default was told %+v", l)
	}
	makeDefault := func(id string) {
		t.Helper()
		if err := a.h.managerStore().PutSetting(context.Background(), managerDefaultKey, id); err != nil {
			t.Fatal(err)
		}
		a.h.managersChanged() // as PATCH /api/v1/managers/{id} {"default": true} does
	}
	makeDefault(other)
	eventually(t, 10*time.Second, "the new default told the list, the old one the empty list", func() bool {
		return m2.listed(hubB) && m1.unlisted()
	})
	var list assistantwire.PartnerHubs
	if err := m2.call(assistantwire.MethodPartnerHubs, map[string]any{}, &list); err != nil || !list.Enabled || len(list.Hubs) != 1 {
		t.Fatalf("partner.hubs for the new default: %+v %v", list, err)
	}
	var res assistantwire.PartnerResult
	if err := m1.call(assistantwire.MethodPartnerSend, assistantwire.PartnerSend{To: hubB, Envelope: partnerEnvelope("message", "hi")}, &res); rpcCode(err) != codeOtherManager {
		t.Fatalf("the old default sending: %v", err)
	}
	makeDefault(assistantwire.AssistantID)
	eventually(t, 10*time.Second, "back to the first", func() bool { return m1.listed(hubB) && m2.unlisted() })
	// Made the default while it is not connected (it hears the list once it says its hello), then back: the first
	// hears the list again, though the one in between was never told it.
	const away = "m-0c0c0c"
	busToken(t, a.h, away, away)
	makeDefault(away)
	eventually(t, 10*time.Second, "the first told the empty list", func() bool { return m1.unlisted() })
	makeDefault(assistantwire.AssistantID)
	eventually(t, 10*time.Second, "the first told the list again", func() bool { return m1.listed(hubB) })
}

// The managers' list says when each pairing was made (the row's created_at, the same in the hub's copy and the
// store's): a pairing made again after an unpair is another, with a later time.
func TestPartnerListSaysWhenPaired(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	ma := connectPartnerManager(t, a, assistantwire.AssistantID, []string{assistantwire.CapabilityPartnerHubs}, nil)
	pairedAt := func() time.Time {
		t.Helper()
		row, _ := a.h.partnerRow(hubB)
		stored, _ := a.store.partnerRowOf(hubB)
		if row.CreatedAt.IsZero() || !row.CreatedAt.Equal(stored.CreatedAt) {
			t.Fatalf("created_at %v here, %v stored", row.CreatedAt, stored.CreatedAt)
		}
		eventually(t, 10*time.Second, "the list with when it was paired", func() bool {
			l, _ := ma.lastChanged()
			return ma.listed(hubB) && l.Hubs[0].PairedAt.Equal(row.CreatedAt)
		})
		var list assistantwire.PartnerHubs
		if err := ma.call(assistantwire.MethodPartnerHubs, map[string]any{}, &list); err != nil || len(list.Hubs) != 1 ||
			!list.Hubs[0].PairedAt.Equal(row.CreatedAt) {
			t.Fatalf("partner.hubs: %+v %v", list, err)
		}
		return row.CreatedAt
	}
	pairPartners(t, a, b)
	first := pairedAt()
	if _, err := a.h.unpairPartner(context.Background(), hubB, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "unpaired on both sides", func() bool {
		_, ra := a.h.partnerRow(hubB)
		_, rb := b.h.partnerRow(hubA)
		return !ra && !rb
	})
	pairPartners(t, a, b)
	if again := pairedAt(); !again.After(first) {
		t.Fatalf("paired again at %v, first at %v", again, first)
	}
}
