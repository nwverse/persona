package hub

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The manager's elicitation card (fleet.interaction) reaches only the apps that take one: the broadcast, the pending
// cards a connecting app gets after its hello, the REST list; the manager is told whether such an app is there.
func TestElicitationReachesOnlyCapableApps(t *testing.T) {
	h := newTestHub(t)
	legacy := &client{out: make(chan []byte, 32)}
	addTestClient(h, legacy)
	capable := &client{out: make(chan []byte, 32)}
	addTestClient(h, capable)
	if h.hasElicitationClient() {
		t.Fatal("no app said it takes elicitations yet")
	}
	h.onClientHello(capable, []byte(`{"platform":"android","version":"test","features":["elicitation","ws_notify"]}`))
	<-capable.out
	if !h.hasElicitationClient() {
		t.Fatal("the manager would not be told an app takes them")
	}
	ix := proto.Interaction{ID: "ix_example", MachineID: "test-machine", SessionID: "test-session", Kind: proto.IxElicitation,
		Summary: "请选择", ExpiresAt: time.Now().Add(time.Minute), Elicitation: &proto.Elicitation{Mode: "form", Message: "请选择"}}
	reportInteraction(t, h, ix)
	select {
	case raw := <-capable.out:
		var event struct {
			Type        string            `json:"type"`
			Interaction proto.Interaction `json:"interaction"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Type != "interaction" || event.Interaction.Kind != proto.IxElicitation {
			t.Fatalf("event: %s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capable client missed interaction")
	}
	for len(legacy.out) > 0 {
		var event map[string]any
		_ = json.Unmarshal(<-legacy.out, &event)
		if event["type"] == "interaction" || event["type"] == "notify" {
			t.Fatalf("legacy received elicitation: %v", event)
		}
	}
	joining := &client{out: make(chan []byte, 8)}
	h.sendPendingInteractions(joining, false)
	if len(joining.out) != 0 {
		t.Fatal("pre-hello snapshot leaked elicitation")
	}
	h.onClientHello(joining, []byte(`{"features":["elicitation"]}`))
	<-joining.out // ack
	if len(joining.out) != 1 {
		t.Fatal("hello must replay pending elicitation")
	}
	for _, tc := range []struct {
		header string
		count  int
	}{{"", 0}, {"ios/test", 0}, {"android/test", 1}} {
		r := httptest.NewRequest("GET", "https://example.com/api/v1/interactions", nil)
		r.RemoteAddr = net.JoinHostPort(netip.AddrFrom4([4]byte{127, 0, 0, 1}).String(), "1234")
		r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		r.Header.Set("X-Persona-Client", tc.header)
		w := httptest.NewRecorder()
		h.routes().ServeHTTP(w, r)
		var body struct {
			Interactions []proto.Interaction `json:"interactions"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Interactions) != tc.count {
			t.Fatalf("REST %q: %d %s", tc.header, w.Code, w.Body.String())
		}
	}
	reportResolved(t, h, ix.ID, "")
	if len(h.pendingInteractions()) != 0 {
		t.Fatal("the card the manager ended is still listed")
	}
}
