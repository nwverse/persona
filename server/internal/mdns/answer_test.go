package mdns

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/ipv4"
)

// countingRunner is an advertising runner with one IPv4 link that counts its address reads. The link's interface
// index does not exist, so send fails at SetMulticastInterface; the socket's multicast TTL is 0 and loopback is off,
// so nothing would leave the host or reach another socket even if it did not.
func countingRunner(t *testing.T) (r *runner, l *link, reads *int) {
	t.Helper()
	svc := testService()
	r, err := newRunner(Options{Advertise: &svc, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no UDP socket here: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	r.p4 = ipv4.NewPacketConn(c)
	if err := r.p4.SetMulticastTTL(0); err != nil {
		t.Fatal(err)
	}
	if err := r.p4.SetMulticastLoopback(false); err != nil {
		t.Fatal(err)
	}
	ifi := net.Interface{Index: 1 << 30, Name: "none0", Flags: net.FlagUp | net.FlagRunning | net.FlagMulticast}
	if _, err := net.InterfaceByIndex(ifi.Index); err == nil {
		t.Skipf("interface index %d exists here", ifi.Index)
	}
	l = &link{ifi: ifi, on4: true}
	r.links[ifi.Index] = l
	reads = new(int)
	r.readAddrs = func(*net.Interface) []netip.Addr {
		*reads++
		return testAddrs()
	}
	return r, l, reads
}

func queryPacket(t testing.TB, l *link, n name, typ uint16) packet {
	t.Helper()
	b, err := packMessage(&message{questions: []question{{name: n, typ: typ, class: classIN}}})
	if err != nil {
		t.Fatal(err)
	}
	return packet{data: b, ifindex: l.ifi.Index, src: &net.UDPAddr{IP: net.IPv4(192, 0, 2, 7), Port: mdnsPort}}
}

// TestAnswerReadsAddressesOnlyWhenAnswering: a query for another service and a repeat of an answer multicast less
// than a second ago read no addresses; an answer without address records reads none either.
func TestAnswerReadsAddressesOnlyWhenAnswering(t *testing.T) {
	r, l, reads := countingRunner(t)
	now := time.Now()
	check := func(desc string, wantReads int, wantSent bool) {
		t.Helper()
		if *reads != wantReads {
			t.Errorf("%s: %d address reads, want %d", desc, *reads, wantReads)
		}
		if sent := !r.lastSend.IsZero(); sent != wantSent {
			t.Errorf("%s: answered %v, want %v", desc, sent, wantSent)
		}
		*reads, r.lastSend = 0, time.Time{}
	}

	r.handle(queryPacket(t, l, mustType(t, "_airplay._tcp"), typePTR), now)
	check("query for another service", 0, false)

	own := queryPacket(t, l, r.svc.typ, typePTR)
	for i := range 5 {
		r.handle(own, now.Add(time.Duration(i)*100*time.Millisecond))
	}
	check("5 PTR queries within 1 s", 1, true)

	r.handle(own, now.Add(1100*time.Millisecond))
	check("PTR query 1.1 s after the answer", 1, true)

	r.handle(queryPacket(t, l, metaName, typePTR), now)
	check("service enumeration query", 0, true)
}

// BenchmarkHandleForeignQuery is the cost of a query for another service on a served interface, which every
// advertising runner pays for each such query on the LAN.
func BenchmarkHandleForeignQuery(b *testing.B) {
	var lo *net.Interface
	ifs, _ := net.Interfaces()
	for i := range ifs {
		if ifs[i].Flags&net.FlagLoopback != 0 && ifs[i].Flags&net.FlagUp != 0 {
			lo = &ifs[i]
			break
		}
	}
	if lo == nil {
		b.Skip("no loopback interface")
	}
	svc := testService()
	r, err := newRunner(Options{Advertise: &svc})
	if err != nil {
		b.Fatal(err)
	}
	l := &link{ifi: *lo, on4: true}
	r.links[lo.Index] = l
	airplay, err := parseType("_airplay._tcp")
	if err != nil {
		b.Fatal(err)
	}
	p := queryPacket(b, l, airplay, typePTR)
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		r.handle(p, now)
	}
}
