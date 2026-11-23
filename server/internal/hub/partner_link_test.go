package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A hub without TLS pairs with one that has it: only it dials (the other waits, dial never), the link comes up, and
// after the link is cut (its connection closed at either end; httptest's CloseClientConnections leaves hijacked
// WebSockets alone) it comes back on the same ports. The dialed side never starts a dialer.
func TestPartnerOneSideDialsAndReconnects(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{noTLS: true})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	pairPartners(t, a, b)
	if row, _ := b.h.partnerRow(hubA); row.Dial != assistantwire.PartnerDialNever {
		t.Fatalf("B dials a hub without TLS: %+v", row)
	}
	noDialer := func() {
		t.Helper()
		b.h.partners.mu.Lock()
		n := len(b.h.partners.dialers)
		b.h.partners.mu.Unlock()
		if n != 0 {
			t.Fatalf("B runs %d dialer(s)", n)
		}
	}
	noDialer()
	// A's server is plain: its partner link and pairing are TLS only, even on loopback, even with a good credential or
	// code.
	_, heldByB := partnerCredOf(t, b, a) // the one A issued B
	if code, body := a.do(t, "GET", partnerLinkPath, heldByB, ""); code != http.StatusForbidden || !strings.Contains(string(body), "tls_required") {
		t.Fatalf("a partner link over plain HTTP: %d %s", code, body)
	}
	plainCode := "jh_" + randomHex(32)
	if err := a.store.CreatePartnerCode(context.Background(), hashToken(plainCode), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if code, body := a.do(t, "POST", partnerPairPath, plainCode, "{}"); code != http.StatusForbidden || !strings.Contains(string(body), "tls_required") {
		t.Fatalf("pairing over plain HTTP: %d %s", code, body)
	}
	full, _ := b.deviceToken(t, roleFull)
	view := func() partnerHubView {
		t.Helper()
		_, body := b.do(t, "GET", "/api/v1/partner-hubs", full, "")
		var list struct {
			Hubs []partnerHubView `json:"hubs"`
		}
		if err := json.Unmarshal(body, &list); err != nil || len(list.Hubs) != 1 {
			t.Fatalf("list %s", body)
		}
		return list.Hubs[0]
	}
	if v := view(); v.State != "connected" || v.DialedBy != "them" || v.Dial != "never" {
		t.Fatalf("B's view: %+v", v)
	}
	// Cut from B's side (its end of the WebSocket): A dials again.
	first := b.h.partnerLinkTo(hubA)
	_ = first.conn.CloseNow()
	eventually(t, 20*time.Second, "the link back after its WebSocket was cut", func() bool {
		l := b.h.partnerLinkTo(hubA)
		return l != nil && l != first && a.h.partnerLinkTo(hubB) != nil
	})
	// Cut from A's side: back again, same ports.
	second := b.h.partnerLinkTo(hubA)
	cutPartnerLink(t, a, b)
	eventually(t, 20*time.Second, "the link back after A's end was cut", func() bool {
		l := b.h.partnerLinkTo(hubA)
		return l != nil && l != second && a.h.partnerLinkTo(hubB) != nil
	})
	noDialer()
	// With B set to never and the link gone, B stays offline (not connecting).
	if v := view(); v.State != "connected" || v.LastSeen == nil {
		t.Fatalf("B's view after: %+v", v)
	}
}

// Both hubs dial: one link stays, the one the smaller hub_id dialed; a second link the larger one dials is refused
// (by itself, or closed 4011 by the other), and the kept one stays.
func TestPartnerDuplicateLink(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	pairPartners(t, a, b)
	eventually(t, 10*time.Second, "the link A dialed", func() bool {
		la, lb := a.h.partnerLinkTo(hubB), b.h.partnerLinkTo(hubA)
		return la != nil && lb != nil && la.dialer == hubA && lb.dialer == hubA
	})
	kept := a.h.partnerLinkTo(hubB)
	row, _ := b.h.partnerRow(hubA)
	token, err := b.h.openKey(row.Token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, c, err := b.h.dialPartner(ctx, row.FP, token, b.h.partnerEndpoints(hubA))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.h.runPartnerLink(ctx, conn, hubA, hubB, c.key, c.url, ""); err == nil {
		t.Fatal("the second link was served")
	} else if !errors.Is(err, errDuplicateLink) && websocket.CloseStatus(err) != partnerCloseDuplicate {
		t.Logf("the second link ended with %v", err) // A may close it before or after the hellos
	}
	eventually(t, 5*time.Second, "the first link kept", func() bool {
		lb := b.h.partnerLinkTo(hubA)
		return a.h.partnerLinkTo(hubB) == kept && lb != nil && lb.dialer == hubA
	})
}

// A link whose hello names another hub than the one its credential was issued to is refused (1008) right after the
// hello and never kept: the partner's own link stays. Each hub has its own TLS key.
func TestPartnerHelloMustNameThePairedHub(t *testing.T) {
	certA, _ := partnerCert(t)
	certB, _ := partnerCert(t)
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{cert: certA})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{cert: certB})
	pairPartners(t, a, b)
	_, kept := waitPartnerLink(t, a, b)
	_, held := partnerCredOf(t, a, b) // the credential B issued A, as A holds it
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, peerWSURL(b.srv.URL, partnerLinkPath), &websocket.DialOptions{HTTPClient: insecureClient,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + held}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	nc := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	peer := rpc.New("t", nc)
	asked := make(chan struct{}, 1)
	peer.OnRequest = func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		if method != "hello" {
			return nil, errors.New("not served here")
		}
		select {
		case asked <- struct{}{}:
		default:
		}
		return partnerHello{HubID: hubC, Name: "Lab", Version: "test", Proto: partnerProto}, nil
	}
	served := make(chan error, 1)
	go func() { served <- peer.Serve(ctx, nc) }()
	select {
	case err := <-served:
		if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatalf("the link ended with %v", err)
		}
	case <-ctx.Done():
		t.Fatal("B kept a link whose hello named another hub")
	}
	select {
	case <-asked:
	default:
		t.Fatal("B closed the link before its hello")
	}
	if b.h.partnerLinkTo(hubA) != kept || b.h.partnerLinkTo(hubC) != nil {
		t.Fatal("the link named another hub replaced the partner's, or was kept")
	}
}

// Unpairing from either side ends it on both: the pairing forgotten, the link closed, the credentials revoked (the
// one the other held no longer opens the link), no dialer left; revoking the credential from Settings › Devices is
// unpairing too. Pairing again works after.
func TestPartnerUnpair(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	unpaired := func(what string) {
		t.Helper()
		eventually(t, 10*time.Second, what, func() bool {
			_, ra := a.h.partnerRow(hubB)
			_, rb := b.h.partnerRow(hubA)
			a.h.partners.mu.Lock()
			da := len(a.h.partners.dialers)
			a.h.partners.mu.Unlock()
			b.h.partners.mu.Lock()
			db := len(b.h.partners.dialers)
			b.h.partners.mu.Unlock()
			return !ra && !rb && a.h.partnerLinkTo(hubB) == nil && b.h.partnerLinkTo(hubA) == nil && da == 0 && db == 0 &&
				a.store.partnerCount() == 0 && b.store.partnerCount() == 0
		})
	}
	// From A, over the apps' route.
	pairPartners(t, a, b)
	_, heldByA := partnerCredOf(t, a, b) // B's credential for A
	fullA, _ := a.deviceToken(t, roleFull)
	if code, body := a.do(t, "DELETE", "/api/v1/partner-hubs/"+hubB, fullA, ""); code != 200 {
		t.Fatalf("unpair: %d %s", code, body)
	}
	unpaired("unpaired from A")
	if code, _ := b.do(t, "GET", partnerLinkPath, heldByA, ""); code != http.StatusUnauthorized {
		t.Fatalf("the credential B issued still opens its link: %d", code)
	}
	if code, _ := a.do(t, "DELETE", "/api/v1/partner-hubs/"+hubB, fullA, ""); code != http.StatusNotFound {
		t.Fatalf("unpair twice: %d", code)
	}
	// From B, by revoking the credential it issued A (Settings › Devices).
	pairPartners(t, a, b)
	issued, _ := partnerCredOf(t, b, a)
	fullB, _ := b.deviceToken(t, roleFull)
	if code, body := b.do(t, "DELETE", "/api/v1/auth/tokens/"+issued.ID, fullB, ""); code != 200 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	unpaired("unpaired by revoking B's credential")
	// From B, with the command line's route.
	pairPartners(t, a, b)
	if _, err := b.h.unpairPartner(context.Background(), hubA, true); err != nil {
		t.Fatal(err)
	}
	unpaired("unpaired from B")
	// From A without telling B (B unreachable for the call): A's own link (A dialed it) ends as unpaired (4012), not as
	// a hub stopping; B's next dial is refused, and its page says so.
	pairPartners(t, a, b)
	la := a.h.partnerLinkTo(hubB)
	if la.dialer != hubA {
		t.Fatalf("the link was dialed by %s", la.dialer)
	}
	if _, err := a.h.unpairPartner(context.Background(), hubB, false); err != nil {
		t.Fatal(err)
	}
	if why := context.Cause(la.ctx); why == nil || why.Error() != "unpaired" {
		t.Fatalf("A's link ended with %v", why)
	}
	eventually(t, 20*time.Second, "B refused by A", func() bool {
		row, ok := b.h.partnerRow(hubA)
		return ok && b.h.partnerView(row, "en").State == "refused"
	})
	if _, err := b.h.unpairPartner(context.Background(), hubA, true); err != nil {
		t.Fatal(err)
	}
	unpaired("unpaired from B after A left")
}

// The dial setting: never stops dialing (and the hub waits), auto starts it again; an unknown hub or setting is refused.
func TestPartnerDialSetting(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	pairPartners(t, a, b)
	fullA, _ := a.deviceToken(t, roleFull)
	fullB, _ := b.deviceToken(t, roleFull)
	for _, mh := range []struct {
		h   *testHub
		tok string
		id  string
	}{{a, fullA, hubB}, {b, fullB, hubA}} {
		if code, body := mh.h.do(t, "PATCH", "/api/v1/partner-hubs/"+mh.id, mh.tok, `{"dial":"never"}`); code != 200 {
			t.Fatalf("dial never: %d %s", code, body)
		}
	}
	if row, _ := a.h.partnerRow(hubB); row.Dial != "never" {
		t.Fatalf("A's pairing after never: %+v", row)
	}
	if row, _ := a.store.partnerRowOf(hubB); row.Dial != "never" {
		t.Fatalf("A's stored pairing after never: %+v", row)
	}
	cutPartnerLink(t, a, b)
	eventually(t, 10*time.Second, "no link, and both offline", func() bool {
		return a.h.partnerLinkTo(hubB) == nil && b.h.partnerLinkTo(hubA) == nil
	})
	_, body := a.do(t, "GET", "/api/v1/partner-hubs", fullA, "")
	var list struct {
		Hubs []partnerHubView `json:"hubs"`
	}
	if err := json.Unmarshal(body, &list); err != nil || list.Hubs[0].State != "offline" {
		t.Fatalf("A's view: %s", body)
	}
	if code, body := a.do(t, "PATCH", "/api/v1/partner-hubs/"+hubB, fullA, `{"dial":"auto"}`); code != 200 || !json.Valid(body) {
		t.Fatalf("dial auto: %d %s", code, body)
	}
	waitPartnerLink(t, a, b)
	if code, _ := a.do(t, "PATCH", "/api/v1/partner-hubs/"+hubB, fullA, `{"dial":"sometimes"}`); code != http.StatusBadRequest {
		t.Fatalf("a bad dial: %d", code)
	}
	if code, _ := a.do(t, "PATCH", "/api/v1/partner-hubs/"+hubC, fullA, `{"dial":"never"}`); code != http.StatusNotFound {
		t.Fatalf("an unknown hub's dial: %d", code)
	}
}

// Stopping a hub closes its partner links (an accepted one holds a WebSocket handler, which the shutdown waits for):
// the other side sees the link go.
func TestPartnerLinksStopWithTheHub(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{noTLS: true})
	b := startPartnerHub(t, hubB, "Office", partnerHubOpts{})
	pairPartners(t, a, b)
	b.h.stopPartnerLinks()
	eventually(t, 10*time.Second, "A sees the link go", func() bool { return a.h.partnerLinkTo(hubB) == nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { b.h.wsHandlers.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("a WebSocket handler stayed after the partner links stopped")
	}
}

// What a partner answers a dial with is said plainly on the partner's row: 403 partner hubs off there, 409 it joined a
// linked-hubs group (not a refusal of this hub's credential), 401 the credential gone.
func TestPartnerDialRefusalsSaid(t *testing.T) {
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{})
	for _, tc := range []struct {
		status  int
		refused bool
		says    string
	}{
		{http.StatusForbidden, true, "turned off"},
		{http.StatusConflict, false, "linked-hubs group"},
		{http.StatusUnauthorized, true, "doesn’t accept"},
	} {
		other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"no"}`, tc.status)
		}))
		sealed, err := a.h.sealKey("jd_" + randomHex(24))
		if err != nil {
			t.Fatal(err)
		}
		row := db.PartnerHub{HubID: hubB, Name: "Office", FP: link.Pin(other.Certificate()), Endpoints: []string{other.URL}, Token: sealed,
			CredentialID: "dev_" + randomHex(6), Dial: assistantwire.PartnerDialAuto, CreatedAt: time.Now()}
		if err := a.h.savePartner(context.Background(), row); err != nil {
			t.Fatal(err)
		}
		eventually(t, 10*time.Second, "the partner's answer said", func() bool {
			got, _ := a.h.partnerRow(hubB)
			v := a.h.partnerView(got, "en")
			return (v.State == "refused") == tc.refused && strings.Contains(v.Error, tc.says)
		})
		if _, err := a.h.unpairPartner(context.Background(), hubB, false); err != nil {
			t.Fatal(err)
		}
		other.Close()
	}
}
