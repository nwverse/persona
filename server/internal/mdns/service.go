package mdns

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"unicode/utf8"
)

// recordTTL is the TTL of every record this package advertises, in seconds (RFC 6762 §10 suggests 120 s for
// records that carry a host name and 75 min for the others; one value keeps it simple).
const recordTTL = 120

// metaName is the DNS-SD service type enumeration name (RFC 6763 §9).
var metaName = name{"_services", "_dns-sd", "_udp", "local"}

// parseType turns a service type such as "_persona-hub._tcp" (".local." optional) into its full name.
func parseType(s string) (name, error) {
	t := strings.TrimSuffix(s, ".")
	if l := len(t) - len(".local"); l >= 0 && asciiEqualFold(t[l:], ".local") {
		t = t[:l]
	}
	parts := strings.Split(t, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("mdns: service type %q is not <_service>.<_tcp|_udp>", s)
	}
	for _, p := range parts {
		if len(p) < 2 || len(p) > maxLabel || p[0] != '_' {
			return nil, fmt.Errorf("mdns: bad service type label %q in %q", p, s)
		}
	}
	if proto := asciiLower(parts[1]); proto != "_tcp" && proto != "_udp" {
		return nil, fmt.Errorf("mdns: service type %q must end in _tcp or _udp", s)
	}
	return name{parts[0], parts[1], "local"}, nil
}

// cutLabel shortens s to at most max bytes without splitting a UTF-8 sequence.
func cutLabel(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size > 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

// hostFromInstance derives a host label from the instance name: "persona-" plus the instance lowercased, with
// everything but letters and digits turned into single dashes.
func hostFromInstance(inst string) string {
	var b strings.Builder
	dash := false
	for i := 0; i < len(inst); i++ {
		c := inst[i]
		switch {
		case 'a' <= c && c <= 'z', '0' <= c && c <= '9':
			b.WriteByte(c)
			dash = false
		case 'A' <= c && c <= 'Z':
			b.WriteByte(c + 'a' - 'A')
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	h := strings.TrimSuffix(b.String(), "-")
	switch {
	case h == "":
		return "persona"
	case !strings.HasPrefix(h, "persona"):
		h = "persona-" + h
	}
	return strings.TrimSuffix(cutLabel(h, maxLabel), "-")
}

// encodeTXT turns the map into sorted "key=value" strings.
func encodeTXT(m map[string]string) ([]string, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			return nil, errors.New("mdns: empty TXT key")
		}
		for i := 0; i < len(k); i++ {
			if k[i] < 0x20 || k[i] > 0x7e || k[i] == '=' {
				return nil, fmt.Errorf("mdns: TXT key %q must be printable ASCII without '='", k)
			}
		}
		s := k + "=" + m[k]
		if len(s) > 255 {
			return nil, fmt.Errorf("mdns: TXT entry %q is longer than 255 bytes", k)
		}
		out = append(out, s)
	}
	return out, nil
}

// decodeTXT reads "key=value" strings into a map (RFC 6763 §6.4): keys are case-insensitive (lowercased here),
// a string without '=' is a key with an empty value, and only the first occurrence of a key counts.
func decodeTXT(txt []string) map[string]string {
	m := make(map[string]string, len(txt))
	for _, s := range txt {
		k, v, _ := strings.Cut(s, "=")
		if k == "" {
			continue
		}
		k = asciiLower(k)
		if _, dup := m[k]; !dup {
			m[k] = v
		}
	}
	return m
}

// served is a validated Service with its DNS names.
type served struct {
	instance string
	typ      name // _persona-hub._tcp.local.
	inst     name // <instance>._persona-hub._tcp.local.
	host     name // <host>.local.
	port     uint16
	txt      []string
}

func newServed(svc Service) (*served, error) {
	typ, err := parseType(svc.Type)
	if err != nil {
		return nil, err
	}
	inst := cutLabel(svc.Instance, maxLabel)
	if inst == "" {
		return nil, errors.New("mdns: empty instance name")
	}
	host := strings.TrimSuffix(svc.Host, ".")
	if l := len(host) - len(".local"); l >= 0 && asciiEqualFold(host[l:], ".local") {
		host = host[:l]
	}
	if host == "" {
		host = hostFromInstance(inst)
	}
	if strings.Contains(host, ".") || len(host) > maxLabel {
		return nil, fmt.Errorf("mdns: host %q must be a single label", svc.Host)
	}
	if svc.Port <= 0 || svc.Port > 0xffff {
		return nil, fmt.Errorf("mdns: bad port %d", svc.Port)
	}
	txt, err := encodeTXT(svc.TXT)
	if err != nil {
		return nil, err
	}
	s := &served{
		instance: inst,
		typ:      typ,
		inst:     append(name{inst}, typ...),
		host:     name{host, "local"},
		port:     uint16(svc.Port),
		txt:      txt,
	}
	// Fail now rather than on every send if the records cannot be packed (too many or too long TXT entries).
	if _, err := s.response(nil, recordTTL); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *served) ptr(ttl uint32) record {
	return record{name: s.typ, typ: typePTR, class: classIN, ttl: ttl, target: s.inst}
}

func (s *served) meta(ttl uint32) record {
	return record{name: metaName, typ: typePTR, class: classIN, ttl: ttl, target: s.typ}
}

func (s *served) srv(ttl uint32) record {
	return record{name: s.inst, typ: typeSRV, class: classIN | topBit, ttl: ttl, port: s.port, target: s.host}
}

func (s *served) txtRecord(ttl uint32) record {
	return record{name: s.inst, typ: typeTXT, class: classIN | topBit, ttl: ttl, txt: s.txt}
}

// addrRecords makes the A (want4) and AAAA (want6) records of the host. Zones are dropped; loopback and
// unspecified addresses are skipped.
func (s *served) addrRecords(addrs []netip.Addr, ttl uint32, want4, want6 bool) []record {
	var out []record
	for _, a := range addrs {
		a = a.Unmap().WithZone("")
		if !a.IsValid() || a.IsLoopback() || a.IsUnspecified() {
			continue
		}
		switch {
		case a.Is4() && want4:
			out = append(out, record{name: s.host, typ: typeA, class: classIN | topBit, ttl: ttl, addr: a})
		case a.Is6() && want6:
			out = append(out, record{name: s.host, typ: typeAAAA, class: classIN | topBit, ttl: ttl, addr: a})
		}
	}
	return out
}

// response is the announcement / answer to a PTR query: the PTR as the answer, SRV, TXT and the addresses as
// additional records. With ttl 0 it is the goodbye.
func (s *served) response(addrs []netip.Addr, ttl uint32) ([]byte, error) {
	add := append([]record{s.srv(ttl), s.txtRecord(ttl)}, s.addrRecords(addrs, ttl, true, true)...)
	return packMessage(&message{
		flags:       flagResponse | flagAA,
		answers:     []record{s.ptr(ttl)},
		additionals: add,
	})
}

// answer picks the records that answer qs: the asked-for records as answers, the ones a querier will want next
// (SRV / TXT / addresses after a PTR, addresses after an SRV, the other address family) as additional records.
func (s *served) answer(qs []question, addrs []netip.Addr, ttl uint32) (ans, add []record) {
	var ptr, meta, srv, txt, a4, a6 bool
	for _, q := range qs {
		if c := q.class &^ topBit; c != classIN && c != classANY {
			continue
		}
		all := q.typ == typeANY
		switch {
		case q.name.equal(s.typ):
			ptr = ptr || q.typ == typePTR || all
		case q.name.equal(metaName):
			meta = meta || q.typ == typePTR || all
		case q.name.equal(s.inst):
			srv = srv || q.typ == typeSRV || all
			txt = txt || q.typ == typeTXT || all
		case q.name.equal(s.host):
			a4 = a4 || q.typ == typeA || all
			a6 = a6 || q.typ == typeAAAA || all
		}
	}
	if ptr {
		ans = append(ans, s.ptr(ttl))
	}
	if meta {
		ans = append(ans, s.meta(ttl))
	}
	if srv {
		ans = append(ans, s.srv(ttl))
	}
	if txt {
		ans = append(ans, s.txtRecord(ttl))
	}
	ans = append(ans, s.addrRecords(addrs, ttl, a4, a6)...)
	if len(ans) == 0 {
		return nil, nil
	}
	if ptr && !srv {
		add = append(add, s.srv(ttl))
	}
	if ptr && !txt {
		add = append(add, s.txtRecord(ttl))
	}
	if ptr || srv {
		add = append(add, s.addrRecords(addrs, ttl, !a4, !a6)...)
	} else if a4 != a6 {
		add = append(add, s.addrRecords(addrs, ttl, !a4, !a6)...)
	}
	return ans, add
}
