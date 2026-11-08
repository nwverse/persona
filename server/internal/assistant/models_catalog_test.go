package assistant

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestClosestEarlierGeneration(t *testing.T) {
	cases := []struct {
		id         string
		candidates []string
		want       string
	}{
		{"qwen4-30b", []string{"qwen3-235b", "qwen3-30b"}, "qwen3-30b"},
		{"qwen3.9-27b", []string{"qwen3.8-max", "qwen3.8-27b", "qwen3.7-27b", "qwen3.9-235b"}, "qwen3.8-27b"},
		{"qwen3.7-27b", []string{"qwen3.8-27b"}, ""},
		{"qwen3.9-235b", []string{"qwen3.8-27b", "qwen3-235b"}, "qwen3-235b"},
		{"qwen3.10-27b", []string{"qwen3.9-27b", "qwen3.8-27b"}, "qwen3.9-27b"},
		{"glm-5.10", []string{"glm-5.9", "glm-5.3", "glm-5.11"}, "glm-5.9"},
		{"grok-4.10", []string{"grok-4.9", "grok-4.11"}, "grok-4.9"},
		{"claude-sonnet-6", []string{"claude-sonnet-4-6", "claude-sonnet-5", "claude-opus-5"}, "claude-sonnet-5"},
		{"claude-opus-5-4", []string{"claude-opus-5-5", "claude-opus-5", "claude-opus-4-8"}, "claude-opus-5"},
		{"claude-opus-5-6", []string{"claude-opus-5-5", "claude-opus-5"}, "claude-opus-5-5"},
		{"private-70b", []string{"private-7b", "private-8b"}, ""},
		{"gpt-oss-120b", []string{"gpt-oss-20b"}, ""},
		{"mixtral-8x22b", []string{"mixtral-8x7b"}, ""},
		{"private-1.5b", []string{"private-0.5b"}, ""},
		{"private-700m", []string{"private-350m"}, ""},
		{"mistral-7b-instruct", []string{"mistral-7b"}, ""},
		{"claude-opus-5-6-20251109", []string{"claude-opus-5-5"}, "claude-opus-5-5"},
		{"qwen3.9-27b-20260101", []string{"qwen3.8-27b-20250101"}, "qwen3.8-27b-20250101"},
		{"anthropic/claude-opus-5-6", []string{"claude-opus-5-5"}, "claude-opus-5-5"},
		{"Anthropic/Claude-Opus-5-6[1m]", []string{"claude-opus-5-5"}, "claude-opus-5-5"},
		{"qwen/qwen3.9-27b-20260101", []string{"qwen3.8-27b"}, "qwen3.8-27b"},
		{"deepseek/deepseek-v5-pro", []string{"deepseek-v4-pro", "deepseek-v4-flash"}, "deepseek-v4-pro"},
	}
	for _, tc := range cases {
		var gens []catalogGeneration
		for _, id := range tc.candidates {
			key := catalogGenerationKey(id)
			g, ok := parseModelGeneration(key)
			if !ok {
				continue
			}
			gens = append(gens, catalogGeneration{model: proto.ProviderModel{ID: id}, gen: g})
		}
		got, ok := closestGeneration(catalogGenerationKey(tc.id), gens)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: inherited %s, want none", tc.id, got.model.ID)
			}
			continue
		}
		if !ok || got.model.ID != tc.want {
			t.Errorf("%s: got %q ok=%v, want %s", tc.id, got.model.ID, ok, tc.want)
		}
	}
}

func TestCatalogGenerationFallback(t *testing.T) {
	cases := []struct {
		id     string
		ok     bool
		from   string
		name   string
		window int
	}{
		{"claude-opus-5-5", true, "", "Opus 5.5", 1000000},
		{"claude-opus-5-6", true, "claude-opus-5-5", "", 1000000},
		{"claude-opus-6", true, "claude-opus-5-5", "", 1000000},
		{"claude-opus-5-4", true, "claude-opus-5", "", 1000000},
		{"claude-opus-4-4", false, "", "", 0},
		{"claude-sonnet-4-7", true, "claude-sonnet-4-6", "", 1000000},
		{"claude-haiku-4-6", true, "claude-haiku-4-5", "", 200000},
		{"claude-haiku-5-6", true, "claude-haiku-5-5", "", 1000000},
		{"glm-5.4", true, "glm-5.3", "", 1000000},
		{"glm-5.4-flash", true, "glm-5.3-flash", "", 1000000},
		{"glm-5.10", true, "glm-5.3", "", 1000000},
		{"glm-4", false, "", "", 0},
		{"qwen3.9-27b", true, "qwen3.8-27b", "", 991808},
		{"qwen3.10-27b", true, "qwen3.8-27b", "", 991808},
		{"qwen3.7-27b", false, "", "", 0},
		{"qwen3.9-235b", false, "", "", 0},
		{"qwen3.9-max", true, "qwen3.8-max", "", 983616},
		{"kimi-k2.8-code", true, "kimi-k2.7-code", "", 262144},
		{"seed-2.2-pro", true, "seed-2.1-pro", "", 256000},
		{"mimo-v2.7-pro", true, "mimo-v2.6-pro", "", 1000000},
		{"MiniMax-M2.8", true, "MiniMax-M2.7", "", 204800},
		{"MiniMax-M2.8-highspeed", true, "MiniMax-M2.7-highspeed", "", 204800},
		{"claude-opus-5-6-20251109", true, "claude-opus-5-5", "", 1000000},
		{"claude-opus-5-5-20260101", true, "", "Opus 5.5", 1000000},
		{"anthropic/claude-opus-5-6", true, "claude-opus-5-5", "", 1000000},
		{"Anthropic/Claude-Opus-5-6[1m]", true, "claude-opus-5-5", "", 1000000},
		{"qwen/qwen3.9-27b-20260101", true, "qwen3.8-27b", "", 991808},
		{"deepseek/deepseek-v5-pro", true, "deepseek-v4-pro", "", 1048576},
		{"nobody-knows", false, "", "", 0},
		{"private-70b", false, "", "", 0},
	}
	for _, tc := range cases {
		got, ok := catalogModel(tc.id)
		if ok != tc.ok || got.InheritedFrom != tc.from || got.Name != tc.name || (tc.ok && got.ContextWindow != tc.window) {
			t.Errorf("%s: got %+v ok=%v", tc.id, got, ok)
		}
		if tc.from != "" && got.Name != "" {
			t.Errorf("%s: inherited a name %q", tc.id, got.Name)
		}
	}
}

func TestCatalogExactEntriesDoNotInherit(t *testing.T) {
	for id, want := range modelCatalog().byID {
		for _, q := range []string{id, "vendor/" + id, strings.ToUpper(id), id + "[1m]", id + "-20260101"} {
			got, ok := catalogModel(q)
			if !ok || got.InheritedFrom != "" || got.ID != want.ID || got.Name != want.Name {
				t.Errorf("%s via %q: %+v ok=%v", id, q, got, ok)
			}
		}
	}
}

func TestEnrichModelGeneration(t *testing.T) {
	got := enrichModel(proto.ProviderModel{ID: "claude-opus-5-6"})
	if got.Name != "" || got.InheritedFrom != "claude-opus-5-5" || got.ContextWindow != 1000000 || got.DefaultEffort != "medium" || len(got.Efforts) != 5 {
		t.Fatalf("bare inherit: %+v", got)
	}
	got.Efforts[0] = "mutated"
	again := enrichModel(proto.ProviderModel{ID: "claude-opus-5-6"})
	exact := enrichModel(proto.ProviderModel{ID: "claude-opus-5-5"})
	if again.Efforts[0] == "mutated" || exact.Efforts[0] == "mutated" || exact.Name != "Opus 5.5" || exact.InheritedFrom != "" {
		t.Fatalf("aliased or exact changed: again=%+v exact=%+v", again, exact)
	}

	// What the gateway reported wins, including when that leaves nothing to inherit.
	full := enrichModel(proto.ProviderModel{
		ID: "glm-5.4", Name: "Gateway", ContextWindow: 111,
		Efforts: []string{"low"}, DefaultEffort: "low",
	})
	if full.Name != "Gateway" || full.ContextWindow != 111 || full.DefaultEffort != "low" || len(full.Efforts) != 1 || full.InheritedFrom != "" {
		t.Fatalf("gateway wins: %+v", full)
	}

	partial := enrichModel(proto.ProviderModel{ID: "glm-5.4", Name: "Gateway", ContextWindow: 111})
	if partial.Name != "Gateway" || partial.ContextWindow != 111 || partial.InheritedFrom != "glm-5.3" || partial.DefaultEffort != "high" || len(partial.Efforts) != 3 {
		t.Fatalf("partial: %+v", partial)
	}

	// The earlier generation's window wins over the [1m] marker; haiku-4-5 is 200K and haiku-5-5 is later.
	marked := enrichModel(proto.ProviderModel{ID: "claude-haiku-4-6[1m]"})
	if marked.ContextWindow != 200000 || marked.InheritedFrom != "claude-haiku-4-5" || marked.Name != "" {
		t.Fatalf("[1m] over inherited window: %+v", marked)
	}
	if m := enrichModel(proto.ProviderModel{ID: "own[1m]"}); m.ContextWindow != 1000000 || m.InheritedFrom != "" {
		t.Fatalf("unknown [1m]: %+v", m)
	}
}
