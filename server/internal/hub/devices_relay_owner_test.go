package hub

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
)

type relayOwnerTestEnv struct {
	h           *Hub
	srv         *httptest.Server
	idents      *memIdents
	openStarted chan struct{}
	releaseOpen chan struct{}
	backendDone chan error
	payload     string
}

type relayOwnerStore struct {
	*memStore
	busStore
}

func newRelayOwnerTestEnv(t *testing.T, storeOverride busStore, payload string) *relayOwnerTestEnv {
	t.Helper()
	idents := &memIdents{}
	var bs busStore = idents
	if storeOverride != nil {
		bs = storeOverride
	}
	store := &relayOwnerStore{memStore: newMemStore(), busStore: bs}
	env := &relayOwnerTestEnv{
		idents:      idents,
		openStarted: make(chan struct{}, 4),
		releaseOpen: make(chan struct{}),
		backendDone: make(chan error, 4),
		payload:     payload,
	}

	linkCtx, linkCancel := context.WithCancel(context.Background())
	t.Cleanup(linkCancel)

	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go func() {
				defer serverConn.Close()
				br := bufio.NewReader(serverConn)
				req, err := http.ReadRequest(br)
				if err != nil {
					env.backendDone <- err
					return
				}
				_ = req.Body.Close()
				env.openStarted <- struct{}{}
				<-env.releaseOpen
				_, err = io.WriteString(serverConn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+nodeUpgrade+"\r\n\r\n")
				if err != nil {
					env.backendDone <- err
					return
				}
				if env.payload != "" {
					_, _ = io.WriteString(serverConn, env.payload)
				}
				var buf [1]byte
				_, rerr := serverConn.Read(buf[:])
				env.backendDone <- rerr
			}()
			return clientConn, nil
		},
	}
	t.Cleanup(transport.CloseIdleConnections)

	h := &Hub{
		cfg:           &Config{},
		logger:        log.New(io.Discard, "", 0),
		store:         store,
		wsConnections: map[*client]struct{}{},
		clients:       map[*client]struct{}{},
	}
	h.devices.kept = map[string]string{"m1": "mgr-old"}
	h.mesh.view.Store(&meshView{
		self: "hub-center",
		hubs: map[string]*viewHub{
			"hub-member": {
				rec:       &hubRecord{Name: "Member"},
				reachable: true,
				hops:      1,
				next:      "hub-member",
			},
		},
	})
	h.mesh.links = map[string]*peerLink{
		"hub-member": {
			h:         h,
			hubID:     "hub-member",
			ctx:       linkCtx,
			transport: transport,
		},
	}
	env.h = h

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+devicewire.RelayPath, h.serveRelay)
	env.srv = httptest.NewServer(mux)
	t.Cleanup(env.srv.Close)
	return env
}

func addTestBusToken(t *testing.T, idents *memIdents, id, tok string) {
	t.Helper()
	err := idents.SaveBusIdentity(context.Background(), db.BusIdentity{
		ID:   id,
		Name: id,
		Role: bus.RoleManager,
	}, hashToken(tok))
	if err != nil {
		t.Fatalf("SaveBusIdentity(%s): %v", id, err)
	}
}

// If a machine moves to another manager while openThroughHub is in flight,
// relayThroughHub must attribute the relay to the authenticated caller (not
// the current machineOwner) so holdRelay rejects it instead of handing a live
// node stream to the displaced manager.
func TestRelayThroughHubRejectsStaleManagerAfterMove(t *testing.T) {
	env := newRelayOwnerTestEnv(t, nil, "secret-node-bytes")
	const oldTok = busTokenPrefix + "old-manager-token"
	addTestBusToken(t, env.idents, "mgr-old", oldTok)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type dialRes struct {
		conn *websocket.Conn
		err  error
	}
	dialed := make(chan dialRes, 1)
	go func() {
		wsURL := "ws" + strings.TrimPrefix(env.srv.URL, "http") + devicewire.RelayPath + "?hub=hub-member&machine=m1"
		c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": {"Bearer " + oldTok}},
		})
		dialed <- dialRes{c, err}
	}()

	select {
	case <-env.openStarted:
	case <-ctx.Done():
		t.Fatal("timed out waiting for openThroughHub to start")
	}

	env.h.devices.mu.Lock()
	env.h.devices.kept["m1"] = "mgr-new"
	env.h.devices.mu.Unlock()
	env.h.cutRelays("", "m1")

	close(env.releaseOpen)

	var conn *websocket.Conn
	select {
	case res := <-dialed:
		if res.err != nil {
			t.Fatalf("dial relay: %v", res.err)
		}
		conn = res.conn
		t.Cleanup(func() { _ = conn.CloseNow() })
	case <-ctx.Done():
		t.Fatal("timed out waiting for relay dial")
	}

	_, data, err := conn.Read(ctx)
	if err == nil {
		t.Fatalf("displaced manager received node data %q after machine moved", string(data))
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusTryAgainLater {
		t.Fatalf("close status = %v (err=%v), want %v", got, err, websocket.StatusTryAgainLater)
	}
	select {
	case berr := <-env.backendDone:
		if berr == nil {
			t.Fatal("expected member stream backend to close")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for member stream backend to close")
	}
}

// If a manager's bus session is revoked while openThroughHub is in flight
// (even before or without changing machineOwner), holdRelay must reject the
// relay using the per-manager cut generation recorded before authentication.
func TestRelayThroughHubRejectsRevokedManagerDuringOpen(t *testing.T) {
	env := newRelayOwnerTestEnv(t, nil, "secret-after-revoke")
	const oldTok = busTokenPrefix + "revoked-manager-token"
	addTestBusToken(t, env.idents, "mgr-old", oldTok)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type dialRes struct {
		conn *websocket.Conn
		err  error
	}
	dialed := make(chan dialRes, 1)
	go func() {
		wsURL := "ws" + strings.TrimPrefix(env.srv.URL, "http") + devicewire.RelayPath + "?hub=hub-member&machine=m1"
		c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": {"Bearer " + oldTok}},
		})
		dialed <- dialRes{c, err}
	}()

	select {
	case <-env.openStarted:
	case <-ctx.Done():
		t.Fatal("timed out waiting for openThroughHub to start")
	}

	if ok, err := env.h.revokeBusToken(ctx, "mgr-old"); err != nil || !ok {
		t.Fatalf("revokeBusToken: ok=%v err=%v", ok, err)
	}
	close(env.releaseOpen)

	var conn *websocket.Conn
	select {
	case res := <-dialed:
		if res.err != nil {
			t.Fatalf("dial relay: %v", res.err)
		}
		conn = res.conn
		t.Cleanup(func() { _ = conn.CloseNow() })
	case <-ctx.Done():
		t.Fatal("timed out waiting for relay dial")
	}

	_, data, err := conn.Read(ctx)
	if err == nil {
		t.Fatalf("revoked manager received node data %q", string(data))
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusTryAgainLater {
		t.Fatalf("close status = %v (err=%v), want %v", got, err, websocket.StatusTryAgainLater)
	}
}

// Cutting an unrelated manager while openThroughHub is in flight must not
// reject the owning manager's stream, and a subsequent cutRelays for the owning
// manager must still sever the live stream.
func TestRelayThroughHubUnrelatedCutAllowedAndOwnerCutSeversLiveStream(t *testing.T) {
	env := newRelayOwnerTestEnv(t, nil, "live-hello")
	const oldTok = busTokenPrefix + "owner-manager-token"
	addTestBusToken(t, env.idents, "mgr-old", oldTok)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type dialRes struct {
		conn *websocket.Conn
		err  error
	}
	dialed := make(chan dialRes, 1)
	go func() {
		wsURL := "ws" + strings.TrimPrefix(env.srv.URL, "http") + devicewire.RelayPath + "?hub=hub-member&machine=m1"
		c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": {"Bearer " + oldTok}},
		})
		dialed <- dialRes{c, err}
	}()

	select {
	case <-env.openStarted:
	case <-ctx.Done():
		t.Fatal("timed out waiting for openThroughHub to start")
	}

	env.h.cutRelays("mgr-unrelated", "")
	close(env.releaseOpen)

	var conn *websocket.Conn
	select {
	case res := <-dialed:
		if res.err != nil {
			t.Fatalf("dial relay: %v", res.err)
		}
		conn = res.conn
		t.Cleanup(func() { _ = conn.CloseNow() })
	case <-ctx.Done():
		t.Fatal("timed out waiting for relay dial")
	}

	typ, data, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(data) != "live-hello" {
		t.Fatalf("live relay read = (%v, %q, %v), want binary %q", typ, string(data), err, "live-hello")
	}

	env.h.cutRelays("mgr-old", "")
	_, _, err = conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusTryAgainLater {
		t.Fatalf("close status after cutRelays(mgr-old) = %v (err=%v), want %v", got, err, websocket.StatusTryAgainLater)
	}
}

type gatedBusStore struct {
	*memIdents
	once    sync.Once
	looked  chan struct{}
	release chan struct{}
}

func (g *gatedBusStore) BusIdentityByHash(ctx context.Context, hash string) (*db.BusIdentity, error) {
	ident, err := g.memIdents.BusIdentityByHash(ctx, hash)
	g.once.Do(func() {
		close(g.looked)
		<-g.release
	})
	return ident, err
}

// If a manager is cut/revoked right between busIdentity lookup and pending
// (?id=) relay delivery — including extension relays with an empty machine ID —
// serveRelay must close the WebSocket with StatusGoingAway and never deliver it.
func TestServeRelayPendingRejectsCutManagerAfterAuth(t *testing.T) {
	idents := &memIdents{}
	gated := &gatedBusStore{
		memIdents: idents,
		looked:    make(chan struct{}),
		release:   make(chan struct{}),
	}
	env := newRelayOwnerTestEnv(t, gated, "")
	const oldTok = busTokenPrefix + "pending-revoked-token"
	addTestBusToken(t, idents, "mgr-old", oldTok)

	id, ch, drop := env.h.offerRelay(relayFor{manager: "mgr-old", machine: ""})
	defer drop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type dialRes struct {
		conn *websocket.Conn
		err  error
	}
	dialed := make(chan dialRes, 1)
	go func() {
		wsURL := "ws" + strings.TrimPrefix(env.srv.URL, "http") + devicewire.RelayPath + "?id=" + id
		c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": {"Bearer " + oldTok}},
		})
		dialed <- dialRes{c, err}
	}()

	select {
	case <-gated.looked:
	case <-ctx.Done():
		t.Fatal("timed out waiting for busIdentity lookup")
	}

	env.h.cutRelays("mgr-old", "")
	close(gated.release)

	var conn *websocket.Conn
	select {
	case res := <-dialed:
		if res.err != nil {
			t.Fatalf("dial pending relay: %v", res.err)
		}
		conn = res.conn
		t.Cleanup(func() { _ = conn.CloseNow() })
	case <-ctx.Done():
		t.Fatal("timed out waiting for pending relay dial")
	}

	_, _, err := conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
		t.Fatalf("close status = %v (err=%v), want %v", got, err, websocket.StatusGoingAway)
	}
	select {
	case taken := <-ch:
		if taken != nil {
			t.Fatal("revoked manager's relay was delivered to offerRelay waiter")
		}
	default:
	}
}
