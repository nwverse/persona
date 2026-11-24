package assistant

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The two tiers Persona seeded under strength names read back under the kind of work, the old name kept as an alias
// and nothing else changed; a tier the user made, and a seeded one whose new name is taken, keep their names (#14700).
func TestSeededTierNamesMigrateWithAliases(t *testing.T) {
	s, _, st := modelService(t)
	ctx := context.Background()
	strong := ModelTier{Launcher: "claude", Model: "test-big", Effort: "high", Speed: "fast", PermissionMode: "plan", Description: "Design"}
	standard := ModelTier{Launcher: "codex", Model: "test-coding", Effort: "medium"}
	bulk := ModelTier{Launcher: "claude", Model: "test-small"}
	storeProfiles(t, st,
		ModelProfile{ID: "performance", Name: "Performance", Tiers: map[string]ModelTier{"strong": strong, "standard": standard, "bulk": bulk}},
		ModelProfile{ID: "economy", Name: "Economy", Tiers: map[string]ModelTier{"standard": standard, "dev": bulk}})
	if err := st.PutSetting(ctx, modelProfileStateKey, modelProfileState{Active: "performance"}); err != nil {
		t.Fatal(err)
	}
	ps, _ := storedTiers(t, s)
	perf, econ := ps.Profiles[0].Tiers, ps.Profiles[1].Tiers
	if len(perf) != 3 || !slices.Equal(slices.Sorted(maps.Keys(perf)), []string{"bulk", "core", "dev"}) {
		t.Fatalf("performance tiers: %+v", perf)
	}
	wantCore, wantDev := strong, standard
	wantCore.Aliases, wantDev.Aliases = []string{"strong"}, []string{"standard"}
	if !perf["core"].equal(wantCore) || !perf["dev"].equal(wantDev) || !perf["bulk"].equal(bulk) {
		t.Fatalf("migration changed what the tiers run on: %+v", perf)
	}
	if _, ok := econ["standard"]; !ok || !econ["dev"].equal(bulk) || len(econ["standard"].Aliases) != 0 {
		t.Fatalf("a seeded tier whose new name is taken was renamed: %+v", econ)
	}
	// The old names keep resolving, to the same launcher and model as before.
	for name, want := range map[string]ModelTier{"strong": strong, "core": strong, "standard": standard, "dev": standard, "bulk": bulk} {
		pick, err := s.pickTierCandidate(ctx, name, tierFacts{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !pick.Tier.equal(want) || pick.Reason != tierReasonOnly || pick.Of != 1 || pick.Candidate != 0 || len(pick.Checks) != 0 {
			t.Fatalf("%s picked %+v", name, pick)
		}
	}
	if pick, _ := s.pickTierCandidate(ctx, "strong", tierFacts{}); pick.Name != "core" {
		t.Fatalf("an alias did not resolve to its tier's own name: %+v", pick)
	}
	// The next write stores the new names; a read again changes nothing more.
	if _, err := s.saveModelProfile(ctx, nil, "performance", ProfileActor{Kind: "hub"}, func(p *ModelProfile, _ bool) error {
		p.Description = "saved"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var raw modelProfiles
	if _, err := st.Setting(ctx, modelProfilesKey, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.Profiles[0].Tiers["core"]; !ok || !slices.Equal(raw.Profiles[0].Tiers["core"].Aliases, []string{"strong"}) {
		t.Fatalf("stored after a save: %+v", raw.Profiles[0].Tiers)
	}
}

// Candidates and aliases are checked on save: at most six candidates, each with a launcher and a valid model, effort,
// speed and permission mode; an alias is a tier name no other tier has or answers to.
func TestTierCandidatesAndAliasesValidation(t *testing.T) {
	ok := map[string]ModelTier{"dev": {Launcher: "claude", Candidates: []TierCandidate{{Launcher: "claude"}, {Launcher: "codex", Effort: "high", Speed: "fast"}},
		Aliases: []string{" standard ", "standard", "bulk"}}, "core": {Launcher: "claude", Aliases: []string{"strong"}}}
	if err := checkModelTiers(ok); err != nil {
		t.Fatalf("good tiers: %v", err)
	}
	if !slices.Equal(ok["dev"].Aliases, []string{"standard", "bulk"}) {
		t.Fatalf("aliases not normalized: %+v", ok["dev"].Aliases)
	}
	seven := make([]TierCandidate, maxTierCandidates+1)
	for i := range seven {
		seven[i] = TierCandidate{Launcher: "claude"}
	}
	for name, tiers := range map[string]map[string]ModelTier{
		"too many candidates":   {"dev": {Launcher: "claude", Candidates: seven}},
		"candidate no launcher": {"dev": {Launcher: "claude", Candidates: []TierCandidate{{Launcher: "claude"}, {Model: "m"}}}},
		"candidate bad effort":  {"dev": {Launcher: "claude", Candidates: []TierCandidate{{Launcher: "claude"}, {Launcher: "codex", Effort: "huge"}}}},
		"candidate bad speed":   {"dev": {Launcher: "claude", Candidates: []TierCandidate{{Launcher: "claude"}, {Launcher: "codex", Speed: "warp"}}}},
		"candidate bad mode":    {"dev": {Launcher: "claude", Candidates: []TierCandidate{{Launcher: "claude"}, {Launcher: "codex", PermissionMode: "anything"}}}},
		"candidate no modes":    {"dev": {Launcher: "claude", Candidates: []TierCandidate{{Launcher: "claude"}, {Launcher: "omp", PermissionMode: "plan"}}}},
		"alias is a tier name":  {"dev": {Launcher: "claude", Aliases: []string{"core"}}, "core": {Launcher: "claude"}},
		"alias of two tiers":    {"dev": {Launcher: "claude", Aliases: []string{"old"}}, "core": {Launcher: "claude", Aliases: []string{"old"}}},
		"alias bad form":        {"dev": {Launcher: "claude", Aliases: []string{"Old Name"}}},
	} {
		if err := checkModelTiers(tiers); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A model id is held only on the candidates added or changed.
	was := ModelTier{Launcher: "claude", Candidates: []TierCandidate{{Launcher: "claude"}, {Launcher: "codex", Model: "old+model"}}}
	if err := checkTierModel("dev", was, &was); err != nil {
		t.Fatalf("an unchanged legacy model blocked the save: %v", err)
	}
	changed := ModelTier{Launcher: "claude", Candidates: []TierCandidate{{Launcher: "claude"}, {Launcher: "codex", Model: "new+model"}}}
	if err := checkTierModel("dev", changed, &was); err == nil || !strings.Contains(err.Error(), "dev #2") {
		t.Fatalf("a changed bad model on candidate 2: %v", err)
	}
}

// An older client's PUT sends a tier's flat fields only: they replace the first candidate, and the other candidates
// and the aliases stay. A body with candidates and aliases says them itself; an empty list clears them.
func TestModelProfilePutMergesOldShapeTiers(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	if _, err := s.saveModelProfile(ctx, nil, "custom", ProfileActor{Kind: "hub"}, func(p *ModelProfile, _ bool) error {
		p.Name = "Custom"
		p.Tiers = map[string]ModelTier{"dev": {Description: "Development",
			Candidates: []TierCandidate{{Launcher: "first", Model: "m-first"}, {Launcher: "second", Model: "m-second"}, {Launcher: "third"}},
			Aliases:    []string{"standard"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tier := func() ModelTier {
		t.Helper()
		ps, _ := storedTiers(t, s)
		got, ok := tierOf(ps, "custom", "dev")
		if !ok {
			t.Fatal("tier dev is gone")
		}
		return got
	}
	if got := tier(); got.Launcher != "first" || got.Model != "m-first" || len(got.Candidates) != 3 {
		t.Fatalf("flat fields do not mirror the first candidate: %+v", got)
	}
	revision := func() int64 { ps, _ := storedTiers(t, s); return ps.Revision }
	put := func(tiers string) {
		t.Helper()
		body := `{"revision":` + jsonInt(revision()) + `,"name":"Custom","tiers":` + tiers + `}`
		if r := request(s, http.MethodPut, "/api/v1/model-profiles/custom", body); r.Code != http.StatusOK {
			t.Fatalf("PUT %s: %d %s", tiers, r.Code, r.Body.String())
		}
	}
	put(`{"dev":{"launcher":"replaced","model":"m-new","effort":"high","description":"Development"}}`)
	got := tier()
	want := []TierCandidate{{Launcher: "replaced", Model: "m-new", Effort: "high"}, {Launcher: "second", Model: "m-second"}, {Launcher: "third"}}
	if !slices.Equal(got.Candidates, want) || got.Launcher != "replaced" || got.Effort != "high" || !slices.Equal(got.Aliases, []string{"standard"}) {
		t.Fatalf("old-shape PUT lost candidates or aliases: %+v", got)
	}
	put(`{"dev":{"launcher":"ignored","candidates":[{"launcher":"second"},{"launcher":"replaced","model":"m-new"}],"aliases":["standard","bulk"]}}`)
	got = tier()
	if got.Launcher != "second" || got.Model != "" || len(got.Candidates) != 2 || !slices.Equal(got.Aliases, []string{"standard", "bulk"}) {
		t.Fatalf("new-shape PUT: %+v", got)
	}
	put(`{"dev":{"launcher":"only","candidates":[],"aliases":[]}}`)
	if got = tier(); got.Launcher != "only" || got.Candidates != nil || got.Aliases != nil {
		t.Fatalf("empty lists did not clear: %+v", got)
	}
	// A body that sends seven candidates is refused.
	seven := strings.Repeat(`{"launcher":"x"},`, maxTierCandidates) + `{"launcher":"x"}`
	body := `{"revision":` + jsonInt(revision()) + `,"name":"Custom","tiers":{"dev":{"launcher":"x","candidates":[` + seven + `]}}}`
	if r := request(s, http.MethodPut, "/api/v1/model-profiles/custom", body); r.Code != http.StatusBadRequest {
		t.Fatalf("seven candidates: %d %s", r.Code, r.Body.String())
	}
}

func jsonInt(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// pickerService is an assistant on machine server, which lists claude and fast, with one profile in use.
func pickerService(t *testing.T, tiers map[string]ModelTier) (*Service, *fakeHub) {
	t.Helper()
	f := newFakeHub().withMachine()
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "fast", Model: "m-fast"}}
	s := newTestService(t, f)
	putTierProfiles(t, s, "economy", tiers, nil)
	return s, f
}

// One candidate starts exactly as a tier did before candidates: the same launcher, model, effort, speed and permission
// mode, nothing read of the computer, and the reason "only".
func TestSingleCandidateTierStartsAsBefore(t *testing.T) {
	tier := ModelTier{Launcher: "absent", Model: "m-one", Effort: "high", Speed: "fast", PermissionMode: "plan", Description: "Dev"}
	s, f := pickerService(t, map[string]ModelTier{"dev": tier})
	listCalls := func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.launcherListParams) }
	before := listCalls()
	ctx, slot := withTierStart(context.Background(), "server")
	params := map[string]any{"tier": "dev"}
	if err := s.applyStartTier(ctx, params); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"launcher": "absent", "model": "m-one", "effort": "high", "speed": "fast", "permission_mode": "plan"} {
		if got, _ := params[key].(string); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if _, ok := params["tier"]; ok {
		t.Fatal("tier leaked to the node")
	}
	if listed := listCalls() - before; listed != 0 {
		t.Fatalf("a one-candidate tier read the computer's launchers %d times", listed)
	}
	if slot.pick == nil || slot.pick.Reason != tierReasonOnly || slot.pick.Of != 1 || slot.pick.Candidate != 0 {
		t.Fatalf("pick: %+v", slot.pick)
	}
	d := slot.pick.dispatch()
	if d.Tier != "dev" || d.Launcher != "absent" || d.Model != "m-one" || d.Reason != "only" || d.Of != 1 || d.Args != nil {
		t.Fatalf("dispatch: %+v", d)
	}
	// A launcher of the start's own: the tier gives nothing, and there is no dispatch to record.
	ctx, slot = withTierStart(context.Background(), "server")
	if err := s.applyStartTier(ctx, map[string]any{"tier": "dev", "launcher": "claude"}); err != nil || slot.pick != nil {
		t.Fatalf("own launcher: %v %+v", err, slot.pick)
	}
}

// Several candidates: one this computer lacks or whose health check failed is passed over, the first usable one in
// order is taken, and with none usable the first; nothing outside the list is ever chosen.
func TestTierCandidatesFilterInOrder(t *testing.T) {
	s, _ := pickerService(t, map[string]ModelTier{
		"dev":  {Launcher: "missing", Candidates: []TierCandidate{{Launcher: "missing", Model: "m0"}, {Launcher: "fast", Model: "m1"}, {Launcher: "claude", Model: "m2"}}},
		"none": {Launcher: "gone", Candidates: []TierCandidate{{Launcher: "gone", Model: "g0"}, {Launcher: "away", Model: "g1"}}},
	})
	ctx := context.Background()
	pick, err := s.pickTierCandidate(ctx, "dev", tierFacts{Machine: "server"})
	if err != nil {
		t.Fatal(err)
	}
	if pick.Candidate != 1 || pick.Tier.Launcher != "fast" || pick.Tier.Model != "m1" || pick.Of != 3 || pick.Reason != tierReasonUnavailable ||
		len(pick.Checks) != 3 || pick.Checks[0].Available || !pick.Checks[1].Available || !pick.Checks[2].Available {
		t.Fatalf("pick: %+v", pick)
	}
	if d := pick.dispatch(); d.Candidate != 1 || d.Of != 3 || d.Reason != "unavailable" || !slices.Equal(d.Args["passed_over"].([]string), []string{"missing"}) {
		t.Fatalf("dispatch: %+v", d)
	}
	// A failed health check passes fast over too.
	s.recordStartFailure(ctx, "server", "fast", terminalRefusal(proto.CodeLauncherMissing, "fast"))
	if pick, _ = s.pickTierCandidate(ctx, "dev", tierFacts{Machine: "server"}); pick.Candidate != 2 || pick.Tier.Launcher != "claude" {
		t.Fatalf("an unhealthy candidate was taken: %+v", pick)
	}
	// None usable: the first, for the start's own refusal to say why.
	if pick, _ = s.pickTierCandidate(ctx, "none", tierFacts{Machine: "server"}); pick.Candidate != 0 || pick.Tier.Launcher != "gone" || pick.Reason != tierReasonFirst {
		t.Fatalf("none usable: %+v", pick)
	}
	// The ranker (#14701) may take another usable one, never an unusable or unlisted one.
	s.tierRank = func(_ context.Context, in tierRankInput) (int, string, bool) { return 0, "score", true }
	if pick, _ = s.pickTierCandidate(ctx, "dev", tierFacts{Machine: "server"}); pick.Candidate != 2 {
		t.Fatalf("the ranker took an unusable candidate: %+v", pick)
	}
	s.tierRank = func(_ context.Context, in tierRankInput) (int, string, bool) { return 2, "score", true }
	s.recordStartSuccess(ctx, "server", "fast")
	if pick, _ = s.pickTierCandidate(ctx, "dev", tierFacts{Machine: "server"}); pick.Candidate != 2 || pick.Reason != "score" {
		t.Fatalf("the ranker's pick: %+v", pick)
	}
	s.tierRank = nil
	// The start-defaults source and the New Agent form take the same first usable candidate.
	got, err := s.candidateSource().candidates(ctx, "server", []string{"dev", "none"})
	if err != nil || !got["dev"].Available || got["dev"].Launcher != "fast" || got["none"].Available || got["none"].Launcher != "gone" {
		t.Fatalf("start-defaults candidates: %+v %v", got, err)
	}
	_, tiers := s.pickerTiers(ctx, "server", []ConfiguredLauncher{{Launcher: proto.Launcher{Name: "claude"}}, {Launcher: proto.Launcher{Name: "fast"}}})
	if len(tiers) != 1 || tiers[0].Name != "dev" || tiers[0].Launcher != "fast" || tiers[0].Candidate != 1 || tiers[0].Candidates != nil {
		t.Fatalf("picker tiers: %+v", tiers)
	}
}

// A plan task's start on a tier records its dispatch on the plan and one model_routing row for the profile in use.
func TestTaskStartRecordsTheDispatch(t *testing.T) {
	s, f, st := newProfileTaskService(t)
	requireProfileMCP(t, s, "model_profile_switch", map[string]any{"id": "performance"}, nil)
	p := dispatchProfilePlan(t, s, st, profilePlanTask())
	if p.Tasks[0].Dispatch != nil {
		t.Fatalf("a plan not started has a dispatch: %+v", p.Tasks[0].Dispatch)
	}
	if w := request(s, http.MethodPost, "/api/v1/task-plans/"+p.ID+"/start", ""); w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	waitFor(t, "the dispatch record", func() bool { return len(st.memStore.modelRoutings()) >= 2 })
	rows := st.memStore.modelRoutings()
	todo := onlyTodo(s)
	if len(rows) != 2 || len(todo.Agents) != 1 {
		t.Fatalf("rows %+v todo %+v", rows, todo)
	}
	r := rows[0]
	if !r.Active || r.ProfileID != "performance" || r.Tier != "dev" || r.Candidate != 0 || r.Of != 1 || r.Launcher != "codex" ||
		r.Model != "test-performance" || r.Effort != "high" || r.Reason != tierReasonOnly || r.TodoID != todo.ID || r.PlanID != p.ID ||
		r.SessionID != todo.Agents[0].SessionID || r.SessionID == "" {
		t.Fatalf("model_routing active row: %+v", r)
	}
	// Inactive profile economy (which has tier standard → dev) gets a trial row; empty (no tiers) writes nothing.
	tr := rows[1]
	if tr.Active || tr.ProfileID != "economy" || tr.Tier != "dev" || tr.Candidate != 0 || tr.Of != 1 || tr.Launcher != "claude" ||
		tr.Model != "test-economy" || tr.Effort != "low" || tr.Reason != tierReasonOnly || tr.TodoID != todo.ID || tr.PlanID != p.ID ||
		tr.SessionID != r.SessionID {
		t.Fatalf("model_routing trial row: %+v", tr)
	}
	waitFor(t, "the plan's dispatch", func() bool {
		current, err := s.loadPlan(context.Background(), p.ID)
		return err == nil && current.Tasks[0].Dispatch != nil
	})
	current, _ := s.loadPlan(context.Background(), p.ID)
	if d := current.Tasks[0].Dispatch; d.Tier != "dev" || d.Launcher != "codex" || d.Reason != "only" || current.Tasks[0].Tier != "standard" {
		t.Fatalf("plan dispatch: %+v (task tier %q)", d, current.Tasks[0].Tier)
	}
	if calls := profileNodeStarts(f); len(calls) != 1 {
		t.Fatalf("starts: %d", len(calls))
	}
	// A task_plan call cannot set the hub's dispatch.
	task := profilePlanTask()
	task.Dispatch = &TaskDispatch{Tier: "forged", Launcher: "x", Reason: "score"}
	res := profileMCPCall(t, s, "main", "task_plan", map[string]any{"tasks": []PlanTask{task}}, true, false)
	if !res.IsError && len(res.Content) > 0 && strings.Contains(res.Content[0].Text, "forged") {
		t.Fatalf("task_plan: %+v", res)
	}
	st.planMu.Lock()
	defer st.planMu.Unlock()
	for _, row := range st.plans {
		if strings.Contains(string(row.Tasks), "forged") {
			t.Fatalf("a tool call stored a dispatch: %s", row.Tasks)
		}
	}
}
