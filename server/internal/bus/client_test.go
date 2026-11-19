package bus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// logLines keeps a client's log for a test to look at.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) count(part string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, part) {
			n++
		}
	}
	return n
}

func (l *logLines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// waitFor returns once a line with part was logged, failing the test after within.
func (l *logLines) waitFor(t *testing.T, part string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for l.count(part) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the log within %s", part, within)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// tryEnded is what Run logs after each try that failed.
const tryEnded = "the connection to the hub ended"

// freeAddr is a loopback address nothing listens on (yet).
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// listenAt listens at addr (a port freeAddr gave or a server let go).
func listenAt(t *testing.T, addr string) net.Listener {
	t.Helper()
	var err error
	for range 50 {
		var l net.Listener
		if l, err = net.Listen("tcp", addr); err == nil {
			return l
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listen again at %s: %v", addr, err)
	return nil
}

// serveOn serves h on l.
func serveOn(l net.Listener, h http.Handler) *httptest.Server {
	srv := httptest.NewUnstartedServer(h)
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	return srv
}

// runClient runs c until the test ends, then cancels it and waits for Run to return.
func runClient(t *testing.T, c *Client) (ctx context.Context, cancel context.CancelFunc, ran <-chan error) {
	t.Helper()
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	done, returned := make(chan error, 1), make(chan struct{})
	go func() {
		done <- c.Run(ctx)
		close(returned)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after its context ended")
		}
	})
	return ctx, cancel, done
}

// A client whose backoff is at its cap (a laptop that slept through failed tries) is back within moments of the hub
// answering again, not after the rest of its minute: one full try, once the probe finds the port.
func TestClientReconnectsSoonAfterTheHubComesBack(t *testing.T) {
	addr := freeAddr(t)
	var log logLines
	c := &Client{Hub: "http://" + addr, Token: "t", HelloMethod: "manager.hello", Logf: log.logf,
		Backoff: time.Minute, MaxBackoff: time.Minute, probeEvery: 20 * time.Millisecond}
	ctx, _, _ := runClient(t, c)
	log.waitFor(t, "again in 1m0s", 5*time.Second)
	time.Sleep(100 * time.Millisecond) // a few probes find nothing

	f := &fakeHub{}
	srv := serveOn(listenAt(t, addr), f)
	defer srv.Close()
	back := time.Now()
	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()
	if err := c.Wait(wctx); err != nil {
		t.Fatalf("not connected %s after the hub came back (the backoff is a minute): %v", time.Since(back), err)
	}
	if n := f.dials.Load(); n != 1 {
		t.Errorf("%d full tries once the hub answered, want 1", n)
	}
	if log.count("the hub answers again") != 1 {
		t.Errorf("the log does not say why it tried early: %q", log.all())
	}
}

// countingDial dials as net.Dialer does and counts the dials: the full tries' and the probes'.
func countingDial(n *atomic.Int32) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		n.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
}

// A hub that stays out of reach gets only probes between full tries, and the full tries keep the backoff's pace.
func TestClientProbesWhileTheHubStaysDown(t *testing.T) {
	var dials atomic.Int32
	var log logLines
	c := &Client{Hub: "http://" + freeAddr(t), Token: "t", HelloMethod: "manager.hello", Logf: log.logf,
		HTTPClient: &http.Client{Transport: &http.Transport{DialContext: countingDial(&dials)}},
		Backoff:    100 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
		probeOver: time.Nanosecond, probeEvery: 10 * time.Millisecond}
	_, cancel, ran := runClient(t, c)
	time.Sleep(time.Second)
	cancel()
	<-ran
	tries := log.count(tryEnded)
	probes := int(dials.Load()) - tries
	t.Logf("%d full tries, %d probes", tries, probes)
	// Waits of 100 ms, then 200 ms: about six tries in the second.
	if tries < 3 || tries > 8 {
		t.Fatalf("%d full tries in a second with waits of 100–200 ms", tries)
	}
	if probes < 3*tries {
		t.Fatalf("%d probes between %d full tries, want several in each wait", probes, tries)
	}
	if log.count("the hub answers again") != 0 {
		t.Fatal("a wait ended early while the hub was down")
	}
}

// A hub whose port takes connections but drops the handshake (a reset mid-way, a proxy in front of a hub that is
// gone) keeps the doubling backoff: its port answered as each wait began, so no wait ends early, and it sees one
// probe a wait, not one every probeEvery.
func TestClientKeepsBackoffWhenThePortAnswersButNotTheBus(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var tries, probes atomic.Int32
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				if n, _ := conn.Read(make([]byte, 512)); n > 0 { // a try's request: hang up on it
					tries.Add(1)
				} else { // a probe says nothing
					probes.Add(1)
				}
			}()
		}
	}()
	var log logLines
	c := &Client{Hub: "http://" + l.Addr().String(), Token: "t", HelloMethod: "manager.hello", Logf: log.logf,
		Backoff: 100 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
		probeOver: time.Nanosecond, probeEvery: 10 * time.Millisecond}
	_, cancel, ran := runClient(t, c)
	time.Sleep(time.Second)
	cancel()
	<-ran
	time.Sleep(50 * time.Millisecond) // the last connections counted
	n, p := int(tries.Load()), int(probes.Load())
	t.Logf("%d full tries, %d probes", n, p)
	if n < 3 || n > 8 {
		t.Fatalf("%d full tries in a second with waits of 100–200 ms (a try every probe would be dozens)", n)
	}
	if p < 1 || p > n+1 {
		t.Fatalf("%d probes for %d waits, want one a wait", p, n)
	}
	if log.count("the hub answers again") != 0 {
		t.Fatal("a wait ended early though the port answered as it began")
	}
}

// A refused token waits MaxBackoff whole, even when the hub goes away and comes back during the wait: only someone
// fixing the token helps, and the probe would only bring its next refusal sooner.
func TestClientRejectedTokenKeepsItsWait(t *testing.T) {
	addr := freeAddr(t)
	f := &fakeHub{}
	f.refuse.Store(true)
	f.plain.Store(true)
	var gone sync.Once
	l := listenAt(t, addr)
	srv := serveOn(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gone.Do(func() { _ = l.Close() }) // out of reach before the client hears the refusal
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	var log logLines
	c := &Client{Hub: "http://" + addr, Token: "t", HelloMethod: "manager.hello", Logf: log.logf,
		Backoff: 10 * time.Millisecond, MaxBackoff: time.Minute, probeEvery: 10 * time.Millisecond}
	runClient(t, c)
	log.waitFor(t, "again in 1m0s", 5*time.Second)
	time.Sleep(50 * time.Millisecond)

	f2 := &fakeHub{}
	f2.refuse.Store(true)
	f2.plain.Store(true)
	srv2 := serveOn(listenAt(t, addr), f2)
	defer srv2.Close()
	time.Sleep(500 * time.Millisecond)
	if n := f.dials.Load() + f2.dials.Load(); n != 1 {
		t.Fatalf("%d tries with a refused token within half a second, want 1 (every %s)", n, c.MaxBackoff)
	}
}

// Run returns as soon as its context ends: between probes, and in the middle of one that hangs.
func TestClientPauseEndsWithItsContext(t *testing.T) {
	t.Run("between probes", func(t *testing.T) {
		var log logLines
		c := &Client{Hub: "http://" + freeAddr(t), Token: "t", HelloMethod: "manager.hello", Logf: log.logf,
			Backoff: time.Minute, MaxBackoff: time.Minute, probeEvery: 20 * time.Millisecond}
		_, cancel, ran := runClient(t, c)
		log.waitFor(t, tryEnded, 5*time.Second)
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case err := <-ran:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run returned %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Run went on waiting after its context ended")
		}
	})
	t.Run("in a probe", func(t *testing.T) {
		var dials atomic.Int32
		probing := make(chan struct{})
		var once sync.Once
		dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
			if dials.Add(1) == 1 {
				return nil, errors.New("down") // the try
			}
			once.Do(func() { close(probing) }) // a probe: nothing answers, for as long as it may wait (a minute)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		c := &Client{Hub: "http://hub.example", Token: "t", HelloMethod: "manager.hello",
			HTTPClient: &http.Client{Transport: &http.Transport{DialContext: dial}},
			Backoff:    time.Minute, MaxBackoff: time.Minute, probeEvery: time.Minute}
		_, cancel, ran := runClient(t, c)
		select {
		case <-probing:
		case <-time.After(5 * time.Second):
			t.Fatal("no probe")
		}
		cancel()
		select {
		case err := <-ran:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Run returned %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Run waited out the probe after its context ended")
		}
	})
}

func TestHubAddr(t *testing.T) {
	for _, c := range []struct{ hub, scheme, addr string }{
		{"http://hub.example", "http", "hub.example:80"},
		{"http://hub.example:18660", "http", "hub.example:18660"},
		{"https://hub.example/base/", "https", "hub.example:443"},
		{"ws://hub.example", "http", "hub.example:80"},
		{"wss://[::1]:8443", "https", "[::1]:8443"},
	} {
		u, addr, err := hubAddr(c.hub)
		if err != nil || u.Scheme != c.scheme || addr != c.addr {
			t.Errorf("hubAddr(%q) = %v, %q, %v; want scheme %s, %q", c.hub, u, addr, err, c.scheme, c.addr)
		}
	}
	if _, _, err := hubAddr("hub.example"); err == nil {
		t.Error("a hub without a scheme has an address")
	}
}

type otherTransport struct{}

func (otherTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, errors.New("no") }

// The probe dials the way the client's connections are dialed, and not at all where its connection would not be to
// the hub itself.
func TestProberFollowsTheClientsRoute(t *testing.T) {
	proxy, _ := url.Parse("http://proxy.example:3128")
	noProxy := func(*http.Request) (*url.URL, error) { return nil, nil }
	tlsDial := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("no") }
	for _, c := range []struct {
		name string
		hub  string
		hc   *http.Client
		want bool
	}{
		// The default transport sends nothing on the loopback through a proxy, whatever the environment says.
		{"default client, loopback hub", "http://127.0.0.1:18660", nil, true},
		{"through a proxy", "https://hub.example", &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}, false},
		{"a proxy func that says direct", "https://hub.example", &http.Client{Transport: &http.Transport{Proxy: noProxy}}, true},
		{"another kind of transport", "https://hub.example", &http.Client{Transport: otherTransport{}}, false},
		{"its own TLS dial", "https://hub.example", &http.Client{Transport: &http.Transport{DialTLSContext: tlsDial}}, false},
		{"its own TLS dial, plain http", "http://hub.example", &http.Client{Transport: &http.Transport{DialTLSContext: tlsDial}}, true},
		{"a bad hub url", "hub.example", nil, false},
	} {
		got := (&Client{Hub: c.hub, HTTPClient: c.hc}).prober() != nil
		if got != c.want {
			t.Errorf("%s: probes %v, want %v", c.name, got, c.want)
		}
	}

	// A transport's own dial (a pinned client's) is the probe's, to the hub's host and port.
	var dialed []string
	var mu sync.Mutex
	hc := &http.Client{Transport: &http.Transport{DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, network+" "+addr)
		mu.Unlock()
		return nil, errors.New("down")
	}}}
	reach := (&Client{Hub: "https://hub.example", HTTPClient: hc}).prober()
	if reach == nil || reach(context.Background(), time.Second) {
		t.Fatal("a dial that fails is a hub out of reach")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != 1 || dialed[0] != "tcp hub.example:443" {
		t.Fatalf("dialed %q", dialed)
	}
}
