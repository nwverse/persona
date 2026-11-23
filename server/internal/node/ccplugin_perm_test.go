//go:build !windows

package node

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestCCPluginPollAuthorizesPermissionsAndBlocksPersonaHooks(t *testing.T) {
	n, f, home := herdrNode(t)
	noPaneEnv(t)
	fh := withFakeHub(t, n)
	p := bgTranscript(t, home, home, sidA, "default")
	inbox := newFakeInbox(t, p)
	writeOutsideClaude(t, home, sidA, "claude-desktop-3p", "idle", inbox, nil)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	c := pluginSocket(t, n)

	// Older plugin without "permissions" capability does not get permissions: true or Approve.
	status, out := pluginPost(t, c, "poll", map[string]any{
		"session_id":     sidA,
		"plugin_version": "0.2.0",
		"capabilities":   []string{"busy_input", "interrupt", "settings"},
		"wait_ms":        50,
	})
	if status != http.StatusOK || out["permissions"] == true {
		t.Fatalf("legacy poll: %d %v", status, out)
	}
	if n.ccPluginPermissionsCapable(sidA, os.Getpid()) {
		t.Fatal("legacy plugin marked permissionsCapable")
	}

	// New plugin with "permissions" capability gets immediate permissions: true authorization (< 500 ms even with wait_ms=5000).
	start := time.Now()
	status, out = pluginPost(t, c, "poll", map[string]any{
		"session_id":     sidA,
		"plugin_version": "0.3.0",
		"capabilities":   []string{"busy_input", "interrupt", "settings", "permissions"},
		"wait_ms":        5000,
	})
	if status != http.StatusOK || out["permissions"] != true || time.Since(start) > time.Second {
		t.Fatalf("initial permissions poll: %d %v after %v", status, out, time.Since(start))
	}
	if !n.ccPluginPermissionsCapable(sidA, os.Getpid()) {
		t.Fatal("expected permissionsCapable=true after authorized poll")
	}
	if s := byID(listSessions(t, n))[sidA]; s.Capabilities == nil || !s.Capabilities.Approve {
		t.Fatalf("session Capabilities.Approve not set: %+v", s.Capabilities)
	}

	// A ReportOnly global hook must NOT suppress plugin permissions.
	roRaw, _ := json.Marshal(hookInput{HookEventName: "SessionStart", SessionID: sidA, Cwd: home})
	if _, err := n.handleHook(context.Background(), sockRequest{Input: roRaw, ReportOnly: true, AgentPID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if !n.ccPluginPermissionsCapable(sidA, os.Getpid()) {
		t.Fatal("ReportOnly global hook unexpectedly disabled plugin permissions")
	}

	// Process launched with persona-hooks in argv is refused permissions on both /poll and /permission.
	oldArgv := processArgv
	processArgv = func(int) ([]string, error) {
		return []string{"claude", "--plugin-dir", filepath.Join(n.dir, "claude-hooks")}, nil
	}
	t.Cleanup(func() { processArgv = oldArgv })

	status, out = pluginPost(t, c, "poll", map[string]any{
		"session_id":     sidA,
		"plugin_version": "0.3.0",
		"capabilities":   []string{"busy_input", "interrupt", "settings", "permissions"},
		"wait_ms":        50,
	})
	if status != http.StatusOK || out["permissions"] == true {
		t.Fatalf("persona-hooks argv session got permissions: %d %v", status, out)
	}
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id": sidA,
		"req_id":     "req-blocked-argv",
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "ls"},
		"wait_ms":    200,
	})
	if status != http.StatusOK || out["action"] != "passthrough" || len(fh.interactions()) != 0 {
		t.Fatalf("persona-hooks argv /permission: %d %v (cards=%d)", status, out, len(fh.interactions()))
	}

	// Clear argv stub, re-authorize, then verify a non-ReportOnly hook dynamically revokes permissions.
	processArgv = oldArgv
	status, out = pluginPost(t, c, "poll", map[string]any{
		"session_id":     sidA,
		"plugin_version": "0.3.0",
		"capabilities":   []string{"busy_input", "interrupt", "settings", "permissions"},
		"wait_ms":        200,
	})
	if status != http.StatusOK || out["permissions"] != true {
		t.Fatalf("re-authorize poll: %d %v", status, out)
	}
	managedRaw, _ := json.Marshal(hookInput{HookEventName: "SessionStart", SessionID: sidA, Cwd: home})
	if _, err := n.handleHook(context.Background(), sockRequest{Input: managedRaw, ReportOnly: false, AgentPID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if n.ccPluginPermissionsCapable(sidA, os.Getpid()) {
		t.Fatal("managed non-ReportOnly hook did not revoke plugin permissions")
	}
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id": sidA,
		"req_id":     "req-blocked-hook",
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "ls"},
		"wait_ms":    200,
	})
	if status != http.StatusOK || out["action"] != "passthrough" || len(fh.interactions()) != 0 {
		t.Fatalf("managed hook /permission: %d %v (cards=%d)", status, out, len(fh.interactions()))
	}
}

func TestCCPluginPermissionLongPollRenewalAndDecisions(t *testing.T) {
	n, f, home := herdrNode(t)
	noPaneEnv(t)
	fh := withFakeHub(t, n)
	p := bgTranscript(t, home, home, sidA, "default")
	inbox := newFakeInbox(t, p)
	writeOutsideClaude(t, home, sidA, "claude-desktop-3p", "waiting", inbox, nil)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	c := pluginSocket(t, n)

	// 1. Renewable long poll: 2 renew rounds, then phone allows with Always=true during the gap before poll 3.
	release := make(chan struct{})
	yes := true
	fh.mu.Lock()
	fh.answer = func(proto.Interaction) proto.Resolution {
		<-release
		return proto.Resolution{Allow: &yes, Always: true}
	}
	fh.mu.Unlock()

	permReq := map[string]any{
		"session_id":  sidA,
		"req_id":      "req-bash-renew",
		"tool_name":   "Bash",
		"tool_use_id": "toolu_bash_renew",
		"tool_input":  map[string]any{"command": "echo hi"},
		"wait_ms":     60,
	}
	for round := 1; round <= 2; round++ {
		status, out := pluginPost(t, c, "permission", permReq)
		if status != http.StatusOK || out["action"] != "renew" {
			t.Fatalf("round %d: %d %v", round, status, out)
		}
	}
	if got := len(fh.interactions()); got != 1 {
		t.Fatalf("expected exactly 1 hub card across renewals, got %d", got)
	}
	// Resolve in the gap between renewal polls; the next poll must immediately return the cached decision.
	close(release)
	waitFor(t, "perm entry settled", func() bool {
		n.ccPlugins.mu.Lock()
		e := n.ccPlugins.perms["req-bash-renew"]
		n.ccPlugins.mu.Unlock()
		if e == nil {
			return false
		}
		_, _, done := e.outcome()
		return done
	})
	status, out := pluginPost(t, c, "permission", permReq)
	if status != http.StatusOK || out["action"] != "decide" {
		t.Fatalf("decided poll: %d %v", status, out)
	}
	dec, _ := out["decision"].(map[string]any)
	if dec["behavior"] != "allow" {
		t.Fatalf("decision: %v", dec)
	}
	perms, _ := dec["updatedPermissions"].([]any)
	if len(perms) != 1 {
		t.Fatalf("expected session addRules updatedPermissions: %v", dec)
	}

	// 2. Deny with custom message.
	no := false
	fh.mu.Lock()
	fh.answer = func(proto.Interaction) proto.Resolution {
		return proto.Resolution{Allow: &no, Message: "do not run this"}
	}
	fh.mu.Unlock()
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id": sidA,
		"req_id":     "req-deny",
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "rm -rf /tmp/x"},
		"wait_ms":    500,
	})
	dec, _ = out["decision"].(map[string]any)
	if status != http.StatusOK || out["action"] != "decide" || dec["behavior"] != "deny" || dec["message"] != "do not run this" {
		t.Fatalf("deny result: %d %v", status, out)
	}

	// 3. Dismiss ("Handle on computer") returns passthrough.
	fh.mu.Lock()
	fh.answer = func(proto.Interaction) proto.Resolution { return proto.Resolution{Dismiss: true} }
	fh.mu.Unlock()
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id": sidA,
		"req_id":     "req-dismiss",
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "ls"},
		"wait_ms":    500,
	})
	if status != http.StatusOK || out["action"] != "passthrough" {
		t.Fatalf("dismiss result: %d %v", status, out)
	}

	// 4. AskUserQuestion answer & cancel (Skip).
	fh.mu.Lock()
	fh.answer = func(ix proto.Interaction) proto.Resolution {
		if ix.Kind != proto.IxQuestion || !ix.CanCancel {
			t.Errorf("unexpected question interaction: %+v", ix)
		}
		return proto.Resolution{Answers: map[string]string{"Color?": "Blue"}}
	}
	fh.mu.Unlock()
	qInput := map[string]any{"questions": []any{map[string]any{
		"question": "Color?", "header": "Pick", "options": []any{map[string]any{"label": "Blue"}, map[string]any{"label": "Red"}},
	}}}
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id":  sidA,
		"req_id":      "req-ask",
		"tool_name":   "AskUserQuestion",
		"tool_use_id": "toolu_ask_1",
		"tool_input":  qInput,
		"wait_ms":     500,
	})
	dec, _ = out["decision"].(map[string]any)
	updated, _ := dec["updatedInput"].(map[string]any)
	answers, _ := updated["answers"].(map[string]any)
	if status != http.StatusOK || out["action"] != "decide" || dec["behavior"] != "allow" || answers["Color?"] != "Blue" {
		t.Fatalf("ask answer result: %d %v", status, out)
	}

	fh.mu.Lock()
	fh.answer = func(proto.Interaction) proto.Resolution { return proto.Resolution{Cancel: true} }
	fh.mu.Unlock()
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id":  sidA,
		"req_id":      "req-ask-cancel",
		"tool_name":   "AskUserQuestion",
		"tool_use_id": "toolu_ask_2",
		"tool_input":  qInput,
		"wait_ms":     500,
	})
	dec, _ = out["decision"].(map[string]any)
	if status != http.StatusOK || out["action"] != "decide" || dec["behavior"] != "deny" || dec["message"] != prompts.RenderQuestionCancelled() {
		t.Fatalf("ask cancel result: %d %v", status, out)
	}

	// 5. ExitPlanMode approve with setMode and reject with feedback.
	fh.mu.Lock()
	fh.answer = func(ix proto.Interaction) proto.Resolution {
		if ix.Kind != proto.IxPlan || ix.Plan != "Step 1" {
			t.Errorf("unexpected plan interaction: %+v", ix)
		}
		return proto.Resolution{Approve: &yes, Mode: "acceptEdits"}
	}
	fh.mu.Unlock()
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id":  sidA,
		"req_id":      "req-plan-ok",
		"tool_name":   "ExitPlanMode",
		"tool_use_id": "toolu_plan_1",
		"tool_input":  map[string]any{"plan": "Step 1"},
		"wait_ms":     500,
	})
	dec, _ = out["decision"].(map[string]any)
	planPerms, _ := dec["updatedPermissions"].([]any)
	firstPerm, _ := planPerms[0].(map[string]any)
	if status != http.StatusOK || out["action"] != "decide" || dec["behavior"] != "allow" || firstPerm["type"] != "setMode" || firstPerm["mode"] != "acceptEdits" {
		t.Fatalf("plan approve result: %d %v", status, out)
	}

	fh.mu.Lock()
	fh.answer = func(proto.Interaction) proto.Resolution {
		return proto.Resolution{Approve: &no, Feedback: "add tests first"}
	}
	fh.mu.Unlock()
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id": sidA,
		"req_id":     "req-plan-no",
		"tool_name":  "ExitPlanMode",
		"tool_input": map[string]any{"plan": "Step 1"},
		"wait_ms":    500,
	})
	dec, _ = out["decision"].(map[string]any)
	if status != http.StatusOK || out["action"] != "decide" || dec["behavior"] != "deny" || !strings.Contains(dec["message"].(string), "add tests first") {
		t.Fatalf("plan reject result: %d %v", status, out)
	}
}

func TestCCPluginPermissionConcurrentAlsoWaitingAndDoneWithdrawal(t *testing.T) {
	n, f, home := herdrNode(t)
	noPaneEnv(t)
	h := withCancelHub(t, n)
	p := bgTranscript(t, home, home, sidA, "default")
	inbox := newFakeInbox(t, p)
	writeOutsideClaude(t, home, sidA, "claude-desktop-3p", "waiting", inbox, nil)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	c := pluginSocket(t, n)

	// Authorize plugin first so claudebg knows permissionsCapable=true.
	status, out := pluginPost(t, c, "poll", map[string]any{
		"session_id":   sidA,
		"capabilities": []string{"busy_input", "interrupt", "settings", "permissions"},
		"wait_ms":      500,
	})
	if status != http.StatusOK || out["permissions"] != true {
		t.Fatalf("poll: %d %v", status, out)
	}

	// Open two concurrent permission requests ("Also waiting"): one main-loop, one subagent.
	type res struct {
		status int
		out    map[string]any
	}
	res1, res2 := make(chan res, 1), make(chan res, 1)
	go func() {
		st, o := pluginPost(t, c, "permission", map[string]any{
			"session_id":  sidA,
			"req_id":      "req-concurrent-1",
			"tool_name":   "Bash",
			"tool_use_id": "toolu_c1",
			"tool_input":  map[string]any{"command": "echo 1"},
			"wait_ms":     5000,
		})
		res1 <- res{st, o}
	}()
	go func() {
		st, o := pluginPost(t, c, "permission", map[string]any{
			"session_id":  sidA,
			"req_id":      "req-concurrent-2",
			"tool_name":   "Bash",
			"tool_use_id": "toolu_c2",
			"agent_id":    "sub-1",
			"tool_input":  map[string]any{"command": "echo 2"},
			"wait_ms":     5000,
		})
		res2 <- res{st, o}
	}()
	waitFor(t, "both concurrent cards on hub", func() bool { asked, _ := h.counts(); return asked == 2 })

	// While an active plugin card exists, session row has Approve=true and NO computer-only AttentionReason.
	if s := byID(listSessions(t, n))[sidA]; s.Capabilities == nil || !s.Capabilities.Approve || s.AttentionReason != "" {
		t.Fatalf("active plugin card row: %+v caps=%+v", s, s.Capabilities)
	}

	// Computer allows toolu_c1 first -> /permission-done withdraws ONLY card 1 immediately.
	status, out = pluginPost(t, c, "permission-done", map[string]any{
		"session_id":  sidA,
		"tool_use_id": "toolu_c1",
		"reason":      "post_tool_use",
	})
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("permission-done c1: %d %v", status, out)
	}
	select {
	case r := <-res1:
		if r.status != http.StatusOK || r.out["action"] != "passthrough" {
			t.Fatalf("req 1 after done: %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("req 1 did not wake on permission-done")
	}
	waitFor(t, "card 1 cancelled on hub", func() bool { _, cancelled := h.counts(); return cancelled == 1 })

	// Main turn_complete must NOT cancel subagent card 2.
	status, _ = pluginPost(t, c, "permission-done", map[string]any{
		"session_id": sidA,
		"reason":     "turn_complete",
	})
	if status != http.StatusOK {
		t.Fatalf("turn_complete: %d", status)
	}
	select {
	case r := <-res2:
		t.Fatalf("subagent req 2 was unexpectedly cancelled by main turn_complete: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}

	// Subagent turn_complete withdraws card 2.
	status, _ = pluginPost(t, c, "permission-done", map[string]any{
		"session_id": sidA,
		"agent_id":   "sub-1",
		"reason":     "turn_complete",
	})
	if status != http.StatusOK {
		t.Fatalf("subagent turn_complete: %d", status)
	}
	select {
	case r := <-res2:
		if r.status != http.StatusOK || r.out["action"] != "passthrough" {
			t.Fatalf("req 2 after subagent turn_complete: %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("req 2 did not wake on subagent turn_complete")
	}
	waitFor(t, "card 2 cancelled on hub", func() bool { _, cancelled := h.counts(); return cancelled == 2 })

	// Early permission-done before /permission prevents card creation altogether.
	pluginPost(t, c, "permission-done", map[string]any{
		"session_id":  sidA,
		"tool_use_id": "toolu_already_done",
		"reason":      "post_tool_use",
	})
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id":  sidA,
		"req_id":      "req-late-after-done",
		"tool_name":   "Bash",
		"tool_use_id": "toolu_already_done",
		"tool_input":  map[string]any{"command": "pwd"},
		"wait_ms":     200,
	})
	if status != http.StatusOK || out["action"] != "passthrough" {
		t.Fatalf("late /permission after done: %d %v", status, out)
	}
	if asked, _ := h.counts(); asked != 2 {
		t.Fatalf("early done did not suppress card creation: asked=%d", asked)
	}
}

func TestCCPluginPermissionDedupWithHookDecidedAndOutsideAskAndDesktopPersonOnly(t *testing.T) {
	defer func(g time.Duration) { permissionGrace = g }(permissionGrace)
	permissionGrace = 10 * time.Millisecond

	n, f, home := herdrNode(t)
	noPaneEnv(t)
	fh := withFakeHub(t, n)
	p := writeTranscript(t, home, home, sidA, time.Now(),
		`{"type":"user","uuid":"u1","timestamp":"2026-11-04T09:00:00Z","cwd":"`+home+`","sessionId":"`+sidA+`","permissionMode":"auto","message":{"role":"user","content":"check"}}`,
		askCall(sidA, "toolu_q_dedup", `{"questions":[{"question":"Which?","header":"Pick","options":[{"label":"A"},{"label":"B"}],"multiSelect":false}]}`))
	inbox := newFakeInbox(t, p)
	writeOutsideClaude(t, home, sidA, "claude-desktop-3p", "waiting", inbox, nil)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	c := pluginSocket(t, n)

	// Without permissions capability (older 0.2.0 plugin), clq_ fallback stays active.
	pluginPost(t, c, "poll", map[string]any{
		"session_id":     sidA,
		"plugin_version": "0.2.0",
		"capabilities":   []string{"busy_input", "interrupt", "settings"},
		"wait_ms":        50,
	})
	if cards := n.outsideAskCards(); len(cards) != 1 {
		t.Fatalf("older plugin should still fall back to clq_ card, got %d", len(cards))
	}

	// Once a permissions-capable plugin is authorized, clq_ fallback is skipped to prevent double cards.
	status, out := pluginPost(t, c, "poll", map[string]any{
		"session_id":     sidA,
		"plugin_version": "0.3.0",
		"capabilities":   []string{"busy_input", "interrupt", "settings", "permissions"},
		"wait_ms":        500,
	})
	if status != http.StatusOK || out["permissions"] != true {
		t.Fatalf("poll: %d %v", status, out)
	}
	if cards := n.outsideAskCards(); len(cards) != 0 {
		t.Fatalf("permissionsCapable session must skip clq_ fallback, got %+v", cards)
	}

	// Desktop session still StatusBlocked with no active plugin card (e.g. Auto-mode personOnly review that bypassed hooks):
	// Approve=true and accurate Claude Desktop attention reason (never tmux guidance).
	s := byID(listSessions(t, n))[sidA]
	if s.Status != proto.StatusBlocked || s.Capabilities == nil || !s.Capabilities.Approve {
		t.Fatalf("desktop waiting row: %+v caps=%+v", s, s.Capabilities)
	}
	if s.AttentionReason != proto.AttentionClaudeDesktopComputer {
		t.Fatalf("desktop personOnly AttentionReason = %q, want %q", s.AttentionReason, proto.AttentionClaudeDesktopComputer)
	}

	// Another PermissionRequest hook already decided this tool call (hookdecided.go): no card created.
	bashInput, _ := json.Marshal(map[string]any{"command": "echo bypass"})
	n.decided.decide(hookInput{
		HookEventName: "PermissionRequest",
		SessionID:     sidA,
		ToolName:      "Bash",
		ToolInput:     bashInput,
	}, time.Now())
	status, out = pluginPost(t, c, "permission", map[string]any{
		"session_id": sidA,
		"req_id":     "req-already-decided",
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "echo bypass"},
		"wait_ms":    500,
	})
	if status != http.StatusOK || out["action"] != "passthrough" || len(fh.interactions()) != 0 {
		t.Fatalf("hook.decided dedup failed: %d %v (cards=%d)", status, out, len(fh.interactions()))
	}
}

func TestCCPluginPermissionIdleAbandonmentCancelsCard(t *testing.T) {
	oldIdle := ccPluginPermIdleTTL
	ccPluginPermIdleTTL = 120 * time.Millisecond
	t.Cleanup(func() { ccPluginPermIdleTTL = oldIdle })

	n, f, home := herdrNode(t)
	noPaneEnv(t)
	h := withCancelHub(t, n)
	p := bgTranscript(t, home, home, sidA, "default")
	inbox := newFakeInbox(t, p)
	writeOutsideClaude(t, home, sidA, "claude-desktop-3p", "waiting", inbox, nil)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	c := pluginSocket(t, n)

	status, out := pluginPost(t, c, "permission", map[string]any{
		"session_id": sidA,
		"req_id":     "req-abandoned",
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": "sleep 1"},
		"wait_ms":    40,
	})
	if status != http.StatusOK || out["action"] != "renew" {
		t.Fatalf("initial poll: %d %v", status, out)
	}
	waitFor(t, "abandoned card cancelled after idle TTL", func() bool {
		asked, cancelled := h.counts()
		return asked == 1 && cancelled == 1
	})
	// Keep race detector busy checking concurrent reads while cleanup completes.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = n.ccPlugins.hasActivePermission(sidA)
	}()
	wg.Wait()
}
