package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

// partnerCredOf is the credential hub issued to the partner other (its pairing's), and the one other issued it (in
// the clear).
func partnerCredOf(t *testing.T, hub, other *testHub) (issued db.DeviceToken, held string) {
	t.Helper()
	row, ok := hub.h.partnerRow(other.id())
	if !ok {
		t.Fatalf("%s has no pairing with %s", hub.h.cfg.Name, other.h.cfg.Name)
	}
	toks, _ := hub.store.DeviceTokens(context.Background())
	for _, tk := range toks {
		if tk.ID == row.CredentialID {
			issued = tk
		}
	}
	held, err := hub.h.openKey(row.Token)
	if err != nil {
		t.Fatal(err)
	}
	return issued, held
}

// Two hubs pair as partners and link: the pairing is kept on both sides with a credential of kind and role partner
// that opens the partner link alone, each side has the other's pin (each hub its own key), the link is the one the
// smaller hub_id dialed, and the mesh never hears of it (no peer, nothing in the hello's mesh.hubs).
func TestPartnerPairAndLink(t *testing.T) {
	certA, fpA := partnerCert(t)
	certB, fpB := partnerCert(t)
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{cert: certA})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{cert: certB})
	if a.h.public.fp != fpA || b.h.public.fp != fpB || fpA == fpB {
		t.Fatalf("the hubs' pins: %q %q", a.h.public.fp, b.h.public.fp)
	}
	appTok, _ := a.deviceToken(t, roleFull)
	app := dialApp(t, a, appTok)
	pairPartners(t, a, b)
	app.wait(t, "partner_hubs", 0, typeIs("partner_hubs")) // the apps reload the list
	eventually(t, 10*time.Second, "the link the smaller hub_id dialed", func() bool {
		la, lb := a.h.partnerLinkTo(hubB), b.h.partnerLinkTo(hubA)
		return la != nil && lb != nil && la.dialer == hubA && lb.dialer == hubA
	})
	for _, mh := range []*testHub{a, b} {
		mh.h.mesh.mu.Lock()
		peers, links := len(mh.h.mesh.peers), len(mh.h.mesh.links)
		mh.h.mesh.mu.Unlock()
		hubs := mh.h.meshHubs() // this hub alone
		if stored, _ := mh.store.Peers(context.Background()); peers != 0 || links != 0 || len(hubs) != 1 || !hubs[0].Self || len(stored) != 0 {
			t.Fatalf("%s: the mesh heard of a partner: %d peers, %d links, hubs %v", mh.h.cfg.Name, peers, links, mh.h.meshHubs())
		}
	}
	ra, _ := a.h.partnerRow(hubB)
	rb, _ := b.h.partnerRow(hubA)
	if ra.Dial != assistantwire.PartnerDialAuto || rb.Dial != assistantwire.PartnerDialAuto || ra.Name != "Office" || rb.Name != "家里" ||
		ra.FP != fpB || rb.FP != fpA {
		t.Fatalf("pairings: %+v / %+v", ra, rb)
	}
	issued, held := partnerCredOf(t, b, a) // held: the one A issued B
	if issued.Kind != rolePartner || issued.Role != rolePartner || issued.MachineID != hubA || !strings.Contains(issued.Name, "家里") {
		t.Fatalf("credential B issued: %+v", issued)
	}
	// The credential opens the partner link (on A, which issued it) and nothing else.
	if code, body := a.do(t, "GET", "/api/v1/machines", held, ""); code != http.StatusForbidden {
		t.Fatalf("a partner credential on the API: %d %s", code, body)
	}
	if code, _ := a.do(t, "POST", partnerPairPath, held, "{}"); code != http.StatusForbidden {
		t.Fatalf("a partner credential pairing: %d", code)
	}
	// The apps' list (full credentials), on both sides.
	full, _ := a.deviceToken(t, roleFull)
	code, body := a.do(t, "GET", "/api/v1/partner-hubs", full, "")
	var list struct {
		Enabled  bool             `json:"enabled"`
		Member   bool             `json:"member"`
		CanIssue bool             `json:"can_issue"`
		Hubs     []partnerHubView `json:"hubs"`
	}
	if err := json.Unmarshal(body, &list); code != 200 || err != nil || !list.Enabled || list.Member || !list.CanIssue || len(list.Hubs) != 1 {
		t.Fatalf("list: %d %s", code, body)
	}
	if v := list.Hubs[0]; v.HubID != hubB || v.State != "connected" || v.DialedBy != "us" || v.Since == nil || v.Dial != "auto" || v.FP == "" {
		t.Fatalf("view: %s", body)
	}
	answer, _ := a.deviceToken(t, roleAnswer)
	if code, _ := a.do(t, "GET", "/api/v1/partner-hubs", answer, ""); code != http.StatusForbidden {
		t.Fatalf("an answer-only device read the partner hubs: %d", code)
	}
}

// A code works once, only within its time, never on its own hub; codes that do not read are refused before anything
// is sent, and a failed pairing leaves no live credential behind.
func TestPartnerPairingCode(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	c := startPartnerHub(t, hubC, "Lab", partnerHubOpts{})
	ctx := context.Background()
	code, err := b.h.newPartnerCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code.Code, "persona://partner?") || !strings.Contains(code.Code, "code=jh_") ||
		time.Until(code.ExpiresAt) > partnerCodeLife || time.Until(code.ExpiresAt) < partnerCodeLife-time.Minute {
		t.Fatalf("code %+v", code)
	}
	if _, err := b.h.joinPartner(ctx, code.Code); partnerErrCode(err) != "own_code" {
		t.Fatalf("own code: %v", err)
	}
	if _, err := a.h.joinPartner(ctx, code.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := c.h.joinPartner(ctx, code.Code); partnerErrCode(err) != "code_refused" || partnerErrStatus(err) != http.StatusUnauthorized {
		t.Fatalf("a used code: %v", err)
	}
	expired, _ := b.h.newPartnerCode(ctx)
	b.store.mu.Lock()
	for k := range b.store.partnerCodes {
		if !b.store.usedPartnerCodes[k] {
			b.store.partnerCodes[k] = time.Now().Add(-time.Second)
		}
	}
	b.store.mu.Unlock()
	if _, err := c.h.joinPartner(ctx, expired.Code); partnerErrCode(err) != "code_refused" {
		t.Fatalf("an expired code: %v", err)
	}
	toks, _ := c.store.DeviceTokens(ctx)
	for _, tk := range toks {
		if tk.Kind == rolePartner && !c.store.isRevoked(tk.ID) {
			t.Fatalf("a failed pairing left a live credential: %+v", tk)
		}
	}
	if _, ok := c.h.partnerRow(hubB); ok {
		t.Fatal("a failed pairing was kept")
	}
	for raw, want := range map[string]string{
		"https://hub.example.com":                                                                            "bad_code",
		"persona://peer?hub=x&code=jx_" + randomHex(32):                                                      "bad_code",
		"persona://partner?hub=" + hubB + "&fp=x&url=https://192.0.2.1&code=jh_short":                        "bad_code",
		"persona://partner?hub=" + hubB + "&url=https://192.0.2.1&code=jh_" + randomHex(32):                  "bad_code",
		"persona://partner?hub=" + hubB + "&fp=x&url=http://192.0.2.1:18660&code=jh_" + randomHex(32):        "bad_code",
		"persona://partner?hub=" + hubB + "&fp=x&url=https://192.0.2.1&url=https://[2001:db8::1]&code=jh_00": "bad_code",
		"persona://partner?hub=..%2Fx&fp=x&url=https://192.0.2.1&code=jh_" + randomHex(32):                   "bad_code",
		"persona://partner?hub=" + hubB + "&fp=x&url=https://user:pw@192.0.2.1&code=jh_" + randomHex(32):     "bad_code",
	} {
		if _, err := c.h.joinPartner(ctx, raw); partnerErrCode(err) != want || partnerErrStatus(err) != http.StatusBadRequest {
			t.Errorf("%s: %v", raw, err)
		}
	}
	// Over the apps' route: the code's own errors with their codes.
	full, _ := c.deviceToken(t, roleFull)
	if st, body := c.do(t, "POST", "/api/v1/partner-hubs", full, `{"code":"persona://partner?hub=x"}`); st != 400 || !strings.Contains(string(body), `"bad_code"`) {
		t.Fatalf("bad code over the route: %d %s", st, body)
	}
	if st, body := c.do(t, "POST", "/api/v1/partner-hubs", full, `{"code":`+strconvQuote(expired.Code)+`}`); st != 401 || !strings.Contains(string(body), `"code_refused"`) {
		t.Fatalf("used code over the route: %d %s", st, body)
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func partnerErrCode(err error) string {
	var pe *partnerError
	if errors.As(err, &pe) {
		return pe.code
	}
	return ""
}

func partnerErrStatus(err error) int {
	var pe *partnerError
	if errors.As(err, &pe) {
		return pe.status
	}
	return 0
}

// Who may not pair: a hub with partner hubs off (either side), a hub without TLS (it cannot hand out a code, but it
// can use one), a hub that is a mesh peer of the other already, a hub that is a mesh member.
func TestPartnerPairingRefusals(t *testing.T) {
	off := false
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{partners: PartnersConfig{Enabled: &off}})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	c := startPartnerHub(t, hubC, "Lab", partnerHubOpts{noTLS: true})
	ctx := context.Background()
	if _, err := a.h.newPartnerCode(ctx); partnerErrCode(err) != "partners_off" || partnerErrStatus(err) != http.StatusForbidden {
		t.Fatalf("code with partners off: %v", err)
	}
	code, _ := b.h.newPartnerCode(ctx)
	if _, err := a.h.joinPartner(ctx, code.Code); partnerErrCode(err) != "partners_off" {
		t.Fatalf("join with partners off: %v", err)
	}
	if _, err := c.h.newPartnerCode(ctx); partnerErrCode(err) != "no_tls" || partnerErrStatus(err) != http.StatusConflict {
		t.Fatalf("code without TLS: %v", err)
	}
	full, _ := c.deviceToken(t, roleFull)
	if st, body := c.do(t, "POST", "/api/v1/partner-hubs/code", full, ""); st != 409 || !strings.Contains(string(body), `"no_tls"`) {
		t.Fatalf("code route without TLS: %d %s", st, body)
	}
	if st, body := c.do(t, "GET", "/api/v1/partner-hubs", full, ""); st != 200 || !strings.Contains(string(body), `"can_issue":false`) {
		t.Fatalf("list without TLS: %d %s", st, body)
	}
	offFull, _ := a.deviceToken(t, roleFull)
	if st, body := a.do(t, "GET", "/api/v1/partner-hubs", offFull, ""); st != 200 || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("list with partners off: %d %s", st, body)
	}
	// A hub without TLS uses B's code (it dials; B stores it to wait for it, dial never).
	if _, err := c.h.joinPartner(ctx, code.Code); err != nil {
		t.Fatalf("join without TLS: %v", err)
	}
	if row, _ := b.h.partnerRow(hubC); row.Dial != assistantwire.PartnerDialNever || row.FP != "" {
		t.Fatalf("B keeps a hub without TLS as %+v", row)
	}
	// A mesh peer of the other hub: refused by the hub that has it (B, answering C's code with already_linked, which C
	// passes on), and by the one that would join.
	d := startPartnerHub(t, "dddddddd-0000-4000-8000-000000000004", "Mesh", partnerHubOpts{})
	b.h.mesh.mu.Lock()
	b.h.mesh.peers[d.id()] = db.Peer{HubID: d.id(), Name: "Mesh"}
	b.h.mesh.mu.Unlock()
	code2, _ := b.h.newPartnerCode(ctx)
	if _, err := d.h.joinPartner(ctx, code2.Code); partnerErrCode(err) != "already_linked" || partnerErrStatus(err) != http.StatusConflict {
		t.Fatalf("a mesh peer pairing as a partner: %v", err)
	}
	code3, _ := d.h.newPartnerCode(ctx)
	if _, err := b.h.joinPartner(ctx, code3.Code); partnerErrCode(err) != "already_linked" {
		t.Fatalf("pairing a mesh peer as a partner: %v", err)
	}
	// A mesh member has none: it refuses codes and pairing, and answers the hub-to-hub routes itself (member_hub),
	// never passing them on to its center.
	d.h.mesh.mu.Lock()
	d.h.mesh.group = meshGroup{Center: hubB, Term: 1}
	d.h.mesh.mu.Unlock()
	if _, err := d.h.newPartnerCode(ctx); partnerErrCode(err) != "member_hub" {
		t.Fatalf("a member's code: %v", err)
	}
	for _, path := range []string{partnerLinkPath, partnerPairPath} {
		if !d.h.servedByMember(httptest.NewRequest("GET", path, nil)) {
			t.Errorf("a member passes %s on to its center", path)
		}
	}
	if _, err := d.h.joinPartner(ctx, code2.Code); partnerErrCode(err) != "member_hub" {
		t.Fatalf("a member joining: %v", err)
	}
	if d.h.partnersOn() || len(d.h.partnerFeatures()) != 0 {
		t.Fatal("partner hubs on on a mesh member")
	}
}

// partnerCodeFor is code (one of some hub's) made to name hub_id id: what a code's maker may write into it.
func partnerCodeFor(t *testing.T, code, id string) string {
	t.Helper()
	u, err := url.Parse(code)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("hub", id)
	u.RawQuery = q.Encode()
	return u.String()
}

// A pairing never replaces another (a code's holder names its own hub_id and pin, and pins are no secret). On the
// code's hub, someone with a fresh code claiming a partner's hub_id is refused: with another pin (pin_changed), with
// the partner's own pin or none (already_paired), and where the partner has no pin with any (already_paired). The
// partner's pairing, credential and link stay as they were, and the code is not used up. The pasting hub refuses a
// code for a hub it is paired with the same way, before it sends anything. Each hub has its own TLS key.
func TestPartnerPairingNeverReplacesAPartner(t *testing.T) {
	certA, fpA := partnerCert(t)
	certB, fpB := partnerCert(t)
	certC, fpC := partnerCert(t)
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{cert: certA})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{cert: certB})
	c := startPartnerHub(t, hubC, "Lab", partnerHubOpts{cert: certC}) // the one with a code of B's
	const hubD = "dddddddd-0000-4000-8000-000000000004"
	d := startPartnerHub(t, hubD, "Laptop", partnerHubOpts{noTLS: true}) // B keeps it without a pin
	pairPartners(t, a, b)
	pairPartners(t, d, b)
	ctx := context.Background()
	rowA, _ := b.h.partnerRow(hubA)
	rowD, _ := b.h.partnerRow(hubD)
	linkA, linkD := b.h.partnerLinkTo(hubA), b.h.partnerLinkTo(hubD)
	if rowA.FP != fpA || rowD.FP != "" || linkA == nil || linkD == nil {
		t.Fatalf("B's pairings: %+v / %+v", rowA, rowD)
	}
	untouched := func(what string) {
		t.Helper()
		for _, want := range []db.PartnerHub{rowA, rowD} {
			got, _ := b.h.partnerRow(want.HubID)
			stored, _ := b.store.partnerRowOf(want.HubID)
			if got.CredentialID != want.CredentialID || got.Token != want.Token || got.FP != want.FP || got.Name != want.Name ||
				!got.CreatedAt.Equal(want.CreatedAt) || stored.CredentialID != want.CredentialID || stored.Token != want.Token {
				t.Fatalf("%s: B's pairing with %s now %+v, was %+v", what, want.HubID, got, want)
			}
			if b.store.isRevoked(want.CredentialID) {
				t.Fatalf("%s: the credential B issued %s was revoked", what, want.HubID)
			}
		}
		if b.h.partnerLinkTo(hubA) != linkA || b.h.partnerLinkTo(hubD) != linkD {
			t.Fatalf("%s: a partner's link was cut", what)
		}
	}
	for _, tc := range []struct {
		hub, fp, code string
	}{
		{hubA, fpC, "pin_changed"},
		{hubA, "", "pin_changed"},
		{hubA, fpA, "already_paired"},
		{hubD, "", "already_paired"},
		{hubD, fpC, "already_paired"},
	} {
		code, err := b.h.newPartnerCode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parsePartnerCode(code.Code)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(partnerInfo{HubID: tc.hub, Name: "Lab", FP: tc.fp, Endpoints: []string{c.srv.URL}, Token: "jd_" + randomHex(24)})
		st, out := b.do(t, "POST", partnerPairPath, parsed.secret, string(body))
		var refusal struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(out, &refusal)
		if st != http.StatusConflict || refusal.Code != tc.code {
			t.Fatalf("claiming %s with pin %q: %d %s", tc.hub, tc.fp, st, out)
		}
		untouched("claiming " + tc.hub)
		if ok, _ := b.store.PartnerCodeValid(ctx, hashToken(parsed.secret)); !ok {
			t.Fatalf("claiming %s used the code up", tc.hub)
		}
	}
	// The pasting side: a new code of its partner's, or someone's code naming it (with the partner's pin or another),
	// refused before it is sent anywhere; the pasting hub issues no credential for it.
	partnerCreds := func(mh *testHub) int {
		toks, _ := mh.store.DeviceTokens(ctx)
		n := 0
		for _, tk := range toks {
			if tk.Kind == rolePartner && !mh.store.isRevoked(tk.ID) {
				n++
			}
		}
		return n
	}
	beforeA, beforeB := partnerCreds(a), partnerCreds(b)
	again, _ := b.h.newPartnerCode(ctx)
	if _, err := a.h.joinPartner(ctx, again.Code); partnerErrCode(err) != "already_paired" || partnerErrStatus(err) != http.StatusConflict {
		t.Fatalf("A pasting a new code of its partner's: %v", err)
	}
	ofC, _ := c.h.newPartnerCode(ctx)
	if _, err := a.h.joinPartner(ctx, partnerCodeFor(t, ofC.Code, hubB)); partnerErrCode(err) != "pin_changed" {
		t.Fatalf("A pasting a code naming B with another pin: %v", err)
	}
	if _, err := b.h.joinPartner(ctx, partnerCodeFor(t, ofC.Code, hubD)); partnerErrCode(err) != "already_paired" {
		t.Fatalf("B pasting a code naming D, which has no pin: %v", err)
	}
	parsedC, _ := parsePartnerCode(ofC.Code)
	if ok, _ := c.store.PartnerCodeValid(ctx, hashToken(parsedC.secret)); !ok {
		t.Fatal("a refused code was sent to its hub")
	}
	if got, _ := a.h.partnerRow(hubB); got.FP != fpB || partnerCreds(a) != beforeA || partnerCreds(b) != beforeB {
		t.Fatalf("a refused paste changed something: %+v, %d/%d credentials, were %d/%d", got, partnerCreds(a), partnerCreds(b), beforeA, beforeB)
	}
	untouched("the pasting side's refusals")
	// The code's hub refusing (it kept a pairing the pasting hub ended alone): passed on with its code and its words,
	// in the pasting hub's language.
	if _, err := c.h.joinPartner(ctx, again.Code); err != nil { // not used by the refusals above
		t.Fatalf("C pairing with B: %v", err)
	}
	if _, err := c.h.unpairPartner(ctx, hubB, false); err != nil {
		t.Fatal(err)
	}
	code, _ := b.h.newPartnerCode(ctx)
	_, err := c.h.joinPartner(i18n.With(ctx, i18n.En), code.Code)
	if partnerErrCode(err) != "already_paired" || partnerErrStatus(err) != http.StatusConflict ||
		!strings.Contains(i18n.Localize(err, i18n.En), i18n.T(i18n.En, "hub-partner_pair.already_paired_here")) {
		t.Fatalf("C pairing again while B kept it: %v", err)
	}
	if row, ok := b.h.partnerRow(hubC); !ok || row.FP != fpC {
		t.Fatalf("B's pairing with C: %+v", row)
	}
	// C again with another key (reinstalled): B refuses with pin_changed in its own words, which name B as the hub to
	// unpair on, not the pasting one (it has no pairing to remove).
	certC2, _ := partnerCert(t)
	c2 := startPartnerHub(t, hubC, "Lab", partnerHubOpts{cert: certC2})
	code, _ = b.h.newPartnerCode(ctx)
	_, err = c2.h.joinPartner(i18n.With(ctx, i18n.Ja), code.Code)
	if partnerErrCode(err) != "pin_changed" || partnerErrStatus(err) != http.StatusConflict ||
		!strings.Contains(i18n.Localize(err, i18n.Ja), i18n.T(i18n.Ja, "hub-partner_pair.pin_changed_here")) {
		t.Fatalf("C with another key pairing again while B kept it: %v", err)
	}
	if row, ok := b.h.partnerRow(hubC); !ok || row.FP != fpC {
		t.Fatalf("B's pairing with C: %+v", row)
	}
}

// Which hub to unpair on, in every language: the pasting hub's own refusals (already_paired, pin_changed) name the
// hub the owner is on; the code's hub's, which that owner reads after "The other hub refused:", name the code's hub.
func TestPartnerPairingWordsSayWhereToUnpair(t *testing.T) {
	for _, tc := range []struct {
		lang       i18n.Lang
		here, code string // where to unpair: this hub (the pasting one), the code's hub
	}{
		{i18n.En, "on this hub", "on that hub"},
		{i18n.ZhHans, "在这台 hub 上", "在那台 hub 上"},
		{i18n.Ja, "このハブで", "相手のハブで"},
	} {
		for _, err := range []error{errPartnerPaired, errPartnerPin} {
			if got := i18n.Localize(err, tc.lang); !strings.Contains(got, tc.here) || strings.Contains(got, tc.code) {
				t.Errorf("%s, the pasting hub's %v: %q", tc.lang, partnerErrCode(err), got)
			}
		}
		for _, err := range []error{errPartnerPairedHere, errPartnerPinHere} {
			if got := i18n.Localize(err, tc.lang); !strings.Contains(got, tc.code) || strings.Contains(got, tc.here) {
				t.Errorf("%s, the code's hub's %v: %q", tc.lang, partnerErrCode(err), got)
			}
		}
	}
}

func setPartnerJoinStep(mh *testHub, step func(string)) {
	mh.h.partners.mu.Lock()
	mh.h.partners.joinStep = step
	mh.h.partners.mu.Unlock()
}

// partnerCreds is how many partner credentials mh issued that are not revoked.
func partnerCreds(t *testing.T, mh *testHub) int {
	t.Helper()
	toks, _ := mh.store.DeviceTokens(context.Background())
	n := 0
	for _, tk := range toks {
		if tk.Kind == rolePartner && !mh.store.isRevoked(tk.ID) {
			n++
		}
	}
	return n
}

// Two owners pasting each other's codes at the same moment: one pairing with a hub at a time on each side. B, joining
// A, refuses A's request (pairing_in_progress, in A's owner's words; B's code kept); A's join revokes only the
// credential it issued; B's goes through, and each hub holds a credential the other did not revoke, so the link comes
// up. Before, each hub's redeem revoked the credential its own join was handing over, and both ended up holding ones
// the other had revoked. A is held after issuing its credential until B has issued its own, and (were it answered)
// until B's request was answered, the order in which the two requests cross.
func TestPartnerPairingCrossed(t *testing.T) {
	certA, _ := partnerCert(t)
	certB, _ := partnerCert(t)
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{cert: certA})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{cert: certB})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	codeA, err := a.h.newPartnerCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	codeB, err := b.h.newPartnerCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatalf("not within 20s: %s", what)
		}
	}
	hold := func(reached, until chan struct{}) {
		close(reached)
		select {
		case <-until:
		case <-ctx.Done():
		}
	}
	aIssued, goA, aAnswered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	bIssued, goB, bAnswered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	setPartnerJoinStep(a, func(step string) {
		switch step {
		case partnerJoinIssued:
			hold(aIssued, goA)
		case partnerJoinAnswered:
			hold(aAnswered, bAnswered)
		}
	})
	setPartnerJoinStep(b, func(step string) {
		switch step {
		case partnerJoinIssued:
			hold(bIssued, goB)
		case partnerJoinAnswered:
			close(bAnswered)
		}
	})
	type joined struct{ err error }
	resA, resB := make(chan joined, 1), make(chan joined, 1)
	go func() { _, err := a.h.joinPartner(i18n.With(ctx, i18n.En), codeB.Code); resA <- joined{err} }()
	wait(aIssued, "A issued its credential")
	go func() { _, err := b.h.joinPartner(ctx, codeA.Code); resB <- joined{err} }()
	wait(bIssued, "B issued its credential")
	close(goA) // A's request reaches B, which is pairing with A
	var gotA joined
	select {
	case gotA = <-resA:
	case <-aAnswered: // B paired with A meanwhile: A waits until B's request was answered too
	case <-ctx.Done():
		t.Fatal("A's request was not answered")
	}
	close(goB) // B's request reaches A
	if gotA == (joined{}) {
		select {
		case gotA = <-resA:
		case <-ctx.Done():
			t.Fatal("A's join did not end")
		}
	}
	var gotB joined
	select {
	case gotB = <-resB:
	case <-ctx.Done():
		t.Fatal("B's join did not end")
	}
	if partnerErrCode(gotA.err) != "pairing_in_progress" || partnerErrStatus(gotA.err) != http.StatusConflict ||
		!strings.Contains(i18n.Localize(gotA.err, i18n.En), i18n.T(i18n.En, "hub-partner_pair.pairing_in_progress_here")) {
		t.Fatalf("A's join: %v", gotA.err)
	}
	if gotB.err != nil {
		t.Fatalf("B's join: %v", gotB.err)
	}
	setPartnerJoinStep(a, nil)
	setPartnerJoinStep(b, nil)
	parsedB, _ := parsePartnerCode(codeB.Code)
	if ok, _ := b.store.PartnerCodeValid(ctx, hashToken(parsedB.secret)); !ok {
		t.Fatal("B's refusal used its code up")
	}
	// Each hub holds a credential the other issued and did not revoke, and only the pairing's is left.
	for _, side := range [][2]*testHub{{a, b}, {b, a}} {
		mh, other := side[0], side[1]
		_, held := partnerCredOf(t, mh, other)
		row, _ := other.h.partnerRow(mh.id())
		if tk, _ := other.store.DeviceTokenByHash(ctx, hashToken(held)); tk == nil || tk.ID != row.CredentialID {
			t.Fatalf("%s holds a credential %s revoked or never paired with: %+v", mh.h.cfg.Name, other.h.cfg.Name, tk)
		}
		if n := partnerCreds(t, mh); n != 1 {
			t.Fatalf("%s: %d live partner credentials", mh.h.cfg.Name, n)
		}
	}
	waitPartnerLink(t, a, b)
	// A pairing with a hub here (a request being answered) refuses another with it, before any credential is issued.
	done, ok := a.h.pairingPartner(hubC)
	if !ok {
		t.Fatal("no pairing with C under way, yet it was refused")
	}
	c := startPartnerHub(t, hubC, "Lab", partnerHubOpts{})
	codeC, _ := c.h.newPartnerCode(ctx)
	if _, err := a.h.joinPartner(ctx, codeC.Code); partnerErrCode(err) != "pairing_in_progress" || partnerErrStatus(err) != http.StatusConflict {
		t.Fatalf("A joining C while pairing with it: %v", err)
	}
	if n := partnerCreds(t, a); n != 1 {
		t.Fatalf("a refused join issued a credential: %d", n)
	}
	done()
	if _, err := a.h.joinPartner(ctx, codeC.Code); err != nil {
		t.Fatalf("A joining C afterwards: %v", err)
	}
}

// What another hub says about itself is bounded: a hub_id that is not one, a token or pin of any size, more addresses
// than are ever dialed. The pairing route refuses the first ones; addresses are cut to partnerMaxEndpoints, https only,
// without credentials in them.
func TestPartnerPairBounds(t *testing.T) {
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	ctx := context.Background()
	pair := func(body string) (int, []byte) {
		t.Helper()
		code, err := b.h.newPartnerCode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parsePartnerCode(code.Code)
		if err != nil {
			t.Fatal(err)
		}
		return b.do(t, "POST", partnerPairPath, parsed.secret, body)
	}
	for _, body := range []string{
		`{"hub_id":"../x","token":"jd_` + randomHex(24) + `"}`,
		`{"hub_id":"` + strings.Repeat("a", 65) + `","token":"jd_` + randomHex(24) + `"}`,
		`{"hub_id":"` + hubA + `","token":"jd_` + strings.Repeat("0", 300) + `"}`,
		`{"hub_id":"` + hubA + `","fp":"` + strings.Repeat("x", 200) + `","token":"jd_` + randomHex(24) + `"}`,
	} {
		if code, out := pair(body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}
	if _, ok := b.h.partnerRow(hubA); ok {
		t.Fatal("a refused pairing was kept")
	}
	var eps []string
	for i := range 40 {
		eps = append(eps, "https://hub"+strconv.Itoa(i)+".example.com:18662")
	}
	eps = append(eps, "http://192.0.2.9:18660", "https://user:pw@192.0.2.10", "https://"+strings.Repeat("a", 600)+".example.com")
	body, _ := json.Marshal(partnerInfo{HubID: hubA, Name: "家里", FP: "fp", Endpoints: eps, Token: "jd_" + randomHex(24)})
	if code, out := pair(string(body)); code != http.StatusOK {
		t.Fatalf("pair: %d %s", code, out)
	}
	row, _ := b.h.partnerRow(hubA)
	if len(row.Endpoints) != partnerMaxEndpoints {
		t.Fatalf("kept %d addresses: %v", len(row.Endpoints), row.Endpoints)
	}
	for _, e := range row.Endpoints {
		if !strings.HasPrefix(e, "https://hub") {
			t.Fatalf("kept %q", e)
		}
	}
}

// A name another hub gives is kept as one plain line: no control or bidirectional formatting characters, 64 at most.
func TestPartnerName(t *testing.T) {
	for in, want := range map[string]string{
		"  Office\n hub ":       "Office hub",
		"Off\u202eice\x1b[31m":  "Office[31m",
		"家里\u0000":              "家里",
		strings.Repeat("长", 70): strings.Repeat("长", 64) + "…",
	} {
		if got := partnerName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// The other hub's refusal: its code passed on when the apps know it, "refused" when not, its words on one short line.
func TestPartnerAnswered(t *testing.T) {
	long := strings.Repeat("why ", 400)
	for _, tc := range []struct {
		status    int
		body      string
		code      string
		statusOut int
	}{
		{401, `{"error":"no"}`, "code_refused", http.StatusUnauthorized},
		{409, `{"error":"linked","code":"already_linked"}`, "already_linked", http.StatusConflict},
		{409, `{"error":"x","code":"` + strings.Repeat("z", 500) + `"}`, "refused", http.StatusBadGateway},
		{500, `{"error":"` + long + `\nsecond line"}`, "refused", http.StatusBadGateway},
	} {
		err := partnerAnswered(tc.status, []byte(tc.body))
		if partnerErrCode(err) != tc.code || partnerErrStatus(err) != tc.statusOut {
			t.Errorf("%d %s: %v (%s, %d)", tc.status, clip(tc.body, 60), err, partnerErrCode(err), partnerErrStatus(err))
		}
		if text := err.Error(); len(text) > 400 || strings.Contains(text, "\n") {
			t.Errorf("the other hub's words not bounded: %q", text)
		}
	}
}
