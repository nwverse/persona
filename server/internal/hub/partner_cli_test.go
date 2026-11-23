package hub

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/clitext"
)

// partnerCLIAt points the command line (HOME's hub.json) at mh's routes on a plain loopback listener, as the hub's
// listen port is, with its master token.
func partnerCLIAt(t *testing.T, mh *testHub) {
	t.Helper()
	plain := httptest.NewServer(mh.srv.Config.Handler)
	t.Cleanup(plain.Close)
	_, port, _ := net.SplitHostPort(plain.Listener.Addr().String())
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".persona"), 0o700); err != nil {
		t.Fatal(err)
	}
	hubJSON := `{"listen":"127.0.0.1:` + port + `","token":"` + mh.h.cfg.Token + `"}`
	if err := os.WriteFile(filepath.Join(home, ".persona", "hub.json"), []byte(hubJSON), 0o600); err != nil {
		t.Fatal(err)
	}
}

// `personad hub partner …` against two hubs: a code on one, add on the other, list, dial, remove; in the terminal's
// language, the other hub's name without the control characters it came with (the hub drops them; the command line
// prints names inert besides).
func TestPartnerCommands(t *testing.T) {
	peerCLIHome(t)
	t.Setenv("PERSONA_LANG", "en")
	b := startPartnerHub(t, hubB, "Office\u001b[31m", partnerHubOpts{})
	a := startPartnerHub(t, hubA, "家里", partnerHubOpts{noTLS: true})
	run := func(args ...string) (string, error) {
		t.Helper()
		var out strings.Builder
		err := PartnerCommand(context.Background(), args, strings.NewReader(""), &out)
		return out.String(), err
	}
	partnerCLIAt(t, b)
	out, err := run("code")
	if err != nil || !strings.HasPrefix(out, "persona://partner?") {
		t.Fatalf("code: %q %v", out, err)
	}
	code := strings.TrimSpace(out)
	partnerCLIAt(t, a)
	if out, err = run("list"); err != nil || out != "No partner hubs yet.\n" {
		t.Fatalf("list before: %q %v", out, err)
	}
	if _, err := run("code"); err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("code on a hub without TLS: %v", err)
	}
	if out, err = run("add", code); err != nil || !strings.Contains(out, "Paired with partner hub “Office[31m” ("+hubB+"): connected") {
		t.Fatalf("add: %q %v", out, err)
	}
	if out, err = run("list"); err != nil || !strings.HasPrefix(out, hubB+"  Office[31m  this hub connects to it  connected") {
		t.Fatalf("list: %q %v", out, err)
	}
	if out, err = run("dial", hubB, "never"); err != nil || out != "“Office[31m”: waits for it to connect\n" {
		t.Fatalf("dial: %q %v", out, err)
	}
	if out, err = run("remove", hubB); err != nil || out != "Unpaired, and its credentials revoked.\n" {
		t.Fatalf("remove: %q %v", out, err)
	}
	if _, err = run("remove", hubB); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("remove twice: %v", err)
	}
	if _, err = run("add", code); err == nil || !strings.Contains(err.Error(), "used or expired") {
		t.Fatalf("add with a used code: %v", err)
	}
}

func TestPartnerArguments(t *testing.T) {
	for _, tc := range []struct {
		args []string
		ok   bool
	}{
		{args: nil},
		{args: []string{"bogus"}},
		{args: []string{"code"}, ok: true},
		{args: []string{"code", "x"}},
		{args: []string{"list"}, ok: true},
		{args: []string{"list", "--all"}},
		{args: []string{"add"}},
		{args: []string{"add", "persona://partner?code=x"}, ok: true},
		{args: []string{"add", "-"}, ok: true},
		{args: []string{"add", "a", "b"}},
		{args: []string{"add", "--code"}},
		{args: []string{"dial", "hub-abc123", "auto"}, ok: true},
		{args: []string{"dial", "hub-abc123", "never"}, ok: true},
		{args: []string{"dial", "hub-abc123"}},
		{args: []string{"dial", "hub-abc123", "always"}},
		{args: []string{"dial", "--never", "auto"}},
		{args: []string{"remove"}},
		{args: []string{"remove", "hub-abc123"}, ok: true},
		{args: []string{"remove", "hub-abc123", "x"}},
		{args: []string{"remove", "--force"}},
	} {
		err := checkPartnerArgs(tc.args)
		var ue clitext.UsageError
		switch {
		case tc.ok && err != nil:
			t.Errorf("partner %q: %v, want it accepted", tc.args, err)
		case !tc.ok && !errors.As(err, &ue):
			t.Errorf("partner %q: %v, want a usage mistake", tc.args, err)
		}
	}
	// Help anywhere prints the usage, a mistake is found before hub.json is read.
	home := peerCLIHome(t)
	for _, args := range [][]string{{"-h"}, {"help"}, {"dial", "x", "--help"}} {
		var out strings.Builder
		if err := PartnerCommand(context.Background(), args, strings.NewReader(""), &out); err != nil || out.String() != clitext.Named(partnerUsage)+"\n" {
			t.Errorf("partner %q: %q %v", args, out.String(), err)
		}
	}
	var ue clitext.UsageError
	if err := PartnerCommand(context.Background(), []string{"dial", "x"}, strings.NewReader(""), io.Discard); !errors.As(err, &ue) {
		t.Fatalf("partner dial x: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".persona", "hub.json")); !os.IsNotExist(err) {
		t.Errorf("a usage mistake or help touched hub.json: %v", err)
	}
}
