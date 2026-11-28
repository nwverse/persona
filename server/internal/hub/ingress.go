package hub

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// source is where a request came from, for the rules that depend on it: plaintext, rate limits, the master token.
type source int

const (
	sourceLoopback source = iota
	sourceLAN             // private, ULA or link-local addresses, or a configured trusted range (a VPN)
	sourcePublic
)

// sourceOf trusts the connection addresses, never forwarding headers. Shared carrier space is trusted only
// when the local address belongs to a VPN interface, or an explicit trusted range includes the peer.
func (h *Hub) sourceOf(r *http.Request) source {
	if f, ok := forwardedOf(r); ok { // a member passed it on, having judged it (peer_http.go)
		return f.src
	}
	if fromPublicProxy(r) {
		return sourcePublic
	}
	ip := remoteIP(r)
	if !ip.IsValid() {
		return sourcePublic
	}
	if ip.IsLoopback() {
		return sourceLoopback
	}
	// A container's gateway relays connections from anywhere, the internet included (docker-proxy, admin_gateway.go):
	// it is the LAN only when security.trusted_cidrs says so.
	if h.adminGateway(ip) {
		if h.trustedSource(ip) {
			return sourceLAN
		}
		return sourcePublic
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return sourceLAN
	}
	if ip.Is6() && h.onLinkIPv6(ip) {
		return sourceLAN
	}
	if h.trustedSource(ip) {
		return sourceLAN
	}
	if ip.Is4() && ip.As4()[0] == 100 && ip.As4()[1]&0xc0 == 64 {
		if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			addr := addressIP(local.String())
			vpn := h.devtok.vpnLocal
			if vpn == nil {
				vpn = localVPNAddress
			}
			if addr.IsValid() && vpn(addr) {
				return sourceLAN
			}
		}
	}
	return sourcePublic
}

// trustedSource: ip is in security.trusted_cidrs.
func (h *Hub) trustedSource(ip netip.Addr) bool {
	for _, cidr := range h.cfg.Security.TrustedCIDRs {
		if prefix, err := netip.ParsePrefix(cidr); err == nil && prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// onLinkIPv6 reports whether ip is on the same /64 (or longer) prefix as one of this machine's interfaces. At home
// the phone and the hub often both have global IPv6 addresses on one LAN, and the phone may pick them over the LAN
// IPv4 address: that traffic never crossed the internet, and treating it as public would refuse today's iOS app
// its plaintext connection. Shorter prefixes are left out so a shared provider range does not count as home.
func (h *Hub) onLinkIPv6(ip netip.Addr) bool {
	t := &h.devtok
	t.mu.Lock()
	if t.linkCache == nil || time.Since(t.linkCached) > onLinkRefresh {
		read := t.onLink
		if read == nil {
			read = localIPv6Prefixes
		}
		t.linkCache, t.linkCached = read(), time.Now()
		if t.linkCache == nil {
			t.linkCache = []netip.Prefix{}
		}
	}
	prefixes := t.linkCache
	t.mu.Unlock()
	for _, p := range prefixes {
		if p.Bits() >= 64 && p.Contains(ip) {
			return true
		}
	}
	return false
}

// onLinkRefresh bounds how stale the interface list may be (addresses change when the provider renumbers).
const onLinkRefresh = 30 * time.Second

func localIPv6Prefixes() []netip.Prefix {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Prefix
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || vpnInterface(iface.Name) {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			n, ok := addr.(*net.IPNet)
			if !ok || n.IP.To4() != nil {
				continue
			}
			ip, ok := netip.AddrFromSlice(n.IP)
			ones, _ := n.Mask.Size()
			if ok && ip.IsGlobalUnicast() && !ip.IsPrivate() {
				out = append(out, netip.PrefixFrom(ip, ones).Masked())
			}
		}
	}
	return out
}

func remoteIP(r *http.Request) netip.Addr { return addressIP(r.RemoteAddr) }

func addressIP(addr string) netip.Addr {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	ip, _ := netip.ParseAddr(addr)
	return ip.Unmap().WithZone("")
}

func vpnInterface(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range []string{"tailscale", "utun", "wg", "tun", "tap", "ppp", "ipsec"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func localVPNAddress(local netip.Addr) bool {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || !vpnInterface(iface.Name) {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err == nil && prefix.Addr().Unmap().WithZone("") == local {
				return true
			}
		}
	}
	return false
}
