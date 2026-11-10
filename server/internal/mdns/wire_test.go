package mdns

import (
	"maps"
	"math/rand/v2"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

// One whole message each way, for the wire tests: the responder sends served.response (mdns.go) and the browser reads
// messages with group into its cache (browse.go); these two use the same functions without the sockets or the cache.

// parseEntries reads the instances of the service type browse that one message describes, without any cache:
// an instance with only a PTR in this message comes back with an empty Host and Port 0. Queries, malformed
// messages and a bad browse type give nil.
func parseEntries(msg []byte, browse string) []Entry {
	typ, err := parseType(browse)
	if err != nil {
		return nil
	}
	m, err := unpackMessage(msg)
	if err != nil || !m.isResponse() {
		return nil
	}
	now := time.Now()
	insts, addrs := group(m, typ)
	out := make([]Entry, 0, len(insts))
	for _, g := range insts {
		e := Entry{Instance: g.label, Seen: now}
		switch {
		case g.ptr != nil:
			e.TTL = clampTTL(g.ptr.ttl)
		case g.srv != nil:
			e.TTL = clampTTL(g.srv.ttl)
		case g.txt != nil:
			e.TTL = clampTTL(g.txt.ttl)
		}
		if g.srv != nil {
			e.Host = g.srv.target.String()
			e.Port = int(g.srv.port)
			for _, a := range addrs {
				if a.name.equal(g.srv.target) && !slices.Contains(e.Addrs, a.addr) {
					e.Addrs = append(e.Addrs, a.addr)
				}
			}
			slices.SortFunc(e.Addrs, netip.Addr.Compare)
		}
		if g.txt != nil {
			e.TXT = decodeTXT(g.txt.txt)
		}
		out = append(out, e)
	}
	return out
}

// buildResponse packs the announcement of svc with addrs (ttl 0: the goodbye).
func buildResponse(svc Service, addrs []netip.Addr, ttl uint32) ([]byte, error) {
	s, err := newServed(svc)
	if err != nil {
		return nil, err
	}
	return s.response(addrs, ttl)
}

func testService() Service {
	return Service{
		Instance: "Hub. One 2",
		Type:     "_persona-hub._tcp",
		Port:     8443,
		TXT: map[string]string{
			"hub_id": "1a2b3c4d",
			"name":   "家里 hub",
			"fp":     "sha256:00ff",
			"tls":    "8443",
			"flag":   "",
		},
	}
}

func testAddrs() []netip.Addr {
	return []netip.Addr{
		netip.MustParseAddr("2001:db8::10"),
		netip.MustParseAddr("192.0.2.10"),
		netip.MustParseAddr("fe80::1%eth0"), // the zone is not on the wire
		netip.MustParseAddr("127.0.0.1"),    // loopback is never advertised
	}
}

func mustType(t *testing.T, s string) name {
	t.Helper()
	n, err := parseType(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBuildParseRoundTrip(t *testing.T) {
	svc := testService()
	b, err := buildResponse(svc, testAddrs(), recordTTL)
	if err != nil {
		t.Fatal(err)
	}
	for _, browse := range []string{"_persona-hub._tcp", "_PERSONA-HUB._TCP.local.", "_persona-hub._tcp.LOCAL"} {
		es := parseEntries(b, browse)
		if len(es) != 1 {
			t.Fatalf("browse %q: got %d entries, want 1", browse, len(es))
		}
		e := es[0]
		if e.Instance != svc.Instance {
			t.Errorf("Instance = %q, want %q", e.Instance, svc.Instance)
		}
		if e.Host != "persona-hub-one-2.local." {
			t.Errorf("Host = %q", e.Host)
		}
		if e.Port != 8443 {
			t.Errorf("Port = %d", e.Port)
		}
		if !maps.Equal(e.TXT, svc.TXT) {
			t.Errorf("TXT = %v, want %v", e.TXT, svc.TXT)
		}
		want := []netip.Addr{
			netip.MustParseAddr("192.0.2.10"),
			netip.MustParseAddr("2001:db8::10"),
			netip.MustParseAddr("fe80::1"),
		}
		if !slices.Equal(e.Addrs, want) {
			t.Errorf("Addrs = %v, want %v", e.Addrs, want)
		}
		if e.TTL != recordTTL*time.Second {
			t.Errorf("TTL = %v", e.TTL)
		}
	}
	if es := parseEntries(b, "_other._tcp"); len(es) != 0 {
		t.Errorf("other type: got %v", es)
	}

	// The instance is one label on the wire, dots and spaces included.
	m, err := unpackMessage(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.answers) != 1 || len(m.additionals) != 2+3 {
		t.Fatalf("sections: %d answers, %d additionals", len(m.answers), len(m.additionals))
	}
	target := m.answers[0].target
	if len(target) != 4 || target[0] != "Hub. One 2" {
		t.Fatalf("PTR target labels = %q", target)
	}
	if got := target.String(); got != `Hub\.\032One\0322._persona-hub._tcp.local.` {
		t.Errorf("PTR target = %s", got)
	}
	if m.answers[0].class != classIN {
		t.Errorf("PTR is a shared record, class = %#x", m.answers[0].class)
	}
	for _, r := range m.additionals {
		if r.class != classIN|topBit {
			t.Errorf("%d record should have the cache-flush bit, class = %#x", r.typ, r.class)
		}
	}
}

func TestBuildCompresses(t *testing.T) {
	b, err := buildResponse(testService(), testAddrs(), recordTTL)
	if err != nil {
		t.Fatal(err)
	}
	// "_persona-hub" is written once; later names point at it.
	if n := strings.Count(string(b), "_persona-hub"); n != 1 {
		t.Errorf("type label written %d times", n)
	}
}

func TestInstanceCut(t *testing.T) {
	svc := testService()
	svc.Instance = strings.Repeat("家", 30) // 90 bytes
	b, err := buildResponse(svc, nil, recordTTL)
	if err != nil {
		t.Fatal(err)
	}
	es := parseEntries(b, svc.Type)
	if len(es) != 1 || es[0].Instance != strings.Repeat("家", 21) {
		t.Fatalf("got %+v", es)
	}
	if !strings.HasPrefix(es[0].Host, "persona.") {
		t.Errorf("Host = %q", es[0].Host) // nothing usable in the instance: the bare prefix
	}
}

func TestExplicitHost(t *testing.T) {
	svc := testService()
	svc.Host = "persona-1a2b3c4d.local."
	es := parseEntries(must(buildResponse(svc, nil, recordTTL)), svc.Type)
	if len(es) != 1 || es[0].Host != "persona-1a2b3c4d.local." {
		t.Fatalf("got %+v", es)
	}
}

func TestHostFromInstance(t *testing.T) {
	for in, want := range map[string]string{
		"1A2B3C4D":                   "persona-1a2b3c4d",
		"Hub. One 2":                 "persona-hub-one-2",
		"--x--":                      "persona-x",
		"persona-1a2b":               "persona-1a2b",
		"家":                          "persona",
		strings.Repeat("ab-", 40):    "persona-" + strings.TrimSuffix(strings.Repeat("ab-", 19)[:maxLabel-len("persona-")], "-"),
		"0123456789abcdef0123456789": "persona-0123456789abcdef0123456789",
	} {
		got := hostFromInstance(in)
		if got != want {
			t.Errorf("hostFromInstance(%q) = %q, want %q", in, got, want)
		}
		if len(got) > maxLabel {
			t.Errorf("hostFromInstance(%q) is %d bytes", in, len(got))
		}
	}
}

func TestBuildErrors(t *testing.T) {
	for name, mut := range map[string]func(*Service){
		"empty instance": func(s *Service) { s.Instance = "" },
		"bad type":       func(s *Service) { s.Type = "persona" },
		"no proto":       func(s *Service) { s.Type = "_persona._sctp" },
		"three labels":   func(s *Service) { s.Type = "_a._b._tcp" },
		"port 0":         func(s *Service) { s.Port = 0 },
		"port too big":   func(s *Service) { s.Port = 70000 },
		"key with =":     func(s *Service) { s.TXT = map[string]string{"a=b": "c"} },
		"empty key":      func(s *Service) { s.TXT = map[string]string{"": "c"} },
		"long value":     func(s *Service) { s.TXT = map[string]string{"k": strings.Repeat("x", 300)} },
		"dotted host":    func(s *Service) { s.Host = "a.example" },
		"too much TXT": func(s *Service) {
			s.TXT = map[string]string{}
			for i := range 60 {
				s.TXT[strings.Repeat("k", 3)+string(rune('A'+i))] = strings.Repeat("v", 200)
			}
		},
	} {
		svc := testService()
		mut(&svc)
		if _, err := buildResponse(svc, nil, recordTTL); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestEmptyTXT(t *testing.T) {
	svc := testService()
	svc.TXT = nil
	m, err := unpackMessage(must(buildResponse(svc, nil, recordTTL)))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range m.additionals {
		if r.typ == typeTXT && !slices.Equal(r.txt, []string{""}) {
			t.Errorf("empty TXT = %q, want one empty string", r.txt)
		}
	}
	es := parseEntries(must(buildResponse(svc, nil, recordTTL)), svc.Type)
	if len(es) != 1 || len(es[0].TXT) != 0 {
		t.Fatalf("got %+v", es)
	}
}

func TestPTROnly(t *testing.T) {
	typ := mustType(t, "_persona-hub._tcp")
	b, err := packMessage(&message{flags: flagResponse | flagAA, answers: []record{
		{name: typ, typ: typePTR, class: classIN, ttl: 4500, target: append(name{"Solo"}, typ...)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	es := parseEntries(b, "_persona-hub._tcp")
	if len(es) != 1 {
		t.Fatalf("got %d entries", len(es))
	}
	e := es[0]
	if e.Instance != "Solo" || e.Host != "" || e.Port != 0 || e.Addrs != nil || e.TTL != 4500*time.Second {
		t.Errorf("got %+v", e)
	}
}

func TestGoodbye(t *testing.T) {
	es := parseEntries(must(buildResponse(testService(), testAddrs(), 0)), "_persona-hub._tcp")
	if len(es) != 1 || es[0].TTL != 0 || es[0].Instance != "Hub. One 2" {
		t.Fatalf("got %+v", es)
	}
}

func TestParseIgnoresQueries(t *testing.T) {
	typ := mustType(t, "_persona-hub._tcp")
	b, err := packMessage(&message{questions: []question{{name: typ, typ: typePTR, class: classIN}}})
	if err != nil {
		t.Fatal(err)
	}
	if es := parseEntries(b, "_persona-hub._tcp"); es != nil {
		t.Errorf("query parsed as %+v", es)
	}
	if es := parseEntries(b, "not a type"); es != nil {
		t.Errorf("bad browse type gave %+v", es)
	}
}

func TestDecodeTXT(t *testing.T) {
	got := decodeTXT([]string{"a=1", "B=2", "flag", "a=dup", "=x", "", "k=v=w"})
	want := map[string]string{"a": "1", "b": "2", "flag": "", "k": "v=w"}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseType(t *testing.T) {
	for _, ok := range []string{"_persona-hub._tcp", "_persona-hub._tcp.", "_persona-hub._tcp.local", "_x._UDP.Local."} {
		if _, err := parseType(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "_tcp", "persona._tcp", "_persona.tcp", "_persona._tcp.example", "_._tcp", "_a._b._tcp"} {
		if _, err := parseType(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestNameString(t *testing.T) {
	for _, c := range []struct {
		n    name
		want string
	}{
		{nil, "."},
		{name{"a b.c", "local"}, `a\032b\.c.local.`},
		{name{`back\slash`, "local"}, `back\\slash.local.`},
		{name{"tab\there\x7f"}, `tab\009here\127.`},
		{name{"家里"}, "家里."},
	} {
		if got := c.n.String(); got != c.want {
			t.Errorf("%q: got %s, want %s", []string(c.n), got, c.want)
		}
	}
}

func TestAnswer(t *testing.T) {
	s, err := newServed(testService())
	if err != nil {
		t.Fatal(err)
	}
	addrs := []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("2001:db8::10")}
	types := func(rs []record) []uint16 {
		var out []uint16
		for _, r := range rs {
			out = append(out, r.typ)
		}
		return out
	}
	for _, c := range []struct {
		desc     string
		q        []question
		ans, add []uint16
	}{
		{"PTR", []question{{s.typ, typePTR, classIN}}, []uint16{typePTR}, []uint16{typeSRV, typeTXT, typeA, typeAAAA}},
		{"PTR with QU bit, other case", []question{{name{"_PERSONA-HUB", "_TCP", "LOCAL"}, typePTR, classIN | topBit}}, []uint16{typePTR}, []uint16{typeSRV, typeTXT, typeA, typeAAAA}},
		{"meta", []question{{metaName, typePTR, classIN}}, []uint16{typePTR}, nil},
		{"SRV", []question{{s.inst, typeSRV, classIN}}, []uint16{typeSRV}, []uint16{typeA, typeAAAA}},
		{"TXT", []question{{s.inst, typeTXT, classIN}}, []uint16{typeTXT}, nil},
		{"ANY instance", []question{{s.inst, typeANY, classIN}}, []uint16{typeSRV, typeTXT}, []uint16{typeA, typeAAAA}},
		{"A", []question{{name{"PERSONA-HUB-ONE-2", "local"}, typeA, classIN}}, []uint16{typeA}, []uint16{typeAAAA}},
		{"AAAA", []question{{s.host, typeAAAA, classIN}}, []uint16{typeAAAA}, []uint16{typeA}},
		{"ANY host", []question{{s.host, typeANY, classIN}}, []uint16{typeA, typeAAAA}, nil},
		{"SRV and A", []question{{s.inst, typeSRV, classIN}, {s.host, typeA, classIN}}, []uint16{typeSRV, typeA}, []uint16{typeAAAA}},
		{"unrelated", []question{{name{"other", "local"}, typeA, classIN}}, nil, nil},
		{"other class", []question{{s.typ, typePTR, 3}}, nil, nil},
		{"other type", []question{{s.typ, typeSRV, classIN}}, nil, nil},
	} {
		ans, add := s.answer(c.q, addrs, recordTTL)
		if !slices.Equal(types(ans), c.ans) || !slices.Equal(types(add), c.add) {
			t.Errorf("%s: answers %v additionals %v, want %v %v", c.desc, types(ans), types(add), c.ans, c.add)
		}
	}
	// Meta answer points at the type.
	ans, _ := s.answer([]question{{metaName, typePTR, classIN}}, nil, recordTTL)
	if !ans[0].target.equal(s.typ) {
		t.Errorf("meta target = %s", ans[0].target)
	}
}

func TestCompressionPointers(t *testing.T) {
	hdr := []byte{0, 0, 0x84, 0, 0, 0, 0, 1, 0, 0, 0, 0} // response, one answer
	for desc, body := range map[string][]byte{
		"self loop":                          {0xC0, 12},
		"forward pointer":                    {0xC0, 20, 0, 0, 0, 0, 0, 0, 0, 0},
		"pointer to label start of own name": {1, 'a', 0xC0, 12},
		"reserved label":                     {0x40, 0},
		"truncated pointer":                  {0xC0},
		"label past end":                     {10, 'a'},
	} {
		msg := append(slices.Clone(hdr), body...)
		if _, err := unpackMessage(msg); err == nil {
			t.Errorf("%s: no error", desc)
		}
	}
	// A legal backward pointer: question "local." at 12, answer name points at it.
	msg := []byte{0, 0, 0x84, 0, 0, 1, 0, 1, 0, 0, 0, 0,
		5, 'l', 'o', 'c', 'a', 'l', 0, 0, 1, 0, 1, // question local. A IN
		0xC0, 12, 0, 1, 0, 1, 0, 0, 0, 10, 0, 4, 192, 0, 2, 1} // answer local. A 192.0.2.1
	m, err := unpackMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.answers) != 1 || m.answers[0].name.String() != "local." || m.answers[0].addr != netip.MustParseAddr("192.0.2.1") {
		t.Errorf("got %+v", m.answers)
	}
}

func TestNameTooLong(t *testing.T) {
	long := name{strings.Repeat("a", 63), strings.Repeat("b", 63), strings.Repeat("c", 63), strings.Repeat("d", 63)}
	if _, err := packMessage(&message{questions: []question{{long, typeA, classIN}}}); err == nil {
		t.Error("pack: no error for a 257-byte name")
	}
	// The same name on the wire is refused when reading.
	msg := []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, l := range long {
		msg = append(msg, byte(len(l)))
		msg = append(msg, l...)
	}
	msg = append(msg, 0, 0, 1, 0, 1)
	if _, err := unpackMessage(msg); err == nil {
		t.Error("unpack: no error for a 257-byte name")
	}
}

func TestSkipsUnknownRecords(t *testing.T) {
	b := must(buildResponse(testService(), testAddrs(), recordTTL))
	// Append a record of a type this package does not read (NSEC, root name) and one of another class.
	msg := append(slices.Clone(b),
		0, 0, 47, 0, 1, 0, 0, 0, 120, 0, 3, 1, 2, 3,
		0, 0, 1, 0, 3, 0, 0, 0, 120, 0, 4, 192, 0, 2, 99)
	msg[11] += 2 // two more additionals
	es := parseEntries(msg, "_persona-hub._tcp")
	if len(es) != 1 || es[0].Port != 8443 || len(es[0].Addrs) != 3 {
		t.Fatalf("got %+v", es)
	}
}

// TestRandomBytes feeds garbage and mutated packets to every parser: nothing may panic.
func TestRandomBytes(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	valid := must(buildResponse(testService(), testAddrs(), recordTTL))
	typ := mustType(t, "_persona-hub._tcp")
	br := newBrowser(typ, "")
	now := time.Now()
	for i := range 20000 {
		var b []byte
		switch i % 3 {
		case 0:
			b = make([]byte, rng.IntN(600))
			for j := range b {
				b[j] = byte(rng.UintN(256))
			}
		case 1:
			b = slices.Clone(valid)
			for range 1 + rng.IntN(8) {
				b[rng.IntN(len(b))] = byte(rng.UintN(256))
			}
		default:
			b = slices.Clone(valid[:rng.IntN(len(valid)+1)])
		}
		parseEntries(b, "_persona-hub._tcp")
		if m, err := unpackMessage(b); err == nil {
			br.apply(m, "eth0", now)
			br.sweep(now.Add(time.Duration(rng.IntN(300)) * time.Second))
		}
	}
}

// FuzzParseEntries: nothing may panic (go test runs the seeds; go test -fuzz explores).
func FuzzParseEntries(f *testing.F) {
	f.Add(must(buildResponse(testService(), testAddrs(), recordTTL)))
	f.Add(must(buildResponse(testService(), nil, 0)))
	f.Add([]byte{0, 0, 0x84, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0xC0, 12})
	f.Fuzz(func(t *testing.T, b []byte) {
		parseEntries(b, "_persona-hub._tcp")
		if m, err := unpackMessage(b); err == nil {
			newBrowser(name{"_persona-hub", "_tcp", "local"}, "").apply(m, "eth0", time.Now())
		}
	})
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
