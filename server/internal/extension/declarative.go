package extension

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// Declarative contributions (docs/extensions/extensions.md "Declarative-only contributions", #1854): an extension made of a manifest and a few files,
// running no program, can give the assistant skills and instructions, the apps quick prompts and a theme, and the
// inbox's triage settings templates.

// SkillItem is a contribution to skills: a directory of the package with SKILL.md, put in with the assistant's skills
// (the same as an assistant contribution's skills).
type SkillItem struct {
	ID  string `json:"id"`
	Dir string `json:"dir"`
}

// Prompt kinds.
const (
	PromptInstructions = "instructions" // a .md of the package added to the assistant's CLAUDE.md
	PromptQuick        = "quick"        // a template the apps offer in the message box
)

// PromptItem is a contribution to prompts.
type PromptItem struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	File  string `json:"file,omitempty"`  // instructions
	Title string `json:"title,omitempty"` // quick
	Text  string `json:"text,omitempty"`  // quick
}

// ThemeRoles are the colors a theme may give: the design system's role colors, nothing else (no sizes, fonts or
// arbitrary colors).
var ThemeRoles = []string{"accent", "positive", "caution", "danger", "ai"}

// ThemeItem is a contribution to themes: one more theme color, each role a #RRGGBB for light and dark.
type ThemeItem struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Light map[string]string `json:"light"`
	Dark  map[string]string `json:"dark"`
}

// TriageTemplateItem is a contribution to triage_templates: a ready text for an inbox source's triage rules.
type TriageTemplateItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// TriageTemplateMost is the longest template (runes), as triage_rules is read.
const TriageTemplateMost = 4000

var hexColor = lazyre.New(`^#[0-9A-Fa-f]{6}$`)

func init() {
	Points["skills"] = Point{Name: "skills", Namespace: "skills", Version: 1, Item: func(raw json.RawMessage) (string, error) {
		var s SkillItem
		if err := strict(raw, &s); err != nil {
			return "", err
		}
		if !idPattern.MatchString(s.ID) {
			return s.ID, fmt.Errorf("skill id %q: a-z0-9_-", s.ID)
		}
		if err := checkPackagePath(s.Dir); err != nil {
			return s.ID, fmt.Errorf("skill %s: dir: %v", s.ID, err)
		}
		return s.ID, nil
	}}
	Points["prompts"] = Point{Name: "prompts", Namespace: "prompts", Version: 1, Item: func(raw json.RawMessage) (string, error) {
		var p PromptItem
		if err := strict(raw, &p); err != nil {
			return "", err
		}
		if !idPattern.MatchString(p.ID) {
			return p.ID, fmt.Errorf("prompt id %q: a-z0-9_-", p.ID)
		}
		switch p.Kind {
		case PromptInstructions:
			if err := checkPackagePath(p.File); err != nil || !strings.HasSuffix(p.File, ".md") || p.Title != "" || p.Text != "" {
				return p.ID, fmt.Errorf("prompt %s: instructions is a .md file inside the package (file), nothing else", p.ID)
			}
		case PromptQuick:
			if n := utf8.RuneCountInString(strings.TrimSpace(p.Title)); n == 0 || n > 40 || p.File != "" {
				return p.ID, fmt.Errorf("prompt %s: a quick prompt has a title (1 to 40 characters) and a text, no file", p.ID)
			}
			if n := utf8.RuneCountInString(strings.TrimSpace(p.Text)); n == 0 || n > 4000 {
				return p.ID, fmt.Errorf("prompt %s: text: 1 to 4000 characters", p.ID)
			}
		default:
			return p.ID, fmt.Errorf("prompt %s: kind %q: instructions or quick", p.ID, p.Kind)
		}
		return p.ID, nil
	}}
	Points["themes"] = Point{Name: "themes", Namespace: "themes", Version: 1, Item: func(raw json.RawMessage) (string, error) {
		var th ThemeItem
		if err := strict(raw, &th); err != nil {
			return "", err
		}
		if !idPattern.MatchString(th.ID) {
			return th.ID, fmt.Errorf("theme id %q: a-z0-9_-", th.ID)
		}
		if n := utf8.RuneCountInString(strings.TrimSpace(th.Name)); n == 0 || n > 20 {
			return th.ID, fmt.Errorf("theme %s: name: 1 to 20 characters", th.ID)
		}
		for scheme, colors := range map[string]map[string]string{"light": th.Light, "dark": th.Dark} {
			if colors["accent"] == "" {
				return th.ID, fmt.Errorf("theme %s: %s.accent is required", th.ID, scheme)
			}
			for role, c := range colors {
				if !strings.Contains(" "+strings.Join(ThemeRoles, " ")+" ", " "+role+" ") {
					return th.ID, fmt.Errorf("theme %s: %s.%s: only %s", th.ID, scheme, role, strings.Join(ThemeRoles, ", "))
				}
				if !hexColor.MatchString(c) {
					return th.ID, fmt.Errorf("theme %s: %s.%s %q: #RRGGBB", th.ID, scheme, role, c)
				}
			}
		}
		return th.ID, nil
	}}
	Points["triage_templates"] = Point{Name: "triage_templates", Namespace: "triage_templates", Version: 1, Item: func(raw json.RawMessage) (string, error) {
		var tt TriageTemplateItem
		if err := strict(raw, &tt); err != nil {
			return "", err
		}
		if !idPattern.MatchString(tt.ID) {
			return tt.ID, fmt.Errorf("triage template id %q: a-z0-9_-", tt.ID)
		}
		if n := utf8.RuneCountInString(strings.TrimSpace(tt.Title)); n == 0 || n > 40 {
			return tt.ID, fmt.Errorf("triage template %s: title: 1 to 40 characters", tt.ID)
		}
		if n := utf8.RuneCountInString(strings.TrimSpace(tt.Text)); n == 0 || n > TriageTemplateMost {
			return tt.ID, fmt.Errorf("triage template %s: text: 1 to %d characters", tt.ID, TriageTemplateMost)
		}
		return tt.ID, nil
	}}
}

// Skills are the manifest's contributions to skills.
func (m *Manifest) Skills() []SkillItem {
	var out []SkillItem
	_ = json.Unmarshal(m.Contributes["skills"], &out)
	return out
}

// Prompts are the manifest's contributions to prompts.
func (m *Manifest) Prompts() []PromptItem {
	var out []PromptItem
	_ = json.Unmarshal(m.Contributes["prompts"], &out)
	return out
}

// Themes are the manifest's contributions to themes.
func (m *Manifest) Themes() []ThemeItem {
	var out []ThemeItem
	_ = json.Unmarshal(m.Contributes["themes"], &out)
	return out
}

// TriageTemplates are the manifest's contributions to triage_templates.
func (m *Manifest) TriageTemplates() []TriageTemplateItem {
	var out []TriageTemplateItem
	_ = json.Unmarshal(m.Contributes["triage_templates"], &out)
	return out
}

// AssistantParts are what the assistant gets of the manifest: its assistant contributions, and the declarative skills
// and instructions as if they were assistant contributions without a gate ("skills/<id>", "prompts/<id>").
func (m *Manifest) AssistantParts() []AssistantItem {
	out := m.Assistants()
	for _, s := range m.Skills() {
		out = append(out, AssistantItem{ID: "skills/" + s.ID, Skills: []string{s.Dir}})
	}
	for _, p := range m.Prompts() {
		if p.Kind == PromptInstructions {
			out = append(out, AssistantItem{ID: "prompts/" + p.ID, Instructions: p.File})
		}
	}
	return out
}

// ThemeProblems are the roles of th whose color does not stand out enough on the background of its scheme (WCAG
// contrast below 3:1 against white in light, black in dark): the apps do not offer a theme with problems.
func ThemeProblems(th ThemeItem) []string {
	var out []string
	for _, scheme := range []struct {
		name   string
		colors map[string]string
		bg     float64
	}{{"light", th.Light, 1}, {"dark", th.Dark, 0}} {
		for _, role := range ThemeRoles {
			c, ok := scheme.colors[role]
			if !ok {
				continue
			}
			if r := contrast(luminance(c), scheme.bg); r < 3 {
				out = append(out, fmt.Sprintf("%s.%s %s 对比度 %.1f，低于 3", scheme.name, role, c, r))
			}
		}
	}
	return out
}

func luminance(hex string) float64 {
	ch := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}

func contrast(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}
