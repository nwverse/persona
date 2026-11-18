package link

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestProxyConfiguration(t *testing.T) {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("ALL_PROXY", "http://user:secret@proxy.example:8080")
	cfg := &Config{}
	p, err := proxyURL(cfg, "https://hub.example:443")
	if err != nil || p == nil {
		t.Fatalf("environment proxy: %v %v", p, err)
	}
	if got := RedactedProxy(p.String()); strings.Contains(got, "user") || strings.Contains(got, "secret") {
		t.Fatalf("credentials visible: %s", got)
	}
	t.Setenv("NO_PROXY", ".example")
	p, err = proxyURL(cfg, "https://hub.example:443")
	if err != nil || p != nil {
		t.Fatalf("NO_PROXY: %v %v", p, err)
	}
	t.Setenv("NO_PROXY", "")
	path := filepath.Join(t.TempDir(), "link.json")
	if err := SetProxy(path, "off"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Read(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err = proxyURL(cfg, "https://hub.example:443")
	if err != nil || p != nil {
		t.Fatalf("off: %v %v", p, err)
	}
	if err := SetProxy(path, "socks5://user:secret@host"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("invalid URL error: %v", err)
	}
}

func TestProxyLinkAndStreamPin(t *testing.T) {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:18711")
	if err != nil {
		t.Fatal(err)
	}
	var connects atomic.Int32
	hubLn, err := net.Listen("tcp", "127.0.0.1:18712")
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	stream := make(chan struct{}, 1)
	hub := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		if r.URL.Path == StreamPath {
			stream <- struct{}{}
			return
		}
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	hub.Listener = hubLn
	hub.StartTLS()
	t.Cleanup(hub.Close)
	proxy := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", 405)
			return
		}
		if r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpzZWNyZXQ=" {
			http.Error(w, "auth", 407)
			return
		}
		up, err := net.Dial("tcp", hubLn.Addr().String())
		if err != nil {
			http.Error(w, "upstream", 502)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			up.Close()
			return
		}
		down, _, err := hijacker.Hijack()
		if err != nil {
			up.Close()
			return
		}
		connects.Add(1)
		_, _ = io.WriteString(down, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { defer down.Close(); defer up.Close(); _, _ = io.Copy(up, down) }()
		_, _ = io.Copy(down, up)
	})}
	go proxy.Serve(ln)
	t.Cleanup(func() { proxy.Close(); ln.Close() })
	cfg := &Config{FP: pinOf(hub), Token: "test", Proxy: "http://user:secret@127.0.0.1:18711", Endpoints: []Endpoint{{URL: "https://192.0.2.1:18712", Kind: "ipv4"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	l, err := dialProxy(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer l.closeNow()
	if l.ep.Kind != KindProxy {
		t.Fatalf("kind: %s", l.ep.Kind)
	}
	node := newFakeNode(t)
	if err := l.forward(ctx, "stream1", node.path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stream:
	case <-ctx.Done():
		t.Fatal("stream did not reach hub")
	}
	if connects.Load() < 2 {
		t.Fatalf("CONNECT count %d", connects.Load())
	}
	wrong := *cfg
	wrong.FP = "wrong"
	if got, err := dialProxy(ctx, &wrong); err == nil {
		got.closeNow()
		t.Fatal("wrong pin accepted")
	} else if !errors.Is(err, errPin) {
		t.Fatalf("wrong pin error: %v", err)
	}

	// The proxy uses its own CA-verified certificate, distinct from the pinned hub.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test proxy"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	tlsLn, err := net.Listen("tcp", "127.0.0.1:18713")
	if err != nil {
		t.Fatal(err)
	}
	tlsProxy := httptest.NewUnstartedServer(proxy.Handler)
	tlsProxy.Listener = tlsLn
	tlsProxy.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	tlsProxy.StartTLS()
	t.Cleanup(tlsProxy.Close)
	tlsCfg := *cfg
	tlsCfg.Proxy = "https://user:secret@127.0.0.1:18713"
	if got, err := dialProxy(ctx, &tlsCfg); err == nil {
		got.closeNow()
		t.Fatal("untrusted HTTPS proxy accepted")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	restore := UseRootsForTesting(pool)
	defer restore()
	secure, err := dialProxy(ctx, &tlsCfg)
	if err != nil {
		t.Fatalf("trusted HTTPS proxy with pinned hub: %v", err)
	}
	secure.closeNow()
	badHostLn, err := net.Listen("tcp", "127.0.0.2:18714")
	if err != nil {
		t.Fatal(err)
	}
	badHost := httptest.NewUnstartedServer(proxy.Handler)
	badHost.Listener = badHostLn
	badHost.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	badHost.StartTLS()
	t.Cleanup(badHost.Close)
	badHostCfg := tlsCfg
	badHostCfg.Proxy = "https://user:secret@127.0.0.2:18714"
	if got, err := dialProxy(ctx, &badHostCfg); err == nil {
		got.closeNow()
		t.Fatal("HTTPS proxy hostname mismatch accepted")
	}
	tlsCfg.FP = "wrong"
	if got, err := dialProxy(ctx, &tlsCfg); err == nil {
		got.closeNow()
		t.Fatal("HTTPS proxy bypassed hub pin")
	} else if !errors.Is(err, errPin) {
		t.Fatalf("wrong pin: %v", err)
	}
}
