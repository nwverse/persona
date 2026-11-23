package hub

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Partner hubs in one process (partner_*_test.go): hubs on httptest servers over TLS (or plain, for one without a TLS
// listener), each on its own in-memory store (meshStore, which keeps partner_hubs too), with the mesh running so a
// test can see partners never reach it, and no manager unless a test connects one (a scripted one:
// connectPartnerManager; real ones with their nodes: startPartnerE2E).

// partnerHubOpts: store (a restart: the one it had), cert (a TLS key of its own: every httptest server otherwise has
// the same one, so the same pin), noTLS (served without TLS: no key, no https address), partners (hub.json's
// partners), endpoints (the https addresses it hands out instead of its server's), machines (hub.json's machines,
// for a manager a test starts on it: partner_e2e_test.go), managers (a roster that keeps which manager is the
// default: partnerManagers).
type partnerHubOpts struct {
	store     *meshStore
	cert      *tls.Certificate
	noTLS     bool
	partners  PartnersConfig
	endpoints []string
	machines  []MachineConfig
	managers  bool
}

func startPartnerHub(t *testing.T, id, name string, o partnerHubOpts) *testHub {
	t.Helper()
	st := o.store
	if st == nil {
		st = newMeshStore()
	}
	cfg := defaultConfig()
	cfg.dir, cfg.Name, cfg.Partners, cfg.Machines = t.TempDir(), name, o.partners, o.machines
	off := false
	cfg.Mesh.Discovery = &off
	logger := log.New(io.Discard, "", 0)
	if meshLogs {
		logger = log.New(os.Stderr, "["+name+"] ", log.Lmicroseconds)
	}
	h := &Hub{cfg: cfg, version: "test", logger: logger, byID: map[string]*Machine{}, clients: map[*client]struct{}{},
		calls: map[string]chan proto.ToolResult{}, push: newPusher(cfg.dir, nil, func(string, ...any) {})}
	h.live = newLiveActivities(h)
	h.push.hubID = h.hubID
	h.store = st
	h.public.id = id
	h.bus.idents = &memIdents{} // a scripted manager's bus token (connectPartnerManager)
	if o.managers {
		h.bus.idents = &partnerManagers{ids: &memIdents{}, settings: map[string][]byte{}}
	}
	h.partners.retry = [2]time.Duration{10 * time.Millisecond, 50 * time.Millisecond} // a pairings' read failed: again soon
	srv := httptest.NewUnstartedServer(h.routes())
	if o.cert != nil {
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{*o.cert}}
	}
	if o.noTLS {
		srv.Start()
	} else {
		srv.StartTLS()
	}
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	h.public.mu.Lock()
	if !o.noTLS {
		h.public.port, h.public.fp = port, link.Pin(srv.Certificate())
		eps := o.endpoints
		if eps == nil {
			eps = []string{srv.URL}
		}
		for _, e := range eps {
			h.public.endpoints = append(h.public.endpoints, Endpoint{URL: e, Kind: "lan", Port: port, Scheme: "https"})
		}
	} else {
		h.public.endpoints = []Endpoint{{URL: srv.URL, Kind: "lan", Port: port, Scheme: "http"}}
	}
	h.public.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	h.initMesh(ctx)
	h.startMesh(ctx)
	h.initPartners(ctx)
	h.partners.mu.Lock()
	h.partners.ping = [2]time.Duration{300 * time.Millisecond, 2 * time.Second}
	h.partners.mu.Unlock()
	h.startPartners(ctx)
	mh := &testHub{h: h, srv: srv, store: st, cancel: cancel, nodes: map[string]*meshNode{}}
	t.Cleanup(func() {
		h.stopPartnerLinks()
		mh.stop()
	})
	return mh
}

// pairPartners pairs a with b as partner hubs (b makes the code, a uses it) and waits for the link on both sides.
func pairPartners(t *testing.T, a, b *testHub) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code, err := b.h.newPartnerCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.h.joinPartner(ctx, code.Code); err != nil {
		t.Fatalf("join: %v", err)
	}
	waitPartnerLink(t, a, b)
}

// waitPartnerLink waits until a and b both have their link up, the one the duplicate rule keeps: with both dialing,
// the smaller hub_id's (a link the other dialed first is replaced by it once that one's dial is through), else the one
// that dials. That link stays: a link the other hub dials now is refused.
func waitPartnerLink(t *testing.T, a, b *testHub) (*partnerLink, *partnerLink) {
	t.Helper()
	var la, lb *partnerLink
	eventually(t, 20*time.Second, "the partner link between "+a.h.cfg.Name+" and "+b.h.cfg.Name, func() bool {
		kept := keptPartnerDialer(a, b)
		la, lb = a.h.partnerLinkTo(b.id()), b.h.partnerLinkTo(a.id())
		return kept != "" && la != nil && lb != nil && la.dialer == kept && lb.dialer == kept
	})
	return la, lb
}

// cutPartnerLink closes a's end of its link to b (its WebSocket, as a network would).
func cutPartnerLink(t *testing.T, a, b *testHub) {
	t.Helper()
	l := a.h.partnerLinkTo(b.id())
	if l == nil {
		t.Fatalf("%s has no link to %s", a.h.cfg.Name, b.h.cfg.Name)
	}
	_ = l.conn.CloseNow()
}

// keptPartnerDialer is the hub whose dialed link the duplicate rule keeps between a and b ("" when neither dials).
func keptPartnerDialer(a, b *testHub) string {
	ra, _ := a.h.partnerRow(b.id())
	rb, _ := b.h.partnerRow(a.id())
	aDials, bDials := partnerDialable(ra) && a.h.partnersOn(), partnerDialable(rb) && b.h.partnersOn()
	switch {
	case aDials && bDials:
		return min(a.id(), b.id())
	case aDials:
		return a.id()
	case bDials:
		return b.id()
	}
	return ""
}

// partnerManagers is memIdents and the setting of which manager is the default (a managerStore), for a test that makes
// another manager the default.
type partnerManagers struct {
	managerStore // the rest: not used by these tests
	ids          *memIdents
	mu           sync.Mutex
	settings     map[string][]byte
}

func (s *partnerManagers) SaveBusIdentity(ctx context.Context, b db.BusIdentity, hash string) error {
	return s.ids.SaveBusIdentity(ctx, b, hash)
}

func (s *partnerManagers) BusIdentityByHash(ctx context.Context, hash string) (*db.BusIdentity, error) {
	return s.ids.BusIdentityByHash(ctx, hash)
}

func (s *partnerManagers) BusIdentities(ctx context.Context) ([]db.BusIdentity, error) {
	return s.ids.BusIdentities(ctx)
}

func (s *partnerManagers) DeleteBusIdentity(ctx context.Context, id string) (bool, error) {
	return s.ids.DeleteBusIdentity(ctx, id)
}

func (s *partnerManagers) BusIdentityByID(ctx context.Context, id string) (*db.BusIdentity, error) {
	list, _ := s.ids.BusIdentities(ctx)
	for _, b := range list {
		if b.ID == id {
			return &b, nil
		}
	}
	return nil, nil
}

func (s *partnerManagers) SeeBusIdentity(context.Context, string, *db.BusIdentity) error { return nil }

func (s *partnerManagers) Setting(_ context.Context, key string, out any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.settings[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(b, out)
}

func (s *partnerManagers) PutSetting(_ context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = b
	return nil
}

// partnerManager is a scripted manager on a hub's bus: it says capabilities in its hello, answers partner.receive with
// answer, keeps what it got and the partner.changed it heard.
type partnerManager struct {
	peer    *rpc.Peer
	mu      sync.Mutex
	got     []assistantwire.PartnerReceive
	changed []assistantwire.PartnerHubs
	answer  func(assistantwire.PartnerReceive) (any, error)
}

func connectPartnerManager(t *testing.T, mh *testHub, id string, caps []string, answer func(assistantwire.PartnerReceive) (any, error)) *partnerManager {
	t.Helper()
	f := &partnerManager{answer: answer}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ws, err := bus.Dial(ctx, mh.srv.URL, busToken(t, mh.h, id, id), insecureClient)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	conn := bus.NewConn(ctx, ws)
	f.peer = rpc.New("m", conn)
	f.peer.OnEvent = func(event string, params json.RawMessage) {
		if event != assistantwire.EventPartnerHubs {
			return
		}
		var l assistantwire.PartnerHubs
		if json.Unmarshal(params, &l) == nil {
			f.mu.Lock()
			f.changed = append(f.changed, l)
			f.mu.Unlock()
		}
	}
	f.peer.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != assistantwire.MethodPartnerReceive {
			return map[string]any{}, nil
		}
		var p assistantwire.PartnerReceive
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		f.mu.Lock()
		f.got = append(f.got, p)
		answer := f.answer
		f.mu.Unlock()
		if answer == nil {
			return assistantwire.PartnerResult{Status: assistantwire.PartnerAccepted}, nil
		}
		return answer(p)
	}
	go func() { _ = f.peer.Serve(ctx, conn) }()
	cctx, cc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cc()
	if err := f.peer.Call(cctx, busHello, map[string]any{"id": id, "capabilities": caps}, nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *partnerManager) call(method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	return f.peer.Call(ctx, method, params, out)
}

func (f *partnerManager) received() []assistantwire.PartnerReceive {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]assistantwire.PartnerReceive(nil), f.got...)
}

// lastChanged is the newest partner.changed heard, and how many came.
func (f *partnerManager) lastChanged() (assistantwire.PartnerHubs, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.changed) == 0 {
		return assistantwire.PartnerHubs{}, 0
	}
	return f.changed[len(f.changed)-1], len(f.changed)
}

// allChanged is every partner.changed heard, oldest first.
func (f *partnerManager) allChanged() []assistantwire.PartnerHubs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]assistantwire.PartnerHubs(nil), f.changed...)
}

// listed: the newest partner.changed is enabled and names hub alone.
func (f *partnerManager) listed(hub string) bool {
	l, _ := f.lastChanged()
	return l.Enabled && len(l.Hubs) == 1 && l.Hubs[0].HubID == hub
}

// unlisted: a partner.changed came, and the newest is the empty list of a manager that is not the default.
func (f *partnerManager) unlisted() bool {
	l, n := f.lastChanged()
	return n > 0 && !l.Enabled && len(l.Hubs) == 0
}

// partnerCert is a TLS key of the hub's own kind for a test hub (loadOrCreateTLS), and its pin.
func partnerCert(t *testing.T) (*tls.Certificate, string) {
	t.Helper()
	cert, fp, err := loadOrCreateTLS(t.TempDir(), []string{"hub.example.com"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &cert, fp
}
