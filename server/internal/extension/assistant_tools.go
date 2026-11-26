package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/toolschema"
)

// Assistant tools and prompt sections (docs/extensions/extensions.md "Assistant tools and prompt sections", #13401):
// an extension declares in its manifest the tools it gives the assistant (mcp_tools) and the sections it adds to the
// assistant's instructions (prompt_sections), so the install page can list them and an upgrade that adds a tool, turns
// one into a write or opens one to coding Agents is agreed to again. The names match modapi.Info's MCPTools and
// PromptSections (#13430), so a built-in module and an extension say the same thing the same way. This file only reads
// and checks them: serving the tools and adding the sections to a conversation are the manager's, and come later.

// MCPToolItem is a contribution to mcp_tools: one tool, its id the tool's name.
type MCPToolItem = wire.MCPToolItem

// PromptSectionItem is a contribution to prompt_sections: one section, a .md file in the package per language.
type PromptSectionItem = wire.PromptSectionItem

// The bounds of the two points, the same as manifest.schema.json's.
const (
	// mcpToolSchemaMost is the largest input_schema (bytes as written).
	mcpToolSchemaMost = 16 << 10
	// PromptSectionMost is the largest section file (bytes), as a data-directory instructions file.
	PromptSectionMost = 6 << 10
	// promptSectionsMost is how many sections one extension may add.
	promptSectionsMost = 3
)

var (
	mcpToolID         = lazyre.New(`^[a-z][a-z0-9_]{0,63}$`)
	promptSectionLang = lazyre.New(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
)

func init() {
	Points["mcp_tools"] = Point{Name: "mcp_tools", Namespace: "mcp_tools", Version: 1, NeedsRPC: true, Item: func(raw json.RawMessage) (string, error) {
		var t MCPToolItem
		if err := strict(raw, &t); err != nil {
			return "", err
		}
		return t.ID, checkMCPTool(t)
	}}
	Points["prompt_sections"] = Point{Name: "prompt_sections", Namespace: "prompt_sections", Version: 1, Item: func(raw json.RawMessage) (string, error) {
		var s PromptSectionItem
		if err := strict(raw, &s); err != nil {
			return "", err
		}
		return s.ID, checkPromptSection(s)
	}}
}

// checkMCPTool holds one tool to what every runtime's model API takes (Points' Item). What depends on the rest of the
// manifest (its name's prefix) is checkAssistantTools'.
func checkMCPTool(t MCPToolItem) error {
	if !mcpToolID.MatchString(t.ID) {
		return fmt.Errorf("mcp_tools id %q: a-z0-9_, a letter first, at most 64", t.ID)
	}
	if n := utf8.RuneCountInString(t.Description); strings.TrimSpace(t.Description) == "" || n > StringMost {
		return fmt.Errorf("mcp_tools %s: a description of 1 to %d characters", t.ID, StringMost)
	}
	if !slices.Contains(wire.MCPToolAccesses, t.Access) {
		return fmt.Errorf("mcp_tools %s: access is read or write", t.ID)
	}
	if len(t.InputSchema) > mcpToolSchemaMost {
		return fmt.Errorf("mcp_tools %s: input_schema larger than %d KB", t.ID, mcpToolSchemaMost>>10)
	}
	var root struct {
		Type json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(t.InputSchema, &root); err != nil || !bytes.Equal(bytes.TrimSpace(root.Type), []byte(`"object"`)) {
		return fmt.Errorf("mcp_tools %s: input_schema is a JSON Schema object with type object", t.ID)
	}
	// Refused here rather than mended on the way out (toolschema.Portable): a Gemini session would otherwise get a
	// schema other than the one the user was shown.
	if problems := toolschema.Problems(t.InputSchema); len(problems) > 0 {
		return fmt.Errorf("mcp_tools %s: input_schema: %s (Gemini refuses it)", t.ID, strings.Join(problems, "; "))
	}
	return nil
}

// checkPromptSection holds one section to its files and tools as written (Points' Item); that the files are there,
// UTF-8 and small enough is checkPromptSectionFiles'.
func checkPromptSection(s PromptSectionItem) error {
	if !idPattern.MatchString(s.ID) {
		return fmt.Errorf("prompt_sections id %q: a-z0-9_-", s.ID)
	}
	if s.Files[string(i18n.ZhHans)] == "" && s.Files[string(i18n.En)] == "" {
		return fmt.Errorf("prompt_sections %s: files has zh-Hans or en", s.ID)
	}
	for _, lang := range sortedKeys(s.Files) {
		f := s.Files[lang]
		if !promptSectionLang.MatchString(lang) {
			return fmt.Errorf("prompt_sections %s: files: %q is not a language (zh-Hans, en, ja)", s.ID, lang)
		}
		if err := checkPackagePath(f); err != nil || !strings.HasSuffix(f, ".md") {
			return fmt.Errorf("prompt_sections %s: files.%s is a .md file inside the package", s.ID, lang)
		}
	}
	if s.Tools != nil && len(s.Tools) == 0 {
		return fmt.Errorf("prompt_sections %s: tools is left out or names at least one", s.ID)
	}
	for i, t := range s.Tools {
		if !mcpToolID.MatchString(t) || slices.Contains(s.Tools[:i], t) {
			return fmt.Errorf("prompt_sections %s: tools: %q is a tool's name, once", s.ID, t)
		}
	}
	return nil
}

// MCPTools are the manifest's contributions to mcp_tools.
func (m *Manifest) MCPTools() []MCPToolItem {
	var out []MCPToolItem
	_ = json.Unmarshal(m.Contributes["mcp_tools"], &out)
	return out
}

// PromptSections are the manifest's contributions to prompt_sections.
func (m *Manifest) PromptSections() []PromptSectionItem {
	var out []PromptSectionItem
	_ = json.Unmarshal(m.Contributes["prompt_sections"], &out)
	return out
}

// MCPToolPrefix is what each of the extension id's tools' names starts with: the id without - and then _
// (example-notes: examplenotes_). The hub's own tools never start with an installed extension's prefix.
func MCPToolPrefix(id string) string { return strings.ReplaceAll(id, "-", "") + "_" }

// checkAssistantTools are the rules of the two points that read the rest of the manifest (Validate): each tool's name
// starts with the extension's prefix and is not a name the hub answers to under an older name, at most
// promptSectionsMost sections, and a section's tools are this extension's.
func (m *Manifest) checkAssistantTools() []error {
	var errs []error
	prefix := MCPToolPrefix(m.ID)
	var names []string
	for _, t := range m.MCPTools() {
		names = append(names, t.ID)
		rest, ok := strings.CutPrefix(t.ID, prefix)
		switch {
		case !ok || rest == "":
			errs = append(errs, fmt.Errorf("contributes.mcp_tools: %s: a tool's name starts with %s", t.ID, prefix))
		case compat.Tool(t.ID) != t.ID || compat.LegacyTool(t.ID) != t.ID:
			errs = append(errs, fmt.Errorf("contributes.mcp_tools: %s is the name of one of the hub's tools", t.ID))
		}
	}
	sections := m.PromptSections()
	if len(sections) > promptSectionsMost {
		errs = append(errs, fmt.Errorf("contributes.prompt_sections: at most %d", promptSectionsMost))
	}
	for _, s := range sections {
		for _, t := range s.Tools {
			if !slices.Contains(names, t) {
				errs = append(errs, fmt.Errorf("contributes.prompt_sections: %s: tool %s is not in this extension's mcp_tools", s.ID, t))
			}
		}
	}
	return errs
}

// checkPromptSectionFiles holds each section's files in the package at root to what the assistant can read: a
// regular file inside the package, UTF-8, at most PromptSectionMost.
func checkPromptSectionFiles(m *Manifest, root string) error {
	for _, s := range m.PromptSections() {
		for _, lang := range sortedKeys(s.Files) {
			if err := checkPromptSectionFile(root, s.Files[lang]); err != nil {
				return fmt.Errorf("prompt_sections %s: %s: %v", s.ID, s.Files[lang], err)
			}
		}
	}
	return nil
}

func checkPromptSectionFile(root, file string) error {
	p := filepath.Join(root, filepath.FromSlash(file))
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return errors.New("not in the package")
	}
	rootResolved, _ := filepath.EvalSymlinks(root)
	if rel, err := filepath.Rel(rootResolved, resolved); err != nil || !inside(filepath.ToSlash(rel)) {
		return errors.New("leads out of the package")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return errors.New("not in the package")
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, PromptSectionMost+1))
	switch {
	case err != nil:
		return err
	case len(raw) > PromptSectionMost:
		return fmt.Errorf("larger than %d KB", PromptSectionMost>>10)
	case !utf8.Valid(raw):
		return errors.New("not UTF-8")
	}
	return nil
}

// mcpToolConsent is what the user agrees to of each tool, sorted: its name, whether it writes, whether coding Agents
// get it. Empty for an extension without tools, so every other digest is what it was.
func mcpToolConsent(m *Manifest) []string {
	var out []string
	for _, t := range m.MCPTools() {
		c := t.ID + ":" + t.Access
		if t.Agents {
			c += ":agents"
		}
		out = append(out, c)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// newMCPTools are m's tools old (nil: none) did not have as they are now: a new name, or a read tool that now writes.
// One that only reads now asks for less and is not among them.
func newMCPTools(old, m *Manifest) []MCPToolItem {
	var was []MCPToolItem
	if old != nil {
		was = old.MCPTools()
	}
	var out []MCPToolItem
	for _, t := range m.MCPTools() {
		i := slices.IndexFunc(was, func(o MCPToolItem) bool { return o.ID == t.ID })
		if i < 0 || t.Access == wire.MCPToolAccessWrite && was[i].Access != wire.MCPToolAccessWrite {
			out = append(out, t)
		}
	}
	return out
}

// newAgentMCPTools are m's tools coding Agents get that old (nil: none) did not give them.
func newAgentMCPTools(old, m *Manifest) []MCPToolItem {
	var was []MCPToolItem
	if old != nil {
		was = old.MCPTools()
	}
	var out []MCPToolItem
	for _, t := range m.MCPTools() {
		if t.Agents && !slices.ContainsFunc(was, func(o MCPToolItem) bool { return o.ID == t.ID && o.Agents }) {
			out = append(out, t)
		}
	}
	return out
}

// mcpToolLines are the lines for tools (the assistant gets them) and agentTools (coding Agents get them too), titled
// for an install (more false) or for what an upgrade adds.
func mcpToolLines(l i18n.Lang, tools, agentTools []MCPToolItem, more bool) []PermissionLine {
	sep := permissionText(l, "list_separator")
	names := func(list []MCPToolItem, write bool) string {
		var out []string
		for _, t := range list {
			if !write || t.Access == wire.MCPToolAccessWrite {
				out = append(out, t.ID)
			}
		}
		return strings.Join(out, sep)
	}
	suffix := "_title"
	if more {
		suffix = "_more_title"
	}
	var out []PermissionLine
	if len(tools) > 0 {
		detail := permissionText(l, "mcp_tools_detail", i18n.Args{"tools": names(tools, false)})
		if writes := names(tools, true); writes != "" {
			detail = permissionText(l, "mcp_tools_write_detail", i18n.Args{"tools": names(tools, false), "writes": writes})
		}
		out = append(out, PermissionLine{Key: "mcp_tools", Title: permissionText(l, "mcp_tools"+suffix), Detail: detail})
	}
	if len(agentTools) > 0 {
		out = append(out, PermissionLine{Key: "mcp_tools_agents", Title: permissionText(l, "mcp_tools_agents"+suffix),
			Detail: permissionText(l, "mcp_tools_agents_detail", i18n.Args{"tools": names(agentTools, false)})})
	}
	return out
}

// MCPToolText is a tool's text in one language (Translation.MCPTools).
type MCPToolText struct {
	Description string `json:"description,omitempty"`
}

// MCPToolDescription is the description of tool name (own) in l.
func (ts Translations) MCPToolDescription(l i18n.Lang, name, own string) string {
	var texts []string
	for _, t := range ts.chain(l) {
		if tt := t.MCPTools[name]; tt != nil {
			texts = append(texts, tt.Description)
		}
	}
	return firstText(own, texts...)
}
