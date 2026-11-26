package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// mcpTool is a valid tool of example-notes, changed by change.
func mcpTool(change func(it map[string]any)) map[string]any {
	it := map[string]any{"id": "examplenotes_find", "description": "Find a note by its title.", "access": "read",
		"input_schema": map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}}}
	if change != nil {
		change(it)
	}
	return it
}

// promptSection is a valid section of example-notes, changed by change.
func promptSection(change func(it map[string]any)) map[string]any {
	it := map[string]any{"id": "notes", "files": map[string]any{"zh-Hans": "assistant/notes.md", "en": "assistant/notes.en.md"}}
	if change != nil {
		change(it)
	}
	return it
}

// assistantToolsManifest is the example manifest contributing these tools and sections (none: the point is left out).
func assistantToolsManifest(t *testing.T, tools, sections []map[string]any) []byte {
	t.Helper()
	return with(t, func(m map[string]any) {
		c := m["contributes"].(map[string]any)
		if tools != nil {
			c["mcp_tools"] = tools
		}
		if sections != nil {
			c["prompt_sections"] = sections
		}
	})
}

func TestAssistantToolPoints(t *testing.T) {
	if p := Points["mcp_tools"]; p.Name != "mcp_tools" || p.Version != 1 || !p.NeedsRPC || p.NeedsHTTP {
		t.Fatalf("mcp_tools: %+v", p)
	}
	if p := Points["prompt_sections"]; p.Name != "prompt_sections" || p.Version != 1 || p.NeedsRPC || p.NeedsHTTP {
		t.Fatalf("prompt_sections: %+v", p)
	}
	m, err := Parse(assistantToolsManifest(t,
		[]map[string]any{mcpTool(nil), mcpTool(func(it map[string]any) { it["id"], it["access"], it["agents"] = "examplenotes_save", "write", true })},
		[]map[string]any{promptSection(func(it map[string]any) { it["tools"] = []string{"examplenotes_find"} })}))
	if err != nil {
		t.Fatal(err)
	}
	tools := m.MCPTools()
	if len(tools) != 2 || tools[0].Access != wire.MCPToolAccessRead || tools[0].Agents || tools[1].Access != wire.MCPToolAccessWrite || !tools[1].Agents {
		t.Fatalf("%+v", tools)
	}
	if !json.Valid(tools[0].InputSchema) || !strings.Contains(string(tools[0].InputSchema), `"title"`) {
		t.Errorf("input_schema as written: %s", tools[0].InputSchema)
	}
	if s := m.PromptSections(); len(s) != 1 || s[0].Files["en"] != "assistant/notes.en.md" || !slices.Equal(s[0].Tools, []string{"examplenotes_find"}) {
		t.Fatalf("%+v", s)
	}
	if got := m.Contributions(); !slices.Equal(got["mcp_tools"], []string{"examplenotes_find", "examplenotes_save"}) || !slices.Equal(got["prompt_sections"], []string{"notes"}) {
		t.Fatalf("%+v", got)
	}
	if MCPToolPrefix("example-notes") != "examplenotes_" || MCPToolPrefix("my_ext") != "my_ext_" {
		t.Error("prefix")
	}
	if (&Manifest{}).MCPTools() != nil || (&Manifest{}).PromptSections() != nil {
		t.Error("no contribution")
	}
}

// What a tool and a section may be: the hub's Parse and the SDK's schema agree, but for what only the hub can say (it
// reads the rest of the manifest, or the input schema's own JSON).
func TestAssistantToolRefusals(t *testing.T) {
	hubOnly := "hub only"
	tool := func(change func(it map[string]any)) []map[string]any { return []map[string]any{mcpTool(change)} }
	section := func(change func(it map[string]any)) []map[string]any { return []map[string]any{promptSection(change)} }
	for _, c := range []struct {
		name            string
		tools, sections []map[string]any
		ok              bool
		schema          string // "" agrees with the hub; hubOnly: the schema cannot tell
	}{
		{"plain", tool(nil), section(nil), true, ""},
		{"a write tool for Agents too", tool(func(it map[string]any) { it["access"], it["agents"] = "write", true }), nil, true, ""},
		{"a 64-character name", tool(func(it map[string]any) { it["id"] = "examplenotes_" + strings.Repeat("a", 51) }), nil, true, ""},
		{"a 2000-character description", tool(func(it map[string]any) { it["description"] = strings.Repeat("找", 2000) }), nil, true, ""},
		{"only sections", nil, section(nil), true, ""},
		{"three sections", nil, []map[string]any{promptSection(nil), promptSection(func(it map[string]any) { it["id"] = "b" }),
			promptSection(func(it map[string]any) { it["id"] = "c" })}, true, ""},
		{"a section in Japanese and English", nil, section(func(it map[string]any) { it["files"] = map[string]any{"ja": "a.ja.md", "en": "a.md"} }), true, ""},
		{"a section for its tool", tool(nil), section(func(it map[string]any) { it["tools"] = []string{"examplenotes_find"} }), true, ""},

		{"a 65-character name", tool(func(it map[string]any) { it["id"] = "examplenotes_" + strings.Repeat("a", 52) }), nil, false, ""},
		{"an upper-case name", tool(func(it map[string]any) { it["id"] = "examplenotes_Find" }), nil, false, ""},
		{"a name with -", tool(func(it map[string]any) { it["id"] = "examplenotes-find" }), nil, false, ""},
		{"another extension's prefix", tool(func(it map[string]any) { it["id"] = "othernotes_find" }), nil, false, hubOnly},
		{"the id with - as prefix", tool(func(it map[string]any) { it["id"] = "example_notes_find" }), nil, false, hubOnly},
		{"the prefix alone", tool(func(it map[string]any) { it["id"] = "examplenotes_" }), nil, false, hubOnly},
		{"a name twice", []map[string]any{mcpTool(nil), mcpTool(nil)}, nil, false, hubOnly},
		{"no description", tool(func(it map[string]any) { delete(it, "description") }), nil, false, ""},
		{"a blank description", tool(func(it map[string]any) { it["description"] = "  " }), nil, false, hubOnly},
		{"a 2001-character description", tool(func(it map[string]any) { it["description"] = strings.Repeat("a", 2001) }), nil, false, ""},
		{"no access", tool(func(it map[string]any) { delete(it, "access") }), nil, false, ""},
		{"an unknown access", tool(func(it map[string]any) { it["access"] = "admin" }), nil, false, ""},
		{"no input_schema", tool(func(it map[string]any) { delete(it, "input_schema") }), nil, false, ""},
		{"an input_schema that is not an object", tool(func(it map[string]any) { it["input_schema"] = map[string]any{"type": "string"} }), nil, false, hubOnly},
		{"an input_schema without a type", tool(func(it map[string]any) { it["input_schema"] = map[string]any{"properties": map[string]any{}} }), nil, false, hubOnly},
		{"an input_schema that is a list", tool(func(it map[string]any) { it["input_schema"] = []any{} }), nil, false, hubOnly},
		{"an empty string in an enum", tool(func(it map[string]any) {
			it["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []any{"", "recent"}}}}
		}), nil, false, hubOnly},
		{"an empty enum", tool(func(it map[string]any) {
			it["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []any{}}}}
		}), nil, false, hubOnly},
		{"an input_schema over 16 KB", tool(func(it map[string]any) {
			it["input_schema"] = map[string]any{"type": "object", "description": strings.Repeat("a", 16<<10)}
		}), nil, false, hubOnly},
		{"an unknown tool field", tool(func(it map[string]any) { it["output_schema"] = map[string]any{"type": "object"} }), nil, false, ""},
		{"no tools", []map[string]any{}, nil, false, ""},

		{"a bad section id", nil, section(func(it map[string]any) { it["id"] = "Notes!" }), false, ""},
		{"no files", nil, section(func(it map[string]any) { delete(it, "files") }), false, ""},
		{"no files named", nil, section(func(it map[string]any) { it["files"] = map[string]any{} }), false, ""},
		{"only Japanese", nil, section(func(it map[string]any) { it["files"] = map[string]any{"ja": "a.ja.md"} }), false, hubOnly},
		{"not a language", nil, section(func(it map[string]any) { it["files"] = map[string]any{"en": "a.md", "English": "b.md"} }), false, ""},
		{"not markdown", nil, section(func(it map[string]any) { it["files"] = map[string]any{"en": "a.txt"} }), false, ""},
		{"out of the package", nil, section(func(it map[string]any) { it["files"] = map[string]any{"en": "../a.md"} }), false, hubOnly},
		{"an absolute path", nil, section(func(it map[string]any) { it["files"] = map[string]any{"en": "/tmp/a.md"} }), false, hubOnly},
		{"a tool it does not have", tool(nil), section(func(it map[string]any) { it["tools"] = []string{"examplenotes_save"} }), false, hubOnly},
		{"a tool without mcp_tools", nil, section(func(it map[string]any) { it["tools"] = []string{"examplenotes_find"} }), false, hubOnly},
		{"a tool twice", tool(nil), section(func(it map[string]any) { it["tools"] = []string{"examplenotes_find", "examplenotes_find"} }), false, ""},
		{"no tools named", tool(nil), section(func(it map[string]any) { it["tools"] = []string{} }), false, ""},
		{"four sections", nil, []map[string]any{promptSection(nil), promptSection(func(it map[string]any) { it["id"] = "b" }),
			promptSection(func(it map[string]any) { it["id"] = "c" }), promptSection(func(it map[string]any) { it["id"] = "d" })}, false, ""},
		{"an unknown section field", nil, section(func(it map[string]any) { it["template"] = "x.tmpl" }), false, ""},
		{"no sections", nil, []map[string]any{}, false, ""},
	} {
		raw := assistantToolsManifest(t, c.tools, c.sections)
		_, hubErr := Parse(raw)
		if (hubErr == nil) != c.ok {
			t.Errorf("%s: hub: want valid=%v, got %v", c.name, c.ok, hubErr)
		}
		schemaErr := wire.ValidateJSON("Manifest", raw)
		if wantSchema := c.ok || c.schema == hubOnly; (schemaErr == nil) != wantSchema {
			t.Errorf("%s: schema: want valid=%v, got %v", c.name, wantSchema, schemaErr)
		}
	}
}

func TestAssistantToolsNeedAProgram(t *testing.T) {
	_, err := Parse(with(t, func(m map[string]any) {
		delete(m, "runtime")
		delete(m, "activation_events")
		m["contributes"] = map[string]any{"mcp_tools": []any{mcpTool(nil)}}
	}))
	if err == nil || !strings.Contains(err.Error(), "contributes.mcp_tools needs a runtime that speaks the protocol") {
		t.Errorf("tools without a program: %v", err)
	}
	// Sections alone are declarative.
	if _, err := Parse(with(t, func(m map[string]any) {
		delete(m, "runtime")
		delete(m, "activation_events")
		delete(m, "engines")
		m["contributes"] = map[string]any{"prompt_sections": []any{promptSection(nil)}}
	})); err != nil {
		t.Errorf("declarative sections: %v", err)
	}
}

// A name the hub answers to under its current or older name is never an extension's, whatever the extension is called.
func TestAssistantToolHubNames(t *testing.T) {
	for _, name := range []string{"persona_settings", "ask_persona"} {
		id, _, _ := strings.Cut(name, "_")
		if name == "ask_persona" {
			id = "ask"
		}
		_, err := Parse(with(t, func(m map[string]any) {
			m["id"] = id
			m["contributes"] = map[string]any{"mcp_tools": []any{mcpTool(func(it map[string]any) { it["id"] = name })}}
		}))
		if err == nil || !strings.Contains(err.Error(), "is the name of one of the hub's tools") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPromptSectionFiles(t *testing.T) {
	write := func(t *testing.T, root, name string, data []byte) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Parse(assistantToolsManifest(t, nil, []map[string]any{promptSection(nil)}))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		setup  func(t *testing.T, root string)
		wantIn string // "" passes
	}{
		{"both files", func(t *testing.T, root string) {
			write(t, root, "assistant/notes.md", []byte("# 笔记\n"))
			write(t, root, "assistant/notes.en.md", []byte(strings.Repeat("a", PromptSectionMost)))
		}, ""},
		{"one missing", func(t *testing.T, root string) { write(t, root, "assistant/notes.md", []byte("x")) }, "assistant/notes.en.md: not in the package"},
		{"too large", func(t *testing.T, root string) {
			write(t, root, "assistant/notes.md", []byte("x"))
			write(t, root, "assistant/notes.en.md", []byte(strings.Repeat("a", PromptSectionMost+1)))
		}, "larger than 6 KB"},
		{"not UTF-8", func(t *testing.T, root string) {
			write(t, root, "assistant/notes.md", []byte{0xff, 0xfe})
			write(t, root, "assistant/notes.en.md", []byte("x"))
		}, "not UTF-8"},
		{"a directory", func(t *testing.T, root string) {
			write(t, root, "assistant/notes.md", []byte("x"))
			if err := os.MkdirAll(filepath.Join(root, "assistant", "notes.en.md"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "not a regular file"},
		{"a link out of the package", func(t *testing.T, root string) {
			write(t, root, "assistant/notes.md", []byte("x"))
			outside := filepath.Join(t.TempDir(), "secret.md")
			write(t, filepath.Dir(outside), "secret.md", []byte("x"))
			if err := os.Symlink(outside, filepath.Join(root, "assistant", "notes.en.md")); err != nil {
				t.Fatal(err)
			}
		}, "leads out of the package"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			c.setup(t, root)
			err := checkPromptSectionFiles(m, root)
			if c.wantIn == "" && err != nil || c.wantIn != "" && (err == nil || !strings.Contains(err.Error(), c.wantIn)) {
				t.Errorf("want %q, got %v", c.wantIn, err)
			}
		})
	}
}

// Installing shows every tool, which of them write and which coding Agents get; the digest holds the same, and an
// extension without tools digests as it did before the point.
func TestAssistantToolPermissions(t *testing.T) {
	parse := func(tools []map[string]any, sections []map[string]any) *Manifest {
		t.Helper()
		m, err := Parse(assistantToolsManifest(t, tools, sections))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	find := mcpTool(nil)
	save := mcpTool(func(it map[string]any) { it["id"], it["access"] = "examplenotes_save", "write" })
	none := parse(nil, nil)
	if Digest(parse(nil, []map[string]any{promptSection(nil)})) != Digest(none) {
		t.Error("sections are not agreed to: the digest stays as it was")
	}
	m := parse([]map[string]any{find, save}, nil)
	if Digest(m) == Digest(none) {
		t.Error("tools are agreed to")
	}
	if slices.ContainsFunc(Explain(none), func(l PermissionLine) bool { return strings.HasPrefix(l.Key, "mcp_tools") }) {
		t.Error("no tools, no line")
	}
	for _, l := range i18n.Langs {
		lines := ExplainIn(l, m)
		i := slices.IndexFunc(lines, func(p PermissionLine) bool { return p.Key == "mcp_tools" })
		if i < 0 {
			t.Fatalf("%s: %+v", l, lines)
		}
		line := lines[i]
		if line.Title != i18n.T(l, "extension-permissions.mcp_tools_title") || strings.Contains(line.Title+line.Detail, "extension-permissions.") ||
			strings.Contains(line.Detail, "{") || !strings.Contains(line.Detail, "examplenotes_find") ||
			strings.Count(line.Detail, "examplenotes_save") != 2 {
			t.Errorf("%s: %+v", l, line)
		}
		if slices.ContainsFunc(lines, func(p PermissionLine) bool { return p.Key == "mcp_tools_agents" }) {
			t.Errorf("%s: no tool is for Agents: %+v", l, lines)
		}
	}
	reads := parse([]map[string]any{find}, nil)
	if d := ExplainIn(i18n.En, reads); !slices.ContainsFunc(d, func(p PermissionLine) bool {
		return p.Key == "mcp_tools" && p.Detail == "examplenotes_find"
	}) {
		t.Errorf("only reads: %+v", d)
	}
	forAgents := parse([]map[string]any{mcpTool(func(it map[string]any) { it["agents"] = true })}, nil)
	if Digest(forAgents) == Digest(reads) {
		t.Error("opening a tool to Agents is agreed to")
	}
	if d := ExplainIn(i18n.En, forAgents); !slices.ContainsFunc(d, func(p PermissionLine) bool {
		return p.Key == "mcp_tools_agents" && p.Title == i18n.T(i18n.En, "extension-permissions.mcp_tools_agents_title") && p.Detail == "examplenotes_find"
	}) {
		t.Errorf("for Agents: %+v", d)
	}
	writes := parse([]map[string]any{mcpTool(func(it map[string]any) { it["access"] = "write" })}, nil)
	if Digest(writes) == Digest(reads) {
		t.Error("a write is agreed to")
	}
	// The order they are written in is not something to agree to.
	if Digest(parse([]map[string]any{save, find}, nil)) != Digest(m) {
		t.Error("the digest sorts the tools")
	}
}

// An upgrade asks again for a new tool, a read tool that now writes and a tool now open to coding Agents; less
// (a tool that now only reads, or is gone) asks nothing.
func TestAssistantToolUpgrade(t *testing.T) {
	parse := func(tools ...map[string]any) *Manifest {
		t.Helper()
		m, err := Parse(assistantToolsManifest(t, tools, nil))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	added := func(old, m *Manifest) map[string]PermissionLine {
		out := map[string]PermissionLine{}
		for _, l := range AddedIn(i18n.En, old, m) {
			if strings.HasPrefix(l.Key, "mcp_tools") {
				out[l.Key] = l
			}
		}
		return out
	}
	more := func(old, m *Manifest) bool {
		return MorePermissions(&Extension{Full: old, Record: Record{Granted: old.Permissions}}, m)
	}
	find := mcpTool(nil)
	findWrites := mcpTool(func(it map[string]any) { it["access"] = "write" })
	findForAgents := mcpTool(func(it map[string]any) { it["agents"] = true })
	save := mcpTool(func(it map[string]any) { it["id"], it["access"] = "examplenotes_save", "write" })
	none, _ := Parse([]byte(exampleManifest))
	for _, c := range []struct {
		name      string
		old, m    *Manifest
		tools     string // the mcp_tools line's detail, "" none
		agents    string // the mcp_tools_agents line's detail, "" none
		moreAsked bool
	}{
		{"the first tools", none, parse(find), "examplenotes_find", "", true},
		{"one more", parse(find), parse(find, save), "examplenotes_save. These act on your behalf: examplenotes_save", "", true},
		{"read to write", parse(find), parse(findWrites), "examplenotes_find. These act on your behalf: examplenotes_find", "", true},
		{"open to Agents", parse(find), parse(findForAgents), "", "examplenotes_find", true},
		{"the same", parse(find, save), parse(save, find), "", "", false},
		{"write to read", parse(findWrites), parse(find), "", "", false},
		{"no longer for Agents", parse(findForAgents), parse(find), "", "", false},
		{"one gone", parse(find, save), parse(find), "", "", false},
	} {
		got := added(c.old, c.m)
		if got["mcp_tools"].Detail != c.tools || got["mcp_tools_agents"].Detail != c.agents {
			t.Errorf("%s: %+v", c.name, got)
		}
		if l, ok := got["mcp_tools"]; ok && l.Title != i18n.T(i18n.En, "extension-permissions.mcp_tools_more_title") {
			t.Errorf("%s: title %q", c.name, l.Title)
		}
		if l, ok := got["mcp_tools_agents"]; ok && l.Title != i18n.T(i18n.En, "extension-permissions.mcp_tools_agents_more_title") {
			t.Errorf("%s: title %q", c.name, l.Title)
		}
		if more(c.old, c.m) != c.moreAsked {
			t.Errorf("%s: more permissions: want %v", c.name, c.moreAsked)
		}
	}
}

func TestAssistantToolTranslations(t *testing.T) {
	tr, err := ParseTranslation([]byte(`{"mcp_tools": {"examplenotes_find": {"description": "Find a note."}}, "later": {"x": 1}}`))
	if err != nil {
		t.Fatal(err)
	}
	ts := Translations{i18n.En: tr}
	for l, want := range map[i18n.Lang]string{i18n.En: "Find a note.", i18n.Ja: "Find a note.", i18n.ZhHans: "按标题找笔记"} {
		if got := ts.MCPToolDescription(l, "examplenotes_find", "按标题找笔记"); got != want {
			t.Errorf("%s: %q", l, got)
		}
	}
	if got := ts.MCPToolDescription(i18n.En, "examplenotes_save", "保存"); got != "保存" {
		t.Errorf("an untranslated tool: %q", got)
	}
	if _, err := ParseTranslation([]byte(`{"mcp_tools": {"examplenotes_find": {"description": "` + strings.Repeat("a", StringMost+1) + `"}}}`)); err == nil ||
		!strings.Contains(err.Error(), "mcp_tools.examplenotes_find.description") {
		t.Errorf("too long: %v", err)
	}
}

// The registry: a tool's name is one installed extension's (extensions ab and a-b share the prefix ab_), refused at
// preview; the preview lists the tools, and a section file the package lacks fails the preview.
func TestAssistantToolsRegistry(t *testing.T) {
	ctx := t.Context()
	r, _, _ := newTestRegistry(t)
	preview := func(id string, sections bool) (*Preview, error) {
		dir := t.TempDir()
		writePackage(t, dir, func(m map[string]any) {
			m["id"] = id
			c := map[string]any{"mcp_tools": []any{mcpTool(func(it map[string]any) { it["id"] = "ab_find" })}}
			if sections {
				c["prompt_sections"] = []any{promptSection(func(it map[string]any) { it["files"] = map[string]any{"en": "notes.md"} })}
			}
			m["contributes"] = c
		})
		return r.Preview(ctx, "dir:"+dir, "")
	}
	if _, err := preview("ab", true); err == nil || !strings.Contains(err.Error(), "notes.md: not in the package") {
		t.Fatalf("a missing section file: %v", err)
	}
	pv, err := preview("ab", false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(pv.Permissions, func(l PermissionLine) bool { return l.Key == "mcp_tools" && l.Detail == "ab_find" }) {
		t.Fatalf("the preview does not list the tool: %+v", pv.Permissions)
	}
	if _, err := r.Install(ctx, pv.ID, pv.Digest, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := preview("a-b", false); err == nil || !strings.Contains(err.Error(), `"ab_find"`) || !strings.Contains(err.Error(), "ab") {
		t.Fatalf("a name another extension has: %v", err)
	}
	r.Reserve("mcp_tools", "cd_find")
	dir := t.TempDir()
	writePackage(t, dir, func(m map[string]any) {
		m["id"] = "cd"
		m["contributes"] = map[string]any{"mcp_tools": []any{mcpTool(func(it map[string]any) { it["id"] = "cd_find" })}}
	})
	if _, err := r.Preview(ctx, "dir:"+dir, ""); err == nil || !strings.Contains(err.Error(), `"cd_find"`) {
		t.Fatalf("a name the hub reserved: %v", err)
	}
}
