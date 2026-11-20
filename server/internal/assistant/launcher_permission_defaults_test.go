package assistant

import (
	"encoding/json"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestConfiguredPermissionUsesAgentStartRPC(t *testing.T) {
	s, f := newQuietServiceOn(t, &memStore{})
	f.launcherList = []proto.Launcher{
		{Name: "claude", Agent: "claude"},
		{Name: "codex", Agent: "codex", Source: "path"},
		{Name: "omp", Agent: "omp", Source: "path"},
	}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "auto"})
	saveTier(t, s, "review", ModelTier{Launcher: "claude", PermissionMode: "plan"})
	bus := &BusHub{svc: s, helloed: make(chan struct{})}
	close(bus.helloed)
	for _, tc := range []struct {
		name, launcher, preset, explicit, resume, want string
		full                                           bool
	}{
		{name: "purpose", full: true, want: "auto"},
		{name: "preset precedes purpose", full: true, preset: "review", want: "plan"},
		{name: "caller precedes preset", full: true, preset: "review", explicit: "default", want: "default"},
		{name: "different launcher", full: true, preset: "review", launcher: "codex"},
		{name: "unsupported launcher", full: true, launcher: "omp"},
		{name: "scope or old hub does not attest full role", preset: "review"},
		{name: "resumed session", full: true, resume: "s1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"launcher": tc.launcher, "tier": tc.preset, "resume_session_id": tc.resume}
			if tc.explicit != "" {
				params["permission_mode"] = tc.explicit
			}
			raw, err := json.Marshal(assistantwire.AgentStartParams{MachineID: "box", Params: params, AllowPermissionDefault: tc.full})
			if err != nil {
				t.Fatal(err)
			}
			result, err := bus.request(t.Context(), assistantwire.MethodAgentStartParams, raw)
			if err != nil {
				t.Fatal(err)
			}
			start := result.(map[string]any)["params"].(map[string]any)
			got, _ := start["permission_mode"].(string)
			if got != tc.want {
				t.Fatalf("start parameters: %v, want permission %q", start, tc.want)
			}
		})
	}
}

func TestConfiguredPermissionUsesAssistantStartRPC(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setSettings(t, s, func(settings *Settings) { settings.DefaultPermissionMode = "acceptEdits" })
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.claude", PermissionMode: "plan"})
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "auto"})
	for _, tc := range []struct {
		name   string
		params StartParams
		want   string
	}{
		{name: "assistant mode", want: "plan"},
		{name: "explicit mode", params: StartParams{PermissionMode: "default"}, want: "default"},
		{name: "supervision inherits", params: StartParams{Purpose: purposeSupervise}, want: "plan"},
		{name: "Inbox stays fixed", params: StartParams{Origin: OriginInbox, Purpose: purposeAgent}, want: "default"},
		{name: "general extension inherits no global mode", params: StartParams{Origin: OriginExtension}, want: "acceptEdits"},
		{name: "Lark does not inherit broader Agents mode", params: StartParams{Origin: OriginLarkAgent, Purpose: purposeLarkAgent}, want: "acceptEdits"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.params.Untitled = true
			sess, err := s.Start(t.Context(), tc.params)
			if err != nil {
				t.Fatal(err)
			}
			start := f.sends[len(f.sends)-1]
			if start["permission_mode"] != tc.want || sess.PermissionMode != tc.want {
				t.Fatalf("start = %v, session reports %q, want %q", start, sess.PermissionMode, tc.want)
			}
		})
	}
	setModel(t, s, purposeLarkAgent, ModelBinding{PermissionMode: "default"})
	sess, err := s.Start(t.Context(), StartParams{Purpose: purposeLarkAgent, Origin: OriginLarkAgent, Untitled: true})
	if err != nil || sess.PermissionMode != "default" || f.sends[len(f.sends)-1]["permission_mode"] != "default" {
		t.Fatalf("explicit owner setting reserved for Lark: %+v, %v", sess, err)
	}
}

func TestConfiguredPermissionDoesNotReplaceReadonlyPolicy(t *testing.T) {
	s, f := newQuietServiceOn(t, &memStore{})
	f.launcherList = []proto.Launcher{{Name: "codex", Agent: "codex", Source: "path"}}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.codex", PermissionMode: proto.ModeBypass})
	task := readonlyTestTask("")
	task.Launcher = "codex"
	if _, _, err := s.startTaskAgent(t.Context(), &task, true); err != nil {
		t.Fatal(err)
	}
	if calls := startsOf(f); len(calls) != 1 || calls[0]["permission_mode"] != "plan" {
		t.Fatalf("read-only task lost its sandbox: %v", calls)
	}
}

func TestConfiguredPermissionTaskPreviewAndDispatchAgree(t *testing.T) {
	for _, tc := range []struct {
		name, preset, explicit, want string
	}{
		{name: "purpose", want: "auto"},
		{name: "preset", preset: "review", want: "plan"},
		{name: "explicit", preset: "review", explicit: "default", want: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newFakeHub()
				f.sessions["box"] = nil
				f.launcherList = []proto.Launcher{{Name: "claude", Agent: "claude"}}
				f.ownModels = true
				s := newServiceOn(t, f, &profileTaskStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}})
				setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: "auto"})
				saveTier(t, s, "review", ModelTier{Launcher: "claude", PermissionMode: "plan"})
				task := readonlyTestTask(tc.explicit)
				task.Readonly, task.Tier = false, tc.preset
				raw := readonlyTaskArgs(t, task)
				out, err := s.toolTaskPlan(t.Context(), "worker-chat", raw)
				if err != nil {
					t.Fatal(err)
				}
				id := planID(t, out)
				p, err := s.loadPlan(t.Context(), id)
				if err != nil || p.Tasks[0].Permission != tc.want {
					t.Fatalf("preview %+v, error %v, want %q", p, err, tc.want)
				}
				// A pending task is approved with the previewed permission.
				// Later settings must not silently replace it at dispatch.
				setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude", PermissionMode: proto.ModeBypass})
				saveTier(t, s, "review", ModelTier{Launcher: "claude", PermissionMode: proto.ModeBypass})
				if _, err = s.startPlan(t.Context(), id, nil, false); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if calls := startsOf(f); len(calls) != 1 || calls[0]["permission_mode"] != tc.want {
					t.Fatalf("wire starts: %v, want %q", calls, tc.want)
				}
			})
		})
	}
}

func TestConfiguredPermissionUsesEffectiveStartCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name     string
		launcher string
		agent    startAgent
		headless bool
		want     []string
	}{
		{name: "Codex terminal", launcher: "codex", want: []string{"plan", proto.ModeBypass}},
		{name: "Codex headless", launcher: "codex", headless: true, agent: startAgent{agent: "codex"}, want: []string{"default", "plan", "auto", proto.ModeBypass}},
		{name: "AGY CLI", launcher: "agy", agent: startAgent{snapshot: "agy", source: "path"}},
		{name: "AGY on explicit herdr", launcher: "agy", agent: startAgent{snapshot: "agy", source: "agy", permissionHost: proto.EngineHerdr}},
		{name: "AGY ACP", launcher: "agy", agent: startAgent{snapshot: "agy", source: "agy"}, want: []string{"default", "acceptEdits", "plan", proto.ModeBypass}},
		{name: "OMP", launcher: "omp", agent: startAgent{snapshot: "omp"}},
		{name: "advertised runtime", launcher: "paseo-codex", agent: startAgent{snapshot: "codex", permissionModes: proto.PermissionModeSetOf("default", proto.ModeBypass)}, want: []string{"default", proto.ModeBypass}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.agent.startPermissionModes(tc.launcher, tc.headless)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("capabilities %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfiguredPermissionSchedulesBindGrantToEffectivePermission(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setSettings(t, s, func(settings *Settings) { settings.DefaultPermissionMode = "default" })
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.claude", PermissionMode: "plan"})
	task := Task{ID: "scheduled", Prompt: "Review the state", MachineID: "server"}
	ex, err := s.taskExecution(t.Context(), task, false, ModelBinding{Provider: "launcher.claude", PermissionMode: "plan"})
	if err != nil || ex.PermissionMode != "plan" {
		t.Fatalf("inherited execution %+v, error %v", ex, err)
	}
	basis, err := s.taskGrantBasis(t.Context(), task)
	if err != nil || basis == task.grantBasis() {
		t.Fatalf("legacy grant would authorize a changed permission: %s, %v", basis, err)
	}
	task.PermissionMode = "acceptEdits"
	ex, err = s.taskExecution(t.Context(), task, false, ModelBinding{Provider: "launcher.claude", PermissionMode: "plan"})
	if err != nil || ex.PermissionMode != "acceptEdits" {
		t.Fatalf("explicit mode lost: %+v, %v", ex, err)
	}
	task.PermissionMode, task.SessionID = "", "existing-conversation"
	ex, err = s.taskExecution(t.Context(), task, false, ModelBinding{Provider: "launcher.claude", PermissionMode: "plan"})
	if err != nil || ex.PermissionMode != "default" {
		t.Fatalf("attached execution inherited a fresh-session preference: %+v, %v", ex, err)
	}
}
