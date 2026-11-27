package compat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestInstalledServiceAndProbeBytesStayStable(t *testing.T) {
	for _, test := range []struct{ name, got, want string }{
		{"node label", NodeLabel, "com.jarvis.node"},
		{"port probe", PortProbeAgent, "jarvis-ports"},
		{"resident node matcher", ResidentNodePattern, "([j]arvisd|[p]ersonad|[p]ersona-node) node --attach"},
	} {
		if test.got != test.want {
			t.Fatalf("%s changed: got %q, want %q", test.name, test.got, test.want)
		}
	}
}

func TestExactRuntimeAliasesPreserveCustomNames(t *testing.T) {
	for _, pair := range [][2]string{{"jarvis-dsh-runtime", "persona-dsh-runtime"}, {"jarvis-pi-runtime", "persona-pi-runtime"}, {"jarvis-paseo-runtime", "persona-paseo-runtime"}} {
		if RuntimePackage(pair[0]) != pair[1] || LegacyRuntimePackage(pair[1]) != pair[0] {
			t.Fatal(pair)
		}
	}
	for _, name := range []string{"jarvis-user-package", "persona-custom-package", "custom"} {
		if RuntimePackage(name) != name || LegacyRuntimePackage(name) != name {
			t.Fatal("custom package was rewritten", name)
		}
	}
}
func TestHeaderAliasesAndConflicts(t *testing.T) {
	h := http.Header{"X-Jarvis-Op-Id": {"11111111-2222-4333-8444-555555555555"}}
	if Header(h, "X-Persona-Op-Id") == "" {
		t.Fatal("baseline header ignored")
	}
	SetHeader(h, "X-Persona-Op-Id", "same")
	if h.Get("X-Jarvis-Op-Id") != "same" || HeaderConflict(h, "X-Persona-Op-Id") {
		t.Fatal(h)
	}
	h.Set("X-Jarvis-Op-Id", "different")
	if !HeaderConflict(h, "X-Persona-Op-Id") {
		t.Fatal("conflicting receipt accepted")
	}
	DelHeader(h, "X-Persona-Op-Id")
	if h.Get("X-Jarvis-Op-Id") != "" || h.Get("X-Persona-Op-Id") != "" {
		t.Fatal("alias survived sanitization")
	}
}
func TestOnlyExactContextTagsAreProjected(t *testing.T) {
	in := `<persona-context kind="context">Persona project</persona-context><persona-file path="x"/><persona-contextual>Keep</persona-contextual>`
	expected := `<jarvis-context kind="context">Persona project</jarvis-context><jarvis-file path="x"/><persona-contextual>Keep</persona-contextual>`
	if out := LegacyContextTags(in); out != expected {
		t.Fatal(out)
	}
}
func TestProjectionIsScopedAndPreservesOpaqueValues(t *testing.T) {
	focus := []byte(`{"type":"herdr_focus","source":"persona","id":"persona-circuit","mine":true}`)
	if string(ProjectJSON(focus, false)) != string(focus) {
		t.Fatal("stable focus identity was rewritten")
	}
	raw := []byte(`{"type":"items","large":9007199254740993,"items":[{"text":"<persona-context>fixture</persona-context>","title":"Jarvis project","id":"persona-visor"}]}`)
	var out map[string]any
	d := json.NewDecoder(bytes.NewReader(ProjectJSON(raw, false)))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["large"] != json.Number("9007199254740993") {
		t.Fatal("opaque number was rounded", out["large"])
	}
	item := out["items"].([]any)[0].(map[string]any)
	if item["text"] != `<jarvis-context>fixture</jarvis-context>` || item["title"] != "Jarvis project" || item["id"] != "persona-visor" {
		t.Fatal(item)
	}
	if string(ProjectJSON(raw, true)) != string(raw) {
		t.Fatal("opt-in peer lost canonical bytes")
	}
}
func TestMCPNamespacesShareOnlyTheBuiltinHandler(t *testing.T) {
	env := map[string]string{"PERSONA_SESSION_ID": "same", "JARVIS_SESSION_ID": "stale", "PERSONA_GUEST": "", "JARVIS_GUEST": "1"}
	shared := map[string]any{"command": "fixture", "env": env}
	servers := AddMCPAliases(map[string]any{"persona": shared, "extension": map[string]any{"command": "other"}})
	if len(servers) != 3 || !reflect.DeepEqual(servers["jarvis"], servers["persona"]) {
		t.Fatal(servers)
	}
	mirrored := shared["env"].(map[string]string)
	if mirrored["JARVIS_SESSION_ID"] != "same" || mirrored["JARVIS_GUEST"] != "" {
		t.Fatal("identity or empty-value precedence was lost")
	}
	expected := []string{"mcp__persona__lark_agent_reply", "mcp__jarvis__lark_agent_reply"}
	if !reflect.DeepEqual(MCPToolNames([]string{"lark_agent_reply"}), expected) {
		t.Fatal("guest permission scope expanded")
	}
}

func TestMCPRegistrationPolicy(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "persona"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			env := map[string]string{"PERSONA_SESSION_ID": "same", "JARVIS_SESSION_ID": "stale", "PERSONA_GUEST": "", "JARVIS_GUEST": "1"}
			builtin := map[string]any{"command": "fixture", "env": env}
			extension := map[string]any{"command": "other", "env": map[string]string{"PERSONA_GUEST": "extension"}}
			servers := MCPRegistrationServers(map[string]any{"persona": builtin, "extension": extension}, legacy)
			wantCount := 2
			if legacy {
				wantCount++
			}
			if len(servers) != wantCount || !reflect.DeepEqual(servers["persona"], builtin) || !reflect.DeepEqual(servers["extension"], extension) {
				t.Fatalf("unexpected MCP servers: %v", servers)
			}
			if alias, ok := servers[MCPServer]; ok != legacy || legacy && !reflect.DeepEqual(alias, builtin) {
				t.Fatalf("legacy handler = %v (%v), want enabled=%v", alias, ok, legacy)
			}
			mirrored := builtin["env"].(map[string]string)
			if mirrored["JARVIS_SESSION_ID"] != "same" || mirrored["JARVIS_GUEST"] != "" || env["JARVIS_SESSION_ID"] != "stale" {
				t.Fatalf("process environment precedence or input changed: %v / %v", mirrored, env)
			}
			if len(extension["env"].(map[string]string)) != 1 {
				t.Fatal("extension environment was aliased")
			}
			wantTools := []string{"mcp__persona__lark_agent_reply", "mcp__persona__ask_user"}
			if legacy {
				wantTools = []string{"mcp__persona__lark_agent_reply", "mcp__jarvis__lark_agent_reply", "mcp__persona__ask_user", "mcp__jarvis__ask_user"}
			}
			if got := MCPAllowedToolNames([]string{"lark_agent_reply", "ask_user"}, legacy); !reflect.DeepEqual(got, wantTools) {
				t.Fatalf("allowed tools = %v, want %v", got, wantTools)
			}
			if got := MCPToolNames([]string{"ask_user"}); !reflect.DeepEqual(got, []string{"mcp__persona__ask_user", "mcp__jarvis__ask_user"}) {
				t.Fatalf("historical tool recognition narrowed: %v", got)
			}
		})
	}
}

func TestLegacyContextTagsPreservesBodiesAndProse(t *testing.T) {
	in := "<persona-context kind=\"quote\" title=\"Persona\" url=\"\" msg=\"m1\">\n正文含 <persona-context> 标签\n</persona-context>\n\n请问 <persona-context> 为什么裸露"
	want := "<jarvis-context kind=\"quote\" title=\"Persona\" url=\"\" msg=\"m1\">\n正文含 <persona-context> 标签\n</jarvis-context>\n\n请问 <persona-context> 为什么裸露"
	if got := LegacyContextTags(in); got != want {
		t.Fatalf("LegacyContextTags = %q, want %q", got, want)
	}
	escaped := "<persona-context kind=\"quote\" title=\"Persona\">\n正文含 &lt;persona-context> 和 &lt;/persona-context>\n</persona-context>\n\n修"
	wantEscaped := "<jarvis-context kind=\"quote\" title=\"Persona\">\n正文含 &lt;persona-context> 和 &lt;/persona-context>\n</jarvis-context>\n\n修"
	if got := LegacyContextTags(escaped); got != wantEscaped {
		t.Fatalf("LegacyContextTags(escaped) = %q, want %q", got, wantEscaped)
	}
}

func TestFindAndStripContextBlocksEdgeCases(t *testing.T) {
	twoOpensOneClose := "<persona-context>\n- 现在：2026-11-27 12:29\n</persona-context>\n\n" +
		"<jarvis-context kind=\"quote\" title=\"Persona\" url=\"\" msg=\"m1\">\n引用含 <jarvis-context> 标签\n</jarvis-context>\n\n修"
	wantAfterSnapshot := "<jarvis-context kind=\"quote\" title=\"Persona\" url=\"\" msg=\"m1\">\n引用含 <jarvis-context> 标签\n</jarvis-context>\n\n修"
	if got := StripSnapshotBlocks(twoOpensOneClose); got != wantAfterSnapshot {
		t.Fatalf("StripSnapshotBlocks(2o1c) = %q, want %q", got, wantAfterSnapshot)
	}
	if got := strings.TrimSpace(StripContextBlocks(wantAfterSnapshot, "", false)); got != "修" {
		t.Fatalf("StripContextBlocks(2o1c) = %q, want %q", got, "修")
	}

	paired := "<jarvis-context kind=\"quote\" title=\"Persona\">\n解释 <persona-context>示例</persona-context> 后半段\n</jarvis-context>\n\n修"
	if got := StripSnapshotBlocks(paired); got != paired {
		t.Fatalf("StripSnapshotBlocks(paired) corrupted quote: %q", got)
	}
	blocks := FindContextBlocks(paired)
	if len(blocks) != 1 || strings.TrimSpace(paired[blocks[0].OpenEnd:blocks[0].CloseAt]) != "解释 <persona-context>示例</persona-context> 后半段" {
		t.Fatalf("FindContextBlocks(paired) = %+v", blocks)
	}
	if got := strings.TrimSpace(StripContextBlocks(paired, "", false)); got != "修" {
		t.Fatalf("StripContextBlocks(paired) = %q, want %q", got, "修")
	}

	twoBlocks := "<persona-context kind=\"quote\" title=\"A\">\n含 <persona-context> 标签\n</persona-context>\n" +
		"<persona-context kind=\"context\" title=\"B\">\n正常块\n</persona-context>\n\n修"
	blocks = FindContextBlocks(twoBlocks)
	if len(blocks) != 2 {
		t.Fatalf("FindContextBlocks(twoBlocks) count = %d, want 2: %+v", len(blocks), blocks)
	}
	if strings.TrimSpace(twoBlocks[blocks[0].OpenEnd:blocks[0].CloseAt]) != "含 <persona-context> 标签" {
		t.Fatalf("block 0 body = %q", twoBlocks[blocks[0].OpenEnd:blocks[0].CloseAt])
	}
	if strings.TrimSpace(twoBlocks[blocks[1].OpenEnd:blocks[1].CloseAt]) != "正常块" {
		t.Fatalf("block 1 body = %q", twoBlocks[blocks[1].OpenEnd:blocks[1].CloseAt])
	}
	if got := strings.TrimSpace(StripContextBlocks(twoBlocks, "", false)); got != "修" {
		t.Fatalf("StripContextBlocks(twoBlocks) = %q, want %q", got, "修")
	}

	if got := FindContextBlocks("请修 <persona-context> 的问题"); len(got) != 0 {
		t.Fatalf("expected 0 blocks, got %+v", got)
	}
}
