package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestDialOutRefusesSelfInterfaceAndMulticast(t *testing.T) {
	var dialed []string
	p := &Proxy{
		HubAddr:    "127.0.0.1:18660",
		LocalAddrs: func() []net.IP { return []net.IP{net.ParseIP("198.51.100.5")} },
		Resolve: func(_ context.Context, name string) ([]net.IP, error) {
			switch name {
			case "self.example.com":
				return []net.IP{net.ParseIP("198.51.100.5")}, nil
			case "mcast.example.com":
				return []net.IP{net.ParseIP("239.0.0.1")}, nil
			case "mcast6.example.com":
				return []net.IP{net.ParseIP("ff05::1")}, nil
			}
			return []net.IP{net.ParseIP("203.0.113.9")}, nil
		},
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			a, b := net.Pipe()
			b.Close()
			return a, nil
		},
		Upstream: func(*url.URL) (*url.URL, error) { return nil, nil },
	}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	_, ownPort, err := net.SplitHostPort(p.Addr())
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	refused := []struct{ host, port string }{
		{"self.example.com", "18660"},
		{"self.example.com", ownPort},
		{"mcast.example.com", "443"},
		{"mcast6.example.com", "443"},
		{"198.51.100.5", "18660"},
		{"198.51.100.5", ownPort},
		{"127.0.0.1", "18660"},
		{"127.0.0.1", ownPort},
	}
	for _, tc := range refused {
		if c, err := p.dialOut(ctx, tc.host, tc.port, Rule{}); !errors.Is(err, errInternal) {
			if c != nil {
				c.Close()
			}
			t.Errorf("dialOut(%s:%s) = %v, want errInternal", tc.host, tc.port, err)
		}
	}
	if len(dialed) != 0 {
		t.Fatalf("refused targets were dialed: %v", dialed)
	}

	// Non-hub, non-proxy ports on the host's own IP or literal loopback remain reachable when permitted.
	for _, tc := range []struct{ host, port, want string }{
		{"self.example.com", "8443", "198.51.100.5:8443"},
		{"127.0.0.1", "8443", "127.0.0.1:8443"},
	} {
		c, err := p.dialOut(ctx, tc.host, tc.port, Rule{})
		if err != nil {
			t.Fatalf("dialOut(%s:%s): %v", tc.host, tc.port, err)
		}
		c.Close()
		if got := dialed[len(dialed)-1]; got != tc.want {
			t.Fatalf("dialOut(%s:%s) dialed %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestRuntimeDialRefusesSelfInterfaceAndLiteralSelfService(t *testing.T) {
	var dialed []string
	p := &Proxy{
		HubAddr:    "127.0.0.1:18660",
		LocalAddrs: func() []net.IP { return []net.IP{net.ParseIP("198.51.100.5")} },
		Resolve: func(_ context.Context, name string) ([]net.IP, error) {
			switch name {
			case "self.example.com":
				return []net.IP{net.ParseIP("198.51.100.5")}, nil
			case "mixed-self.example.com":
				return []net.IP{net.ParseIP("203.0.113.9"), net.ParseIP("198.51.100.5")}, nil
			}
			return []net.IP{net.ParseIP("203.0.113.9")}, nil
		},
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			a, b := net.Pipe()
			b.Close()
			return a, nil
		},
		Upstream: func(*url.URL) (*url.URL, error) { return nil, nil },
	}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	_, ownPort, err := net.SplitHostPort(p.Addr())
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	refused := []struct{ host, port string }{
		{"self.example.com", "18660"},
		{"self.example.com", ownPort},
		{"mixed-self.example.com", "18660"},
		{"198.51.100.5", "18660"},
		{"198.51.100.5", ownPort},
		{"127.0.0.1", "18660"},
		{"127.0.0.1", ownPort},
	}
	for _, tc := range refused {
		if c, err := p.runtimeDial(ctx, tc.host, tc.port, false); !errors.Is(err, errInternal) {
			if c != nil {
				c.Close()
			}
			t.Errorf("runtimeDial(%s:%s, false) = %v, want errInternal", tc.host, tc.port, err)
		}
	}
	if len(dialed) != 0 {
		t.Fatalf("refused runtime targets were dialed: %v", dialed)
	}

	for _, tc := range []struct{ host, port, want string }{
		{"self.example.com", "8443", "198.51.100.5:8443"},
		{"127.0.0.1", "8443", "127.0.0.1:8443"},
	} {
		c, err := p.runtimeDial(ctx, tc.host, tc.port, false)
		if err != nil {
			t.Fatalf("runtimeDial(%s:%s, false): %v", tc.host, tc.port, err)
		}
		c.Close()
		if got := dialed[len(dialed)-1]; got != tc.want {
			t.Fatalf("runtimeDial(%s:%s, false) dialed %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestHandlerRefusesLiteralLoopbackAndSelfInterfaceToHubAndOwnPort(t *testing.T) {
	var dialed []string
	p := &Proxy{
		HubAddr:    "127.0.0.1:18660",
		LocalAddrs: func() []net.IP { return []net.IP{net.ParseIP("198.51.100.5")} },
		Resolve: func(_ context.Context, name string) ([]net.IP, error) {
			if name == "api.example.com" {
				return []net.IP{net.ParseIP("198.51.100.5")}, nil
			}
			return []net.IP{net.ParseIP("203.0.113.9")}, nil
		},
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			a, b := net.Pipe()
			b.Close()
			return a, nil
		},
		Upstream: func(*url.URL) (*url.URL, error) { return nil, nil },
	}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	_, ownPort, err := net.SplitHostPort(p.Addr())
	if err != nil {
		t.Fatal(err)
	}

	purl := p.Register("ext", Rule{Hosts: []string{"127.0.0.1", "api.example.com"}})
	if got := connectStatus(t, purl, "127.0.0.1:"+ownPort); got != http.StatusForbidden {
		t.Fatalf("CONNECT 127.0.0.1:%s = %d, want 403", ownPort, got)
	}
	if got := connectStatus(t, purl, "127.0.0.1:18660"); got != http.StatusForbidden {
		t.Fatalf("CONNECT 127.0.0.1:18660 = %d, want 403", got)
	}
	if got := connectStatus(t, purl, "api.example.com:18660"); got != http.StatusBadGateway {
		t.Fatalf("CONNECT api.example.com:18660 = %d, want 502", got)
	}
	if len(dialed) != 0 {
		t.Fatalf("forbidden hub/proxy targets were dialed: %v", dialed)
	}
}

func TestForSettingsProtectsDefaultHubPortWhenHubAddrUnset(t *testing.T) {
	p := &Proxy{
		LocalAddrs: func() []net.IP { return []net.IP{net.ParseIP("198.51.100.5")} },
	}
	if p.forSettings(net.ParseIP("198.51.100.5"), "18660") {
		t.Fatal("forSettings allowed LocalAddrs on default hub port 18660 when HubAddr was unset")
	}
	if !p.forSettings(net.ParseIP("198.51.100.5"), "8443") {
		t.Fatal("forSettings refused LocalAddrs on non-hub port 8443")
	}
}
