package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestModelBindingPermissionModeEditInheritAndValidate(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	f.mu.Lock()
	f.hello.Models.AI.API = "messages"
	f.hello.Models.AI.BaseURL = "https://api.example.invalid"
	f.hello.Models.AI.Model = "legacy-model"
	f.zshrc = map[string][]proto.Launcher{
		"server": {
			{Name: "codex", Agent: "codex", Source: "zshrc"},
			{Name: "agy", Agent: "agy", Source: "agy", Assistant: true},
			{Name: "omp", Agent: "omp", Source: "zshrc"},
			{Name: "grok", Agent: "paseo", Source: "zshrc", PermissionModes: proto.PermissionModeSetOf("default", "bypassPermissions")},
		},
	}
	f.mu.Unlock()
	putLaunchers(t, s, apiProvider(t, s, "direct", "messages"), storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "cc-alt", Model: "alt-model"}})

	// Launcher purposes expose permission_mode; direct API purposes do not.
	for _, id := range []string{"assistant", "agent.new", purposeSupervise, purposeMeetingRoom, purposeAgent, purposeLarkAgent} {
		p := purposeOf(t, s, id)
		if !slices.Contains(p.Fields, "permission_mode") {
			t.Fatalf("launcher purpose %s missing permission_mode in fields: %v", id, p.Fields)
		}
	}
	for _, id := range []string{"kb", "inbox.triage", purposeMeetingMinutes} {
		p := purposeOf(t, s, id)
		if slices.Contains(p.Fields, "permission_mode") {
			t.Fatalf("API purpose %s unexpectedly exposes permission_mode in fields: %v", id, p.Fields)
		}
		if w := modelRequest(s, http.MethodPatch, id, `{"revision":0,"permission_mode":"acceptEdits"}`); w.Code != http.StatusBadRequest {
			t.Fatalf("API purpose %s accepted permission_mode: %d %s", id, w.Code, w.Body.String())
		}
	}

	// Setting permission_mode on assistant is inherited by assistant.supervise and does not alter fingerprint.
	fpBefore, err := s.modelFingerprint(ctx, "assistant")
	if err != nil {
		t.Fatal(err)
	}
	if w := modelRequest(s, http.MethodPatch, "assistant", `{"revision":0,"permission_mode":"acceptEdits"}`); w.Code != http.StatusOK {
		t.Fatalf("save assistant permission_mode: %d %s", w.Code, w.Body.String())
	}
	fpAfter, err := s.modelFingerprint(ctx, "assistant")
	if err != nil || fpAfter != fpBefore {
		t.Fatalf("permission_mode changed fingerprint: before=%q after=%q err=%v", fpBefore, fpAfter, err)
	}
	if p := purposeOf(t, s, purposeSupervise); p.Effective.PermissionMode != "acceptEdits" || p.Override.PermissionMode != "" {
		t.Fatalf("supervisor did not inherit assistant permission_mode: %+v", p)
	}

	// Child override wins over parent; changing provider resets permission_mode; null restores inheritance.
	if w := modelRequest(s, http.MethodPatch, purposeSupervise, `{"revision":0,"permission_mode":"plan"}`); w.Code != http.StatusOK {
		t.Fatalf("save supervisor permission_mode: %d %s", w.Code, w.Body.String())
	}
	if p := purposeOf(t, s, purposeSupervise); p.Effective.PermissionMode != "plan" || p.Override.PermissionMode != "plan" {
		t.Fatalf("supervisor override: %+v", p)
	}
	if w := modelRequest(s, http.MethodPatch, purposeSupervise, `{"revision":1,"provider":"launcher.cc-alt"}`); w.Code != http.StatusOK {
		t.Fatalf("change supervisor provider: %d %s", w.Code, w.Body.String())
	}
	if p := purposeOf(t, s, purposeSupervise); p.Effective.PermissionMode != "" || p.Override.PermissionMode != "" {
		t.Fatalf("changing provider did not reset permission_mode: %+v", p)
	}
	if w := modelRequest(s, http.MethodPatch, purposeSupervise, `{"revision":2,"provider":null,"permission_mode":null}`); w.Code != http.StatusOK {
		t.Fatalf("reset supervisor: %d %s", w.Code, w.Body.String())
	}
	if p := purposeOf(t, s, purposeSupervise); p.Effective.PermissionMode != "acceptEdits" {
		t.Fatalf("reset supervisor did not re-inherit permission_mode: %+v", p)
	}

	// Capability validation across claude, codex, agy, omp, and restricted runtime provider.
	if w := modelRequest(s, http.MethodPatch, "agent.new", `{"revision":0,"permission_mode":"yolo"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid permission_mode accepted: %d %s", w.Code, w.Body.String())
	}
	if w := modelRequest(s, http.MethodPatch, "agent.new", `{"revision":0,"provider":"launcher.omp","permission_mode":"acceptEdits"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("omp accepted permission_mode: %d %s", w.Code, w.Body.String())
	}
	if w := modelRequest(s, http.MethodPatch, "agent.new", `{"revision":0,"provider":"launcher.grok","permission_mode":"plan"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("restricted runtime provider accepted unsupported mode: %d %s", w.Code, w.Body.String())
	}
	if w := modelRequest(s, http.MethodPatch, "agent.new", `{"revision":0,"provider":"launcher.grok","permission_mode":"bypassPermissions"}`); w.Code != http.StatusOK {
		t.Fatalf("restricted runtime provider rejected supported mode: %d %s", w.Code, w.Body.String())
	}
	rev := purposeOf(t, s, "agent.new").Revision
	for _, tc := range []struct{ provider, mode string }{
		{"launcher.codex", "acceptEdits"},
		{"launcher.agy", "auto"},
		{"launcher.claude", "plan"},
	} {
		body := `{"revision":` + string(rune('0'+rev)) + `,"provider":"` + tc.provider + `","permission_mode":"` + tc.mode + `"}`
		if w := modelRequest(s, http.MethodPatch, "agent.new", body); w.Code != http.StatusOK {
			t.Fatalf("%s/%s rejected: %d %s", tc.provider, tc.mode, w.Code, w.Body.String())
		}
		rev++
	}
}

func TestModelProfileAndTierPermissionMode(t *testing.T) {
	s, f, st := modelService(t)
	ctx := context.Background()
	f.mu.Lock()
	f.zshrc = map[string][]proto.Launcher{"server": {{Name: "omp", Agent: "omp", Source: "zshrc"}, {Name: "codex", Agent: "codex", Source: "zshrc"}}}
	f.mu.Unlock()
	putLaunchers(t, s, apiProvider(t, s, "direct", "messages"))

	// Reject permission_mode on API purpose in profile and on omp tier.
	badAPI := ModelProfile{ID: "bad-api", Name: "Bad API", Purposes: map[string]ModelBinding{
		"kb": {Provider: "launcher.direct", PermissionMode: "acceptEdits"},
	}}
	if err := checkModelProfile(&badAPI, s.storedLaunchers(ctx)); err == nil {
		t.Fatal("API purpose in profile accepted permission_mode")
	}
	badOmp := ModelProfile{ID: "bad-omp", Name: "Bad OMP", Tiers: map[string]ModelTier{
		"fast": {Launcher: "omp", PermissionMode: "acceptEdits"},
	}}
	if err := checkModelProfile(&badOmp, s.storedLaunchers(ctx)); err == nil {
		t.Fatal("omp tier accepted permission_mode")
	}

	// Save, switch, and rollback a profile carrying permission_mode on purposes and tiers.
	storeProfiles(t, st, ModelProfile{
		ID:   "safe",
		Name: "Safe",
		Purposes: map[string]ModelBinding{
			"assistant": {Provider: "launcher.claude", PermissionMode: "acceptEdits"},
			"agent.new": {Provider: "launcher.codex", PermissionMode: "plan"},
		},
		Tiers: map[string]ModelTier{
			"review": {Launcher: "claude", Model: "sonnet", PermissionMode: "plan", Description: "Read and review"},
		},
	})
	if _, err := s.applyModelProfile(ctx, "safe", ProfileActor{Kind: "hub"}); err != nil {
		t.Fatalf("apply safe profile: %v", err)
	}
	if b, err := s.resolveModel(ctx, "assistant"); err != nil || b.PermissionMode != "acceptEdits" {
		t.Fatalf("assistant binding after switch: %+v %v", b, err)
	}
	if b, err := s.resolveModel(ctx, "agent.new"); err != nil || b.PermissionMode != "plan" {
		t.Fatalf("agent.new binding after switch: %+v %v", b, err)
	}
	snap := s.modelProfileContext(ctx)
	if snap == nil || len(snap.Tiers) != 1 || snap.Tiers[0].PermissionMode != "plan" {
		t.Fatalf("snapshot tiers missing permission_mode: %+v", snap)
	}
	rendered := prompts.RenderSnapshot(prompts.Snapshot{ModelProfile: snap, Now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), Zone: "UTC"})
	if !strings.Contains(rendered, "review = claude · sonnet · plan（Read and review）") {
		t.Fatalf("snapshot did not render tier permission_mode:\n%s", rendered)
	}

	// Rollback restores prior empty permission_mode.
	if _, err := s.rollbackModelProfile(ctx, ProfileActor{Kind: "hub"}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if b, err := s.resolveModel(ctx, "assistant"); err != nil || b.PermissionMode != "" {
		t.Fatalf("assistant binding after rollback: %+v %v", b, err)
	}

	// editTiers supports setting permission_mode and clears it when launcher changes.
	ps, _, err := s.readModelProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mode := "auto"
	changes, err := editTiers(&ps, tierEdit{Tier: "review", PermissionMode: &mode, Profiles: []string{"safe"}}, time.Now().UTC())
	if err != nil || len(changes) != 1 || changes[0].After.PermissionMode != "auto" {
		t.Fatalf("editTiers permission_mode: changes=%+v err=%v", changes, err)
	}
	nextLauncher := "codex"
	changes, err = editTiers(&ps, tierEdit{Tier: "review", Launcher: &nextLauncher, Profiles: []string{"safe"}}, time.Now().UTC())
	if err != nil || len(changes) != 1 || changes[0].After.PermissionMode != "" {
		t.Fatalf("editTiers launcher switch did not clear permission_mode: changes=%+v err=%v", changes, err)
	}
}

func TestPermissionModeLaunchPropagationAndPrecedence(t *testing.T) {
	ctx := context.Background()

	// 1. AgentStartParams (New Agent): explicit > tier > agent.new > CLI default; omp drops inherited mode.
	t.Run("AgentStartParams precedence and capability filtering", func(t *testing.T) {
		s, f := newQuietServiceOn(t, &memStore{})
		f.launcherList = []proto.Launcher{
			{Name: "claude", Agent: "claude"},
			{Name: "codex", Agent: "codex"},
			{Name: "agy", Agent: "agy"},
			{Name: "omp", Agent: "omp"},
		}
		setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "acceptEdits"})
		putTierProfiles(t, s, "performance", nil, map[string]ModelTier{
			"guarded":  {Launcher: "claude", PermissionMode: "plan"},
			"agy-auto": {Launcher: "agy", PermissionMode: "auto"},
		})

		// Inherits agent.new permission_mode
		got, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{}})
		if err != nil || got["permission_mode"] != "acceptEdits" {
			t.Fatalf("agent.new default: %v err=%v", got, err)
		}
		// Tier overrides agent.new
		got, err = s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"tier": "guarded"}})
		if err != nil || got["permission_mode"] != "plan" {
			t.Fatalf("tier override: %v err=%v", got, err)
		}
		// Explicit caller overrides tier
		got, err = s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"tier": "guarded", "permission_mode": "default"}})
		if err != nil || got["permission_mode"] != "default" {
			t.Fatalf("explicit override: %v err=%v", got, err)
		}
		// AGY tier propagates permission_mode
		got, err = s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"tier": "agy-auto"}})
		if err != nil || got["permission_mode"] != "auto" || got["agent_type"] != "agy" {
			t.Fatalf("agy tier: %v err=%v", got, err)
		}
		// Switching to omp drops inherited mode without error
		setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.omp"})
		got, err = s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{}})
		if err != nil || got["permission_mode"] != nil {
			t.Fatalf("omp inherited mode: %v err=%v", got, err)
		}
	})

	// 2. agent_start: explicit > tier > agent.new > allowAll
	t.Run("agent_start precedence across Claude, Codex and AGY", func(t *testing.T) {
		runStart := func(t *testing.T, binding ModelBinding, tiers map[string]ModelTier, input map[string]any) map[string]any {
			t.Helper()
			s, f := newQuietServiceOn(t, &memStore{})
			f.launcherList = []proto.Launcher{
				{Name: "claude", Agent: "claude"},
				{Name: "codex", Agent: "codex"},
				{Name: "agy", Agent: "agy"},
			}
			setModel(t, s, "agent.new", binding)
			for name, tier := range tiers {
				saveTier(t, s, name, tier)
			}
			input["machine"], input["cwd"], input["prompt"] = "box", "/code/app", "Run check"
			args, _ := json.Marshal(input)
			if _, err := s.runTool(ctx, "server", "conv1", "agent_start", args); err != nil {
				t.Fatalf("agent_start failed: %v", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.starts) != 1 {
				t.Fatalf("starts = %v", f.starts)
			}
			return f.starts[0]
		}

		// Purpose binding applies to Claude, Codex, AGY
		if start := runStart(t, ModelBinding{Provider: "launcher.claude", PermissionMode: "acceptEdits"}, nil, map[string]any{}); start["permission_mode"] != "acceptEdits" {
			t.Fatalf("claude agent.new permission_mode: %v", start)
		}
		if start := runStart(t, ModelBinding{Provider: "launcher.codex", PermissionMode: "bypassPermissions"}, nil, map[string]any{}); start["permission_mode"] != "bypassPermissions" {
			t.Fatalf("codex agent.new permission_mode: %v", start)
		}
		if start := runStart(t, ModelBinding{Provider: "launcher.agy", PermissionMode: "auto"}, nil, map[string]any{}); start["permission_mode"] != "auto" {
			t.Fatalf("agy agent.new permission_mode: %v", start)
		}
		// Tier overrides agent.new
		if start := runStart(t,
			ModelBinding{Provider: "launcher.claude", PermissionMode: "acceptEdits"},
			map[string]ModelTier{"careful": {Launcher: "claude", PermissionMode: "plan"}},
			map[string]any{"tier": "careful"},
		); start["permission_mode"] != "plan" {
			t.Fatalf("tier override: %v", start)
		}
		// Explicit overrides tier
		if start := runStart(t,
			ModelBinding{Provider: "launcher.claude", PermissionMode: "acceptEdits"},
			map[string]ModelTier{"careful": {Launcher: "claude", PermissionMode: "plan"}},
			map[string]any{"tier": "careful", "permission_mode": "default"},
		); start["permission_mode"] != "default" {
			t.Fatalf("explicit override: %v", start)
		}
	})

	// 3. task_plan & startTaskAgent: writable honors precedence; readonly Codex NEVER weakened.
	t.Run("task dispatch honors precedence and protects readonly", func(t *testing.T) {
		s, f := noModesService(t, "codex")
		f.launcherList = []proto.Launcher{
			{Name: "claude", Agent: "claude"},
			{Name: "codex", Agent: "codex"},
			{Name: "agy", Agent: "agy"},
		}
		setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "acceptEdits"})
		putTierProfiles(t, s, "performance", nil, map[string]ModelTier{
			"codex-bypass": {Launcher: "codex", PermissionMode: "bypassPermissions"},
			"agy-auto":     {Launcher: "agy", PermissionMode: "auto"},
		})

		// Writable task on Claude inherits agent.new permission_mode
		wTask := readonlyTestTask("")
		wTask.Readonly = false
		if _, _, err := s.startTaskAgent(ctx, &wTask, false); err != nil {
			t.Fatal(err)
		}
		if got := f.starts[len(f.starts)-1]["permission_mode"]; got != "acceptEdits" {
			t.Fatalf("writable claude task got %v, want acceptEdits", got)
		}

		// Writable task on AGY tier propagates auto
		agyTask := readonlyTestTask("")
		agyTask.Readonly, agyTask.Tier = false, "agy-auto"
		if _, _, err := s.startTaskAgent(ctx, &agyTask, false); err != nil {
			t.Fatal(err)
		}
		if got := f.starts[len(f.starts)-1]["permission_mode"]; got != "auto" {
			t.Fatalf("writable agy task got %v, want auto", got)
		}

		// Read-only task on Codex tier with bypassPermissions MUST remain plan!
		roCodex := readonlyTestTask("")
		roCodex.Readonly, roCodex.Tier = true, "codex-bypass"
		if _, _, err := s.startTaskAgent(ctx, &roCodex, false); err != nil {
			t.Fatal(err)
		}
		if got := f.starts[len(f.starts)-1]["permission_mode"]; got != "plan" || roCodex.Permission != "plan" {
			t.Fatalf("readonly codex task weakened: launch=%v task=%+v", got, roCodex)
		}
	})

	// 4. Headless assistant, supervisor, lark.agent, inbox.agent (safety boundary!), and scheduled task
	t.Run("headless purposes and safety boundaries", func(t *testing.T) {
		s, f, _ := modelService(t)
		setModel(t, s, "assistant", ModelBinding{Provider: "launcher.claude", PermissionMode: "acceptEdits"})
		setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "auto"})
		setModel(t, s, purposeAgent, ModelBinding{Provider: "launcher.claude", PermissionMode: "bypassPermissions"})

		// Main chat inherits assistant permission_mode
		sess, err := s.startOn(ctx, "server", StartParams{Text: "hi"}, true)
		if err != nil {
			t.Fatal(err)
		}
		if sess.PermissionMode != "acceptEdits" || f.sends[len(f.sends)-1]["permission_mode"] != "acceptEdits" {
			t.Fatalf("assistant start mode: sess=%q send=%v", sess.PermissionMode, f.sends[len(f.sends)-1])
		}

		// Background supervisor inherits assistant permission_mode
		sess, err = s.startOn(ctx, "server", StartParams{Text: "review", Purpose: purposeSupervise}, false)
		if err != nil {
			t.Fatal(err)
		}
		if sess.PermissionMode != "acceptEdits" || f.sends[len(f.sends)-1]["permission_mode"] != "acceptEdits" {
			t.Fatalf("supervisor start mode: sess=%q send=%v", sess.PermissionMode, f.sends[len(f.sends)-1])
		}

		// Explicit caller mode beats purpose binding
		sess, err = s.startOn(ctx, "server", StartParams{Text: "hi", PermissionMode: "plan"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if sess.PermissionMode != "plan" || f.sends[len(f.sends)-1]["permission_mode"] != "plan" {
			t.Fatalf("explicit startOn mode: sess=%q send=%v", sess.PermissionMode, f.sends[len(f.sends)-1])
		}

		// Lark /agent inherits agent.new permission_mode
		sess, err = s.startOn(ctx, "server", StartParams{Text: "slash", Purpose: purposeLarkAgent}, false)
		if err != nil {
			t.Fatal(err)
		}
		if sess.PermissionMode != "auto" || f.sends[len(f.sends)-1]["permission_mode"] != "auto" {
			t.Fatalf("lark.agent start mode: sess=%q send=%v", sess.PermissionMode, f.sends[len(f.sends)-1])
		}

		// OriginInbox (AI twin) MUST stay "default" even when inbox.agent configured bypassPermissions
		sess, err = s.startOn(ctx, "server", StartParams{Text: "triage", Purpose: purposeAgent, Origin: OriginInbox}, false)
		if err != nil {
			t.Fatal(err)
		}
		if sess.PermissionMode != "default" || f.sends[len(f.sends)-1]["permission_mode"] != "default" {
			t.Fatalf("OriginInbox weakened: sess=%q send=%v", sess.PermissionMode, f.sends[len(f.sends)-1])
		}

		// Guest conversation MUST never inherit or receive a non-empty permission_mode
		sess, err = s.startOn(ctx, "server", StartParams{Text: "room", Purpose: purposeMeetingRoom, GuestTools: []string{"foo"}}, false)
		if err != nil {
			t.Fatal(err)
		}
		if gotMode, _ := f.sends[len(f.sends)-1]["permission_mode"].(string); sess.PermissionMode != "" || gotMode != "" {
			t.Fatalf("guest session received permission_mode: sess=%q send=%v", sess.PermissionMode, f.sends[len(f.sends)-1])
		}

		// Ongoing supervisor message (backgroundModel) must NOT inject permission_mode
		msg := map[string]any{"session_id": "bg-1", "text": "update"}
		s.backgroundModel(ctx, msg)
		if _, has := msg["permission_mode"]; has {
			t.Fatalf("backgroundModel injected permission_mode into existing chat: %v", msg)
		}

		// Scheduled task inherits assistant permission_mode unless task explicitly sets PermissionMode
		ex, err := s.taskExecution(ctx, Task{ID: "t1"}, false, ModelBinding{Provider: "launcher.claude", PermissionMode: "acceptEdits"})
		if err != nil || ex.PermissionMode != "acceptEdits" {
			t.Fatalf("scheduled task inherited mode: %+v err=%v", ex, err)
		}
		exExplicit, err := s.taskExecution(ctx, Task{ID: "t1", PermissionMode: "plan"}, false, ModelBinding{Provider: "launcher.claude", PermissionMode: "acceptEdits"})
		if err != nil || exExplicit.PermissionMode != "plan" {
			t.Fatalf("scheduled task explicit mode: %+v err=%v", exExplicit, err)
		}

		// sendOnPick with empty pick.Model must NOT inject assistant purpose's permission_mode into an existing conversation.
		if err := s.setModelPick(ctx, "conv-pick", &modelPick{MachineID: "server", Launcher: "claude"}); err != nil {
			t.Fatal(err)
		}
		pickParams := map[string]any{"session_id": "conv-pick", "text": "follow up"}
		if err := s.sendModel(ctx, "server", "conv-pick", pickParams, i18n.En); err != nil {
			t.Fatalf("sendModel on saved pick: %v", err)
		}
		if _, has := pickParams["permission_mode"]; has {
			t.Fatalf("sendOnPick injected permission_mode into existing chat: %v", pickParams)
		}
		pickExplicit := map[string]any{"session_id": "conv-pick", "text": "follow up", "permission_mode": "plan"}
		if err := s.sendModel(ctx, "server", "conv-pick", pickExplicit, i18n.En); err != nil {
			t.Fatalf("sendModel on saved pick with explicit mode: %v", err)
		}
		if pickExplicit["permission_mode"] != "plan" {
			t.Fatalf("sendOnPick clobbered explicit permission_mode: %v", pickExplicit)
		}
		_ = i18n.En
	})
}

func TestPermissionModePerMachineResolution(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	f.mu.Lock()
	f.sessions["mac"] = nil
	f.zshrc = map[string][]proto.Launcher{
		"server": {
			{Name: "grok", Agent: "paseo", Source: "paseo", Assistant: true, PermissionModes: proto.PermissionModeSetOf("default", "bypassPermissions")},
			{Name: "custom-cli", Agent: "omp", Source: "zshrc"},
		},
		"mac": {
			{Name: "grok", Agent: "paseo", Source: "paseo", Assistant: true, PermissionModes: proto.PermissionModeSetOf("default", "plan")},
			{Name: "custom-cli", Agent: "claude", Source: "zshrc"},
		},
	}
	f.mu.Unlock()

	unionGrok := &ModelProvider{
		ID:              "launcher.grok",
		Kind:            "launcher",
		Agent:           "paseo",
		PermissionModes: proto.PermissionModeSetOf("default", "bypassPermissions", "plan"),
	}
	if modes := s.permissionModesForLauncher(ctx, "server", "grok", unionGrok); slices.Contains(modes, "plan") || !slices.Contains(modes, "bypassPermissions") {
		t.Fatalf("server grok modes = %v, want only server's [default bypassPermissions]", modes)
	}
	if modes := s.permissionModesForLauncher(ctx, "mac", "grok", unionGrok); slices.Contains(modes, "bypassPermissions") || !slices.Contains(modes, "plan") {
		t.Fatalf("mac grok modes = %v, want only mac's [default plan]", modes)
	}

	unionCustom := &ModelProvider{ID: "launcher.custom-cli", Kind: "launcher", Agent: "claude"}
	if modes := s.permissionModesForLauncher(ctx, "server", "custom-cli", unionCustom); len(modes) != 0 {
		t.Fatalf("server custom-cli (omp) modes = %v, want empty", modes)
	}
	if modes := s.permissionModesForLauncher(ctx, "mac", "custom-cli", unionCustom); !slices.Contains(modes, "acceptEdits") {
		t.Fatalf("mac custom-cli (claude) modes = %v, want standard modes", modes)
	}

	// assistant is bound to "server": saving grok with mac-only "plan" must fail with 400, while server's "bypassPermissions" succeeds.
	if w := modelRequest(s, http.MethodPatch, "assistant", `{"revision":0,"provider":"launcher.grok","permission_mode":"plan"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("assistant on server accepted mac-only grok mode: %d %s", w.Code, w.Body.String())
	}
	if w := modelRequest(s, http.MethodPatch, "assistant", `{"revision":0,"provider":"launcher.grok","permission_mode":"bypassPermissions"}`); w.Code != http.StatusOK {
		t.Fatalf("assistant on server rejected server-supported grok mode: %d %s", w.Code, w.Body.String())
	}
}

func TestModelProfilePermissionModeValidationAndCapture(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	f.mu.Lock()
	f.zshrc = map[string][]proto.Launcher{
		"server": {
			{Name: "grok", Agent: "paseo", Source: "zshrc", PermissionModes: proto.PermissionModeSetOf("default", "bypassPermissions")},
			{Name: "alias-omp", Agent: "omp", Source: "zshrc"},
		},
	}
	f.mu.Unlock()
	putLaunchers(t, s, apiProvider(t, s, "direct", "messages"))

	// HTTP PUT /api/v1/model-profiles/{id} returns 400 Bad Request (not 500) for launcher-only and unsupported permission modes.
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "api purpose with permission_mode",
			body: `{"revision":1,"name":"Bad API","purposes":{"kb":{"provider":"launcher.direct","permission_mode":"acceptEdits"}}}`,
		},
		{
			name: "purpose with runtime-unsupported permission_mode",
			body: `{"revision":1,"name":"Bad Grok","purposes":{"agent.new":{"provider":"launcher.grok","permission_mode":"plan"}}}`,
		},
		{
			name: "purpose with discovered omp alias",
			body: `{"revision":1,"name":"Bad OMP Alias","purposes":{"agent.new":{"provider":"launcher.alias-omp","permission_mode":"acceptEdits"}}}`,
		},
		{
			name: "tier with runtime-unsupported permission_mode",
			body: `{"revision":1,"name":"Bad Grok Tier","tiers":{"fast":{"launcher":"grok","permission_mode":"plan"}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := request(s, http.MethodPut, "/api/v1/model-profiles/custom", tc.body); w.Code != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, want 400; body = %s", tc.name, w.Code, w.Body.String())
			}
		})
	}

	// Supported mode on discovered runtime succeeds.
	if w := request(s, http.MethodPut, "/api/v1/model-profiles/custom", `{"revision":1,"name":"Good Grok","purposes":{"agent.new":{"provider":"launcher.grok","permission_mode":"bypassPermissions"}}}`); w.Code != http.StatusOK {
		t.Fatalf("supported runtime permission_mode rejected: %d %s", w.Code, w.Body.String())
	}

	// captureModels preserves purpose permission_mode but never copies assistant/agent.new permission_mode into seeded tiers.
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.claude", PermissionMode: "bypassPermissions"})
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "acceptEdits"})
	purposes, _, tiers, err := s.captureModels(ctx)
	if err != nil {
		t.Fatalf("captureModels: %v", err)
	}
	if purposes["assistant"].PermissionMode != "bypassPermissions" || purposes["agent.new"].PermissionMode != "acceptEdits" {
		t.Fatalf("captured purposes missing permission_mode: %+v", purposes)
	}
	if tiers["strong"].PermissionMode != "" || tiers["standard"].PermissionMode != "" {
		t.Fatalf("captureModels leaked permission_mode into seeded tiers: %+v", tiers)
	}
}
