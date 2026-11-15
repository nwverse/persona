package node

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// unreportedProviders: providers whose models report none of #8778's new fields, with what providerEnv and
// envClearFor gave them before #8798 (golden, from origin/main at c1bd826ff).
func unreportedProviders() []struct {
	l     proto.ManagedLauncher
	model string
	main  bool
} {
	const gw = "https://gw.example.com"
	models := []proto.ProviderModel{
		{ID: "big", ContextWindow: 1000000, Efforts: []string{"low", "high"}, DefaultEffort: "high"},
		{ID: "small", ContextWindow: 128000, DefaultEffort: "low"},
		{ID: "std", ContextWindow: 200000},
	}
	return []struct {
		l     proto.ManagedLauncher
		model string
		main  bool
	}{
		{proto.ManagedLauncher{Name: "p", Kind: "api", BaseURL: gw, APIKey: "sk-test-k", Model: "big", Models: models}, "", false},
		{proto.ManagedLauncher{Name: "p", Kind: "api", BaseURL: gw, APIKey: "sk-test-k", Model: "big", Models: models}, "small", true},
		{proto.ManagedLauncher{Name: "p", BaseURL: gw, APIKey: "sk-test-k", Auth: "bearer", Model: "std", Models: models,
			OpusModel: "small", SubagentModel: "std", SmallModel: "small", CustomModel: "c1", Effort: "high",
			Env: "FOO=1\nCLAUDE_CODE_EFFORT_LEVEL=max", TimeoutMS: 60000, Proxy: "http://proxy.example.com:8080"}, "", false},
		{proto.ManagedLauncher{Name: "p", BaseURL: gw, Model: "claude-opus-5-5", Models: []proto.ProviderModel{{ID: "claude-opus-5-5"}}}, "", true},
		{proto.ManagedLauncher{Name: "p", Kind: "api", BaseURL: gw, APIKey: "sk-test-k", Model: "other", NoProxy: "localhost"}, "", false},
	}
}

// A provider whose models report no output limit and no "no thinking level" launches exactly as before #8798.
func TestProviderEnvUnchangedWithoutReportedLimits(t *testing.T) {
	golden := []struct{ env, clear []string }{
		{
			env:   []string{"ANTHROPIC_BASE_URL=https://gw.example.com", "ANTHROPIC_API_KEY=sk-test-k", "ANTHROPIC_AUTH_TOKEN=", "ANTHROPIC_MODEL=big[1m]", "ANTHROPIC_DEFAULT_FABLE_MODEL=big[1m]", "ANTHROPIC_DEFAULT_OPUS_MODEL=big[1m]", "ANTHROPIC_DEFAULT_SONNET_MODEL=big[1m]", "CLAUDE_CODE_SUBAGENT_MODEL=big[1m]", "ANTHROPIC_DEFAULT_HAIKU_MODEL=big[1m]", "ANTHROPIC_SMALL_FAST_MODEL=big[1m]", "CLAUDE_CODE_DISABLE_ARTIFACT=1", "API_TIMEOUT_MS=3000000", "CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_MAX_CONTEXT_TOKENS=1000000"},
			clear: []string{"ANTHROPIC_*", "CLAUDE_CODE_USE_BEDROCK*", "CLAUDE_CODE_USE_VERTEX*", "CLAUDE_CODE_USE_FOUNDRY*", "CLAUDE_CODE_OAUTH_TOKEN*", "CLAUDE_CODE_SUBAGENT_MODEL*", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "API_TIMEOUT_MS"},
		},
		{
			env:   []string{"ANTHROPIC_BASE_URL=https://gw.example.com", "ANTHROPIC_API_KEY=sk-test-k", "ANTHROPIC_AUTH_TOKEN=", "ANTHROPIC_MODEL=small", "ANTHROPIC_DEFAULT_FABLE_MODEL=small", "ANTHROPIC_DEFAULT_OPUS_MODEL=small", "ANTHROPIC_DEFAULT_SONNET_MODEL=small", "CLAUDE_CODE_SUBAGENT_MODEL=small", "ANTHROPIC_DEFAULT_HAIKU_MODEL=small", "ANTHROPIC_SMALL_FAST_MODEL=small", "CLAUDE_CODE_DISABLE_ARTIFACT=1", "API_TIMEOUT_MS=3000000", "CLAUDE_CODE_AUTO_COMPACT_WINDOW=128000", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"},
			clear: []string{"ANTHROPIC_*", "CLAUDE_CODE_USE_BEDROCK*", "CLAUDE_CODE_USE_VERTEX*", "CLAUDE_CODE_USE_FOUNDRY*", "CLAUDE_CODE_OAUTH_TOKEN*", "CLAUDE_CODE_SUBAGENT_MODEL*", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "API_TIMEOUT_MS"},
		},
		{
			env:   []string{"ANTHROPIC_BASE_URL=https://gw.example.com", "ANTHROPIC_AUTH_TOKEN=sk-test-k", "ANTHROPIC_API_KEY=", "ANTHROPIC_MODEL=std", "ANTHROPIC_DEFAULT_FABLE_MODEL=std", "ANTHROPIC_DEFAULT_OPUS_MODEL=small", "ANTHROPIC_DEFAULT_SONNET_MODEL=std", "CLAUDE_CODE_SUBAGENT_MODEL=std", "ANTHROPIC_CUSTOM_MODEL_OPTION=c1", "ANTHROPIC_DEFAULT_HAIKU_MODEL=small", "ANTHROPIC_SMALL_FAST_MODEL=small", "CLAUDE_CODE_DISABLE_ARTIFACT=1", "API_TIMEOUT_MS=60000", "CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_EFFORT_LEVEL=max", "FOO=1", "HTTPS_PROXY=http://proxy.example.com:8080", "HTTP_PROXY=http://proxy.example.com:8080", "NO_PROXY=", "http_proxy=http://proxy.example.com:8080", "https_proxy=http://proxy.example.com:8080", "no_proxy="},
			clear: []string{"ANTHROPIC_*", "CLAUDE_CODE_USE_BEDROCK*", "CLAUDE_CODE_USE_VERTEX*", "CLAUDE_CODE_USE_FOUNDRY*", "CLAUDE_CODE_OAUTH_TOKEN*", "CLAUDE_CODE_SUBAGENT_MODEL*", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "API_TIMEOUT_MS", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "FOO"},
		},
		{
			env:   []string{"ANTHROPIC_BASE_URL=https://gw.example.com", "ANTHROPIC_MODEL=claude-opus-5-5", "ANTHROPIC_DEFAULT_FABLE_MODEL=claude-opus-5-5", "ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-5-5", "ANTHROPIC_DEFAULT_SONNET_MODEL=claude-opus-5-5", "CLAUDE_CODE_SUBAGENT_MODEL=claude-opus-5-5", "ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-opus-5-5", "ANTHROPIC_SMALL_FAST_MODEL=claude-opus-5-5", "CLAUDE_CODE_DISABLE_ARTIFACT=1", "API_TIMEOUT_MS=3000000", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"},
			clear: []string{"ANTHROPIC_*", "CLAUDE_CODE_USE_BEDROCK*", "CLAUDE_CODE_USE_VERTEX*", "CLAUDE_CODE_USE_FOUNDRY*", "CLAUDE_CODE_OAUTH_TOKEN*", "CLAUDE_CODE_SUBAGENT_MODEL*", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "API_TIMEOUT_MS"},
		},
		{
			env:   []string{"ANTHROPIC_BASE_URL=https://gw.example.com", "ANTHROPIC_API_KEY=sk-test-k", "ANTHROPIC_AUTH_TOKEN=", "ANTHROPIC_MODEL=other", "ANTHROPIC_DEFAULT_FABLE_MODEL=other", "ANTHROPIC_DEFAULT_OPUS_MODEL=other", "ANTHROPIC_DEFAULT_SONNET_MODEL=other", "CLAUDE_CODE_SUBAGENT_MODEL=other", "ANTHROPIC_DEFAULT_HAIKU_MODEL=other", "ANTHROPIC_SMALL_FAST_MODEL=other", "CLAUDE_CODE_DISABLE_ARTIFACT=1", "API_TIMEOUT_MS=3000000", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NO_PROXY=localhost", "no_proxy=localhost"},
			clear: []string{"ANTHROPIC_*", "CLAUDE_CODE_USE_BEDROCK*", "CLAUDE_CODE_USE_VERTEX*", "CLAUDE_CODE_USE_FOUNDRY*", "CLAUDE_CODE_OAUTH_TOKEN*", "CLAUDE_CODE_SUBAGENT_MODEL*", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "API_TIMEOUT_MS", "NO_PROXY", "no_proxy"},
		},
	}
	for i, f := range unreportedProviders() {
		if got := envList(providerEnv(f.l, f.model, "", f.main)); !slices.Equal(got, golden[i].env) {
			t.Errorf("%d env:\n got  %q\n want %q", i, got, golden[i].env)
		}
		if got := envClearFor(f.l); !slices.Equal(got, golden[i].clear) {
			t.Errorf("%d clear:\n got  %q\n want %q", i, got, golden[i].clear)
		}
		if got, _ := managedNode(f.l).launcherEnv(f.l.Name)["no_effort"].([]string); got != nil {
			t.Errorf("%d: launcher.env no_effort %q", i, got)
		}
	}
}

func managedNode(l proto.ManagedLauncher) *Node {
	n := &Node{}
	n.setManaged([]proto.ManagedLauncher{l})
	return n
}

// CLAUDE_CODE_MAX_OUTPUT_TOKENS (#8798): the least reported limit of the models the process runs that Claude Code
// does not know, over its 32000 and capped at 128000, for an Agent and the main conversation alike; the provider's
// own env wins.
func TestProviderEnvMaxOutputTokens(t *testing.T) {
	models := []proto.ProviderModel{
		{ID: "wide", ContextWindow: 1000000, MaxOutputTokens: 128000},
		{ID: "mid", MaxOutputTokens: 64000},
		{ID: "huge", MaxOutputTokens: 300000},
		{ID: "short", MaxOutputTokens: 16000},
		{ID: "edge", MaxOutputTokens: 32000},
		{ID: "plain"},
		{ID: "claude-opus-5-5", MaxOutputTokens: 64000},
		{ID: "anthropic/claude-haiku-4-5", MaxOutputTokens: 64000},
		{ID: "mixed-claude-distill", MaxOutputTokens: 16000},
	}
	base := proto.ManagedLauncher{Name: "p", BaseURL: "https://gw.example.com", APIKey: "sk-test-k", Model: "wide",
		Models: models}
	with := func(f func(*proto.ManagedLauncher)) proto.ManagedLauncher {
		l := base
		f(&l)
		return l
	}
	cases := []struct {
		name  string
		l     proto.ManagedLauncher
		model string
		want  string // "-" is unset
	}{
		{"reported", base, "", "128000"},
		{"marked [1m], still found", base, "wide[1m]", "128000"},
		{"started on another model: the tiers follow it", base, "mid", "64000"},
		{"over the cap", base, "huge", "128000"},
		{"no more than Claude Code asks anyway", base, "edge", "-"},
		{"under it", base, "short", "-"},
		{"not reported", base, "plain", "-"},
		{"not listed", base, "other", "-"},
		{"a Claude model knows its own", base, "claude-opus-5-5", "-"},
		{"a gateway's spelling of one too", base, "anthropic/claude-haiku-4-5", "-"},
		{"a tier with less", with(func(l *proto.ManagedLauncher) { l.OpusModel = "mid" }), "", "64000"},
		{"a tier with none reported", with(func(l *proto.ManagedLauncher) { l.SonnetModel = "plain" }), "", "-"},
		{"a tier under 32000", with(func(l *proto.ManagedLauncher) { l.HaikuModel = "short" }), "", "-"},
		{"a Claude tier with no reported limit is Claude Code's", with(func(l *proto.ManagedLauncher) { l.FableModel = "claude-sonnet-5-5" }), "", "128000"},
		{"a Claude-looking tier's reported limit counts", with(func(l *proto.ManagedLauncher) { l.FableModel = "claude-opus-5-5" }), "", "64000"},
		{"so does a gateway model that only looks like one", with(func(l *proto.ManagedLauncher) { l.HaikuModel = "mixed-claude-distill" }), "", "-"},
		{"the small model", with(func(l *proto.ManagedLauncher) { l.SmallModel = "mid" }), "", "64000"},
		{"the sub-Agent model", with(func(l *proto.ManagedLauncher) { l.SubagentModel = "plain" }), "", "-"},
		{"the /model extra entry", with(func(l *proto.ManagedLauncher) { l.CustomModel = "mid" }), "", "64000"},
		{"a model the provider's env names", with(func(l *proto.ManagedLauncher) { l.Env = "ANTHROPIC_DEFAULT_OPUS_MODEL=mid" }), "", "64000"},
		{"a tier alias names no model of its own", with(func(l *proto.ManagedLauncher) { l.Env = "CLAUDE_CODE_SUBAGENT_MODEL=haiku" }), "", "128000"},
		{"the provider's env wins", with(func(l *proto.ManagedLauncher) { l.Env = "CLAUDE_CODE_MAX_OUTPUT_TOKENS=50000" }), "", "50000"},
		{"the provider's env sets it empty", with(func(l *proto.ManagedLauncher) { l.Env = "CLAUDE_CODE_MAX_OUTPUT_TOKENS=" }), "", ""},
		{"the provider's env alone", with(func(l *proto.ManagedLauncher) {
			l.Models = []proto.ProviderModel{{ID: "wide"}}
			l.Env = "CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000"
		}), "", "64000"},
	}
	for _, c := range cases {
		for _, main := range []bool{false, true} {
			got, set := providerEnv(c.l, c.model, "", main)[maxOutputEnv]
			if c.want == "-" && set || c.want != "-" && (!set || got != c.want) {
				t.Errorf("%s (main %v): %s=%q (set %v), want %q", c.name, main, maxOutputEnv, got, set, c.want)
			}
		}
	}
	// `personad launch` sets it for the session's model (sessionOutput), so the CLI's settings file leaves it to the
	// process env; one the provider's env sets is pinned like the rest of that env. The Agent SDK bridge, given the
	// conversation's model, pins its own.
	if _, pinned := managedSettingsEnv(base)[maxOutputEnv]; pinned || !slices.Contains(managedEnv(base), maxOutputEnv+"=128000") {
		t.Errorf("settings / launch env: %v / %q", managedSettingsEnv(base), managedEnv(base))
	}
	if own := with(func(l *proto.ManagedLauncher) { l.Env = "CLAUDE_CODE_MAX_OUTPUT_TOKENS=50000" }); managedSettingsEnv(own)[maxOutputEnv] != "50000" {
		t.Errorf("provider env not pinned: %v", managedSettingsEnv(own))
	}
	if pinnedEnv(providerEnv(base, "mid", "", true))[maxOutputEnv] != "64000" {
		t.Error("SDK bridge env")
	}
	// Reporting a limit adds this one variable and changes nothing else.
	plain := with(func(l *proto.ManagedLauncher) {
		l.Models = slices.Clone(models)
		for i := range l.Models {
			l.Models[i].MaxOutputTokens = 0
		}
	})
	a, b := providerEnv(base, "", "", false), providerEnv(plain, "", "", false)
	delete(a, maxOutputEnv)
	if !maps.Equal(a, b) {
		t.Errorf("reported vs not:\n %v\n %v", a, b)
	}
}

// `personad launch` drops the shell's CLAUDE_CODE_MAX_OUTPUT_TOKENS only when the provider sets one: a value exported
// for the computer's own gateway still reaches a provider that reports no limit, as before.
func TestEnvClearForMaxOutputTokens(t *testing.T) {
	shell := []string{"CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000", "PATH=/bin"}
	reported := proto.ManagedLauncher{Name: "p", Kind: "api", BaseURL: "https://gw.example.com", Model: "r",
		Models: []proto.ProviderModel{{ID: "r", MaxOutputTokens: 100000}}}
	if got := withoutEnv(shell, envClearFor(reported)); !slices.Equal(got, []string{"PATH=/bin"}) {
		t.Errorf("reported: %q", got)
	}
	if c, _ := managedNode(reported).launcherEnv("p")["clear"].([]string); !slices.Contains(c, maxOutputEnv) {
		t.Errorf("launcher.env clear: %q", c)
	}
	unreported := reported
	unreported.Models = []proto.ProviderModel{{ID: "r"}}
	if got := withoutEnv(shell, envClearFor(unreported)); !slices.Equal(got, shell) {
		t.Errorf("not reported: %q", got)
	}
	if got := envClearFor(unreported); !slices.Equal(got, providerEnvClear) {
		t.Errorf("not reported clear: %q", got)
	}
}

// The provider's default level is not passed as --effort for a session model the gateway says takes none (#8798).
func TestLaunchEffortSkipsModelsWithoutLevels(t *testing.T) {
	none := []string{"flat", "flat2"}
	env := []string{"ANTHROPIC_MODEL=deep", "ANTHROPIC_DEFAULT_OPUS_MODEL=flat2", "ANTHROPIC_DEFAULT_SONNET_MODEL=deep"}
	cases := []struct {
		name               string
		env                []string
		args, launcherArgs []string
		want               string
	}{
		{"default model takes some", env, nil, nil, "high"},
		{"default model takes none", []string{"ANTHROPIC_MODEL=flat"}, nil, nil, ""},
		{"the provider's env names the model", []string{"ANTHROPIC_MODEL=flat[1m]"}, nil, nil, ""},
		{"started on one that takes none", env, []string{"--session-id", "x", "--model", "flat"}, nil, ""},
		{"started on one that takes some", []string{"ANTHROPIC_MODEL=flat"}, []string{"--model", "deep"}, nil, "high"},
		{"marked [1m]", env, []string{"--model=flat[1m]"}, nil, ""},
		{"the last --model counts", env, []string{"--model", "deep", "--model", "flat"}, nil, ""},
		{"a tier alias is its tier's model", env, []string{"--model", "opus"}, nil, ""},
		{"another tier alias", []string{"ANTHROPIC_MODEL=flat", "ANTHROPIC_DEFAULT_SONNET_MODEL=deep"}, []string{"--model", "sonnet"}, nil, "high"},
		{"the launcher's own --model", env, nil, []string{"--model", "flat"}, ""},
		{"the caller's wins over the launcher's", env, []string{"--model", "deep"}, []string{"--model", "flat"}, "high"},
		{"a prompt after -- is no model", env, []string{"--", "--model", "flat"}, nil, "high"},
	}
	for _, c := range cases {
		if got := launchEffort("high", none, sessionModel(c.args, c.launcherArgs, c.env)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if launchEffort("high", nil, "flat") != "high" || launchEffort("", none, "flat") != "" {
		t.Error("an older node lists none / no default level")
	}
	// launcher.env names the models; argv then carries no --effort for them.
	l := proto.ManagedLauncher{Name: "p", Kind: "api", BaseURL: "https://gw.example.com", Model: "flat", Effort: "high",
		Models: []proto.ProviderModel{{ID: "flat", NoEffort: true}, {ID: "deep", Efforts: []string{"low", "high"}}}}
	out := managedNode(l).launcherEnv("p")
	ids, _ := out["no_effort"].([]string)
	if out["effort"] != "high" || !slices.Equal(ids, []string{"flat"}) {
		t.Fatalf("launcher.env: effort %v, no_effort %v", out["effort"], out["no_effort"])
	}
	launchEnv, _ := out["env"].([]string)
	for _, c := range []struct {
		args []string
		want bool
	}{{nil, false}, {[]string{"--model", "deep"}, true}} {
		argv := launchArgv("claude", c.args, nil, launchEffort("high", ids, sessionModel(c.args, nil, launchEnv)), "flat", nil, "")
		if got := slices.Contains(argv, "--effort"); got != c.want {
			t.Errorf("argv %q: --effort %v, want %v", argv, got, c.want)
		}
	}
	// A provider with no such model sends no list, as before.
	l.Models[0].NoEffort = false
	if _, sent := managedNode(l).launcherEnv("p")["no_effort"]; sent {
		t.Error("no_effort sent for a provider without such models")
	}
}

// `personad launch` works the reply limit out again for the model the start names (#8798): launcher.env's value is the
// provider's model's, and a --model beside it runs with the provider's tiers.
func TestSessionOutputFollowsTheStartsModel(t *testing.T) {
	l := proto.ManagedLauncher{Name: "p", Kind: "api", BaseURL: "https://gw.example.com", Model: "wide",
		Models: []proto.ProviderModel{{ID: "wide", ContextWindow: 1000000, MaxOutputTokens: 128000},
			{ID: "mid", MaxOutputTokens: 40000}, {ID: "short", MaxOutputTokens: 16000}, {ID: "plain"}}, OpusModel: "mid"}
	out := managedNode(l).launcherEnv("p")
	env, _ := out["env"].([]string)
	clear, _ := out["clear"].([]string)
	limits, _ := out["max_output_tokens"].(map[string]int)
	if envValue(env, maxOutputEnv) != "40000" || !slices.Contains(clear, maxOutputEnv) ||
		!maps.Equal(limits, map[string]int{"wide": 128000, "mid": 40000, "short": 16000}) {
		t.Fatalf("launcher.env: %q / %q / %v", env, clear, limits)
	}
	shell := []string{"CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000", "PATH=/bin"}
	for _, c := range []struct {
		name string
		args []string
		want string // "" unset, the shell's then kept
	}{
		{"the provider's model", nil, "40000"},
		{"marked", []string{"--model", "wide[1m]"}, "40000"},
		{"a model with less", []string{"--model", "short"}, ""},
		{"a model reporting none", []string{"--model", "plain"}, ""},
		{"a model the provider does not list", []string{"--model", "other"}, ""},
		{"a Claude model", []string{"--model", "claude-opus-5-5"}, ""},
		{"the Opus tier", []string{"--model", "opus"}, "40000"},
	} {
		e, cl := sessionOutput(env, clear, limits, sessionModel(c.args, nil, env))
		got := envValue(e, maxOutputEnv)
		if got != c.want || slices.Contains(cl, maxOutputEnv) != (c.want != "") {
			t.Errorf("%s: %s=%q, clear %v", c.name, maxOutputEnv, got, slices.Contains(cl, maxOutputEnv))
		}
		if c.want == "" && !slices.Equal(withoutEnv(shell, cl), shell) {
			t.Errorf("%s: the shell's value dropped", c.name)
		}
		if n := strings.Count(strings.Join(e, "\n"), maxOutputEnv+"="); n > 1 {
			t.Errorf("%s: %d values", c.name, n)
		}
	}
	// An older node sends no limits, and a provider whose env sets the value sends none: the env stands.
	if e, cl := sessionOutput(env, clear, nil, "short"); !slices.Equal(e, env) || !slices.Equal(cl, clear) {
		t.Errorf("no limits: %q %q", e, cl)
	}
	own := l
	own.Env = "CLAUDE_CODE_MAX_OUTPUT_TOKENS=50000"
	if _, sent := managedNode(own).launcherEnv("p")["max_output_tokens"]; sent {
		t.Error("limits sent though the provider's env sets the value")
	}
	// Codex providers get none.
	codex := l
	codex.Agent = "codex"
	codex.Kind = ""
	if _, sent := managedNode(codex).launcherEnv("p")["max_output_tokens"]; sent {
		t.Error("limits sent for a Codex provider")
	}
}
