package hub

import (
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"
)

// Who may reach the admin UI (docs/operations/admin.md "Access scope"). Everything here trusts connection addresses only; forwarding
// headers can only narrow: when a proxy put them there, every address in them must be in scope as well.

// ifacePrefix is one address of this machine with its prefix (a network it is on).
type ifacePrefix struct {
	prefix netip.Prefix
	vpn    bool
}

func localInterfacePrefixes() []ifacePrefix {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []ifacePrefix
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			n, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(n.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			ones, _ := n.Mask.Size()
			if ip.Is4() && ones > 32 {
				ones -= 96
			}
			out = append(out, ifacePrefix{prefix: netip.PrefixFrom(ip, ones).Masked(), vpn: vpnInterface(iface.Name)})
		}
	}
	return out
}

// inContainer: Docker writes /.dockerenv, Podman /run/.containerenv. A container on a bridge network sees only its
// bridge's network, never the LAN its host is on.
func inContainer() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func (h *Hub) adminContainer() bool {
	if h.admin.container != nil {
		return h.admin.container()
	}
	return inContainer()
}

// adminPrefixes is this machine's networks, read at most every 30 seconds (addresses change with DHCP, a new IPv6
// prefix, a VPN coming up).
func (h *Hub) adminPrefixes() []ifacePrefix {
	a := &h.admin
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ifCache == nil || time.Since(a.ifCached) > onLinkRefresh {
		read := a.prefixes
		if read == nil {
			read = localInterfacePrefixes
		}
		a.ifCache, a.ifCached = read(), time.Now()
		if a.ifCache == nil {
			a.ifCache = []ifacePrefix{}
		}
	}
	return a.ifCache
}

// onAdminLAN: a private (RFC 1918, ULA) or link-local address on one of this machine's networks, or a global IPv6
// address on one of its /64s that is not a VPN's. In a container any private address counts (the host's LAN is not
// among the container's networks) but the container's gateway: docker-proxy relays everything, the internet
// included, from there (admin_gateway.go, docs/operations/admin.md "Docker bridge network").
func (h *Hub) onAdminLAN(ip netip.Addr) bool {
	if h.adminContainer() {
		if h.adminGateway(ip) {
			h.noteGatewayRefused(ip)
			return false
		}
		return ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	for _, p := range h.adminPrefixes() {
		if !p.prefix.Contains(ip) {
			continue
		}
		if ip.IsPrivate() {
			return true
		}
		if ip.Is6() && ip.IsGlobalUnicast() && p.prefix.Bits() >= 64 && !p.vpn {
			return true
		}
	}
	return false
}

// addrInScope is whether one address may reach the admin UI under access, transport aside: "any" lets in what "lan"
// does over plaintext (the request decides about TLS).
func (h *Hub) addrInScope(scope adminScope, ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	switch scope.access {
	case accessLoopback:
		return false
	case accessCIDRs:
		return prefixesContain(scope.cidrs, ip)
	default: // lan, any
		return prefixesContain(scope.trusted, ip) || h.onAdminLAN(ip)
	}
}

func prefixesContain(list []netip.Prefix, ip netip.Addr) bool {
	for _, p := range list {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// requestAddrs is the connection's address and, after it, every address in forwarding headers. ok is false when a
// header names something that is not an address ("unknown", an obfuscated id): nobody can tell where that came from.
func requestAddrs(r *http.Request) (addrs []netip.Addr, ok bool) {
	addrs = append(addrs, remoteIP(r))
	add := func(s string) bool {
		ip := forwardedIP(s)
		if !ip.IsValid() {
			return false
		}
		addrs = append(addrs, ip)
		return true
	}
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) == "" {
				continue
			}
			if !add(part) {
				return addrs, false
			}
		}
	}
	for _, v := range r.Header.Values("X-Real-Ip") {
		if strings.TrimSpace(v) != "" && !add(v) {
			return addrs, false
		}
	}
	for _, v := range r.Header.Values("Forwarded") {
		for _, element := range strings.Split(v, ",") {
			for _, pair := range strings.Split(element, ";") {
				k, val, found := strings.Cut(strings.TrimSpace(pair), "=")
				if found && strings.EqualFold(strings.TrimSpace(k), "for") && !add(val) {
					return addrs, false
				}
			}
		}
	}
	// A proxy that says it is one but not for whom: nobody can tell where the request came from.
	if len(addrs) == 1 {
		for _, k := range []string{"Via", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Forwarded-Server", "Forwarded"} {
			if r.Header.Get(k) != "" {
				return addrs, false
			}
		}
	}
	return addrs, true
}

// noteGatewayRefused logs, at most every 10 minutes, that the container's gateway was kept out: on Docker Desktop
// and OrbStack every browser looks like that, and this line says why /admin is not there.
func (h *Hub) noteGatewayRefused(ip netip.Addr) {
	h.admin.mu.Lock()
	log := time.Since(h.admin.gatewayLogged) > 10*time.Minute
	if log {
		h.admin.gatewayLogged = time.Now()
	}
	h.admin.mu.Unlock()
	if log {
		h.logf("admin: %s is this container's gateway (port mapping hides the real source): not let into the admin UI as LAN; "+
			"see docs/operations/admin.md for Docker Desktop / OrbStack", ip)
	}
}

// forwardedIP reads 192.0.2.1, 192.0.2.1:8080, [2001:db8::1]:443, "[2001:db8::1]" or 2001:db8::1.
func forwardedIP(s string) netip.Addr {
	s = strings.Trim(strings.TrimSpace(s), `"`)
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone("")
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return ip.Unmap().WithZone("")
}

// adminInScope decides whether the admin UI exists for this request at all. "any" over TLS lets in anyone; anything
// else needs the connection and every forwarded address in scope.
func (h *Hub) adminInScope(r *http.Request) bool {
	scope := h.adminScope()
	if !remoteIP(r).IsValid() {
		return false
	}
	if scope.access == accessAny && r.TLS != nil {
		return true
	}
	return h.addrsInScope(scope, r)
}

func (h *Hub) addrsInScope(scope adminScope, r *http.Request) bool {
	addrs, ok := requestAddrs(r)
	if !ok {
		return false
	}
	for _, ip := range addrs {
		if !h.addrInScope(scope, ip) {
			return false
		}
	}
	return true
}

// adminCanSetup: only a source the scope lets in by address may set the first password; under "any" someone from
// the internet must not get there first (docs/operations/admin.md "Admin password and sessions").
func (h *Hub) adminCanSetup(r *http.Request) bool {
	return h.addrsInScope(h.adminScope(), r)
}

// adminWouldAllow is whether this request would still get in under another scope (the network page's warning
// before narrowing, and the server's own check).
func (h *Hub) adminWouldAllow(r *http.Request, scope adminScope) bool {
	if scope.access == accessAny && r.TLS != nil {
		return true
	}
	return h.addrsInScope(scope, r)
}

// narrowerAccess is whether moving from one scope to another only takes sources away (the web may only narrow;
// widening is for the hub machine's command line and deploy scripts).
func narrowerAccess(from, to adminScope) bool {
	switch to.access {
	case accessLoopback:
		return true
	case accessAny:
		return from.access == accessAny
	case accessLAN:
		return from.access == accessLAN || from.access == accessAny
	case accessCIDRs:
		switch from.access {
		case accessAny:
			return true
		case accessCIDRs:
			for _, p := range to.cidrs {
				covered := false
				for _, q := range from.cidrs {
					if q.Bits() <= p.Bits() && q.Contains(p.Addr()) {
						covered = true
						break
					}
				}
				if !covered {
					return false
				}
			}
			return true
		}
	}
	return false
}
