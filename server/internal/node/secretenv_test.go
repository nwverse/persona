package node

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// handoffTestValue is the one value staged in these tests: nothing the node sends or logs may carry it.
const handoffTestValue = "synthetic-handoff-value-5b1e9d"

func stageForTest(t *testing.T, n *Node, env ...proto.SecretEnvVar) (proto.SecretStageResult, error) {
	t.Helper()
	raw, _ := json.Marshal(proto.SecretStageParams{Env: env})
	out, err := n.secretStageCall(raw)
	if err != nil {
		return proto.SecretStageResult{}, err
	}
	return out.(proto.SecretStageResult), nil
}

func envVar(name, value string) proto.SecretEnvVar {
	return proto.SecretEnvVar{Name: name, Value: []byte(value)}
}

// Staged values go once, with the machine's launch key, to the first who asks with the ticket; nobody after.
func TestSecretStageAndTake(t *testing.T) {
	launchKeyHome(t)
	n := launchKeyTestNode()
	key, err := ensureLaunchKey(nodeclient.LaunchKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := n.secretStageCall(json.RawMessage(`{"check":true}`)); err != nil || out.(proto.SecretStageResult).Ticket != "" {
		t.Fatalf("check: %v %v", out, err)
	}
	for what, env := range map[string][]proto.SecretEnvVar{
		"none":         nil,
		"PATH":         {envVar("PATH", handoffTestValue)},
		"a loader's":   {envVar("LD_PRELOAD", handoffTestValue)},
		"a mac loader": {envVar("DYLD_INSERT_LIBRARIES", handoffTestValue)},
		"Persona's":    {envVar("PERSONA_HOME", handoffTestValue)},
		"lower case":   {envVar("github_token", handoffTestValue)},
		"too short":    {envVar("GITHUB_TOKEN", "short")},
		"a NUL":        {envVar("GITHUB_TOKEN", "abcdefgh\x00ij")},
		"twice":        {envVar("A_TOKEN", handoffTestValue), envVar("A_TOKEN", handoffTestValue)},
		"nine":         {envVar("A1", handoffTestValue), envVar("A2", handoffTestValue), envVar("A3", handoffTestValue), envVar("A4", handoffTestValue), envVar("A5", handoffTestValue), envVar("A6", handoffTestValue), envVar("A7", handoffTestValue), envVar("A8", handoffTestValue), envVar("A9", handoffTestValue)},
	} {
		if _, err := stageForTest(t, n, env...); err == nil || strings.Contains(err.Error(), handoffTestValue) {
			t.Errorf("%s: %v", what, err)
		}
	}

	res, err := stageForTest(t, n, envVar("GITHUB_TOKEN", handoffTestValue))
	if err != nil || len(res.Ticket) != 64 || time.Until(res.ExpiresAt) > proto.SecretStageKeep || time.Until(res.ExpiresAt) < time.Minute {
		t.Fatalf("staged: %+v %v", res, err)
	}
	ask := func(ticket, k string) map[string]any {
		return n.secretEnvFor(sockRequest{Kind: "secret.env", Ticket: ticket, LaunchKey: k})
	}
	for what, k := range map[string]string{"no key": "", "another key": strings.Repeat("0", launchKeyLen)} {
		if resp := ask(res.Ticket, k); resp["error"] == nil || resp["env"] != nil {
			t.Fatalf("%s: %v", what, resp)
		}
	}
	if !n.handoffs().has(res.Ticket) {
		t.Fatal("a refused caller used the ticket up")
	}
	if resp := ask(res.Ticket, key); fmtEnv(resp) != "GITHUB_TOKEN="+handoffTestValue {
		t.Fatalf("with the key and the ticket: %v", resp)
	}
	if resp := ask(res.Ticket, key); resp["error"] == nil || resp["env"] != nil {
		t.Fatalf("a second time: %v", resp)
	}
	if resp := ask("not-a-ticket", key); resp["error"] == nil {
		t.Fatalf("an unknown ticket: %v", resp)
	}

	// Not used in time: gone.
	res, _ = stageForTest(t, n, envVar("GITHUB_TOKEN", handoffTestValue))
	s := n.handoffs()
	s.mu.Lock()
	st := s.staged[res.Ticket]
	st.expires = time.Now().Add(-time.Second)
	s.staged[res.Ticket] = st
	s.mu.Unlock()
	if n.handoffs().has(res.Ticket) {
		t.Fatal("expired, still good")
	}
	if resp := ask(res.Ticket, key); resp["error"] == nil {
		t.Fatalf("expired: %v", resp)
	}
}

func fmtEnv(resp map[string]any) string {
	env, _ := resp["env"].([]string)
	return strings.Join(env, " ")
}

// Through the real socket, with what personad launch --ticket sends.
func TestSecretEnvOverTheSocket(t *testing.T) {
	launchKeyHome(t)
	n := launchKeyTestNode()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := n.listenOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	res, err := stageForTest(t, n, envVar("GITHUB_TOKEN", handoffTestValue), envVar("NPM_TOKEN", handoffTestValue+"-2"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := fetchSecretEnv(ctx, res.Ticket)
	if err != nil || strings.Join(env, " ") != "GITHUB_TOKEN="+handoffTestValue+" NPM_TOKEN="+handoffTestValue+"-2" {
		t.Fatalf("fetched: %v %v", env, err)
	}
	if _, err := fetchSecretEnv(ctx, res.Ticket); err == nil || !strings.Contains(err.Error(), "approve it again") {
		t.Fatalf("again: %v", err)
	}
}

// What the node sends has every handed-over value hidden, in any string, and stays valid JSON.
func TestSecretScrubberHidesValuesInLines(t *testing.T) {
	s := &secretScrubber{}
	plain := []byte(`{"method":"session.items","params":{"items":[{"text":"nothing here"}]}}` + "\n")
	if got := s.line(plain); !bytes.Equal(got, plain) {
		t.Fatalf("nothing to hide: %s", got)
	}
	value := "tok<en>&" + handoffTestValue + `"quoted"\back`
	s.add([]byte(value))
	encoded := func(v any, html bool) []byte {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(html)
		_ = enc.Encode(v)
		return b.Bytes()
	}
	for _, html := range []bool{false, true} {
		for what, v := range map[string]any{
			"a transcript item": map[string]any{"method": "session.items", "params": map[string]any{"items": []any{map[string]any{"text": "the token is " + value + " (from the env)"}}}},
			"a card's input":    map[string]any{"method": "hook.event", "params": map[string]any{"tool_input": map[string]any{"command": "curl -H 'Authorization: " + value + "'"}}},
			"a screen":          map[string]any{"result": map[string]any{"text": "$ echo $TOKEN\n" + value + "\n$ "}},
			"base64 of it":      map[string]any{"result": map[string]any{"text": "printf %s $TOKEN | base64 → " + base64.StdEncoding.EncodeToString([]byte(value))}},
			"next to escapes":   map[string]any{"result": map[string]any{"text": "\n\t" + value + "\\\n"}},
			"twice":             map[string]any{"result": map[string]any{"a": value, "b": value + value}},
		} {
			got := s.line(encoded(v, html))
			if !json.Valid(bytes.TrimSpace(got)) {
				t.Errorf("%s (html %v): not JSON any more: %s", what, html, got)
			}
			if bytes.Contains(got, []byte(handoffTestValue)) || !bytes.Contains(got, []byte(proto.Redacted)) {
				t.Errorf("%s (html %v): %s", what, html, got)
			}
		}
	}

	// A match must start a character: never inside an escape, so the line stays valid.
	s2 := &secretScrubber{}
	s2.add([]byte("nsynthetic-value"))
	line := encoded(map[string]any{"text": "a\nsynthetic-value and nsynthetic-value"}, false)
	got := s2.line(line)
	if !json.Valid(bytes.TrimSpace(got)) || !strings.Contains(string(got), `a\nsynthetic-value and `+proto.Redacted) {
		t.Fatalf("escape boundary: %s", got)
	}
}

// The connection's writer hides values only once there are some, and reports the bytes it was given.
func TestScrubWriter(t *testing.T) {
	var out bytes.Buffer
	s := &secretScrubber{}
	w := scrubWriter{w: &out, s: s}
	line := []byte(`{"result":{"text":"` + handoffTestValue + `"}}` + "\n")
	if n, err := w.Write(line); err != nil || n != len(line) || out.String() != string(line) {
		t.Fatalf("nothing staged: %d %v %s", n, err, out.String())
	}
	s.add([]byte(handoffTestValue))
	out.Reset()
	if n, err := w.Write(line); err != nil || n != len(line) || strings.Contains(out.String(), handoffTestValue) {
		t.Fatalf("staged: %d %v %s", n, err, out.String())
	}
}

// Staging hides the values in what the node sends from then on; terminal bytes sent as base64 are hidden at their
// source.
func TestStagedValuesAreHidden(t *testing.T) {
	launchKeyHome(t)
	n := launchKeyTestNode()
	if _, err := stageForTest(t, n, envVar("GITHUB_TOKEN", handoffTestValue)); err != nil {
		t.Fatal(err)
	}
	if got := n.scrubBytes([]byte("out: " + handoffTestValue)); string(got) != "out: "+proto.Redacted {
		t.Fatalf("terminal bytes: %q", got)
	}
	var out bytes.Buffer
	p := rpc.New("t", scrubWriter{w: &out, s: n.handoffs().scrub})
	_ = p.Notify("session.items", map[string]any{"items": []any{map[string]any{"text": handoffTestValue}}})
	if strings.Contains(out.String(), handoffTestValue) {
		t.Fatalf("sent: %s", out.String())
	}
}

// Only a new Claude Code start whose command personad can exec takes a ticket, and only one staged now; its command
// line carries the ticket, never a value, and runs the agent through personad launch.
func TestSecretStartCommand(t *testing.T) {
	launchKeyHome(t)
	n := launchKeyTestNode()
	n.bin = "/opt/persona/personad"
	res, err := stageForTest(t, n, envVar("GITHUB_TOKEN", handoffTestValue))
	if err != nil {
		t.Fatal(err)
	}
	code := func(err error) string {
		var re *rpc.Error
		if errors.As(err, &re) {
			return re.Code
		}
		return ""
	}
	for what, err := range map[string]error{
		"codex":          n.secretStartProblem("codex", "codex", "", res.Ticket),
		"a resume":       n.secretStartProblem("claude", "gw", "11111111-2222-3333-4444-555555555555", res.Ticket),
		"an rc function": n.secretStartProblem("claude", "myclaude", "", res.Ticket),
		"not staged":     n.secretStartProblem("claude", "gw", "", "nope"),
		"omp":            n.secretStartProblem(ompAgent, ompAgent, "", res.Ticket),
	} {
		if err == nil {
			t.Errorf("%s: allowed", what)
		}
	}
	if c := code(n.secretStartProblem("claude", "gw", "", "nope")); c != secretEnvGoneCode {
		t.Errorf("not staged: %q", c)
	}
	for _, launcher := range []string{"gw", "claude"} {
		if err := n.secretStartProblem("claude", launcher, "", res.Ticket); err != nil {
			t.Errorf("%s: %v", launcher, err)
		}
	}

	spec := claudeLaunch{Launcher: "gw"}
	line := n.withSecretTicket(n.claudeCommandLine(spec, "--session-id x", shellQuote), "gw", res.Ticket, shellQuote)
	if !strings.HasPrefix(line, "/opt/persona/personad launch --ticket "+res.Ticket+" gw --session-id x") || strings.Contains(line, handoffTestValue) {
		t.Fatalf("managed: %s", line)
	}
	spec.Launcher = "claude"
	line = n.withSecretTicket(n.claudeCommandLine(spec, "--session-id x", shellQuote), "claude", res.Ticket, shellQuote)
	if !strings.HasPrefix(line, "/opt/persona/personad launch --ticket "+res.Ticket+" -- claude --session-id x") {
		t.Fatalf("plain claude: %s", line)
	}
	dir := "~/.claude-work"
	n.claudeStartDir.Store(&dir)
	line = n.withSecretTicket(n.claudeCommandLine(spec, "--session-id x", shellQuote), "claude", res.Ticket, shellQuote)
	if !strings.HasPrefix(line, `CLAUDE_CONFIG_DIR="$HOME"/.claude-work /opt/persona/personad launch --ticket `+res.Ticket+" -- claude --session-id x") {
		t.Fatalf("plain claude with its directory: %s", line)
	}
}
