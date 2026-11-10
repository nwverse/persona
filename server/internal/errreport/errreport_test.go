package errreport

import (
	"bytes"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
)

func TestScrubText(t *testing.T) {
	s := NewScrubber("build-box.example.net", "studio")
	cases := []struct{ in, want string }{
		{"dial https://user:pw@api.example.com:8443/v1/items?id=7&token=x#frag", "dial https://<host>/v1/items"},
		{"Authorization: Bearer abcdefghijklmnop", "Authorization: <redacted> <redacted>"},
		{"token=cafe00a1&x=1", "token=" + Redacted + "&x=1"},
		{`{"password": "hunter22"}`, `{"password": "` + Redacted + `"}`},
		{"key pext_abcdef0123456789 leaked", "key " + Redacted + " leaked"},
		{"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.sig", "jwt " + Redacted},
		{"hash 0123456789abcdef0123456789abcdef", "hash " + Redacted},
		{"blob QWxhZGRpbjpvcGVuIHNlc2FtZQ1234567890abcd", "blob " + Redacted},
		{"mail me@example.com now", "mail <email> now"},
		{"from 198.51.100.20:5432 and [2001:db8:85a3:0:0:8a2e:370:7334]", "from <ip> and <ip>"},
		{"dial tcp [2001:db8::c3d4:e5f6:7890]:443 via fe80::21a:2bff:fe3c:4d5e%en0", "dial tcp <ip>:443 via <ip>%en0"},
		{"route 2001:db8:1:2::/64 and 2001:db8:1:2:3::5", "route <ip>/64 and <ip>"},
		{"at 12:30:45 ok", "at 12:30:45 ok"},
		{"open /Users/alice/Library/x and /home/bob/.persona and C:\\Users\\carol\\a", "open ~/Library/x and ~/.persona and ~\\a"},
		{"/home/user/code", "~/code"},
		{"on build-box.example.net and Studio and studio.local", "on <machine> and <machine> and <machine>.local"},
		{"InboxViewModelLoadMoreItemsUseCaseImpl failed", "InboxViewModelLoadMoreItemsUseCaseImpl failed"},
		{"session expired", "session expired"},
		// a secret's name with a prefix: db_password, PGPASSWORD, aws_secret_access_key, X-Auth-Token
		{"db_password=hunter2 PGPASSWORD=hunter3 private_key=abc123 x_auth_token=zzz1234",
			"db_password=" + Redacted + " PGPASSWORD=" + Redacted + " private_key=" + Redacted + " x_auth_token=" + Redacted},
		{"aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "aws_secret_access_key=" + Redacted},
		{"MYSQL_PWD=pw1 GITHUB_TOKEN: ghx1 X-Auth-Token: abc1", "MYSQL_PWD=" + Redacted + " GITHUB_TOKEN: " + Redacted + " X-Auth-Token: " + Redacted},
		{`{"ssh_passphrase": "s3same", "aws_credentials": "c1"}`, `{"ssh_passphrase": "` + Redacted + `", "aws_credentials": "` + Redacted + `"}`},
		// not secrets: a bare "key" inside a word or after a prefix is a name like any other, and only names that end in
		// a secret's name count (sort_key, max_tokens and PASSWORD_FILE are not secrets)
		{"keyboard: x monkey=3 sort_key=name max_tokens=4096 PASSWORD_FILE=/run/x", "keyboard: x monkey=3 sort_key=name max_tokens=4096 PASSWORD_FILE=/run/x"},
	}
	for _, c := range cases {
		if got := s.Text(c.in); got != c.want {
			t.Errorf("Text(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A rune whose lowercase has another byte length (U+0130, the Kelvin sign) must not stop mixed-case names from
	// being hidden, nor shift where they are cut out.
	mac := NewScrubber("Alices-MacBook-Pro")
	for in, want := range map[string]string{
		"on Alices-MacBook-Pro":                       "on <machine>",
		"\u0130stanbul on Alices-MacBook-Pro":         "\u0130stanbul on <machine>",
		"5 \u212A on ALICES-MACBOOK-PRO.local":        "5 \u212A on <machine>.local",
		"\u023A Alices-MacBook-Pro \u0130 x":          "\u023A <machine> \u0130 x", // one grows, one shrinks: same total length
		"\u212A\u0130\u0130 alices-macbook-pro x":     "\u212A\u0130\u0130 <machine> x",
		"\u212Bngstr\u00F6m Alices-MacBook-Pro\u0130": "\u212Bngstr\u00F6m <machine>\u0130",
	} {
		got := mac.Text(in)
		if got != want || !utf8.ValidString(got) {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
	if got := NewScrubber("kiln-box").Text("on \u212Ailn-Box"); got != "on <machine>" { // the Kelvin sign folds to k
		t.Errorf("Kelvin sign: %q", got)
	}
	if got := s.Message(`send "the user's words" and 「日本語」 'it''s' failed`); strings.Contains(got, "words") || strings.Contains(got, "日本語") {
		t.Errorf("quoted spans stay: %q", got)
	}
}

func TestScrubReportLimits(t *testing.T) {
	r := wire.ErrorReport{Message: strings.Repeat("x", 5000), Tags: map[string]string{"Bad Key": "v", "screen": "inbox"}}
	for range 8 {
		e := wire.ErrorException{Type: "E"}
		for range 100 {
			e.Frames = append(e.Frames, wire.ErrorFrame{Function: "f", Line: -3})
		}
		r.Exceptions = append(r.Exceptions, e)
	}
	for range 50 {
		r.Breadcrumbs = append(r.Breadcrumbs, wire.ErrorBreadcrumb{Category: "c", Message: strings.Repeat("y", 500)})
	}
	NewScrubber().Report(&r)
	if len(r.Message) != MessageMost || len(r.Exceptions) != ExceptionsMost || len(r.Exceptions[0].Frames) != FramesMost ||
		r.Exceptions[0].Frames[0].Line != 0 || len(r.Breadcrumbs) != CrumbsMost || len(r.Breadcrumbs[0].Message) != CrumbMost {
		t.Fatalf("limits: msg %d exc %d frames %d crumbs %d", len(r.Message), len(r.Exceptions), len(r.Exceptions[0].Frames), len(r.Breadcrumbs))
	}
	if _, ok := r.Tags["Bad Key"]; ok || r.Tags["screen"] != "inbox" {
		t.Fatalf("tags: %v", r.Tags)
	}
}

func TestNormalize(t *testing.T) {
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	r := wire.ErrorReport{ID: "a1", Source: "web", Kind: "exception", Time: "garbage"}
	if err := Normalize(&r, now); err != nil || r.Level != "error" || r.Time != now.Format(time.RFC3339Nano) || r.Count != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	for _, bad := range []wire.ErrorReport{{ID: "", Source: "web", Kind: "log"}, {ID: "x", Source: "phone", Kind: "log"},
		{ID: "x", Source: "web", Kind: "oops"}, {ID: "x", Source: "web", Kind: "log", Level: "info"}, {ID: "a b", Source: "web", Kind: "log"}} {
		if Normalize(&bad, now) == nil {
			t.Errorf("taken: %+v", bad)
		}
	}
}

func TestFingerprintAndGate(t *testing.T) {
	a := wire.ErrorReport{Source: "hub", Kind: "log", Message: "2026/11/06 09:00:00 dial timeout after 31s (conn 7)"}
	b := wire.ErrorReport{Source: "hub", Kind: "log", Message: "2026/11/06 09:05:00 dial timeout after 2s (conn 12)"}
	c := wire.ErrorReport{Source: "web", Kind: "log", Message: a.Message}
	if Fingerprint(&a) != Fingerprint(&b) || Fingerprint(&a) == Fingerprint(&c) {
		t.Fatal("numbers are not the problem; the source is")
	}
	ex := func(fn string) wire.ErrorReport {
		return wire.ErrorReport{Source: "android", Kind: "crash", Exceptions: []wire.ErrorException{{Type: "NPE", Value: "v" + fn,
			Frames: []wire.ErrorFrame{{Module: "lib", Function: "x"}, {Module: "app", Function: fn, InApp: true}}}}}
	}
	e1, e2, e3 := ex("load"), ex("load"), ex("save")
	if Fingerprint(&e1) != Fingerprint(&e2) || Fingerprint(&e1) == Fingerprint(&e3) {
		t.Fatal("by type and in-app frames")
	}

	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	g := NewGate(func() time.Time { return now })
	mk := func(id, fp string) *wire.ErrorReport { return &wire.ErrorReport{ID: id, Fingerprint: fp, Count: 1} }
	if !g.Admit(mk("1", "f"), "d") || g.Admit(mk("1", "g"), "d") {
		t.Fatal("an id once")
	}
	if g.Admit(mk("2", "f"), "d") || g.Admit(mk("3", "f"), "d") {
		t.Fatal("the same problem folded")
	}
	now = now.Add(FoldWindow)
	r := mk("4", "f")
	if !g.Admit(r, "d") || r.Count != 3 {
		t.Fatalf("after the window, with what it folded: %d", r.Count)
	}
	now = now.Add(time.Minute)
	n := 0
	for i := range DeviceRate + 5 {
		if g.Admit(mk("r"+string(rune('a'+i)), "p"+string(rune('a'+i))), "d") {
			n++
		}
	}
	if n != DeviceRate {
		t.Fatalf("a device's rate: %d", n)
	}
	if !g.Admit(mk("other", "other"), "hub") {
		t.Fatal("another sender has its own")
	}
}

func TestGoFrames(t *testing.T) {
	var frames []wire.ErrorFrame
	func() {
		defer func() {
			_ = recover()
			frames = GoFrames(debug.Stack())
		}()
		panicHere()
	}()
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	last := frames[len(frames)-1]
	if last.Function != "panicHere" || !strings.HasSuffix(last.Module, "/errreport") || !last.InApp ||
		last.File != "server/internal/errreport/errreport_test.go" || last.Line == 0 {
		t.Fatalf("innermost last, the panicking function: %+v", last)
	}
}

func panicHere() { panic("boom") }

func TestTee(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	var got []wire.ErrorReport
	on := false
	tee := &Tee{W: &buf, Build: Build{Source: "hub", Release: "v1"}, On: func() bool { return on },
		Sink: func(r wire.ErrorReport) { mu.Lock(); got = append(got, r); mu.Unlock() }}
	_, _ = tee.Write([]byte("2026/11/06 09:00:00 inbox: round failed: x\n"))
	on = true
	_, _ = tee.Write([]byte("2026/11/06 09:00:00 all good\n2026/11/06 09:00:01 [abc] GET /api/v1/x -> 502 (3ms)\n"))
	_, _ = tee.Write([]byte("2026/11/06 09:00:02 mail: sync error: "))
	_, _ = tee.Write([]byte("refused\n"))
	_, _ = tee.Write([]byte(Reported + " handler GET /x: panic\nstack error\n"))
	if !strings.Contains(buf.String(), "all good") || !strings.Contains(buf.String(), "round failed") {
		t.Fatal("every line goes on")
	}
	if len(got) != 2 || got[0].Message != "[abc] GET /api/v1/x -> 502 (3ms)" || got[1].Message != "mail: sync error: refused" ||
		got[0].Kind != "log" || got[0].Release != "v1" {
		t.Fatalf("only error lines while on, whole, without the time; a reported write skipped: %+v", got)
	}
}
