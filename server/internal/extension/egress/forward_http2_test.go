package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func localForwardTransport(t *testing.T, server *httptest.Server, ctx context.Context) (*http.Transport, *atomic.Int32) {
	t.Helper()
	var dials atomic.Int32
	transport := forwardTransport(func() (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	})
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	// Trust only this local fixture, never disable verification or change production roots.
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	t.Cleanup(transport.CloseIdleConnections)
	if transport.Proxy != nil || !transport.DisableKeepAlives || transport.ResponseHeaderTimeout != 2*time.Minute {
		t.Fatal("forward transport policy changed")
	}
	return transport, &dials
}

func TestForwardTransportTLSProtocols(t *testing.T) {
	for _, http2 := range []bool{true, false} {
		name := "http1-fallback"
		want := 1
		if http2 {
			name, want = "http2", 2
		}
		t.Run(name, func(t *testing.T) {
			seen := make(chan int, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.ProtoMajor
				w.Header().Set("Content-Type", "application/rss+xml")
				io.WriteString(w, "<rss><channel/></rss>")
			}))
			server.EnableHTTP2 = http2
			server.StartTLS()
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			transport, dials := localForwardTransport(t, server, ctx)
			request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/feed.rss", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != "<rss><channel/></rss>" {
				t.Fatalf("response body: %q, %v", body, err)
			}
			if response.ProtoMajor != want || <-seen != want || dials.Load() != 1 {
				t.Fatalf("protocol=%s, approved dials=%d", response.Proto, dials.Load())
			}
		})
	}
}

func TestForwardTransportHTTP2Cancellation(t *testing.T) {
	started := make(chan int, 1)
	canceled := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.ProtoMajor
		<-r.Context().Done()
		close(canceled)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport, dials := localForwardTransport(t, server, ctx)
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/blocked", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := transport.RoundTrip(request)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	select {
	case protocol := <-started:
		if protocol != 2 {
			t.Fatalf("request did not use HTTP/2: %d", protocol)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local HTTP/2 request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP/2 client did not cancel")
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP/2 upstream did not receive cancellation")
	}
	if dials.Load() != 1 {
		t.Fatalf("unexpected additional approved dials: %d", dials.Load())
	}
}
