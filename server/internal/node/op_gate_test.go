package node

import (
	"strings"
	"testing"
)

// The gate's own reading of a command line: op as the program after assignments and wrappers, inside sh -c, in a
// command substitution, or via an absolute path; never as an argument.
func TestOpCalls(t *testing.T) {
	for _, c := range []struct {
		cmd       string
		wantCalls int
	}{
		{"op vault list", 1},
		{"/opt/homebrew/bin/op vault list", 1},
		{"sudo op vault list", 1},
		{"sudo -u me op vault list", 1},
		{"env FOO=1 op vault list", 1},
		{"timeout 5 op vault list", 1},
		{"timeout -s KILL 5 op vault list", 1},
		{"nice -n 10 op vault list", 1},
		{"exec op vault list", 1},
		{"sh -c 'op vault list'", 1},
		{"bash -lc 'op read op://v/i/f'", 1},
		{"echo $(op read op://v/i/f)", 1},
		{"op vault list | head -3", 1},
		{"echo hi && op whoami", 1},
		{"op vault list; op whoami", 2},
		{"echo op", 0},
		{"grep -r op .", 0},
		{"echo 'op vault list'", 0},
		{"openssl version", 0},
		{"ls -la", 0},
	} {
		calls, ok := opCalls(c.cmd)
		if !ok {
			t.Errorf("%q: could not be followed", c.cmd)
			continue
		}
		if len(calls) != c.wantCalls {
			t.Errorf("%q: %d op calls, want %d (%v)", c.cmd, len(calls), c.wantCalls, calls)
		}
	}
}

func TestOpBashVerdict(t *testing.T) {
	oldOS := opGateOS
	opGateOS = "darwin"
	t.Cleanup(func() { opGateOS = oldOS })
	t.Setenv(opServiceAccountEnv, "")

	denied := []string{
		"op vault list",
		"/opt/homebrew/bin/op vault list",
		"op",
		"op item get GitHub --fields password",
		"op read op://vault/item/field",
		"op run -- env",
		"sudo op vault list",
		"timeout 5 op vault list",
		"env FOO=1 op vault list",
		"sh -c 'op vault list'",
		"bash -lc 'op read op://v/i/f'",
		"echo $(op read op://v/i/f)",
		"op vault list | head -3",
		"op read op://v/i/f | ssh host cat",
		"(op vault list)",
		`bash -c "op vault list"`,
	}
	for _, cmd := range denied {
		if why := opBashVerdict(cmd); why == "" {
			t.Errorf("%q: allowed, want refused", cmd)
		} else if !strings.HasPrefix(why, "Refused:") {
			t.Errorf("%q: reason does not start with Refused: %q", cmd, why)
		}
	}

	allowed := []string{
		"op --version",
		"op -v",
		"op version",
		"op whoami",
		"op whoami --account my",
		"echo op",
		"grep -r op .",
		"echo 'op vault list'",
		"echo 'op",
		"openssl version",
		"ls -la",
		"",
		"echo 'x",
	}
	for _, cmd := range allowed {
		if why := opBashVerdict(cmd); why != "" {
			t.Errorf("%q: refused, want allowed: %q", cmd, why)
		}
	}
}

// The service-account token authorizes without the App, whether the hook's own environment has it or the command
// carries it inline.
func TestOpBashVerdictServiceAccount(t *testing.T) {
	oldOS := opGateOS
	opGateOS = "darwin"
	t.Cleanup(func() { opGateOS = oldOS })

	t.Setenv(opServiceAccountEnv, "")
	if why := opBashVerdict("OP_SERVICE_ACCOUNT_TOKEN=ops_abc op vault list"); why != "" {
		t.Errorf("an inline token: refused, want allowed: %q", why)
	}
	if why := opBashVerdict("env OP_SERVICE_ACCOUNT_TOKEN=ops_abc op vault list"); why != "" {
		t.Errorf("an env token: refused, want allowed: %q", why)
	}
	if why := opBashVerdict("OP_SERVICE_ACCOUNT_TOKEN= op vault list"); why == "" {
		t.Error("an empty inline token: allowed, want refused")
	}

	t.Setenv(opServiceAccountEnv, "ops_abc")
	if why := opBashVerdict("op vault list"); why != "" {
		t.Errorf("a token in the environment: refused, want allowed: %q", why)
	}
}

// The approval dialog is the Mac App's: elsewhere op never asks, so nothing is refused.
func TestOpBashVerdictOtherOS(t *testing.T) {
	oldOS := opGateOS
	opGateOS = "linux"
	t.Cleanup(func() { opGateOS = oldOS })
	t.Setenv(opServiceAccountEnv, "")
	if why := opBashVerdict("op vault list"); why != "" {
		t.Errorf("on Linux: refused, want allowed: %q", why)
	}
}
