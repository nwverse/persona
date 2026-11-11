package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The alias every hide case imports from m1 ("Laptop"): its own gateway and key.
var hideAlias = zshrcAlias{Alias: "acc", Command: "claude", File: "~/.bashrc", Env: map[string]string{
	"ANTHROPIC_BASE_URL": "https://a.example.com", "ANTHROPIC_API_KEY": importKeyA, "ANTHROPIC_MODEL": "a1"}}

// hideTestHub is an import hub with four computers: the assistant's (server), m1 (Laptop, imported from), m2 (Studio)
// and the work computer w1 (Work PC); rc are their rc-file launchers.
func hideTestHub(t *testing.T, aliases []zshrcAlias, rc map[string][]proto.Launcher) (*Service, http.Handler) {
	t.Helper()
	s, f, _ := modelService(t)
	f.devices = []Device{{ID: "server", Name: "server", Online: true, Reachable: true}, {ID: "m1", Name: "Laptop", Online: true, Reachable: true},
		{ID: "m2", Name: "Studio", Online: true, Reachable: true}, {ID: "w1", Name: "Work PC", Online: true, Reachable: true}}
	for _, d := range f.devices {
		f.sessions[d.ID] = nil // a node answers there
	}
	f.hello.Assistant.WorkMachines = []string{"w1"}
	f.zshrc = rc
	old := zshrcAliases
	zshrcAliases = func(context.Context, *Service, string) ([]zshrcAlias, error) { return aliases, nil }
	t.Cleanup(func() { zshrcAliases = old })
	return s, s
}

// previewProviders is the preview's providers.
func previewProviders(t *testing.T, mux http.Handler) []importedProvider {
	t.Helper()
	r := importRequest(mux, "GET", "")
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var out struct {
		Providers []importedProvider `json:"providers"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || len(out.Providers) == 0 {
		t.Fatalf("%s: %v", r.Body.String(), err)
	}
	return out.Providers
}

// previewSources is the preview's sources of its first provider.
func previewSources(t *testing.T, mux http.Handler) []importSource {
	t.Helper()
	return previewProviders(t, mux)[0].Sources
}

func importAnswer(t *testing.T, mux http.Handler, body string) importResult {
	t.Helper()
	r := importRequest(mux, "POST", body)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var res importResult
	if err := json.Unmarshal(r.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func sealedKey(t *testing.T, s *Service, key string) string {
	t.Helper()
	sealed, err := s.sealKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// One question decides whether an imported alias is hidden: the preview's hide and the import's agree, and an alias
// stays when something uses it or a computer would not get the provider in its place.
func TestImportHideCheck(t *testing.T) {
	laptop := []proto.Launcher{{Name: "acc", BaseURL: "https://a.example.com", Model: "a1"}}
	lang := func(s *Service) i18n.Lang { return s.requestCtxLang(context.Background()) }
	cases := []struct {
		name  string
		rc    map[string][]proto.Launcher
		setup func(t *testing.T, s *Service)
		want  func(s *Service) importSource
	}{
		{
			name: "listed only where it comes from",
			rc:   map[string][]proto.Launcher{"m1": laptop},
			want: func(*Service) importSource { return importSource{Alias: "acc", Hide: "ok"} },
		},
		{
			name: "another computer reaches the same address",
			rc:   map[string][]proto.Launcher{"m1": laptop, "m2": {{Name: "acc", BaseURL: "https://a.example.com/v1/"}}},
			want: func(*Service) importSource { return importSource{Alias: "acc", Hide: "ok"} },
		},
		{
			name: "another computer reaches another address",
			rc:   map[string][]proto.Launcher{"m1": laptop, "m2": {{Name: "acc", BaseURL: "https://b.example.com"}}},
			want: func(*Service) importSource {
				return importSource{Alias: "acc", Hide: "elsewhere", Elsewhere: []string{"Studio"}}
			},
		},
		{
			name: "another computer's address is unknown",
			rc:   map[string][]proto.Launcher{"m1": laptop, "m2": {{Name: "acc"}}},
			want: func(*Service) importSource {
				return importSource{Alias: "acc", Hide: "elsewhere", Elsewhere: []string{"Studio"}}
			},
		},
		{
			name: "a work computer is out of the new provider's scope",
			rc:   map[string][]proto.Launcher{"m1": laptop, "w1": {{Name: "acc", BaseURL: "https://a.example.com"}}},
			want: func(*Service) importSource {
				return importSource{Alias: "acc", Hide: "elsewhere", Elsewhere: []string{"Work PC"}}
			},
		},
		{
			name: "the existing provider skips computers with a service of their own",
			rc:   map[string][]proto.Launcher{"m1": laptop},
			setup: func(t *testing.T, s *Service) {
				putLaunchers(t, s, storedLauncher{KeySealed: sealedKey(t, s, importKeyA), Share: shareAll, ManagedLauncher: proto.ManagedLauncher{
					Name: "gw-a", BaseURL: "https://a.example.com", Model: "a1", OwnService: proto.OwnServiceSkip}})
			},
			want: func(*Service) importSource {
				return importSource{Alias: "acc", Hide: "elsewhere", Elsewhere: []string{"Laptop"}}
			},
		},
		{
			name: "the existing provider is out of another computer's scope",
			rc:   map[string][]proto.Launcher{"m1": laptop, "m2": {{Name: "acc", BaseURL: "https://a.example.com"}}},
			setup: func(t *testing.T, s *Service) {
				putLaunchers(t, s, storedLauncher{KeySealed: sealedKey(t, s, importKeyA), Share: shareSelected, ScopeMachines: []string{"m1"},
					ManagedLauncher: proto.ManagedLauncher{Name: "gw-a", BaseURL: "https://a.example.com", Model: "a1", OwnService: proto.OwnServiceUse}})
			},
			want: func(*Service) importSource {
				return importSource{Alias: "acc", Hide: "elsewhere", Elsewhere: []string{"Studio"}}
			},
		},
		{
			name: "merged into an existing provider that stands in everywhere",
			rc:   map[string][]proto.Launcher{"m1": laptop},
			setup: func(t *testing.T, s *Service) {
				putLaunchers(t, s, storedLauncher{KeySealed: sealedKey(t, s, importKeyA), ManagedLauncher: proto.ManagedLauncher{
					Name: "gw-a", BaseURL: "https://a.example.com", Model: "a1"}})
			},
			want: func(*Service) importSource { return importSource{Alias: "acc", Hide: "ok"} },
		},
		{
			name: "a purpose uses it",
			rc:   map[string][]proto.Launcher{"m1": laptop},
			setup: func(t *testing.T, s *Service) {
				if err := s.saveModelRecord(context.Background(), "agent.new", 0, ModelBinding{Provider: "launcher.acc"}); err != nil {
					t.Fatal(err)
				}
			},
			want: func(s *Service) importSource {
				return importSource{Alias: "acc", Hide: "in_use", Uses: agentUses(t, s, "acc")}
			},
		},
		{
			name: "a scheduled task runs on it",
			rc:   map[string][]proto.Launcher{"m1": laptop},
			setup: func(t *testing.T, s *Service) {
				addScheduledTask(t, s, Task{ID: "t1", Title: "Nightly check", Prompt: "x", Repeat: repeatDaily, Time: "09:00", Enabled: true})
				if err := s.saveModelRecord(context.Background(), "task.t1", 0, ModelBinding{Provider: "launcher.acc"}); err != nil {
					t.Fatal(err)
				}
			},
			want: func(s *Service) importSource {
				return importSource{Alias: "acc", Hide: "in_use", Uses: []string{i18n.T(lang(s), "assistant-models_import.use_task", i18n.Args{"name": "Nightly check"})}}
			},
		},
		{
			name: "a profile not in use names it, and a profile's Agent preset runs on it",
			rc:   map[string][]proto.Launcher{"m1": laptop},
			setup: func(t *testing.T, s *Service) {
				putProfiles(t, s, ModelProfile{ID: "economy", Name: "Economy", Purposes: map[string]ModelBinding{"kb": {Provider: "launcher.acc"}}},
					ModelProfile{ID: "performance", Name: "Performance", Tiers: map[string]ModelTier{"fast": {Launcher: "acc"}, "slow": {Launcher: "other"}}})
			},
			want: func(s *Service) importSource {
				return importSource{Alias: "acc", Hide: "in_use", Uses: []string{
					i18n.T(lang(s), "assistant-models_import.use_profile", i18n.Args{"name": "Economy"}),
					i18n.T(lang(s), "assistant-models_import.use_tier", i18n.Args{"tier": "fast", "profile": "Performance"})}}
			},
		},
		{
			name: "in use wins over elsewhere",
			rc:   map[string][]proto.Launcher{"m1": laptop, "m2": {{Name: "acc", BaseURL: "https://b.example.com"}}},
			setup: func(t *testing.T, s *Service) {
				putProfiles(t, s, ModelProfile{ID: "performance", Name: "Performance", Tiers: map[string]ModelTier{"fast": {Launcher: "acc"}}})
			},
			want: func(s *Service) importSource {
				return importSource{Alias: "acc", Hide: "in_use", Uses: []string{
					i18n.T(lang(s), "assistant-models_import.use_tier", i18n.Args{"tier": "fast", "profile": "Performance"})}}
			},
		},
		{
			name: "Persona does not list it",
			rc:   map[string][]proto.Launcher{"m1": {{Name: "other", BaseURL: "https://a.example.com"}}},
			want: func(*Service) importSource { return importSource{Alias: "acc", Hide: "none"} },
		},
		{
			name: "already hidden",
			rc:   map[string][]proto.Launcher{"m1": laptop},
			setup: func(t *testing.T, s *Service) {
				if err := s.putSetting(context.Background(), launchersHiddenKey, []string{"acc"}); err != nil {
					t.Fatal(err)
				}
			},
			want: func(*Service) importSource { return importSource{Alias: "acc", Hide: "none"} },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, mux := hideTestHub(t, []zshrcAlias{hideAlias}, c.rc)
			if c.setup != nil {
				c.setup(t, s)
			}
			want := c.want(s)
			preview := want
			preview.File = "~/.bashrc"
			if got := previewSources(t, mux); !reflect.DeepEqual(got, []importSource{preview}) {
				t.Fatalf("preview %+v, want %+v", got, preview)
			}
			ctx := context.Background()
			before, hiddenBefore := importWatched(t, s), s.hiddenLaunchers(ctx)
			res := importAnswer(t, mux, `{"machine":"m1","providers":["acc"],"hide_aliases":true}`)
			wantHidden, wantKept := []string{}, []importSource{}
			switch want.Hide {
			case "ok":
				wantHidden = []string{"acc"}
			case "in_use", "elsewhere":
				wantKept = []importSource{want}
			}
			if !slices.Equal(res.Hidden, wantHidden) || !reflect.DeepEqual(res.Kept, wantKept) {
				t.Fatalf("hidden %v kept %+v, want %v %+v", res.Hidden, res.Kept, wantHidden, wantKept)
			}
			if hidden := s.hiddenLaunchers(ctx); !slices.Equal(hidden, append(hiddenBefore, wantHidden...)) {
				t.Fatalf("launchers.hidden %v", hidden)
			}
			if after := importWatched(t, s); after != before {
				t.Fatalf("the import changed what uses launchers:\n%s\n%s", before, after)
			}
		})
	}
}

// A body from before hide_aliases (iOS, Android, an older web) merges the group and hides nothing.
func TestImportWithoutHideHidesNothing(t *testing.T) {
	_, mux := hideTestHub(t, []zshrcAlias{hideAlias}, map[string][]proto.Launcher{"m1": {{Name: "acc", BaseURL: "https://a.example.com"}}})
	// The alias is listed, so its name is taken: the preview says the name the import really gives.
	if got := previewProviders(t, mux)[0]; got.Name != "acc" || got.CreateAs != "acc-2" || got.Sources[0].Hide != "ok" {
		t.Fatalf("%+v", got)
	}
	r := importRequest(mux, "POST", `{"machine":"m1","providers":["acc"]}`)
	if want := `{"created":["acc-2"],"updated":[],"hidden":[],"kept":[]}`; r.Code != 200 || strings.TrimSpace(r.Body.String()) != want {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
}

// Plain claude and the reserved names are never hidden: an alias named claude is imported, and stays listed.
func TestImportNeverHidesClaude(t *testing.T) {
	s, _ := hideTestHub(t, nil, map[string][]proto.Launcher{"m1": {{Name: "claude", BaseURL: "https://a.example.com"},
		{Name: "presets", BaseURL: "https://a.example.com", Source: "zshrc"}}})
	ctx := context.Background()
	f, err := s.importHideFacts(ctx, "m1", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{"claude"}, reservedLauncherNames...) {
		src, err := s.aliasHide(ctx, f, name, importedLauncher(importGroup{}, time.Time{}))
		if err != nil || src.Hide != "none" {
			t.Fatalf("%s: %+v %v", name, src, err)
		}
	}
}

// Aliases on one gateway and key that set more than the model differently: split imports each as its own provider
// with its own settings; a variant made from its alias before merges its model there; hiding goes per alias.
func TestImportSplitGroup(t *testing.T) {
	gw := func(alias string, env map[string]string, args ...string) zshrcAlias {
		e := map[string]string{"ANTHROPIC_BASE_URL": "https://gw.example.com", "ANTHROPIC_AUTH_TOKEN": importKeyA}
		for k, v := range env {
			e[k] = v
		}
		return zshrcAlias{Alias: alias, Env: e, Command: "claude", Args: args}
	}
	aliases := []zshrcAlias{
		gw("fast", map[string]string{"ANTHROPIC_MODEL": "m-fast", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "m-mini", "CLAUDE_CODE_EFFORT_LEVEL": "low",
			"DISABLE_TELEMETRY": "1"}, "--verbose"),
		gw("deep", map[string]string{"ANTHROPIC_MODEL": "m-deep[1m]", "ANTHROPIC_DEFAULT_OPUS_MODEL": "m-deep", "CLAUDE_CODE_EFFORT_LEVEL": "high",
			"API_TIMEOUT_MS": "900000", "ANTHROPIC_CUSTOM_MODEL_OPTION": "m-extra"}),
		gw("again", map[string]string{"ANTHROPIC_MODEL": "m-again"}),
	}
	rc := map[string][]proto.Launcher{"m1": {{Name: "fast", BaseURL: "https://gw.example.com"}, {Name: "deep", BaseURL: "https://gw.example.com"},
		{Name: "again", BaseURL: "https://gw.example.com"}}}
	s, mux := hideTestHub(t, aliases, rc)
	ctx := context.Background()
	// "again" was imported on another computer before: its variant merges there.
	putLaunchers(t, s, storedLauncher{KeySealed: sealedKey(t, s, importKeyA), ManagedLauncher: proto.ManagedLauncher{
		Name: "again-old", BaseURL: "https://gw.example.com", Model: "m-again", Source: "zshrc:again@m2", Models: []proto.ProviderModel{{ID: "m-again"}}}})
	if err := s.saveModelRecord(ctx, "agent.new", 0, ModelBinding{Provider: "launcher.deep"}); err != nil {
		t.Fatal(err)
	}

	r := importRequest(mux, "GET", "")
	var out struct {
		Providers []importedProvider `json:"providers"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || len(out.Providers) != 1 {
		t.Fatalf("%s", r.Body.String())
	}
	g := out.Providers[0]
	if g.Existing != "again-old" || !slices.Equal(g.Differs, []string{"tiers", "effort", "args", "limits", "env", "custom_model"}) || len(g.Variants) != 3 {
		t.Fatalf("group: existing %q differs %v variants %d", g.Existing, g.Differs, len(g.Variants))
	}
	if v := g.Variants; v[0].Existing != "" || v[1].Existing != "" || v[2].Existing != "again-old" ||
		v[1].Sources[0].Hide != "in_use" || v[0].Sources[0].Hide != "ok" || v[2].Sources[0].Hide != "ok" {
		t.Fatalf("variants: %+v", v)
	}
	// The names the import gives: the listed aliases' are taken; one that lands in a stored provider creates none.
	if v := g.Variants; g.CreateAs != "" || v[0].CreateAs != "fast-2" || v[1].CreateAs != "deep-2" || v[2].CreateAs != "" {
		t.Fatalf("create_as: %q %q %q %q", g.CreateAs, v[0].CreateAs, v[1].CreateAs, v[2].CreateAs)
	}

	res := importAnswer(t, mux, `{"machine":"m1","providers":["fast"],"split":["fast"],"hide_aliases":true}`)
	if !slices.Equal(res.Created, []string{"fast-2", "deep-2"}) || !slices.Equal(res.Updated, []string{"again-old"}) ||
		!slices.Equal(res.Hidden, []string{"fast", "again"}) ||
		!reflect.DeepEqual(res.Kept, []importSource{{Alias: "deep", Hide: "in_use", Uses: agentUses(t, s, "deep")}}) {
		t.Fatalf("%+v", res)
	}
	byName := map[string]storedLauncher{}
	for _, l := range s.storedLaunchers(ctx) {
		byName[l.Name] = l
	}
	fast, deep, again := byName["fast-2"], byName["deep-2"], byName["again-old"]
	if fast.Model != "m-fast" || fast.HaikuModel != "m-mini" || fast.Effort != "low" || !slices.Equal(fast.Args, []string{"--verbose"}) ||
		!reflect.DeepEqual(fast.Models, []proto.ProviderModel{{ID: "m-fast"}}) || fast.OwnService != proto.OwnServiceUse || fast.Source != "zshrc:fast@m1" {
		t.Fatalf("fast: %+v", fast)
	}
	if env, err := s.openKey(fast.EnvSealed); err != nil || env != "DISABLE_TELEMETRY=1" {
		t.Fatalf("fast env %q %v", env, err)
	}
	if deep.Model != "m-deep" || deep.OpusModel != "m-deep" || deep.Effort != "high" || deep.TimeoutMS != 900000 || deep.CustomModel != "m-extra" ||
		deep.Args != nil || deep.EnvSealed != "" || !reflect.DeepEqual(deep.Models, []proto.ProviderModel{{ID: "m-deep", ContextWindow: 1000000}}) {
		t.Fatalf("deep: %+v", deep)
	}
	if again.Model != "m-again" || again.Source != "zshrc:again@m2" || !reflect.DeepEqual(again.Models, []proto.ProviderModel{{ID: "m-again"}}) {
		t.Fatalf("again changed beyond its models: %+v", again)
	}
	if b, _ := s.modelRecord(ctx, "agent.new"); b.Binding.Provider != "launcher.deep" {
		t.Fatalf("a purpose moved: %+v", b)
	}
}

// create_as is the name the import gives, as the import counts what is taken: the launchers Persona lists on every
// computer, the stored providers, and the names given earlier in the same import (groups and variants in order).
func TestImportPreviewSaysTheNameItCreates(t *testing.T) {
	alias := func(name, base, key string) zshrcAlias {
		return zshrcAlias{Alias: name, Command: "claude", Env: map[string]string{"ANTHROPIC_BASE_URL": base, "ANTHROPIC_API_KEY": key, "ANTHROPIC_MODEL": "m"}}
	}
	aliases := []zshrcAlias{
		alias("a.cc", "https://one.example.com", importKeyA), // named a-cc, which another alias is
		alias("a-cc", "https://two.example.com", importKeyA),
		alias("fresh", "https://three.example.com", importKeyA), // listed nowhere under that name
		alias("kept", "https://four.example.com", importKeyB),   // its name is a stored provider's
	}
	rc := map[string][]proto.Launcher{"m1": {{Name: "a.cc"}, {Name: "a-cc"}}, "m2": {{Name: "kept"}}}
	s, mux := hideTestHub(t, aliases, rc)
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "fresh-old", BaseURL: "https://other.example.com", Model: "x"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "kept-2", BaseURL: "https://other.example.com", Model: "x"}})
	var names, createAs []string
	for _, p := range previewProviders(t, mux) {
		names, createAs = append(names, p.Name), append(createAs, p.CreateAs)
	}
	if !slices.Equal(names, []string{"a-cc", "a-cc", "fresh", "kept"}) || !slices.Equal(createAs, []string{"a-cc-2", "a-cc-3", "", "kept-3"}) {
		t.Fatalf("names %v create_as %v", names, createAs)
	}
	res := importAnswer(t, mux, `{"machine":"m1","providers":["fresh","kept"]}`)
	if !slices.Equal(res.Created, []string{"fresh", "kept-3"}) {
		t.Fatalf("%+v", res)
	}
}

// split names a group that is not imported: refused like an unknown name, nothing saved.
func TestImportSplitMustBeImported(t *testing.T) {
	s, mux := importTestHub(t, importAliases, nil)
	r := importRequest(mux, "POST", `{"machine":"m1","providers":["kcc"],"split":["gcc"]}`)
	if r.Code != 400 || !strings.Contains(r.Body.String(), "gcc") {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if list := s.storedLaunchers(context.Background()); len(list) != 0 {
		t.Fatalf("saved %+v", list)
	}
}

// The merged group keeps the first alias's settings; its aliases are each hidden when that is safe.
func TestImportMergedGroupHidesEachAlias(t *testing.T) {
	aliases := []zshrcAlias{
		{Alias: "one", Command: "claude", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://gw.example.com", "ANTHROPIC_API_KEY": importKeyA,
			"ANTHROPIC_MODEL": "m1", "CLAUDE_CODE_EFFORT_LEVEL": "low"}},
		{Alias: "two", Command: "claude", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://gw.example.com", "ANTHROPIC_API_KEY": importKeyA,
			"ANTHROPIC_MODEL": "m2", "CLAUDE_CODE_EFFORT_LEVEL": "high"}},
	}
	rc := map[string][]proto.Launcher{"m1": {{Name: "one", BaseURL: "https://gw.example.com"}, {Name: "two", BaseURL: "https://gw.example.com"}},
		"m2": {{Name: "two", BaseURL: "https://elsewhere.example.com"}}}
	s, mux := hideTestHub(t, aliases, rc)
	putLaunchers(t, s, storedLauncher{KeySealed: sealedKey(t, s, importKeyA), ManagedLauncher: proto.ManagedLauncher{
		Name: "gw", BaseURL: "https://gw.example.com", Model: "m1", Effort: "medium", Models: []proto.ProviderModel{{ID: "m1"}}}})
	res := importAnswer(t, mux, `{"machine":"m1","providers":["one"],"hide_aliases":true}`)
	if !slices.Equal(res.Updated, []string{"gw"}) || len(res.Created) != 0 || !slices.Equal(res.Hidden, []string{"one"}) ||
		!reflect.DeepEqual(res.Kept, []importSource{{Alias: "two", Hide: "elsewhere", Elsewhere: []string{"Studio"}}}) {
		t.Fatalf("%+v", res)
	}
	gw := s.storedLaunchers(context.Background())[0]
	if gw.Effort != "medium" || !reflect.DeepEqual(gw.Models, []proto.ProviderModel{{ID: "m1"}, {ID: "m2"}}) {
		t.Fatalf("%+v", gw)
	}
}

// agentUses are the purposes that follow New Agent's choice (alias), as Model configuration names them.
func agentUses(t *testing.T, s *Service, alias string) []string {
	t.Helper()
	ctx := context.Background()
	uses, err := s.providerModelUses(ctx, launcherProvider(alias))
	p, _ := s.modelPurposeDef(ctx, "agent.new")
	if err != nil || !slices.Contains(uses, p.Name) {
		t.Fatalf("%v %v", uses, err)
	}
	return uses
}

func addScheduledTask(t *testing.T, s *Service, task Task) {
	t.Helper()
	sc := s.loadSchedule(context.Background())
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if err := s.saveLocked(sc, append(sc.tasks, task)); err != nil {
		t.Fatal(err)
	}
}

func putProfiles(t *testing.T, s *Service, profiles ...ModelProfile) {
	t.Helper()
	if err := s.putSetting(context.Background(), modelProfilesKey, modelProfiles{Revision: 1, Profiles: profiles}); err != nil {
		t.Fatal(err)
	}
}

// importWatched is what an import must not change, as text: the purposes' records, the tasks' and the profiles.
func importWatched(t *testing.T, s *Service) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	for _, id := range append(slices.Clone(modelConfigIDs), "task.t1") {
		r, err := s.modelRecord(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(r)
		b.WriteString(id + " " + string(raw) + "\n")
	}
	ps, _, err := s.readModelProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ps)
	b.Write(raw)
	return b.String()
}
