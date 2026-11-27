package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	pserve "github.com/nwverse/persona/server/internal/extension/serve"
	"github.com/nwverse/persona/server/internal/rpc"
)

// serveAgentRulesExtensionForTest is a fake agent_rules program: agent_rules.for_start answers rules naming what it was
// asked, nothing for a start on the launcher's default model, and too late for slow-model.
func serveAgentRulesExtensionForTest() {
	_ = pserve.Serve(context.Background(), os.Stdin, os.Stdout, pserve.Program{ID: "qa-rules", Version: "1.0.0",
		Namespaces: map[string]pserve.Handler{
			"agent_rules": func(_ context.Context, method string, params json.RawMessage) (any, error) {
				if method != "for_start" {
					return nil, pserve.UnknownMethod(method)
				}
				var p wire.AgentRulesForStartParams
				_ = json.Unmarshal(params, &p)
				switch p.Model {
				case "":
					return wire.AgentRulesForStartResult{}, nil
				case "slow-model":
					time.Sleep(3 * time.Second)
				}
				return wire.AgentRulesForStartResult{Text: fmt.Sprintf("qa rules for %s %s %s in %s on %s", p.Contribution, p.Agent, p.Model,
					p.Cwd, p.Machine), Rev: "rev-1"}, nil
			}}})
}

// agentRulesPackage is a fake extension contributing agent_rules.
func agentRulesPackage(t *testing.T) string {
	dir := t.TempDir()
	self, _ := os.Executable()
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "rules"), []byte("#!/bin/sh\nPERSONA_TEST_PLUGIN=agent-rules exec "+self+" \"$@\"\n"), 0o755)
	manifest := fmt.Sprintf(`{"manifest_version": 1, "id": "qa-rules", "name": "QA Rules", "version": "1.0.0",
		"engines": {"plugin_protocol": 1}, "runtime": {"command": {%q: "bin/rules"}},
		"contributes": {"agent_rules": [{"id": "rules", "name": "QA rules"}]}}`, runtime.GOOS+"-"+runtime.GOARCH)
	_ = os.WriteFile(filepath.Join(dir, extension.ManifestFile), []byte(manifest), 0o644)
	return dir
}

// No extension contributes agent_rules: nothing is asked. One that does: the install page says so, its program is asked
// with the start (and its contribution) and its rules come back; an empty text adds nothing; one that answers past the
// bounds adds nothing without holding the start up; switched off, it is not asked.
func TestAgentRulesFor(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	ctx := context.Background()
	start := wire.AgentRulesForStartParams{Agent: "claude", Launcher: "claude", Model: "example-model[1m]", Cwd: "/home/me/src/app", Machine: "my-laptop"}

	waitLong(t, "the extensions started", h.startedNow)
	if got := h.AgentRulesFor(ctx, start); got != nil {
		t.Fatalf("no extension contributes: %q", got)
	}

	pkg := agentRulesPackage(t)
	code, pv := call(t, mux, "POST", "/api/v1/extensions/-/preview", master, `{"source": "dir:`+pkg+`"}`)
	if code != 200 {
		t.Fatalf("preview: %d %v", code, pv)
	}
	if !strings.Contains(fmt.Sprint(pv["permissions"]), "agent_rules") {
		t.Fatalf("the install page says it adds rules to new Agents: %v", pv["permissions"])
	}
	if c, body := call(t, mux, "POST", "/api/v1/extensions/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); c != 200 {
		t.Fatalf("install: %d %v", c, body)
	}

	want := "qa rules for rules claude example-model[1m] in /home/me/src/app on my-laptop"
	var got []string
	waitLong(t, "the rules", func() bool {
		got = h.AgentRulesFor(ctx, start)
		return len(got) == 1
	})
	if got[0] != want {
		t.Fatalf("rules %q", got)
	}

	def := start
	def.Model = ""
	if got := h.AgentRulesFor(ctx, def); len(got) != 0 {
		t.Fatalf("the launcher's default model: %q", got)
	}

	slow := start
	slow.Model = "slow-model"
	began := time.Now()
	got = h.AgentRulesFor(ctx, slow)
	if took := time.Since(began); took > agentRulesTotal+500*time.Millisecond {
		t.Errorf("a slow program held the start for %s", took)
	}
	if len(got) != 0 {
		t.Fatalf("a slow program: %q", got)
	}

	if code, out := call(t, mux, "PUT", "/api/v1/extensions/qa-rules/-/contributions/agent_rules/rules", master, `{"enabled": false}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	waitLong(t, "switched off, it is not asked", func() bool { return len(h.extensions.askAgentRulesAll(ctx, start)) == 0 })
}

// fakeRulesCaller answers agent_rules.for_start with raw, or err.
type fakeRulesCaller struct {
	raw string
	err error
}

func (f fakeRulesCaller) Call(_ context.Context, method string, _, out any) error {
	if method != "agent_rules.for_start" {
		return errors.New("unexpected " + method)
	}
	if f.err != nil {
		return f.err
	}
	*(out.(*json.RawMessage)) = json.RawMessage(f.raw)
	return nil
}

// An answer is used only whole: a text of at most 4096 bytes of UTF-8 and a rev of at most 64 characters; anything
// else, and an error, gives no rules. The text is trimmed of the blank lines around it.
func TestAskAgentRulesChecksTheAnswer(t *testing.T) {
	ctx := context.Background()
	p := wire.AgentRulesForStartParams{Contribution: "rules", Agent: "claude", Launcher: "claude", Model: "example-model"}
	text, rev, err := askAgentRules(ctx, fakeRulesCaller{raw: `{"text": "\n  run the doc tests  \n", "rev": "abc"}`}, p, time.Second)
	if err != nil || text != "run the doc tests" || rev != "abc" {
		t.Fatalf("%q %q %v", text, rev, err)
	}
	if text, _, err := askAgentRules(ctx, fakeRulesCaller{raw: `{"text": ""}`}, p, time.Second); err != nil || text != "" {
		t.Fatalf("an empty text: %q %v", text, err)
	}
	exact, _ := json.Marshal(map[string]string{"text": strings.Repeat("a", agentRulesTextMost)})
	if text, _, err := askAgentRules(ctx, fakeRulesCaller{raw: string(exact)}, p, time.Second); err != nil || len(text) != agentRulesTextMost {
		t.Fatalf("4096 bytes: %d %v", len(text), err)
	}
	long, _ := json.Marshal(map[string]string{"text": strings.Repeat("规", agentRulesTextMost/3+1)}) // i18n-ignore: a multi-byte filler
	for name, c := range map[string]fakeRulesCaller{
		"no text":      {raw: `{"rev": "abc"}`},
		"not object":   {raw: `"rules"`},
		"text number":  {raw: `{"text": 3}`},
		"over 4096 B":  {raw: string(long)},
		"long rev":     {raw: `{"text": "x", "rev": "` + strings.Repeat("r", agentRulesRevMost+1) + `"}`},
		"rpc error":    {err: &rpc.Error{Code: "failed", Message: "no target"}},
		"unknown":      {err: &rpc.Error{Code: extension.CodeUnknownMethod, Message: "agent_rules.for_start"}},
		"plain error":  {err: errors.New("broken pipe")},
		"huge payload": {raw: `{"text": "x", "pad": "` + strings.Repeat("p", agentRulesAnswerMost) + `"}`},
	} {
		if text, rev, err := askAgentRules(ctx, c, p, time.Second); err == nil || text != "" || rev != "" {
			t.Errorf("%s: %q %q %v", name, text, rev, err)
		}
	}
}
