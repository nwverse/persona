package iphone

import (
	"net/netip"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func rrHeader(name string, t dnsmessage.Type) dnsmessage.ResourceHeader {
	return dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Type: t, Class: dnsmessage.ClassINET, TTL: 120}
}

func response(t *testing.T, answers, additionals []dnsmessage.Resource) []byte {
	t.Helper()
	m := dnsmessage.Message{Header: dnsmessage.Header{Response: true, Authoritative: true}, Answers: answers, Additionals: additionals}
	b, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const testInstance = "Phone One._remotepairing._tcp.local."

func fullResponse(t *testing.T, identifier string, port uint16, a4 [4]byte, a6 [16]byte) []byte {
	return response(t,
		[]dnsmessage.Resource{{Header: rrHeader(ServiceType, dnsmessage.TypePTR), Body: &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(testInstance)}}},
		[]dnsmessage.Resource{
			{Header: rrHeader(testInstance, dnsmessage.TypeSRV), Body: &dnsmessage.SRVResource{Port: port, Target: dnsmessage.MustNewName("phone-one.local.")}},
			{Header: rrHeader(testInstance, dnsmessage.TypeTXT), Body: &dnsmessage.TXTResource{TXT: []string{"Identifier=" + identifier, "ver=1"}}},
			{Header: rrHeader("phone-one.local.", dnsmessage.TypeAAAA), Body: &dnsmessage.AAAAResource{AAAA: a6}},
			{Header: rrHeader("phone-one.local.", dnsmessage.TypeA), Body: &dnsmessage.AResource{A: a4}},
		})
}

func TestCacheAssemblesService(t *testing.T) {
	c := newCache()
	ll := netip.MustParseAddr("fe80::1").As16()
	if !c.add(fullResponse(t, "ABC-123", 49152, [4]byte{192, 0, 2, 10}, ll), "en0") {
		t.Fatal("response not taken")
	}
	got := c.services()
	if len(got) != 1 {
		t.Fatalf("services = %+v", got)
	}
	s := got[0]
	if s.Port != 49152 || s.Host != "phone-one.local." || s.Identifier() != "ABC-123" {
		t.Fatalf("service = %+v", s)
	}
	want := []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("fe80::1%en0")}
	if len(s.Addrs) != 2 || s.Addrs[0] != want[0] || s.Addrs[1] != want[1] {
		t.Fatalf("addrs = %v, want %v (IPv4 first, link-local with its zone)", s.Addrs, want)
	}
	if f := FindByIdentifier(got, "abc-123"); f == nil {
		t.Fatal("identifier match is case-insensitive")
	}
	if f := FindByIdentifier(got, "other"); f != nil {
		t.Fatal("matched another phone")
	}
	if len(c.missing()) != 0 {
		t.Fatalf("nothing missing, asked %v", c.missing())
	}
}

func TestCacheDropsLinkLocalWithoutZone(t *testing.T) {
	c := newCache()
	c.add(fullResponse(t, "X", 1, [4]byte{192, 0, 2, 11}, netip.MustParseAddr("fe80::2").As16()), "")
	if s := c.services(); len(s[0].Addrs) != 1 || !s[0].Addrs[0].Is4() {
		t.Fatalf("addrs = %v", s[0].Addrs)
	}
}

func TestCacheAsksForWhatIsMissing(t *testing.T) {
	c := newCache()
	c.add(response(t, []dnsmessage.Resource{{Header: rrHeader(ServiceType, dnsmessage.TypePTR), Body: &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(testInstance)}}}, nil), "")
	qs := c.missing()
	types := map[dnsmessage.Type]bool{}
	for _, q := range qs {
		types[q.Type] = true
		if q.Class&(1<<15) == 0 {
			t.Errorf("%v asked without the unicast-response bit", q)
		}
	}
	if !types[dnsmessage.TypeSRV] || !types[dnsmessage.TypeTXT] {
		t.Fatalf("missing() = %v", qs)
	}
	if FindByIdentifier(c.services(), "") != nil {
		t.Fatal("an empty identifier matches nothing")
	}
}

func TestCacheIgnoresQueries(t *testing.T) {
	q, _ := packQuery([]dnsmessage.Question{question(ServiceType, dnsmessage.TypePTR)})
	if newCache().add(q, "") {
		t.Fatal("a query (someone else's) was taken as a response")
	}
}
