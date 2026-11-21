package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

// Proxy-only mode on the command line (#12299): persona-link proxy --always | --no-always, --proxy-always and
// --no-proxy-always on pair and install, and what status says about it.

func readConfig(t *testing.T, f *fakeEnv) *link.Config {
	t.Helper()
	c, err := link.Read(f.configPath())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProxyCommandAlways(t *testing.T) {
	f := newFakeEnv(t)
	run := func(args ...string) string {
		t.Helper()
		f.out.Reset()
		if err := proxyCmd(f.env, args); err != nil {
			t.Fatalf("proxy %v: %v", args, err)
		}
		noSecret(t, "persona-link proxy", f.out.String())
		return f.out.String()
	}
	// No URL saved: refused, whatever the environment has (a service rarely has it).
	t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
	if err := proxyCmd(f.env, []string{"--always"}); !errors.Is(err, link.ErrProxyAlwaysNeedsURL) {
		t.Fatalf("--always without a URL: %v", err)
	}
	if _, err := link.Read(f.configPath()); err == nil {
		t.Fatal("a refused --always wrote link.json")
	}
	const always = "代理：http://proxy.example:8080（总是走代理）\n"
	if got := run(proxyWithSecret, "--always"); got != always {
		t.Fatalf("<url> --always: %q", got)
	}
	if c := readConfig(t, f); c.Proxy != proxyWithSecret || !c.ProxyAlways {
		t.Fatalf("link.json: %+v", c)
	}
	if got := run(); got != always {
		t.Fatalf("show: %q", got)
	}
	// A new URL keeps it.
	if got := run("http://other.example:8080"); got != "代理：http://other.example:8080（总是走代理）\n" {
		t.Fatalf("a new URL: %q", got)
	}
	if got := run("--no-always"); got != "代理：http://other.example:8080\n" {
		t.Fatalf("--no-always: %q", got)
	}
	if c := readConfig(t, f); c.Proxy != "http://other.example:8080" || c.ProxyAlways {
		t.Fatalf("--no-always kept: %+v", c)
	}
	// --always on its own uses the URL saved earlier; --off and --env turn it off.
	for _, off := range []string{"--off", "--env"} {
		if got := run("--always"); !strings.Contains(got, "（总是走代理）") {
			t.Fatalf("--always with a saved URL: %q", got)
		}
		if got := run(off); strings.Contains(got, "总是走代理") || readConfig(t, f).ProxyAlways {
			t.Fatalf("%s kept proxy-only mode: %q", off, got)
		}
		run("http://other.example:8080")
	}
	for _, args := range [][]string{{"--always", "--off"}, {"--env", "--always"}, {"--always", "--no-always"}, {"--always", "--always"}} {
		if err := proxyCmd(f.env, args); !errors.Is(err, errUsage) {
			t.Fatalf("proxy %v: %v", args, err)
		}
	}
	// Under Always use SSH: refused before anything is saved.
	if _, err := link.SetSSH(f.configPath(), &link.SSH{Server: "user@example.com", Target: "127.0.0.1:18663", Always: true}); err != nil {
		t.Fatal(err)
	}
	err := proxyCmd(f.env, []string{proxyWithSecret, "--always"})
	if !errors.Is(err, link.ErrProxyAlwaysSSHAlways) {
		t.Fatalf("--always under Always use SSH: %v", err)
	}
	if c := readConfig(t, f); c.Proxy != "http://other.example:8080" || c.ProxyAlways {
		t.Fatalf("a refused --always changed link.json: %+v", c)
	}
	if txt := link.ErrorText(i18n.Terminal(), err); !strings.Contains(txt, "ssh --no-always") {
		t.Fatalf("the refusal does not say what to do: %q", txt)
	}
	// English.
	if _, err := link.SetSSH(f.configPath(), nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERSONA_LANG", "en")
	if got := run(proxyWithSecret, "--always"); got != "Proxy: http://proxy.example:8080 (always uses the proxy)\n" {
		t.Fatalf("English: %q", got)
	}
}

func TestSSHAlwaysRefusedInProxyOnlyMode(t *testing.T) {
	f := newFakeEnv(t)
	f.pairHere(t)
	if err := proxyCmd(f.env, []string{proxyWithSecret, "--always"}); err != nil {
		t.Fatal(err)
	}
	err := sshCmd(f.env, []string{"user@example.com", "--always"})
	if !errors.Is(err, link.ErrSSHAlwaysProxyAlways) {
		t.Fatalf("ssh --always in proxy-only mode: %v", err)
	}
	if txt := link.ErrorText(i18n.Terminal(), err); !strings.Contains(txt, "proxy --no-always") {
		t.Fatalf("the refusal does not say what to do: %q", txt)
	}
	if c := readConfig(t, f); c.SSH != nil || !c.ProxyAlways {
		t.Fatalf("a refused ssh --always changed link.json: %+v", c)
	}
	// A tunnel it need not use can be saved; it waits unused until proxy-only mode is off.
	if err := sshCmd(f.env, []string{"user@example.com", "--no-always"}); err != nil {
		t.Fatalf("ssh --no-always in proxy-only mode: %v", err)
	}
	if err := proxyCmd(f.env, []string{"--no-always"}); err != nil {
		t.Fatal(err)
	}
	if err := sshCmd(f.env, []string{"user@example.com", "--always"}); err != nil {
		t.Fatalf("ssh --always after proxy --no-always: %v", err)
	}
}

func TestPairAndInstallTakeProxyAlways(t *testing.T) {
	f := newFakeEnv(t)
	for _, args := range [][]string{{"--listen", "--proxy-always"}, {"--listen", "--no-proxy-always"}} {
		if err := pairCmd(f.env, args); err == nil || !strings.Contains(err.Error(), "--proxy-always") {
			t.Fatalf("pair %v: %v", args, err)
		}
		if err := install(f.env, args); err == nil || !strings.Contains(err.Error(), "--proxy-always") {
			t.Fatalf("install %v: %v", args, err)
		}
	}
	const noAddresses = "persona://link?token=jp_x"
	// No URL saved or given: refused before pairing.
	if err := pairCmd(f.env, []string{"--proxy-always", noAddresses}); !errors.Is(err, link.ErrProxyAlwaysNeedsURL) {
		t.Fatalf("pair --proxy-always without a URL: %v", err)
	}
	if _, err := link.Read(f.configPath()); err == nil {
		t.Fatal("a refused --proxy-always wrote link.json")
	}
	// Always use SSH and proxy-only mode on one command line: refused before anything is saved.
	both := []string{"--ssh", "user@example.com", "--ssh-always", "--proxy", proxyWithSecret, "--proxy-always", noAddresses}
	if err := pairCmd(f.env, both); !errors.Is(err, link.ErrSSHAlwaysProxyAlways) {
		t.Fatalf("pair with both: %v", err)
	}
	if err := install(f.env, both[:len(both)-1]); !errors.Is(err, link.ErrSSHAlwaysProxyAlways) {
		t.Fatalf("install with both: %v", err)
	}
	if _, err := link.Read(f.configPath()); err == nil {
		t.Fatal("a refused command line wrote link.json")
	}
	// Saved before the pairing, which then goes through the proxy alone (here: a link that cannot be paired).
	if err := pairCmd(f.env, []string{"--proxy", proxyWithSecret, "--proxy-always", noAddresses}); err == nil {
		t.Fatal("pairing a link without addresses")
	}
	if c := readConfig(t, f); c.Proxy != proxyWithSecret || !c.ProxyAlways {
		t.Fatalf("pair --proxy --proxy-always: %+v", c)
	}
	// A saved proxy-only mode refuses Always use SSH from a pairing too.
	sshLink := "persona://link?url=http://hub.example.com:18661&tls=18662&fp=pin&hub=h1&token=jp_x&machine=pi"
	if err := pairCmd(f.env, []string{"--ssh", "user@example.com", "--ssh-always", sshLink}); !errors.Is(err, link.ErrSSHAlwaysProxyAlways) {
		t.Fatalf("pair --ssh-always in proxy-only mode: %v", err)
	}
	if err := pairCmd(f.env, []string{"--no-proxy-always", noAddresses}); err == nil {
		t.Fatal("pairing a link without addresses")
	}
	if c := readConfig(t, f); c.Proxy != proxyWithSecret || c.ProxyAlways {
		t.Fatalf("pair --no-proxy-always: %+v", c)
	}
}

func TestStatusReportsProxyOnly(t *testing.T) {
	f := newFakeEnv(t)
	f.pairHere(t)
	if err := link.SetProxy(f.configPath(), proxyWithSecret); err != nil {
		t.Fatal(err)
	}
	statusText := func() string {
		t.Helper()
		f.out.Reset()
		_ = status(f.env, nil)
		noSecret(t, "status", f.out.String())
		return f.out.String()
	}
	statusJSON := func() (report, string) {
		t.Helper()
		f.out.Reset()
		_ = status(f.env, []string{"--json"})
		noSecret(t, "status --json", f.out.String())
		var rep report
		if err := json.Unmarshal(f.out.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
		return rep, f.out.String()
	}
	if rep, raw := statusJSON(); rep.ProxyAlways || strings.Contains(raw, "proxy_always") {
		t.Fatalf("off: %s", raw)
	}
	if strings.Contains(statusText(), "总是走代理") {
		t.Fatal("off: marked proxy-only")
	}

	if err := link.SetProxyAlways(f.configPath(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := link.SetSSH(f.configPath(), &link.SSH{Server: "user@example.com", Target: "127.0.0.1:18663"}); err != nil {
		t.Fatal(err)
	}
	// Not running: the proxy first, marked; direct addresses and the tunnel marked as skipped.
	txt := statusText()
	proxied := strings.Index(txt, "  · 代理 http://proxy.example:8080（总是走代理）\n")
	direct := strings.Index(txt, "  · 内网 https://192.0.2.10:18662（已设总是走代理）\n")
	manual := strings.Index(txt, "hub.example.com:18662（手动加的，已设总是走代理）\n")
	tunnel := strings.Index(txt, "user@example.com → 127.0.0.1:18663（已设总是走代理）\n")
	if proxied < 0 || direct < proxied || manual < direct || tunnel < manual {
		t.Fatalf("status in proxy-only mode:\n%s", txt)
	}
	if rep, raw := statusJSON(); !rep.ProxyAlways || rep.Proxy != "http://proxy.example:8080" {
		t.Fatalf("status --json: %s", raw)
	}

	release, err := link.LockRun(f.configPath())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	since := f.now()
	f.saveStatus(t, link.Status{State: "connected", Mode: link.ModeActive, Machine: "pi", HubName: "home",
		Address: "https://hub.example.com:18662", Kind: link.KindProxy, Proxy: "http://proxy.example:8080", Since: &since, Node: "online"}, 4242)
	txt = statusText()
	for _, want := range []string{"链路：已连接 · 代理 hub.example.com:18662", "  · 代理 http://proxy.example:8080（在用，总是走代理）\n"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("status output missing %q:\n%s", want, txt)
		}
	}
	if rep, raw := statusJSON(); !rep.ProxyAlways || rep.Link == nil || rep.Link.Kind != link.KindProxy {
		t.Fatalf("status --json connected: %s", raw)
	}

	// The same in English.
	t.Setenv("PERSONA_LANG", "en")
	txt = statusText()
	for _, want := range []string{"  · Proxy http://proxy.example:8080 (in use, always uses the proxy)", "proxy-only mode is set"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("English status output missing %q:\n%s", want, txt)
		}
	}
}
