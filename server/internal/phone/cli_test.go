package phone

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func runCLI(t *testing.T, m *Manager, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	// command closes the Manager it is given; hand it a fresh one over the same store and bridge.
	c := New(m.store.Dir)
	c.findBridge, c.browse = m.findBridge, m.browse
	code := command(context.Background(), c, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestCommand(t *testing.T) {
	m, _ := bridgeManager(t)
	if code, out, _ := runCLI(t, m, "list"); code != 0 || !strings.Contains(out, testUDID) || !strings.Contains(out, "Test Phone") {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, errOut := runCLI(t, m, "pair")
	if code != 0 || !strings.Contains(out, "Paired Test Phone") {
		t.Fatalf("pair: %d %q %q", code, out, errOut)
	}
	if code, out, _ := runCLI(t, m, "address", testUDID, "[2001:db8::5]:49152"); code != 0 || !strings.Contains(out, "Saved") {
		t.Fatalf("address: %d %q", code, out)
	}
	code, out, _ = runCLI(t, m, "list", "--json")
	var devices []Device
	if code != 0 || json.Unmarshal([]byte(out), &devices) != nil || len(devices) != 1 || !devices[0].Paired || devices[0].ManualAddress != "[2001:db8::5]:49152" {
		t.Fatalf("list --json: %d %s", code, out)
	}
	if code, _, _ := runCLI(t, m, "address", testUDID, "--clear"); code != 0 {
		t.Fatal("address --clear failed")
	}
	if rec, _ := m.store.Load(testUDID); rec.Address != "" {
		t.Fatalf("address not cleared: %q", rec.Address)
	}
	if code, _, _ := runCLI(t, m, "address", testUDID, "host:0"); code != 1 {
		t.Fatal("a bad address was accepted")
	}
	if code, _, _ := runCLI(t, m, "forget", testUDID); code != 0 {
		t.Fatal("forget failed")
	}
	if code, _, errOut := runCLI(t, m, "forget", testUDID); code != 1 || !strings.Contains(errOut, "isn't paired") {
		t.Fatalf("forget twice: %d %q", code, errOut)
	}
	t.Setenv("FAKE_PAIR_FAIL", "1")
	if code, _, errOut := runCLI(t, m, "pair", testUDID); code != 1 || !strings.Contains(errOut, "Trust") {
		t.Fatalf("pair failure: %d %q", code, errOut)
	}
	for _, args := range [][]string{{}, {"nope"}, {"pair", "../x"}, {"forget"}, {"call", testUDID}, {"call", testUDID, "tap", "{bad"}} {
		if code, _, _ := runCLI(t, m, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, out, _ := runCLI(t, m, "-h"); code != 0 || !strings.Contains(out, "phone pair") {
		t.Fatalf("-h: %d %q", code, out)
	}
}

func TestCommandBridgeMissing(t *testing.T) {
	m, _ := bridgeManager(t)
	m.findBridge = func() (string, error) { return "", &Error{Code: CodeBridgeMissing, Message: "x"} }
	if code, _, errOut := runCLI(t, m, "pair"); code != 1 || !strings.Contains(errOut, BridgeName) {
		t.Fatalf("pair: %d %q", code, errOut)
	}
}
