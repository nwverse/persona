package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/streams"
)

// portNode is a node as the hub sees it for ports: port.dial connects to 127.0.0.1 and streams it, port.probe says
// whether anything is there.
func portNode(t *testing.T, h *Hub, sessions []proto.Session) *Machine {
	t.Helper()
	return portNodeAt(t, h, sessions, func(port int) string { return "127.0.0.1:" + strconv.Itoa(port) })
}

// portNodeAt is portNode whose ports are at addr(port) (the live screen's 5900 at a stand-in for Screen Sharing);
// seen, when given, hears each request.
func portNodeAt(t *testing.T, h *Hub, sessions []proto.Session, addr func(port int) string, seen ...func(method string, params json.RawMessage)) *Machine {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h2nR, h2nW := io.Pipe()
	n2hR, n2hW := io.Pipe()
	hubSide, nodeSide := rpc.New("h", h2nW), rpc.New("n", n2hW)
	hubMux, nodeMux := streams.New(hubSide.Notify), streams.New(nodeSide.Notify)
	hubSide.OnEvent = func(e string, p json.RawMessage) { hubMux.Event(e, p) }
	nodeSide.OnEvent = func(e string, p json.RawMessage) { nodeMux.Event(e, p) }
	nodeSide.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		var p struct {
			ID   string `json:"id"`
			Port int    `json:"port"`
		}
		_ = json.Unmarshal(params, &p)
		for _, f := range seen {
			f(method, params)
		}
		c, err := net.DialTimeout("tcp", addr(p.Port), 2*time.Second)
		switch method {
		case "port.probe":
			if err != nil {
				return map[string]any{"port": p.Port, "listening": false}, nil
			}
			c.Close()
			return map[string]any{"port": p.Port, "listening": true, "http": true}, nil
		case "port.dial":
			if err != nil {
				return nil, &rpc.Error{Code: "port_gone", Message: "nothing is listening"}
			}
			if err := nodeMux.Open(p.ID); err != nil {
				c.Close()
				return nil, err
			}
			nodeMux.Start(p.ID, c)
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("unknown method %s", method)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = hubSide.Serve(ctx, n2hR) }()
	go func() { defer wg.Done(); _ = nodeSide.Serve(ctx, h2nR) }()
	t.Cleanup(func() { cancel(); h2nW.Close(); n2hW.Close(); wg.Wait() })

	m := newMachine(h, MachineConfig{ID: "m1", Name: "dev box"})
	m.peer, m.online, m.streams = hubSide, true, hubMux
	m.sessions = sessions
	h.byID["m1"] = m
	return m
}

// devServer is what an agent runs: a page, a login that redirects and sets a cookie, a page reading it, and a
// WebSocket that echoes (HMR's channel).
func devServer(t *testing.T) (*httptest.Server, int) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<title>Dev</title><script src=\"/@vite/client\"></script> host=%s", r.Host)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s3cr3t", Path: "/"})
		http.Redirect(w, r, "/me", http.StatusFound)
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("sid")
		if err != nil {
			http.Error(w, "no cookie", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, "hello "+c.Value)
	})
	mux.HandleFunc("/hmr", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(1 << 20)
		for {
			typ, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, append([]byte("echo:"), b...)); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Listener.Addr().(*net.TCPAddr).Port
}

// phone listens on loopback the way the app does and opens one WebSocket to the hub per connection.
func phone(t *testing.T, hubURL, token string, port int) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ws := "ws" + strings.TrimPrefix(hubURL, "http") + "/api/v1/machines/m1/ports/" + strconv.Itoa(port) + "/connect"
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				wc, _, err := websocket.Dial(ctx, ws, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
				if err != nil {
					return
				}
				nc := websocket.NetConn(ctx, wc, websocket.MessageBinary)
				defer nc.Close()
				go func() { _, _ = io.Copy(nc, c); nc.Close() }()
				_, _ = io.Copy(c, nc)
			}()
		}
	}()
	return ln.Addr().String()
}

func portHub(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	h := newTestHub(t)
	h.cfg.Token = "test-token"
	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)
	return h, srv
}

// An agent's dev server through the hub: the page with the browser's own Host, a redirect setting a cookie that the
// next page reads, and a WebSocket both ways.
func TestForwardedPortCarriesHTTPAndWebSocket(t *testing.T) {
	h, srv := portHub(t)
	_, port := devServer(t)
	portNode(t, h, []proto.Session{{SessionID: "s1", Ports: []proto.Port{{Port: port, Title: "Dev"}}}})
	local := phone(t, srv.URL, "test-token", port)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	res, err := client.Get("http://" + local + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "<title>Dev</title>") || !strings.Contains(string(body), "host="+local) {
		t.Fatalf("page = %q (the Host must be the one the browser used)", body)
	}

	res, err = client.Get("http://" + local + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.Request.URL.Path != "/me" || string(body) != "hello s3cr3t" {
		t.Fatalf("after the redirect: %s %q", res.Request.URL, body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+local+"/hmr", nil)
	if err != nil {
		t.Fatalf("websocket through the forward: %v", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	for _, msg := range []string{"update", strings.Repeat("x", 200<<10)} {
		if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		_, got, err := c.Read(ctx)
		if err != nil || string(got) != "echo:"+msg {
			t.Fatalf("echo of %.10q: %.20q %v", msg, got, err)
		}
	}
}

// The hub refuses before the upgrade: a bad token, a read-only device, a port no agent listens on and nobody opened,
// and an allowed port with nothing on it. Opening a port that answers allows it.
func TestForwardedPortRefusals(t *testing.T) {
	h, srv := portHub(t)
	_, agentPort := devServer(t)
	_, ownPort := devServer(t) // the user's own server: no agent's
	portNode(t, h, []proto.Session{{SessionID: "s1", Ports: []proto.Port{{Port: agentPort}}}})
	connect := func(port int, token string) int {
		req, _ := http.NewRequest("GET", srv.URL+"/api/v1/machines/m1/ports/"+strconv.Itoa(port)+"/connect", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	post := func(path string) int {
		req, _ := http.NewRequest("POST", srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer test-token")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	if got := connect(agentPort, "wrong"); got != http.StatusUnauthorized {
		t.Errorf("bad token: %d", got)
	}
	if got := connect(agentPort, "test-token"); got != http.StatusSwitchingProtocols {
		t.Errorf("the agent's port: %d", got)
	}
	if got := connect(ownPort, "test-token"); got != http.StatusForbidden {
		t.Errorf("a port nobody opened: %d", got)
	}
	if got := connect(22, "test-token"); got != http.StatusForbidden {
		t.Errorf("ssh: %d", got)
	}
	if got := post("/api/v1/machines/m1/ports/" + strconv.Itoa(ownPort) + "/open"); got != http.StatusOK {
		t.Errorf("open the user's server: %d", got)
	}
	if got := connect(ownPort, "test-token"); got != http.StatusSwitchingProtocols {
		t.Errorf("an opened port: %d", got)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if got := post("/api/v1/machines/m1/ports/" + strconv.Itoa(closed) + "/open"); got != http.StatusNotFound {
		t.Errorf("open a closed port: %d", got)
	}
	h.byID["m1"].sessions[0].Ports = append(h.byID["m1"].sessions[0].Ports, proto.Port{Port: closed}) // listed, then stopped
	if got := connect(closed, "test-token"); got != http.StatusNotFound {
		t.Errorf("a listed port that is gone: %d", got)
	}
	if got := post("/api/v1/machines/m1/ports/0/open"); got != http.StatusBadRequest {
		t.Errorf("port 0: %d", got)
	}

	// A read-only device (the watch) cannot connect even to an agent's port.
	ro := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/machines/m1/ports/"+strconv.Itoa(agentPort)+"/connect", nil)
	req.SetPathValue("mid", "m1")
	req.SetPathValue("port", strconv.Itoa(agentPort))
	req = req.WithContext(context.WithValue(req.Context(), principalKey{}, principal{Role: roleAnswer}))
	h.withMachine(h.connectPort)(ro, req)
	if ro.Code != http.StatusForbidden {
		t.Errorf("read-only device: %d", ro.Code)
	}
}

// The live screen as the app opens it (#1498): the page's WebSocket carries this device's token in ?token= (a page
// cannot set a WebSocket's headers) and reaches the Mac's Screen Sharing through the relay. A wrong token, the app's
// as it arrived before (encoded twice: jd%5F…) and an answer-only device's are refused before anything is dialed.
func TestTheLiveScreenTakesTheAppsDeviceToken(t *testing.T) {
	ctx := context.Background()
	h := newTestHub(t)
	h.store = &tokenStore{memStore: *newMemStore(), rows: map[string]db.DeviceToken{}, rev: map[string]bool{}}
	phone, watch := "jd_"+randomHex(24), "jd_"+randomHex(24)
	_ = h.store.CreateDeviceToken(ctx, db.DeviceToken{ID: "dev_phone", Name: "iPhone", Kind: "phone", Role: roleFull}, hashToken(phone))
	_ = h.store.CreateDeviceToken(ctx, db.DeviceToken{ID: "dev_watch", Name: "Watch", Kind: "watch", Role: roleAnswer}, hashToken(watch))

	// Screen Sharing speaks first: its protocol version.
	vnc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vnc.Close() })
	var dialed atomic.Int32
	go func() {
		for {
			c, err := vnc.Accept()
			if err != nil {
				return
			}
			dialed.Add(1)
			_, _ = c.Write([]byte("RFB 003.889\n"))
			go func() { _, _ = io.Copy(io.Discard, c); c.Close() }()
		}
	}()
	var dials atomic.Int32 // port.dial asking the node to keep the Mac awake while it is watched
	mac := portNodeAt(t, h, nil, func(port int) string {
		if port == screenPort {
			return vnc.Addr().String()
		}
		return "127.0.0.1:1"
	}, func(method string, params json.RawMessage) {
		var p struct {
			KeepAwake bool `json:"keep_awake"`
		}
		if method == "port.dial" && json.Unmarshal(params, &p) == nil && p.KeepAwake {
			dials.Add(1)
		}
	})
	mac.mu.Lock()
	mac.info = proto.NodeInfo{OS: "darwin"}
	mac.mu.Unlock()
	h.mu.Lock()
	h.machines = append(h.machines, mac)
	h.mu.Unlock()
	// The plugin cua is on for m1 and keeps a watched Mac awake (its default): the manager's report says so.
	h.ext.report.Store(&extwire.Report{Machines: map[string]extwire.MachineExtras{"m1": {ScreenPort: true}}, ScreenKeepAwake: true})

	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)
	open := func(token string) (*websocket.Conn, int) {
		u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/machines/m1/ports/5900/connect?token=" + url.QueryEscape(token)
		dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		c, res, err := websocket.Dial(dctx, u, &websocket.DialOptions{Subprotocols: []string{"binary"}})
		if err != nil {
			if res == nil {
				t.Fatal(err)
			}
			return nil, res.StatusCode
		}
		return c, http.StatusSwitchingProtocols
	}

	c, code := open(phone)
	if c == nil {
		t.Fatalf("the app's device token: %d", code)
	}
	defer c.CloseNow()
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, b, err := c.Read(rctx); err != nil || string(b) != "RFB 003.889\n" {
		t.Fatalf("Screen Sharing's greeting through the relay: %q %v", b, err)
	}
	if dials.Load() != 1 {
		t.Fatal("someone is watching: the node keeps the Mac awake (「操作或开着 VNC 时保持唤醒」, on unless turned off)")
	}

	before := dialed.Load()
	for _, bad := range []struct {
		why   string
		token string
		want  int
	}{
		{"a token the hub never gave", "jd_" + randomHex(24), http.StatusUnauthorized},
		{"the app's, encoded twice", strings.Replace(phone, "_", "%5F", 1), http.StatusUnauthorized},
		{"an answer-only device's", watch, http.StatusForbidden},
	} {
		if c, code := open(bad.token); c != nil || code != bad.want {
			if c != nil {
				c.CloseNow()
			}
			t.Errorf("%s: %d, want %d", bad.why, code, bad.want)
		}
	}
	if dialed.Load() != before {
		t.Fatal("refused before anything is dialed")
	}
}

// A node's streams outlive a hub restart, and it refuses for a while an id it heard closed before it opened
// (streams.Mux.Open): a restarted hub (a new generator) never hands out an id the last one did.
func TestStreamIDsDifferAcrossHubRestarts(t *testing.T) {
	before, after := newStreamIDs(), newStreamIDs()
	seen := map[string]bool{}
	for _, ids := range []*streamIDs{before, after} {
		for _, kind := range []string{"p", "v", "p"} {
			id := ids.next(kind)
			if !strings.HasPrefix(id, kind) || seen[id] {
				t.Fatalf("id %q: want a new one starting with %q (had %v)", id, kind, seen)
			}
			seen[id] = true
		}
	}
	if before.token == "" || before.token == after.token {
		t.Fatalf("tokens %q and %q", before.token, after.token)
	}
}
