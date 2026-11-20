package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestModelConfigurationPermissionMode(t *testing.T) {
	s, f, _ := modelService(t)
	f.launcherList = []proto.Launcher{
		{Name: "claude", Agent: "claude"},
		{Name: "dev-codex", Agent: "codex"},
		{Name: "agy-terminal", Agent: "agy", Source: "path"},
		{Name: "agy-companion", Agent: "agy", Source: "agy", Assistant: true},
		{Name: "dev-claude", Agent: "claude"},
		{Name: "dev-omp", Agent: "omp"},
		{Name: "dev-unknown"},
	}
	patch := func(id, body string, status int) {
		t.Helper()
		if r := modelRequest(s, "PATCH", id, body); r.Code != status {
			t.Fatalf("%s %s: got %d, want %d (%s)", id, body, r.Code, status, r.Body)
		}
	}
	expect := func(id, override, effective string) {
		t.Helper()
		got := purposeOf(t, s, id)
		if got.Override.PermissionMode != override || got.Effective.PermissionMode != effective {
			t.Fatalf("%s: override and effective mode = %q and %q, want %q and %q",
				id, got.Override.PermissionMode, got.Effective.PermissionMode, override, effective)
		}
	}
	patch("agent.new", `{"revision":0,"provider":"launcher.dev-claude","permission_mode":"auto"}`, http.StatusOK)
	expect("agent.new", "auto", "auto")
	expect("lark.agent", "", "")
	expect("inbox.agent", "", "")
	patch("inbox.agent", `{"revision":0,"permission_mode":"bypassPermissions"}`, http.StatusBadRequest)
	patch("meeting.room", `{"revision":0,"permission_mode":"bypassPermissions"}`, http.StatusBadRequest)
	patch("lark.agent", `{"revision":0,"permission_mode":"default"}`, http.StatusOK)
	expect("lark.agent", "default", "default")
	patch("lark.agent", `{"revision":1,"permission_mode":null}`, http.StatusOK)
	expect("lark.agent", "", "")
	patch("agent.new", `{"revision":1,"permission_mode":"unrecognized"}`, http.StatusBadRequest)
	patch("agent.new", `{"revision":1,"provider":"launcher.dev-omp","permission_mode":"plan"}`, http.StatusBadRequest)
	expect("agent.new", "auto", "auto")
	patch("agent.new", `{"revision":1,"provider":"launcher.dev-omp"}`, http.StatusOK)
	expect("agent.new", "", "")
	expect("lark.agent", "", "")
	patch("assistant", `{"revision":0,"permission_mode":"plan"}`, http.StatusOK)
	expect(purposeSupervise, "", "plan")
	expect(purposeMeetingRoom, "", "")
	patch("kb", `{"revision":0,"permission_mode":"default"}`, http.StatusBadRequest)
	if p := purposeOf(t, s, "kb"); slices.Contains(p.Fields, "permission_mode") {
		t.Fatalf("direct API purpose advertised a permission picker: %+v", p.Fields)
	}

	r := modelRequest(s, "GET", "", "")
	var result struct {
		Providers []struct {
			ID             string          `json:"id"`
			Modes          json.RawMessage `json:"permission_modes"`
			AssistantModes json.RawMessage `json:"assistant_permission_modes"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatalf("model config: %v (%s)", err, r.Body)
	}
	all := `["default","acceptEdits","plan","auto","bypassPermissions"]`
	agy := `["default","acceptEdits","plan","bypassPermissions"]`
	want := map[string][2]string{
		"launcher.dev-claude":    {all, all},
		"launcher.dev-codex":     {`["plan","bypassPermissions"]`, `["default","plan","auto","bypassPermissions"]`},
		"launcher.dev-omp":       {"[]", "[]"},
		"launcher.dev-unknown":   {"", ""},
		"launcher.agy-terminal":  {"[]", "[]"},
		"launcher.agy-companion": {agy, agy},
	}
	for _, p := range result.Providers {
		if modes, found := want[p.ID]; found {
			got := [2]string{string(p.Modes), string(p.AssistantModes)}
			if got != modes {
				t.Errorf("%s: engine/headless modes %v, want %v", p.ID, got, modes)
			}
			delete(want, p.ID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("catalog lacks providers: %v", want)
	}
}

func TestModelProfilePermissionModeSwitchAndRollback(t *testing.T) {
	s, f, _ := modelService(t)
	f.launcherList = []proto.Launcher{{Name: "omp", Agent: "omp"}}
	ctx := context.Background()
	actor := ProfileActor{Kind: "hub"}
	agent := ProfileActor{Kind: "assistant"}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "plan"})
	p, err := s.saveModelProfile(ctx, nil, "custom", actor, func(p *ModelProfile, _ bool) error {
		p.Name = "Custom"
		p.Purposes = map[string]ModelBinding{"agent.new": {PermissionMode: "auto"}}
		p.Tiers = map[string]ModelTier{"review": {Launcher: "claude", PermissionMode: "default"}}
		return nil
	})
	if err != nil || p.Tiers["review"].PermissionMode != "default" {
		t.Fatalf("profile: %+v, %v", p, err)
	}
	profiles := profilesOf(t, s)
	var preview []ModelProfilePermissionChange
	for _, profile := range profiles.Profiles {
		if profile.ID == "custom" {
			preview = profile.PermissionChanges
		}
	}
	want := []ModelProfilePermissionChange{
		{Purpose: "agent.new", Before: "plan", After: "auto", BeforeLauncher: "claude"},
		{Tier: "review", After: "default", AfterLauncher: "claude"},
	}
	if !slices.Equal(preview, want) {
		t.Fatalf("switch preview = %+v, want %+v", preview, want)
	}
	if _, err := s.applyModelProfile(ctx, "custom", agent); !errors.Is(err, errModelProfilePermissionOwner) {
		t.Fatalf("model could switch to different preset permissions: %v", err)
	}
	if p := purposeOf(t, s, "agent.new"); p.Effective.PermissionMode != "plan" {
		t.Fatalf("refused switch changed mode to %q", p.Effective.PermissionMode)
	}
	postReview := func(path string, permissionChanges []ModelProfilePermissionChange, want int) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"permission_changes": permissionChanges})
		if err != nil {
			t.Fatal(err)
		}
		if r := request(s, http.MethodPost, path, string(body)); r.Code != want {
			t.Fatalf("post %s: got %d, want %d (%s)", path, r.Code, want, r.Body)
		}
	}
	postReview("/api/v1/model-profiles/custom/apply", []ModelProfilePermissionChange{}, http.StatusConflict)
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "default"})
	postReview("/api/v1/model-profiles/custom/apply", preview, http.StatusConflict)
	if p := purposeOf(t, s, "agent.new"); p.Effective.PermissionMode != "default" {
		t.Fatalf("stale switch changed mode to %q", p.Effective.PermissionMode)
	}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "plan"})
	postReview("/api/v1/model-profiles/custom/apply", preview, http.StatusOK)
	if p := purposeOf(t, s, "agent.new"); p.Effective.PermissionMode != "auto" {
		t.Fatalf("switch left mode at %q", p.Effective.PermissionMode)
	}
	undoPermission := []ModelProfilePermissionChange{
		{Purpose: "agent.new", Before: "auto", After: "plan", AfterLauncher: "claude"},
		{Tier: "review", Before: "default", BeforeLauncher: "claude"},
	}
	if undo := profilesOf(t, s).Rollback; undo == nil || !slices.Equal(undo.PermissionChanges, undoPermission) {
		t.Fatalf("rollback preview = %+v", undo)
	}
	if _, err := s.rollbackModelProfile(ctx, agent); !errors.Is(err, errModelProfilePermissionOwner) {
		t.Fatalf("model could restore different preset permissions: %v", err)
	}
	setModel(t, s, "agent.new", ModelBinding{PermissionMode: "default"})
	postReview("/api/v1/model-profiles/rollback", undoPermission, http.StatusConflict)
	if p := purposeOf(t, s, "agent.new"); p.Effective.PermissionMode != "default" {
		t.Fatalf("stale rollback changed mode to %q", p.Effective.PermissionMode)
	}
	setModel(t, s, "agent.new", ModelBinding{PermissionMode: "auto"})
	postReview("/api/v1/model-profiles/rollback", undoPermission, http.StatusOK)
	if p := purposeOf(t, s, "agent.new"); p.Effective.PermissionMode != "plan" {
		t.Fatalf("rollback left mode at %q", p.Effective.PermissionMode)
	}
	if _, err := s.saveModelProfile(ctx, nil, "custom", actor, func(p *ModelProfile, _ bool) error {
		p.Tiers["review"] = ModelTier{Launcher: "omp", PermissionMode: "default"}
		return nil
	}); err == nil {
		t.Fatal("profile accepted an unsupported tier permission mode")
	}
	if _, err := s.saveModelProfile(ctx, nil, "purpose-only", actor, func(p *ModelProfile, _ bool) error {
		p.Name = "Purpose only"
		p.Purposes = map[string]ModelBinding{"agent.new": {PermissionMode: "bypassPermissions"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyModelProfile(ctx, "purpose-only", agent); !errors.Is(err, errModelProfilePermissionOwner) {
		t.Fatalf("model could switch to a different purpose permission: %v", err)
	}
	if _, err := s.applyModelProfile(ctx, "purpose-only", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.rollbackModelProfile(ctx, agent); !errors.Is(err, errModelProfilePermissionOwner) {
		t.Fatalf("model could restore a different purpose permission: %v", err)
	}
}

func TestModelTierPermissionApprovalShowsAndPersistsMode(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	putTierProfiles(t, s, "economy", map[string]ModelTier{"review": {Launcher: "claude", PermissionMode: "plan"}}, nil)
	change, err := s.setModelTiers(ctx, json.RawMessage(`{"tier":"review","permission_mode":"default","profiles":["economy"]}`))
	if err != nil {
		t.Fatal(err)
	}
	profiles, _ := storedTiers(t, s)
	if before, _ := tierOf(profiles, "economy", "review"); before.PermissionMode != "plan" {
		t.Fatalf("permission changed before approval: %+v", before)
	}
	card := change.cardText(s.lang(ctx))
	if !strings.Contains(card, "claude · plan") || !strings.Contains(card, "claude · default") {
		t.Fatalf("permission change missing from approval: %s", card)
	}
	if _, err := change.apply(ctx); err != nil {
		t.Fatal(err)
	}
	profiles, _ = storedTiers(t, s)
	if after, _ := tierOf(profiles, "economy", "review"); after.PermissionMode != "default" {
		t.Fatalf("permission not changed after approval: %+v", after)
	}
}

func TestAssistantPermissionDefaultLeavesRunningModelFingerprint(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	binding := ModelBinding{Provider: "launcher.claude", Model: "test-model"}
	fingerprint := func() any {
		t.Helper()
		params := map[string]any{"session_id": "current", "permission_mode": "default"}
		if err := s.applyModel(ctx, "assistant", params); err != nil {
			t.Fatal(err)
		}
		if params["permission_mode"] != "default" || params["model_fingerprint"] == nil {
			t.Fatalf("existing conversation options changed: %+v", params)
		}
		return params["model_fingerprint"]
	}
	setModel(t, s, "assistant", binding)
	before := fingerprint()
	binding.PermissionMode = "plan"
	setModel(t, s, "assistant", binding)
	if after := fingerprint(); after != before {
		t.Fatalf("new default changed running model fingerprint: %v → %v", before, after)
	}
	if got := s.launchPermissionDefault(ctx, "assistant", "server", "claude", startAgent{agent: "claude"}, launchPermission{}, true); got != "plan" {
		t.Fatalf("fresh start mode = %q, want plan", got)
	}
	binding.Model = "different-model"
	setModel(t, s, "assistant", binding)
	if after := fingerprint(); after == before {
		t.Fatal("a model change must change the running model fingerprint")
	}
}
