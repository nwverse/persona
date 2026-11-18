package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func externalTestLauncher() proto.Launcher {
	e := &proto.ExternalLauncher{AdapterID: "sample-adapter", ExternalTarget: proto.ExternalTarget{
		ID: "sample-target", Name: "Sample connection", Engine: "claude-code", Revision: "recipe-one",
		Capabilities:    proto.ExternalCapabilities{Contexts: []string{"main-chat"}, Runtimes: []string{"cli"}, Platforms: []string{"posix"}},
		CredentialState: "configured",
		Models:          []proto.AgentModel{{ID: "vendor/model-one", Name: "First model", Default: true}, {ID: "vendor/model-two", Name: "Second model"}},
	}}
	return proto.Launcher{Name: proto.ExternalLauncherName(e.AdapterID, e.ID), Source: proto.ExternalLauncherSource,
		Agent: "claude", Assistant: true, External: e}
}

func externalPickFixture(t *testing.T) (*Service, *fakeHub, proto.Launcher) {
	t.Helper()
	s, f := pickFixture(t)
	l := externalTestLauncher()
	f.mu.Lock()
	f.launcherList = append(f.launcherList, l)
	f.agentModels = map[string][]proto.AgentModel{l.Name: l.External.Models}
	f.mu.Unlock()
	return s, f, l
}

func setExternalAdvertisement(f *fakeHub, l proto.Launcher) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := slices.Clone(f.launcherList)
	for i := range list {
		if list[i].Name == l.Name {
			list[i] = l
		}
	}
	f.launcherList = list
	f.agentModels[l.Name] = l.External.Models
}

func pickExternal(t *testing.T, s *Service, l proto.Launcher, model string) modelPick {
	t.Helper()
	r := putPick(t, s, "main", fmt.Sprintf(`{"launcher":%q,"model":%q}`, l.Name, model))
	if r.Code != 200 {
		t.Fatalf("external pick: %d %s", r.Code, r.Body.String())
	}
	pick, ok := s.modelPickOf(t.Context(), "server", "main")
	if !ok {
		t.Fatal("external pick was not kept")
	}
	return pick
}

func requireExternalError(t *testing.T, err error, suffix string) {
	t.Helper()
	if err == nil || err.Error() != i18n.Err("external-adapter."+suffix).Error() {
		t.Fatalf("got %v, want external-adapter.%s", err, suffix)
	}
}

func externalExecutionOf(t *testing.T, p map[string]any, l proto.Launcher) proto.ExternalExecution {
	t.Helper()
	e, ok := p["external"].(*proto.ExternalExecution)
	want := proto.ExternalExecution{Scope: proto.ExternalMainChatScope, SessionID: "main", AdapterID: l.External.AdapterID,
		TargetID: l.External.ID, Revision: l.External.Revision}
	if !ok || *e != want || p["is_main"] != true || p["launch"] != "headless" {
		t.Fatalf("untrusted or missing execution binding: %v; want %+v", p, want)
	}
	return *e
}

func TestExternalModelPickBindingAndNodeReadback(t *testing.T) {
	s, f, l := externalPickFixture(t)
	pick := pickExternal(t, s, l, "vendor/model-two")
	if pick.Model != "vendor/model-two" || pick.MachineID != "server" {
		t.Fatalf("pick: %+v", pick)
	}
	params, err := send(t, s, "main", map[string]any{"external": map[string]any{"scope": "coding", "target_id": "forged"},
		"source": "forged", "is_main": false})
	if err != nil {
		t.Fatal(err)
	}
	externalExecutionOf(t, params, l)
	if params["launcher"] != l.Name || params["model"] != pick.Model || params["model_fingerprint"] == nil || params["runtime"] != nil {
		t.Fatalf("external model was not applied on CLI: %v", params)
	}
	out, handled, err := s.NodeAnswer(t.Context(), "server", "assistant.model", json.RawMessage(`{"session_id":"main","is_main":false,"external":{"target_id":"forged"}}`))
	if err != nil || !handled {
		t.Fatalf("node model readback: handled=%v error=%v", handled, err)
	}
	readback, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("node model readback: %#v", out)
	}
	externalExecutionOf(t, readback, l)
	if readback["model"] != pick.Model || readback["model_fingerprint"] != params["model_fingerprint"] {
		t.Fatalf("resume/compact changed the selected model: %v", readback)
	}
	// A restarted manager uses the same persisted pick, not client metadata.
	again := newServiceOn(t, f, s.store())
	params, err = send(t, again, "main", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	externalExecutionOf(t, params, l)
	// Ordinary conversations never acquire a binding, even when a client supplies one.
	params, err = send(t, s, "side", map[string]any{"external": &proto.ExternalExecution{Scope: proto.ExternalMainChatScope}})
	if err != nil || params["external"] != nil || params["launcher"] != "cx" {
		t.Fatalf("external leaked into an ordinary conversation: %v %v", params, err)
	}
}

func TestExternalDefaultPickPinsRawModelAndNotDirectoryRevision(t *testing.T) {
	s, f, l := externalPickFixture(t)
	pick := pickExternal(t, s, l, "")
	if pick.Model != "vendor/model-one" {
		t.Fatalf("default was not materialized into the persisted pick: %+v", pick)
	}
	first, err := send(t, s, "main", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	// Change the recipe, display name and directory default while the chosen raw model remains.
	next := externalTestLauncher()
	next.External.Revision, next.External.Name = "recipe-two", "Renamed connection"
	next.External.Models[0].Default, next.External.Models[1].Default = false, true
	setExternalAdvertisement(f, next)
	second, err := send(t, s, "main", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	externalExecutionOf(t, second, next)
	if first["model_fingerprint"] != second["model_fingerprint"] || second["model"] != "vendor/model-one" {
		t.Fatalf("catalog refresh switched a picked model or its execution fingerprint: %v -> %v", first, second)
	}
	if kept, _ := s.modelPickOf(t.Context(), "server", "main"); kept != pick {
		t.Fatalf("catalog refresh changed the pick: %+v", kept)
	}
	// Selecting the default again is an explicit adoption of the new raw ID.
	pick = pickExternal(t, s, next, "")
	if pick.Model != "vendor/model-two" {
		t.Fatalf("explicit default selection did not take the new default: %+v", pick)
	}
}

func TestExternalPickFailureKeepsSelectionAndNeverFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name, suffix string
		change       func(*proto.Launcher)
	}{
		{"missing credentials", "credentials_missing", func(l *proto.Launcher) { l.External.CredentialState = "missing" }},
		{"unavailable", "config_conflict", func(l *proto.Launcher) { l.External.UnavailableCode = "adapter_config_conflict" }},
		{"different target", "target_not_found", func(l *proto.Launcher) { l.External.ID = "another-target" }},
		{"missing revision", "target_not_found", func(l *proto.Launcher) { l.External.Revision = "" }},
		{"wrong source", "target_not_found", func(l *proto.Launcher) { l.Source = "path" }},
		{"missing advertisement", "target_not_found", func(l *proto.Launcher) { l.External = nil }},
		{"wrong context", "purpose_unsupported", func(l *proto.Launcher) { l.External.Capabilities.Contexts = []string{"coding"} }},
		{"wrong engine", "purpose_unsupported", func(l *proto.Launcher) { l.External.Engine = "another-engine" }},
		{"wrong platform", "platform_unsupported", func(l *proto.Launcher) { l.External.Capabilities.Platforms = []string{"windows"} }},
		{"wrong runtime", "runtime_unsupported", func(l *proto.Launcher) { l.External.Capabilities.Runtimes = []string{"agentsdk"} }},
		{"model removed", "model_not_found", func(l *proto.Launcher) { l.External.Models = l.External.Models[1:] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, l := externalPickFixture(t)
			pick := pickExternal(t, s, l, "vendor/model-one")
			if !s.launcherModelSet(withLauncherPurpose(t.Context(), "assistant"), "server", l.Name)[pick.Model] {
				t.Fatal("the model metadata cache was not primed")
			}
			changed := externalTestLauncher()
			tc.change(&changed)
			f.mu.Lock()
			for i := range f.launcherList {
				if f.launcherList[i].Name == l.Name {
					f.launcherList[i] = changed
				}
			}
			f.mu.Unlock()
			before, puts := profileStoreSnapshot(s.store().(*memStore))
			p := map[string]any{}
			err := s.sendModel(t.Context(), "server", "main", p, i18n.En)
			requireExternalError(t, err, tc.suffix)
			if p["launcher"] != nil || p["external"] != nil {
				t.Fatalf("refused pick changed the connection: %v", p)
			}
			if kept, _ := s.modelPickOf(t.Context(), "server", "main"); kept != pick {
				t.Fatalf("refusal changed the selection: %+v", kept)
			}
			requireProfileStoreUnchanged(t, s.store().(*memStore), before, puts)
			if len(f.sends) != 0 || len(f.starts) != 0 || len(f.checked) != 0 {
				t.Fatal("a refused external request reached model execution or fallback")
			}
		})
	}
	t.Run("source disabled or unreadable", func(t *testing.T) {
		s, f, l := externalPickFixture(t)
		pickExternal(t, s, l, "vendor/model-one")
		for _, list := range [][]proto.Launcher{{{Name: "claude"}}, nil} {
			f.mu.Lock()
			f.launcherList = list
			f.mu.Unlock()
			requireExternalError(t, s.sendModel(t.Context(), "server", "main", map[string]any{}, i18n.En), "target_not_found")
		}
	})
	t.Run("requested SDK refuses without changing pick", func(t *testing.T) {
		s, _, l := externalPickFixture(t)
		pick := pickExternal(t, s, l, "vendor/model-one")
		requireExternalError(t, s.sendModel(t.Context(), "server", "main", map[string]any{"runtime": "agentsdk"}, i18n.En), "runtime_unsupported")
		if kept, _ := s.modelPickOf(t.Context(), "server", "main"); kept != pick {
			t.Fatalf("runtime refusal changed the pick: %+v", kept)
		}
	})
}

func TestExternalExecutionRequiresActualPersistedMainPick(t *testing.T) {
	s, f, l := externalPickFixture(t)
	for _, sid := range []string{"main", "side", "agent", "unknown"} {
		_, err := send(t, s, sid, map[string]any{"launcher": l.Name, "is_main": true,
			"external": &proto.ExternalExecution{Scope: proto.ExternalMainChatScope, SessionID: sid}})
		if err == nil {
			t.Fatalf("a message's forged binding granted execution for %s", sid)
		}
	}
	p := modelPick{MachineID: "server", Launcher: l.Name, Model: "vendor/model-one", At: time.Now().UTC()}
	_, err := s.externalPickContext(t.Context(), "server", "main", p)
	requireExternalError(t, err, "scope_required")
	if err := s.setModelPick(t.Context(), "side", &p); err == nil {
		t.Fatal("a side conversation acquired an external pick")
	}
	pick := pickExternal(t, s, l, "vendor/model-one")
	_, err = s.externalPickContext(t.Context(), "elsewhere", "main", pick)
	requireExternalError(t, err, "scope_required")
	_, err = s.externalPickContext(t.Context(), "server", "side", pick)
	requireExternalError(t, err, "scope_required")
	fake := pick
	fake.Model = "vendor/model-two"
	_, err = s.externalPickContext(t.Context(), "server", "main", fake)
	requireExternalError(t, err, "scope_required")
	for _, purpose := range []string{"assistant", "agent.new", "schedule"} {
		params := map[string]any{"launcher": l.Name, "model": pick.Model, "is_main": true,
			"external": &proto.ExternalExecution{Scope: proto.ExternalMainChatScope, SessionID: "main"}}
		if err := s.applyModel(t.Context(), purpose, params); err == nil {
			t.Fatalf("purpose %s accepted an untrusted external binding", purpose)
		}
	}
	for _, main := range []bool{false, true} {
		if _, err := s.startOn(t.Context(), "server", StartParams{Launcher: l.Name, Model: pick.Model}, main); err == nil {
			t.Fatalf("a new conversation acquired external scope (main=%v)", main)
		}
	}
	for _, launcher := range []string{"", l.Name} {
		s.warm(launcher)
	}
	if len(f.prepared) != 0 || len(f.sends) != 0 || len(f.starts) != 0 {
		t.Fatalf("prewarm/start ran an external selection: prepare=%v sends=%v starts=%v", f.prepared, f.sends, f.starts)
	}
	// Making another session the main one removes scope, but never deletes the old pick.
	setSettings(t, s, func(v *Settings) { v.Main = "another-main" })
	_, err = s.externalPickContext(t.Context(), "server", "main", pick)
	requireExternalError(t, err, "scope_required")
	picks, _ := s.modelPicksSet()
	if !reflect.DeepEqual(picks["main"], pick) {
		t.Fatal("changing main deleted its external selection")
	}
}

func TestExternalPickOnlyOffersExactModels(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		change      func(*proto.ExternalLauncher)
		ok          bool
	}{
		{name: "raw ID", model: "vendor/model-two", ok: true},
		{name: "default", ok: true},
		{name: "display name", model: "First model"},
		{name: "typed unknown ID", model: "vendor/not-listed"},
		{name: "resolved alias", model: "vendor/resolved", change: func(e *proto.ExternalLauncher) { e.Models[0].Resolved = "vendor/resolved" }},
		{name: "no declared default", change: func(e *proto.ExternalLauncher) { e.Models[0].Default = false }},
		{name: "empty directory", change: func(e *proto.ExternalLauncher) { e.Models = nil }},
		{name: "unknown credentials", model: "vendor/model-one", change: func(e *proto.ExternalLauncher) { e.CredentialState = "unknown" }, ok: true},
		{name: "stale advertisement", model: "vendor/model-one", change: func(e *proto.ExternalLauncher) { e.Stale = true }, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, l := externalPickFixture(t)
			if tc.change != nil {
				tc.change(l.External)
				setExternalAdvertisement(f, l)
			}
			r := putPick(t, s, "main", fmt.Sprintf(`{"launcher":%q,"model":%q}`, l.Name, tc.model))
			if (r.Code == 200) != tc.ok {
				t.Fatalf("pick: %d %s", r.Code, r.Body.String())
			}
		})
	}
}

func TestExternalScopeUsesActualDeviceSessionClassification(t *testing.T) {
	s, _, _ := externalPickFixture(t)
	d := newFleetDevs()
	d.add("server", "Computer")
	s.devs = d
	for _, tc := range []struct {
		name string
		sess proto.Session
		want bool
	}{
		{"raw headless session", proto.Session{SessionID: "main", Cwd: "/home/u/.persona/assistant"}, true},
		{"pane in assistant directory", proto.Session{SessionID: "main", Cwd: "/home/u/.persona/assistant", PaneID: "pane"}, false},
		{"ordinary coding directory", proto.Session{SessionID: "main", Cwd: "/code/sample", Assistant: true}, false},
		{"another manager", proto.Session{SessionID: "main", Cwd: "/home/u/.persona/assistant-other", Assistant: true}, false},
		{"health space", proto.Session{SessionID: "main", Cwd: "/home/u/.persona/health", Space: SpaceHealth}, false},
		{"meeting room", proto.Session{SessionID: "main", Cwd: "/home/u/.persona/assistant", Origin: OriginMeeting}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d.setSessions("server", []proto.Session{tc.sess})
			if got := s.externalMainScope(t.Context(), "server", "main"); got != tc.want {
				t.Fatalf("external scope=%v, want %v for %+v", got, tc.want, tc.sess)
			}
		})
	}
	d.setSessions("server", []proto.Session{{SessionID: "main", Cwd: "/home/u/.persona/assistant"}})
	d.mu.Lock()
	d.machines["server"].trust = "work"
	d.mu.Unlock()
	if s.externalMainScope(t.Context(), "server", "main") {
		t.Fatal("a work computer acquired personal main-chat scope")
	}
}

func TestExternalScopeRechecksThePersistedPickAtExecution(t *testing.T) {
	s, _, l := externalPickFixture(t)
	pick := pickExternal(t, s, l, "vendor/model-one")
	ctx, err := s.externalPickContext(t.Context(), "server", "main", pick)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setModelPick(t.Context(), "main", nil); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"external": &proto.ExternalExecution{Scope: proto.ExternalMainChatScope}}
	err = s.finishModel(ctx, "assistant", params, ModelBinding{Provider: launcherProvider(l.Name), Model: pick.Model})
	requireExternalError(t, err, "scope_required")
	if params["external"] != nil {
		t.Fatal("a revoked pick retained a binding")
	}
}

func TestExternalPickDoesNotAcceptMessageOnlyModelChanges(t *testing.T) {
	s, _, l := externalPickFixture(t)
	pickExternal(t, s, l, "vendor/model-one")
	for _, model := range []string{"vendor/model-two", "ordinary-model"} {
		err := s.sendModel(t.Context(), "server", "main", map[string]any{"model": model}, i18n.En)
		requireExternalError(t, err, "scope_required")
	}
	params, err := send(t, s, "main", map[string]any{"model": "vendor/model-one", "session_id": "forged"})
	if err != nil || params["session_id"] != "main" {
		t.Fatalf("the real main session was not pinned: %v %v", params, err)
	}
	externalExecutionOf(t, params, l)
	for _, extra := range []map[string]any{{"guest": map[string]any{}}, {"new_session_id": "forged"}, {"runtime": "agentsdk"}, {"runtime": "future-runtime"}} {
		if _, err := send(t, s, "main", extra); err == nil {
			t.Fatalf("external execution accepted out-of-scope params: %v", extra)
		}
	}
}

func TestExternalRuntimeAdmissionKeepsTheRawRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime any
		allowed bool
	}{
		{"CLI", "cli", true},
		{"default", "", true},
		{"SDK", "agentsdk", false},
		{"unknown", "future-runtime", false},
		{"padded CLI", " cli ", false},
		{"null", nil, false},
		{"number", 1, false},
		{"boolean", true, false},
		{"object", map[string]any{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, l := externalPickFixture(t)
			pickExternal(t, s, l, "vendor/model-one")
			for _, entry := range []string{"send params", "model boundary"} {
				t.Run(entry, func(t *testing.T) {
					params := map[string]any{"runtime": tc.runtime}
					var err error
					if entry == "send params" {
						params, err = send(t, s, "main", params)
					} else {
						err = s.sendModel(t.Context(), "server", "main", params, i18n.En)
					}
					if (err == nil) != tc.allowed {
						t.Fatalf("runtime %v: allowed=%v, params=%v, error=%v", tc.runtime, tc.allowed, params, err)
					}
					if err == nil {
						externalExecutionOf(t, params, l)
						if _, sent := params["runtime"]; sent {
							t.Fatal("external runtime reached the node")
						}
					} else if params["external"] != nil {
						t.Fatal("a refused runtime acquired an execution binding")
					}
				})
			}
		})
	}
}

func TestExternalLegacyStoredRuntimeDoesNotChooseTheRuntime(t *testing.T) {
	s, _, l := externalPickFixture(t)
	pickExternal(t, s, l, "vendor/model-one")
	putRawSetting(t, s, modelRecordKey("assistant"),
		`{"revision":2,"binding":{"provider":"launcher.gw","runtime":"agentsdk"}}`)
	params, err := send(t, s, "main", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	externalExecutionOf(t, params, l)
	if _, sent := params["runtime"]; sent {
		t.Fatal("a legacy stored runtime reached external execution")
	}
}

func TestExternalRuntimeAdmissionLeavesOrdinaryRuntimeIgnored(t *testing.T) {
	s, _, _ := externalPickFixture(t)
	if err := s.guestSet(t.Context(), "server", "guest", []string{"sample.tool"}); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"main", "side", "unknown", "guest"} {
		for _, runtime := range []any{"agentsdk", "future-runtime", map[string]any{}} {
			params, err := send(t, s, sid, map[string]any{"runtime": runtime})
			if err != nil {
				t.Fatalf("ordinary %s runtime %v: %v", sid, runtime, err)
			}
			if _, sent := params["runtime"]; sent || params["external"] != nil {
				t.Fatalf("ordinary %s retained execution metadata: %v", sid, params)
			}
		}
	}
}

func TestExternalModelReadbackFollowsTheConfirmedMainComputer(t *testing.T) {
	s, f, l := externalPickFixture(t)
	pickExternal(t, s, l, "vendor/model-one")
	f.mu.Lock()
	f.sessions["other"] = nil
	f.mu.Unlock()
	setSettings(t, s, func(v *Settings) { v.MachineID, v.MainMachineID = "other", "server" })
	out, handled, err := s.NodeAnswer(t.Context(), "server", "assistant.model", json.RawMessage(`{"session_id":"main"}`))
	if err != nil || !handled {
		t.Fatalf("readback on the main computer: %v %v", handled, err)
	}
	externalExecutionOf(t, out.(map[string]any), l)
	out, _, err = s.NodeAnswer(t.Context(), "other", "assistant.model", json.RawMessage(`{"session_id":"main","is_main":true}`))
	if err != nil || out.(map[string]any)["external"] != nil {
		t.Fatalf("the default computer inherited the main computer's pick: %v %v", out, err)
	}
}

func TestExternalColdInventoryRestoresPersistedMainPick(t *testing.T) {
	s, f, l := externalPickFixture(t)
	pick := pickExternal(t, s, l, "vendor/model-two")
	again := newServiceOn(t, f, s.store())
	var requests []map[string]any
	f.answerNode(DeviceLaunchersList, func(machine string, raw json.RawMessage) (any, error) {
		var params map[string]any
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		requests = append(requests, params)
		if machine != pick.MachineID {
			t.Errorf("inventory read on %q, want %q", machine, pick.MachineID)
		}
		if params["refresh"] == true {
			return launcherListing{Launchers: []proto.Launcher{l}}, nil
		}
		return launcherListing{Launchers: []proto.Launcher{{Name: "claude"}}}, nil
	})
	params, err := send(t, again, "main", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	externalExecutionOf(t, params, l)
	if params["launcher"] != pick.Launcher || params["model"] != pick.Model {
		t.Fatalf("cold inventory changed the selected target or raw model: %v", params)
	}
	if kept, ok := again.modelPickOf(t.Context(), pick.MachineID, "main"); !ok || kept != pick {
		t.Fatalf("cold inventory changed the persisted pick: %+v %v", kept, ok)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(requests) != 2 || len(requests[0]) != 0 || !reflect.DeepEqual(requests[1], map[string]any{"refresh": true}) {
		t.Fatalf("cold inventory calls: %v", requests)
	}
}

func TestExternalColdInventoryValidatesExplicitPick(t *testing.T) {
	s, f, l := externalPickFixture(t)
	var refreshes int
	f.answerNode(DeviceLaunchersList, func(_ string, raw json.RawMessage) (any, error) {
		var params struct {
			Refresh bool `json:"refresh"`
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		if params.Refresh {
			refreshes++
		}
		if refreshes > 0 {
			return launcherListing{Launchers: []proto.Launcher{l}}, nil
		}
		return launcherListing{}, nil
	})
	pick := pickExternal(t, s, l, "")
	if pick.Model != "vendor/model-one" {
		t.Fatalf("cold default did not persist the exact raw model: %+v", pick)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if refreshes != 1 {
		t.Fatalf("explicit pick refreshes=%d, want 1", refreshes)
	}
}

func TestExternalColdInventoryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, suffix string
		change       func(*proto.Launcher)
		missing      bool
		refreshErr   error
	}{
		{name: "refresh failed", suffix: "target_not_found", refreshErr: errors.New("inventory unavailable")},
		{name: "target still missing", suffix: "target_not_found", missing: true},
		{name: "forged identity", suffix: "target_not_found", change: func(l *proto.Launcher) { l.External.ID = "another-target" }},
		{name: "wrong source", suffix: "target_not_found", change: func(l *proto.Launcher) { l.Source = "path" }},
		{name: "missing revision", suffix: "target_not_found", change: func(l *proto.Launcher) { l.External.Revision = "" }},
		{name: "context changed", suffix: "purpose_unsupported", change: func(l *proto.Launcher) { l.External.Capabilities.Contexts = []string{"coding"} }},
		{name: "runtime changed", suffix: "runtime_unsupported", change: func(l *proto.Launcher) { l.External.Capabilities.Runtimes = []string{"agentsdk"} }},
		{name: "platform changed", suffix: "platform_unsupported", change: func(l *proto.Launcher) { l.External.Capabilities.Platforms = []string{"windows"} }},
		{name: "credentials removed", suffix: "credentials_missing", change: func(l *proto.Launcher) { l.External.CredentialState = "missing" }},
		{name: "raw model removed", suffix: "model_not_found", change: func(l *proto.Launcher) { l.External.Models = l.External.Models[:1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, l := externalPickFixture(t)
			pick := pickExternal(t, s, l, "vendor/model-two")
			changed := externalTestLauncher()
			if tc.change != nil {
				tc.change(&changed)
			}
			var refreshes int
			f.answerNode(DeviceLaunchersList, func(_ string, raw json.RawMessage) (any, error) {
				var params struct {
					Refresh bool `json:"refresh"`
				}
				if err := json.Unmarshal(raw, &params); err != nil {
					return nil, err
				}
				if !params.Refresh {
					return launcherListing{}, nil
				}
				refreshes++
				if tc.refreshErr != nil {
					return nil, tc.refreshErr
				}
				if tc.missing {
					return launcherListing{}, nil
				}
				return launcherListing{Launchers: []proto.Launcher{changed}}, nil
			})
			before, puts := profileStoreSnapshot(s.store().(*memStore))
			params := map[string]any{}
			requireExternalError(t, s.sendModel(t.Context(), pick.MachineID, "main", params, i18n.En), tc.suffix)
			if params["launcher"] != nil || params["external"] != nil {
				t.Fatalf("refused cold pick changed the connection: %v", params)
			}
			if kept, _ := s.modelPickOf(t.Context(), pick.MachineID, "main"); kept != pick {
				t.Fatalf("refused refresh changed the pick: %+v", kept)
			}
			requireProfileStoreUnchanged(t, s.store().(*memStore), before, puts)
			f.mu.Lock()
			defer f.mu.Unlock()
			if refreshes != 1 || len(f.sends) != 0 || len(f.starts) != 0 || len(f.checked) != 0 {
				t.Fatalf("refused cold pick executed or retried: refreshes=%d sends=%v starts=%v checks=%v", refreshes, f.sends, f.starts, f.checked)
			}
		})
	}
}

type externalInventoryContextHub struct {
	Hub
	list func(context.Context, DeviceCall) (json.RawMessage, error)
}

func (h externalInventoryContextHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method == DeviceLaunchersList {
		return h.list(ctx, p)
	}
	return h.Hub.DeviceCall(ctx, p)
}

func TestExternalColdInventoryRefreshKeepsCallerContextAndBound(t *testing.T) {
	for _, shorterDeadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("shorterDeadline=%v", shorterDeadline), func(t *testing.T) {
			s, f, l := externalPickFixture(t)
			pickExternal(t, s, l, "vendor/model-one")
			type contextKey struct{}
			parent := context.WithValue(t.Context(), contextKey{}, "caller")
			var cancel context.CancelFunc
			if shorterDeadline {
				parent, cancel = context.WithTimeout(parent, time.Second)
			} else {
				parent, cancel = context.WithCancel(parent)
			}
			defer cancel()
			calls := 0
			s.hub = externalInventoryContextHub{Hub: f, list: func(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
				calls++
				if string(p.Params) != `{"refresh":true}` {
					return json.Marshal(launcherListing{})
				}
				if ctx.Value(contextKey{}) != "caller" {
					t.Error("refresh discarded caller context")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > launcherSyncTimeout {
					t.Error("refresh has no bounded deadline")
				}
				if parentDeadline, ok := parent.Deadline(); ok && deadline.After(parentDeadline) {
					t.Error("refresh extended caller deadline")
				}
				cancel()
				<-ctx.Done()
				return nil, ctx.Err()
			}}
			requireExternalError(t, s.sendModel(parent, "server", "main", map[string]any{}, i18n.En), "target_not_found")
			if calls != 2 {
				t.Fatalf("canceled inventory calls=%d, want 2", calls)
			}
		})
	}
}

func TestExternalColdInventoryDoesNotGrantAnotherMachineScope(t *testing.T) {
	s, f, l := externalPickFixture(t)
	pick := pickExternal(t, s, l, "vendor/model-one")
	f.mu.Lock()
	f.sessions["other"] = nil
	f.mu.Unlock()
	var calls int
	f.answerNode(DeviceLaunchersList, func(_ string, _ json.RawMessage) (any, error) {
		calls++
		return launcherListing{Launchers: []proto.Launcher{l}}, nil
	})
	_, err := s.externalPickContext(t.Context(), "other", "main", pick)
	requireExternalError(t, err, "scope_required")
	forged := pick
	forged.MachineID = "other"
	_, err = s.resolveExternalPick(t.Context(), "main", forged)
	requireExternalError(t, err, "scope_required")
	f.mu.Lock()
	defer f.mu.Unlock()
	if calls != 0 {
		t.Fatalf("out-of-scope pick queried inventory %d times", calls)
	}
}

func TestExternalHotInventoryAndOrdinaryListsDoNotRefresh(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid=%v", invalid), func(t *testing.T) {
			s, f, l := externalPickFixture(t)
			pickExternal(t, s, l, "vendor/model-one")
			if invalid {
				l.External.Capabilities.Contexts = []string{"coding"}
			}
			var calls int
			f.answerNode(DeviceLaunchersList, func(_ string, raw json.RawMessage) (any, error) {
				calls++
				var params map[string]any
				if err := json.Unmarshal(raw, &params); err != nil {
					return nil, err
				}
				if len(params) != 0 {
					t.Errorf("hot/ordinary list changed params: %s", raw)
				}
				return launcherListing{Launchers: []proto.Launcher{l}}, nil
			})
			err := s.sendModel(t.Context(), "server", "main", map[string]any{}, i18n.En)
			if invalid {
				requireExternalError(t, err, "purpose_unsupported")
			} else if err != nil {
				t.Fatal(err)
			}
			if _, err := s.listLaunchers(t.Context(), Device{ID: "server", Online: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.listLaunchers(t.Context(), Device{ID: "server", Online: true}, false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.machineLaunchers(t.Context(), "server"); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if calls != 4 {
				t.Fatalf("hot inventory or ordinary lists made extra calls: %d", calls)
			}
		})
	}
}

func TestExternalUnavailableCodesAreSanitized(t *testing.T) {
	for _, suffix := range []string{"config_conflict", "credentials_missing", "runtime_unsupported", "platform_unsupported", "revision_mismatch", "timeout"} {
		requireExternalError(t, externalUnavailableError("adapter_"+suffix), suffix)
	}
	requireExternalError(t, externalUnavailableError("private diagnostic: https://example.invalid/token"), "target_not_found")
}

func TestExternalFailureDoesNotTriggerHealthChecks(t *testing.T) {
	s, f, l := externalPickFixture(t)
	s.recordStartFailure(t.Context(), "server", l.Name, errors.New("adapter_config_conflict"))
	if _, ok := s.launcherHealth(t.Context(), "server", l.Name); ok {
		t.Fatal("adapter configuration failure became provider health")
	}
	if _, asked := s.probeLauncherHealth(t.Context(), "server", l.Name); asked {
		t.Fatal("external source was automatically probed")
	}
	list, err := s.healthLaunchers(t.Context(), "server")
	if err != nil || slices.Contains(list, l.Name) || len(f.checked) != 0 {
		t.Fatalf("external source entered automatic health polling: %v %v %v", list, err, f.checked)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.applyPick(ctx, map[string]any{}, modelPick{MachineID: "server", Launcher: l.Name, Model: "vendor/model-one"}); err == nil {
		t.Fatal("an arbitrary modelPick struct granted external scope")
	}
}
