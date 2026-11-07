package link

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// NodeDownMarker is in the error a computer's link reports when its resident node is not running (nodeMissing,
// listen.go): the hub reads it as error_code node_service_down (hub/machine_trust.go machineErrorCode). An older link
// says it the same way, so it is kept as it is.
const NodeDownMarker = "常驻 node 没在运行"

// NodeDownSelf comes right before NodeDownMarker in nodeMissing: the link's own computer. The hub swaps it for the
// computer's name on the machine's page (devices nodeUnavailable, #4080).
const NodeDownSelf = "本机的"

// nodeMissing says the resident node is not answering at target, and how this computer runs it (docs/operations/link.md "Installing
// the node service"): the hub shows it on the machine's page.
func nodeMissing(goos, target string) error {
	how := "让 personad node --attach 一直跑"
	switch goos {
	case "windows":
		how = "persona-link install --personad <personad.exe 的路径> 装成登录时启动的计划任务（docs/operations/windows.md）"
	case "darwin":
		how = "菜单栏 App「Persona Link」里的「安装本机 node 服务」，或者 persona-link node install"
	case "linux":
		how = "persona-link node install（systemd 用户服务）"
	}
	return fmt.Errorf("%s%s（%s 连不上）：%s", NodeDownSelf, NodeDownMarker, target, how)
}

// These are settings, not measurements.
const (
	stagger     = 300 * time.Millisecond // between starting one candidate and the next (docs/operations/link.md "Route selection")
	dialTimeout = 8 * time.Second        // one candidate: TCP, TLS and the WebSocket handshake
	checkPing   = 3 * time.Second        // a ping after the network changed
	refusedWait = 5 * time.Minute        // after the hub refused the credential, unless link.json changes
	replaceWait = 30 * time.Second       // after another persona-link of this machine took over
)

// errPairedAgain: link.json holds another credential than the link's (paired again meanwhile): nothing of this link's
// is written into it.
var errPairedAgain = errors.New("paired again meanwhile")

var (
	errPin    = errors.New("TLS 公钥指纹和配对时的不一样，不是那台 hub")
	errRedial = errors.New("手动重连")
)

// Status is what persona-link reports: Run hands one to Options.Status every time something changes, and
// cmd/persona-link prints each as a JSON line for the menu bar app.
type Status struct {
	// State: unpaired | connecting | connected | waiting | refused; passive mode: unpaired | listening (paired, no
	// stream from the hub open) | connected (one is) | waiting (cannot listen).
	State string `json:"state"`
	// Mode: active (this computer dials the hub) | passive (the hub dials it).
	Mode    string `json:"mode,omitempty"`
	Machine string `json:"machine,omitempty"`
	// HubName: what the hub calls itself, once a hub that says it has (link.json keeps it). MeshHubs, while
	// connected: how many hubs its mesh has, itself included. Both missing with older hubs.
	HubName  string `json:"hub_name,omitempty"`
	MeshHubs int    `json:"mesh_hubs,omitempty"`
	// MachineName: what the hub calls this computer, once a hub that says it has (link.json keeps it); missing with
	// older hubs, which show Machine (its id) instead.
	MachineName string `json:"machine_name,omitempty"`
	// Rename: how the last rename asked of this persona-link went (RequestRename waits for it by its ID).
	Rename *RenameResult `json:"rename,omitempty"`
	// Pending, while connected: the hub waits for the user to confirm this computer ("It's mine" in the app);
	// missing once confirmed, and with older hubs.
	Pending bool `json:"pending,omitempty"`
	// connected: the hub URL in use, its kind (lan | ipv6 | name | ipv4) and since when.
	Address string     `json:"address,omitempty"`
	Kind    string     `json:"kind,omitempty"`
	Since   *time.Time `json:"since,omitempty"`
	// Trust: "system" when the hub is checked with the system's CAs and host name (it sits behind an HTTPS reverse
	// proxy, Config.Trust); empty when it is pinned.
	Trust string `json:"trust,omitempty"`
	// connected: whether the hub got through to the resident node (online | offline | unknown), and why not.
	Node      string `json:"node,omitempty"`
	NodeError string `json:"node_error,omitempty"`
	// waiting, refused: why the last attempt or link failed, and when the next attempt is.
	Error string     `json:"error,omitempty"`
	Retry *time.Time `json:"retry,omitempty"`
	// The candidates, best first; never the credential.
	Endpoints []Endpoint `json:"endpoints,omitempty"`
	// Passive mode: where it listens; this computer's addresses the hub may use (best first, host:port); when the hub
	// last came in and from where; until when a pairing code from `pair --listen` is good. Connected: Address is the
	// hub's address and Since when its stream opened.
	Listen      string     `json:"listen,omitempty"`
	Addrs       []string   `json:"addrs,omitempty"`
	LastSeen    *time.Time `json:"last_seen,omitempty"`
	From        string     `json:"from,omitempty"`
	CodeExpires *time.Time `json:"code_expires,omitempty"`
}

// Options are what Run needs from its caller.
type Options struct {
	Path    string // link.json
	Target  string // the resident node's socket
	Version string
	Status  func(Status)
	// Check: look at the link now (the network changed). Redial: drop it and connect again.
	Check, Redial <-chan struct{}
	Logf          func(string, ...any)
	// PingEvery, PingTimeout replace the defaults (tests). A hub that says its own heartbeat (endpoints, PingHeader) is
	// followed on that link or stream instead.
	PingEvery, PingTimeout time.Duration
	// limiter replaces passive mode's (tests).
	limiter *authLimiter
}

// httpError is a refused handshake: 401 / 403 mean the credential is no good, 429 that the hub wants a pause.
type httpError struct {
	status int
	retry  time.Duration
}

func (e *httpError) Error() string { return "HTTP " + strconv.Itoa(e.status) }

func handshakeError(resp *http.Response, err error) error {
	if resp == nil || resp.StatusCode == http.StatusSwitchingProtocols {
		return err
	}
	he := &httpError{status: resp.StatusCode}
	if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil {
		he.retry = time.Duration(s) * time.Second
	}
	return he
}

// Pinned is an HTTP/1.1 client that only talks to the hub whose TLS key hashes to fp (Pin): what a manager on another
// computer dials the hub's public port with (docs/assistant/managers.md).
func Pinned(fp string) *http.Client { return pinned(fp, nil) }

// pinned is an HTTP/1.1 client that only talks to the hub whose TLS key hashes to fp. onConn sees each new
// connection (the link keeps its local address, to tell when the network under it is gone).
func pinned(fp string, onConn func(net.Conn)) *http.Client {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				raw, err := d.DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				host, _, _ := net.SplitHostPort(addr)
				cfg := &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
					InsecureSkipVerify: true, // the pin below is the check
					VerifyConnection: func(cs tls.ConnectionState) error {
						if len(cs.PeerCertificates) == 0 {
							return errPin
						}
						sum := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
						if base64.RawURLEncoding.EncodeToString(sum[:]) != fp {
							return errPin
						}
						return nil
					}}
				if net.ParseIP(host) == nil {
					cfg.ServerName = host
				}
				c := tls.Client(raw, cfg)
				if err := c.HandshakeContext(ctx); err != nil {
					raw.Close()
					return nil, err
				}
				if onConn != nil {
					onConn(raw)
				}
				return c, nil
			},
			ResponseHeaderTimeout: dialTimeout,
			MaxIdleConns:          1,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// systemRoots replaces the system's CAs for systemTrusted (UseRootsForTesting); nil means the system's.
var systemRoots atomic.Pointer[x509.CertPool]

// UseRootsForTesting makes the system-trust client trust pool instead of the system's CAs, until restore is called.
// For tests (a reverse proxy with a test CA), never the keychain.
func UseRootsForTesting(pool *x509.CertPool) (restore func()) {
	old := systemRoots.Swap(pool)
	return func() { systemRoots.Store(old) }
}

// systemTrusted is the HTTP/1.1 client for a hub without a pin: it sits behind an HTTPS reverse proxy whose
// certificate comes from a public CA (docs/operations/link.md "Hub behind a reverse proxy"). The certificate must chain to the system's
// CAs and name the host dialed; nothing else is accepted. onConn is pinned's.
func systemTrusted(onConn func(net.Conn)) *http.Client {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				raw, err := d.DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				host, _, _ := net.SplitHostPort(addr)
				c := tls.Client(raw, &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
					ServerName: host, RootCAs: systemRoots.Load()})
				if err := c.HandshakeContext(ctx); err != nil {
					raw.Close()
					return nil, err
				}
				if onConn != nil {
					onConn(raw)
				}
				return c, nil
			},
			ResponseHeaderTimeout: dialTimeout,
			MaxIdleConns:          1,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// hubClient is how c's hub is checked: pinned to its key when the pairing gave one (never anything else then), else
// with the system's CAs.
func hubClient(c *Config, onConn func(net.Conn)) *http.Client {
	if c.FP != "" {
		return pinned(c.FP, onConn)
	}
	return systemTrusted(onConn)
}

// trustOf is Status.Trust for c.
func trustOf(c *Config) string {
	if c.FP == "" {
		return TrustSystem
	}
	return ""
}

// wsURL joins path under base: base keeps the path a reverse proxy puts the hub under.
func wsURL(base, path string) string {
	return "wss" + strings.TrimPrefix(base, "https") + path
}

func bearer(token string) http.Header { return http.Header{"Authorization": {"Bearer " + token}} }

// race tries the candidates best first, starting the next one stagger after the previous, and keeps the first that
// works; drop closes any that succeed after it.
func race[T any](ctx context.Context, eps []Endpoint, try func(context.Context, Endpoint) (T, error), drop func(T)) (T, Endpoint, error) {
	eps = ranked(eps)
	var zero T
	if len(eps) == 0 {
		return zero, Endpoint{}, errors.New("没有 hub 的候选地址：重新配对，或用 persona-link address add 加一个")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		v   T
		ep  Endpoint
		err error
	}
	results := make(chan result, len(eps))
	for i, ep := range eps {
		go func() {
			select {
			case <-time.After(time.Duration(i) * stagger):
			case <-ctx.Done():
				results <- result{ep: ep, err: ctx.Err()}
				return
			}
			actx, acancel := context.WithTimeout(ctx, dialTimeout)
			defer acancel()
			v, err := try(actx, ep)
			results <- result{v, ep, err}
		}()
	}
	var errs []error
	for left := len(eps); left > 0; left-- {
		r := <-results
		if r.err == nil {
			go func(n int) {
				for ; n > 0; n-- {
					if late := <-results; late.err == nil {
						drop(late.v)
					}
				}
			}(left - 1)
			return r.v, r.ep, nil
		}
		if !errors.Is(r.err, context.Canceled) {
			errs = append(errs, &attemptError{r.ep, r.err})
		}
	}
	if len(errs) == 0 { // every attempt was called off: ctx ended (errors.Join of nothing would be no error)
		return zero, Endpoint{}, cmp.Or(ctx.Err(), context.Canceled)
	}
	return zero, Endpoint{}, errors.Join(errs...)
}

// parsePairLink reads what `personad hub pair` printed: persona://link?… (made for one machine) or persona://pair?…
// (the apps' link; then the machine is named on the command line).
func parsePairLink(raw string) (token string, c Config, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !pairScheme(u.Scheme) || (u.Host != "link" && u.Host != "pair") {
		return "", c, errors.New("这不是 personad hub pair 给的配对链接（persona://link?… 或 persona://pair?…）")
	}
	q := u.Query()
	token, c.Hub, c.FP, c.Machine = q.Get("token"), q.Get("hub"), q.Get("fp"), q.Get("machine")
	if token == "" {
		return "", c, errors.New("配对链接里没有 token")
	}
	if c.FP != "" {
		// The hub's own TLS port, pinned: url names the host, tls the port (the hub serves the API at its root).
		port := q.Get("tls")
		if port == "" {
			return "", c, errors.New("配对链接有指纹却没有 TLS 端口：在 hub 上重新运行 personad hub pair --machine")
		}
		if base, err := url.Parse(q.Get("url")); err == nil && base.Hostname() != "" {
			u := "https://" + net.JoinHostPort(base.Hostname(), port)
			c.Endpoints = append(c.Endpoints, Endpoint{URL: u, Kind: kindOf(u)})
		}
	} else {
		// No pin: the hub has no TLS of its own and url is the HTTPS reverse proxy in front of it, path and all.
		e, err := proxyEndpoint(q.Get("url"))
		if err != nil {
			return "", c, errors.New("这台 hub 没开 TLS（hub.json 的 public.tls_listen），public_url 也不是 https 的反向代理地址：电脑链路只走 HTTPS")
		}
		c.Trust = TrustSystem
		c.Endpoints = append(c.Endpoints, e)
	}
	for _, alt := range q["alt"] {
		if e, err := normalize(alt, nil); err == nil {
			e.Manual = false
			c.Endpoints = append(c.Endpoints, e)
		}
	}
	if len(c.Endpoints) == 0 {
		return "", c, errors.New("配对链接里没有 hub 的地址")
	}
	return token, c, nil
}

// proxyEndpoint is the candidate for a hub behind an HTTPS reverse proxy: its https URL with the port spelled out (as
// the hub's own list has it) and the path prefix kept.
func proxyEndpoint(raw string) (Endpoint, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "https://") {
		return Endpoint{}, errors.New("not an https URL")
	}
	e, err := normalize(raw, nil)
	if err != nil {
		return Endpoint{}, err
	}
	u, _ := url.Parse(e.URL)
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), "443")
	}
	u.Host = strings.ToLower(u.Host)
	return Endpoint{URL: u.String(), Kind: kindOf(u.String())}, nil
}

// Pair redeems a pairing link for this computer's credential and writes link.json. machine names the computer in
// the hub's hub.json when the link does not carry it; name is how the hub lists the credential.
func Pair(ctx context.Context, raw, machine, name, path string) (*Config, error) {
	token, c, err := parsePairLink(raw)
	if err != nil {
		return nil, err
	}
	if c.Machine != "" && machine != "" && machine != c.Machine {
		return nil, fmt.Errorf("这条配对链接是给 %q 的，不是 %q", c.Machine, machine)
	}
	if c.Machine == "" {
		c.Machine = machine
	}
	if c.Machine == "" {
		return nil, errors.New("要填这台电脑在 hub 里的机器 id（hub.json 里 kind 为 link 的那台）")
	}
	// system_name: a hub that has no name of its own for this computer yet names it so (docs/operations/link.md "The
	// computer's name").
	body, _ := json.Marshal(map[string]string{"name": name, "kind": "link", "machine": c.Machine, "system_name": SystemName()})
	client := hubClient(&c, nil)
	defer client.CloseIdleConnections()
	type redeemed struct {
		Token string `json:"token"`
		Role  string `json:"role"`
	}
	got, _, err := race(ctx, c.Endpoints, func(ctx context.Context, ep Endpoint) (redeemed, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL+"/api/v1/auth/tokens", bytes.NewReader(body))
		if err != nil {
			return redeemed{}, err
		}
		req.Header = bearer(token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return redeemed{}, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode != http.StatusOK {
			// With the cause a localized summary moved to detail (as apierr.Describe, #5728).
			var e struct {
				Error  string `json:"error"`
				Detail any    `json:"detail"`
			}
			_ = json.Unmarshal(b, &e)
			why := e.Error
			if d, ok := e.Detail.(string); ok && d != "" && d != why {
				why = strings.TrimPrefix(why+"：", "：") + d
			}
			if why == "" {
				why = resp.Status
			}
			return redeemed{}, fmt.Errorf("hub 拒绝了：%s", why)
		}
		var out redeemed
		if err := json.Unmarshal(b, &out); err != nil || out.Token == "" || out.Role != "link" {
			return redeemed{}, errors.New("hub 没给出链路凭据：升级 hub")
		}
		return out, nil
	}, func(redeemed) {})
	if err != nil {
		return nil, fmt.Errorf("配对失败：%w", err)
	}
	c.Token = got.Token
	err = Update(path, func(old *Config) error {
		var manual []Endpoint
		if old.Hub == c.Hub {
			for _, e := range old.Endpoints {
				if e.Manual {
					manual = append(manual, e)
				}
			}
		}
		keep := c
		keep.Endpoints = append(manual, c.Endpoints...)
		// Pairing with a hub's link means dialing it; what passive mode had stays for switching back.
		keep.Mode, keep.Passive = ModeActive, old.Passive
		*old = keep
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// AddAddress adds a candidate typed on this computer (a domain name, say); RemoveAddress takes one out again.
func AddAddress(path, raw string) (Endpoint, error) {
	var added Endpoint
	err := Update(path, func(c *Config) error {
		if c.Token == "" {
			return errors.New("还没配对")
		}
		e, err := normalize(raw, c.Endpoints)
		if err != nil {
			return err
		}
		added = e
		c.Endpoints = append([]Endpoint{e}, slicesDelete(c.Endpoints, e.URL)...)
		return nil
	})
	return added, err
}

func RemoveAddress(path, raw string) error {
	return Update(path, func(c *Config) error {
		e, err := normalize(raw, c.Endpoints)
		if err != nil {
			return err
		}
		c.Endpoints = slicesDelete(c.Endpoints, e.URL)
		return nil
	})
}

func slicesDelete(eps []Endpoint, u string) []Endpoint {
	out := eps[:0:0]
	for _, e := range eps {
		if e.URL != u {
			out = append(out, e)
		}
	}
	return out
}

// backoff: exponential from 1 s to a minute, each wait randomized down to half so links that broke together do not
// come back together.
type backoff struct{ n int }

func (b *backoff) next() time.Duration {
	d := min(time.Second<<b.n, time.Minute)
	if d < time.Minute {
		b.n++
	}
	return d/2 + rand.N(d/2+1)
}

// reporter keeps the status and hands every change to Options.Status, saying which mode it is about.
type reporter struct {
	mu   sync.Mutex
	st   Status
	out  func(Status)
	mode string
}

// switchTo starts reporting about mode.
func (r *reporter) switchTo(mode string) {
	r.mu.Lock()
	r.mode = mode
	r.mu.Unlock()
}

func (r *reporter) set(change func(*Status)) {
	r.mu.Lock()
	change(&r.st)
	r.st.Mode = r.mode
	st := r.st
	r.mu.Unlock()
	if r.out != nil {
		r.out(st)
	}
}

// Run keeps this computer reachable from the hub until ctx ends: in active mode by keeping the link to the hub up,
// in passive mode by listening for it (listen.go). It follows link.json: switched to the other mode (persona-link
// mode, pair, pair --listen), it changes over within a second or two.
func Run(ctx context.Context, o Options) error {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.PingEvery <= 0 || o.PingTimeout <= 0 {
		o.PingEvery, o.PingTimeout = PingEvery, PingTimeout
	}
	rep := &reporter{out: o.Status}
	for ctx.Err() == nil {
		mode := currentMode(o.Path)
		mctx, cancel := context.WithCancel(ctx)
		go watchMode(mctx, o.Path, mode, cancel)
		if mode == ModePassive {
			runPassive(mctx, o, rep)
		} else {
			runActive(mctx, o, rep)
		}
		cancel()
		if ctx.Err() == nil {
			o.Logf("mode: %s → %s", mode, currentMode(o.Path))
		}
	}
	return nil
}

func currentMode(path string) string {
	c, err := Read(path)
	if err != nil {
		return ModeActive
	}
	return c.CurrentMode()
}

// modeCheck is how often Run looks whether link.json switched modes (a setting).
const modeCheck = time.Second

// watchMode ends the mode being run (cancel) once link.json says another.
func watchMode(ctx context.Context, path, mode string, cancel context.CancelFunc) {
	stamp := func() time.Time {
		if fi, err := os.Stat(path); err == nil {
			return fi.ModTime()
		}
		return time.Time{}
	}
	was := stamp()
	if currentMode(path) != mode {
		cancel()
		return
	}
	t := time.NewTicker(modeCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := stamp()
			if now.Equal(was) {
				continue
			}
			was = now
			if currentMode(path) != mode {
				cancel()
				return
			}
		}
	}
}

// runActive keeps the link to the hub up until ctx ends.
func runActive(ctx context.Context, o Options, rep *reporter) {
	rep.switchTo(ModeActive)
	var b backoff
	for ctx.Err() == nil {
		c, err := Load(o.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				rep.set(func(s *Status) { *s = Status{State: "unpaired"} })
			} else {
				rep.set(func(s *Status) { *s = Status{State: "waiting", Error: err.Error()} })
			}
			o.wait(ctx, time.Hour)
			continue
		}
		rep.set(func(s *Status) {
			*s = Status{State: "connecting", Machine: c.Machine, HubName: c.HubName, MachineName: c.MachineName, Trust: trustOf(c),
				Endpoints: ranked(c.Endpoints)}
		})
		l, err := dial(ctx, c)
		var end error
		state := "waiting"
		if err != nil {
			end = err
		} else {
			start := time.Now()
			end = l.serve(ctx, c, o, rep)
			if time.Since(start) > time.Minute {
				b = backoff{}
			}
		}
		if ctx.Err() != nil {
			return
		}
		wait := b.next()
		var he *httpError
		switch {
		case errors.Is(end, errRedial):
			b, wait = backoff{}, 0
		case errors.As(end, &he) && (he.status == http.StatusUnauthorized || he.status == http.StatusForbidden):
			state, wait = "refused", refusedWait
			end = errors.New("hub 不认这台的凭据（可能撤销了）：重新配对")
		case errors.As(end, &he) && he.status == http.StatusTooManyRequests:
			wait = max(wait, he.retry)
		case websocket.CloseStatus(end) == CloseReplaced:
			wait = replaceWait
			end = errors.New("这台电脑在别处又连了一条链路（同一个凭据开了两个 Persona Link？）")
		case websocket.CloseStatus(end) == CloseRevoked:
			state, wait = "refused", refusedWait
			end = errors.New("凭据被撤销了：重新配对")
		}
		retry := time.Now().Add(wait)
		o.Logf("link: %v; next attempt in %s", end, wait.Round(time.Second))
		rep.set(func(s *Status) {
			*s = Status{State: state, Machine: c.Machine, HubName: c.HubName, MachineName: c.MachineName, Error: summary(end), Retry: &retry,
				Trust: trustOf(c), Endpoints: ranked(c.Endpoints)}
		})
		o.wait(ctx, wait)
	}
}

// wait sleeps up to d, less if the network changed, a reconnect was asked for or link.json changed (paired again,
// an address added).
func (o Options) wait(ctx context.Context, d time.Duration) {
	stamp := func() time.Time {
		if fi, err := os.Stat(o.Path); err == nil {
			return fi.ModTime()
		}
		return time.Time{}
	}
	was := stamp()
	timer := time.NewTimer(d)
	defer timer.Stop()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case <-o.Check:
			return
		case <-o.Redial:
			return
		case <-poll.C:
			if !stamp().Equal(was) {
				return
			}
		}
	}
}

// attemptError is how one candidate failed in a race.
type attemptError struct {
	ep  Endpoint
	err error
}

func (e *attemptError) Error() string { return e.ep.URL + ": " + e.err.Error() }
func (e *attemptError) Unwrap() error { return e.err }

var kindNames = map[string]string{"lan": "内网", "ipv6": "公网 IPv6", "name": "域名", "ipv4": "公网 IPv4"}

// summary says in one line why the link failed, for the menu bar app: per candidate when no candidate worked.
func summary(err error) string {
	if err == nil {
		return ""
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var parts []string
		for _, e := range joined.Unwrap() {
			parts = append(parts, summary(e))
		}
		return strings.Join(parts, "；")
	}
	var ae *attemptError
	if errors.As(err, &ae) {
		host := ae.ep.URL
		if u, err := url.Parse(ae.ep.URL); err == nil {
			host = u.Host + u.Path // a reverse proxy's path says which hub
		}
		return kindNames[ae.ep.Kind] + " " + host + "：" + describe(ae.err)
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusGoingAway:
		return "hub 重启了"
	case CloseRefused:
		return "hub 不认这台的 hello（机器 id 和凭据对不上）"
	}
	return err.Error()
}

func describe(err error) string {
	var he *httpError
	var ne net.Error
	switch {
	case errors.As(err, &he):
		return "hub 回了 " + he.Error()
	case errors.Is(err, errPin):
		return errPin.Error()
	case errors.As(err, new(x509.HostnameError)):
		return "证书上的域名对不上（系统证书校验）"
	case errors.As(err, new(x509.UnknownAuthorityError)), errors.As(err, new(*tls.CertificateVerificationError)):
		return "证书不受系统信任（系统证书校验）"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "超时"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "拒绝连接"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EADDRNOTAVAIL):
		return "不可达"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "域名解析不了"
	}
	return err.Error()
}

// link is one live link to the hub.
type link struct {
	conn    *websocket.Conn
	ep      Endpoint
	local   netip.Addr   // this side's address of the link's TCP connection
	streams *http.Client // checked the same way (pin or system CAs); streams go where the link went
	token   string
	// named: the hub said what it calls this computer (endpoints), so it renames computers (rename.go).
	named atomic.Bool
}

func dial(ctx context.Context, c *Config) (*link, error) {
	l, ep, err := race(ctx, c.Endpoints, func(ctx context.Context, ep Endpoint) (*link, error) {
		var local netip.Addr
		client := hubClient(c, func(raw net.Conn) {
			if a, err := netip.ParseAddrPort(raw.LocalAddr().String()); err == nil {
				local = a.Addr().Unmap()
			}
		})
		conn, resp, err := websocket.Dial(ctx, wsURL(ep.URL, Path), &websocket.DialOptions{HTTPClient: client, HTTPHeader: bearer(c.Token)})
		if err != nil {
			return nil, handshakeError(resp, err)
		}
		conn.SetReadLimit(1 << 20)
		return &link{conn: conn, local: local, streams: hubClient(c, nil), token: c.Token}, nil
	}, func(l *link) { l.conn.CloseNow() })
	if err != nil {
		return nil, err
	}
	l.ep = ep
	return l, nil
}

var streamID = lazyre.New(`^[A-Za-z0-9_-]{1,64}$`)

// serve runs the link until it ends and says why.
func (l *link) serve(ctx context.Context, c *Config, o Options, rep *reporter) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	defer l.conn.CloseNow()
	defer l.streams.CloseIdleConnections()
	if err := l.send(ctx, Message{Type: "hello", Machine: c.Machine, Version: o.Version, Address: l.ep.URL}); err != nil {
		return err
	}
	since := time.Now()
	rep.set(func(s *Status) {
		*s = Status{State: "connected", Machine: c.Machine, HubName: c.HubName, MachineName: c.MachineName, Address: l.ep.URL,
			Kind: l.ep.Kind, Since: &since, Node: "unknown", Trust: trustOf(c), Endpoints: ranked(c.Endpoints)}
	})
	go l.renames(ctx, o, rep)
	o.Logf("link: connected to %s (%s)", l.ep.URL, l.ep.Kind)
	drop := func(err error) {
		cancel(err)
		l.conn.CloseNow()
	}
	// The heartbeat is o's until the hub says another (endpoints); beat hands the pinger the latest, the link stays.
	beat := make(chan heartbeat, 1)
	go func() {
		hb := heartbeat{every: o.PingEvery, timeout: o.PingTimeout}
		t := time.NewTicker(hb.every)
		defer t.Stop()
		// A ping waits for its pong on its own, so a reconnect or a network change is not held up behind one that is
		// out (with a slow link's heartbeat, for up to the hub's timeout). One of each kind at a time; the first that
		// fails drops the link.
		type pong struct {
			check bool
			err   error
		}
		pongs := make(chan pong)
		var beating, checking bool
		ping := func(check bool, d time.Duration) {
			go func() {
				err := l.ping(ctx, d)
				select {
				case pongs <- pong{check, err}:
				case <-ctx.Done():
				}
			}()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-pongs:
				why := "hub 没有回应心跳"
				if p.check {
					checking, why = false, "网络变了以后 hub 没有回应"
				} else {
					beating = false
				}
				if p.err != nil && ctx.Err() == nil {
					drop(fmt.Errorf("%s：%w", why, p.err))
					return
				}
			case b := <-beat:
				if b == hb {
					continue
				}
				if b.every != hb.every {
					t.Reset(b.every)
				}
				hb = b
				o.Logf("link: heartbeat every %s, %s for a pong", hb.every, hb.timeout)
			case <-t.C:
				if !beating {
					beating = true
					ping(false, hb.timeout)
				}
			case <-o.Check:
				if l.localGone() {
					drop(errors.New("网络变了，原来的地址已经不在"))
					return
				}
				if !checking {
					checking = true
					ping(true, hb.check())
				}
			case <-o.Redial:
				cancel(errRedial)
				_ = l.conn.Close(websocket.StatusNormalClosure, "reconnect")
				return
			}
		}
	}()
	for {
		_, data, err := l.conn.Read(ctx)
		if err != nil {
			if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
				return cause
			}
			return err
		}
		var m Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "endpoints":
			l.endpoints(m, c, o, rep)
			select { // only the latest counts
			case <-beat:
			default:
			}
			beat <- o.heartbeatOf(m)
		case "renamed":
			l.renamed(m, c, o, rep)
		case "node":
			rep.set(func(s *Status) {
				s.Node, s.NodeError = "offline", m.Error
				if m.Online {
					s.Node, s.NodeError = "online", ""
				}
			})
		case "open":
			if !streamID.MatchString(m.ID) {
				continue
			}
			go func(id string) {
				if err := l.forward(ctx, id, o.Target); err != nil {
					o.Logf("link: stream %s: %v", id, err)
					_ = l.send(ctx, Message{Type: "open_failed", ID: id, Error: err.Error()})
					rep.set(func(s *Status) { s.Node, s.NodeError = "offline", err.Error() })
				}
			}(m.ID)
		}
	}
}

// endpoints takes in what the hub says about where it is and who it is: link.json keeps the candidates (the ones added
// here stay), the hub's name and what it calls this computer; the mesh size is only shown while connected.
func (l *link) endpoints(m Message, c *Config, o Options, rep *reporter) {
	name, machine := clipName(m.HubName), clipName(m.MachineName)
	if machine != "" {
		l.named.Store(true)
	}
	if len(m.Endpoints) > 0 || (name != "" && name != c.HubName) || (machine != "" && machine != c.MachineName) {
		var eps []Endpoint
		err := Update(o.Path, func(f *Config) error {
			if f.Token != c.Token {
				return errPairedAgain
			}
			if name != "" {
				f.HubName = name
			}
			if machine != "" {
				f.MachineName = machine
			}
			if len(m.Endpoints) > 0 {
				var manual []Endpoint
				for _, e := range f.Endpoints {
					if e.Manual {
						manual = append(manual, e)
					}
				}
				hub := make([]Endpoint, 0, len(m.Endpoints))
				for _, e := range m.Endpoints {
					if strings.HasPrefix(e.URL, "https://") {
						hub = append(hub, Endpoint{URL: e.URL, Kind: e.Kind})
					}
				}
				f.Endpoints = append(manual, hub...)
			}
			eps = f.Endpoints
			return nil
		})
		if err != nil {
			return
		}
		c.Endpoints = eps
		if name != "" {
			c.HubName = name
		}
		if machine != "" {
			c.MachineName = machine
		}
	}
	rep.set(func(s *Status) {
		s.Endpoints = ranked(c.Endpoints)
		s.HubName = c.HubName
		s.MachineName = c.MachineName
		if m.MeshHubs > 0 {
			s.MeshHubs = min(m.MeshHubs, 999)
		}
		s.Pending = m.Pending
	})
}

// heartbeat is how a link is pinged: every, and how long a pong may take; hub: the hub said so (endpoints).
type heartbeat struct {
	every, timeout time.Duration
	hub            bool
}

// heartbeatOf is the ping interval and timeout the hub asks for in m (endpoints: hub.json link), brought into range;
// o's when it asks for none or for nothing usable (older hubs, hubs that leave it at the default).
func (o Options) heartbeatOf(m Message) heartbeat {
	if every, timeout, ok := PingTimesMS(int64(m.PingMS), int64(m.PingTimeoutMS)); ok {
		return heartbeat{every, timeout, true}
	}
	return heartbeat{o.PingEvery, o.PingTimeout, false}
}

// check is how long the ping after a network change waits: checkPing, to notice a dead network quickly, but not less
// than the hub's timeout when it set one (on its slow link a pong may take that long, network change or not).
func (h heartbeat) check() time.Duration {
	if h.hub {
		return max(checkPing, h.timeout)
	}
	return checkPing
}

// clipName keeps a hub's name to one short line of printable text: it is shown in a menu and a terminal.
func clipName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

func (l *link) send(ctx context.Context, m Message) error {
	b, _ := json.Marshal(m)
	ctx, cancel := context.WithTimeout(ctx, PingTimeout)
	defer cancel()
	return l.conn.Write(ctx, websocket.MessageText, b)
}

func (l *link) ping(ctx context.Context, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return l.conn.Ping(ctx)
}

// localGone: the address the link's TCP connection left from is no longer on this computer (another network).
func (l *link) localGone() bool {
	if !l.local.IsValid() {
		return false
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Unmap().WithZone("") == l.local.WithZone("") {
			return false
		}
	}
	return true
}

// forward joins stream id to the resident node: nothing else, and nowhere else (docs/operations/link.md "Forwarding"). It returns an
// error only when the stream could not be opened; once bytes flow, its end is the node's or the hub's business.
func (l *link) forward(ctx context.Context, id, target string) error {
	node, err := net.DialTimeout("unix", target, 2*time.Second)
	if err != nil {
		return nodeMissing(runtime.GOOS, target)
	}
	defer node.Close()
	dctx, dcancel := context.WithTimeout(ctx, dialTimeout) // the handshake only: the stream lives as long as the link
	defer dcancel()
	ws, resp, err := websocket.Dial(dctx, wsURL(l.ep.URL, StreamPath+"?id="+id), &websocket.DialOptions{
		HTTPClient: l.streams, HTTPHeader: bearer(l.token), CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return fmt.Errorf("开不了流：%w", handshakeError(resp, err))
	}
	nc := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	defer nc.Close()
	// hub -> node: when the hub ends the stream, half-close so the node sees EOF and finishes. node -> hub decides
	// when the stream is over, as node.Relay does: everything the node wrote must reach the hub.
	go func() {
		_, _ = io.Copy(node, nc)
		if u, ok := node.(*net.UnixConn); ok {
			_ = u.CloseWrite()
		}
	}()
	_, _ = io.Copy(nc, node)
	return nil
}
