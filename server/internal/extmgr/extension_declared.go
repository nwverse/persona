package extmgr

import (
	"net/http"

	"github.com/nwverse/persona/server/internal/extension"
)

// What the extensions that are on declare for the apps (docs/extensions/extensions.md "Purely declarative contributions", #1854):
// quick prompts for the message box, themes (theme colors), and templates of an inbox source's triage rules. Skills and
// instructions go to the assistant with the assistant contributions' (extension_assistant.go).

// QuickPromptView is a quick prompt as the apps list it.
type QuickPromptView struct {
	Extension     string `json:"extension"`
	ExtensionName string `json:"extension_name"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	Text          string `json:"text"`
}

// ThemeView is a theme as the apps list it; Problems (contrast) non-empty: the apps do not offer it.
type ThemeView struct {
	Extension     string            `json:"extension"`
	ExtensionName string            `json:"extension_name"`
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Light         map[string]string `json:"light"`
	Dark          map[string]string `json:"dark"`
	Problems      []string          `json:"problems,omitempty"`
}

// TriageTemplateView is a template of triage rules as the apps list it.
type TriageTemplateView struct {
	Extension     string `json:"extension"`
	ExtensionName string `json:"extension_name"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	Text          string `json:"text"`
}

// WebShellView is an installed Web/Desktop shell contribution as Web Settings lists it.
type WebShellView struct {
	Extension     string `json:"extension"`
	ExtensionName string `json:"extension_name"`
	Version       string `json:"version"`
	ID            string `json:"id"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
}

// declared calls f with each extension that is on and runs here (its contributions as they take effect).
func (ps *extensions) declared(f func(p *extension.Extension)) {
	if ps == nil {
		return
	}
	for _, p := range ps.reg.List() {
		if p.Record.Enabled && p.Problem == "" {
			f(p)
		}
	}
}

func (h *Manager) declaredRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/v1/extensions/-/prompts", func(w http.ResponseWriter, r *http.Request) {
		out := []QuickPromptView{}
		h.extensions.declared(func(p *extension.Extension) {
			for _, it := range p.Manifest.Prompts() {
				if it.Kind == extension.PromptQuick {
					out = append(out, QuickPromptView{Extension: p.Record.ID, ExtensionName: p.Full.Name, ID: it.ID, Title: it.Title, Text: it.Text})
				}
			}
		})
		writeJSON(w, map[string]any{"prompts": out})
	})
	api.HandleFunc("GET /api/v1/extensions/-/themes", func(w http.ResponseWriter, r *http.Request) {
		out := []ThemeView{}
		h.extensions.declared(func(p *extension.Extension) {
			for _, it := range p.Manifest.Themes() {
				out = append(out, ThemeView{Extension: p.Record.ID, ExtensionName: p.Full.Name, ID: it.ID, Name: it.Name,
					Light: it.Light, Dark: it.Dark, Problems: extension.ThemeProblems(it)})
			}
		})
		writeJSON(w, map[string]any{"themes": out})
	})
	api.HandleFunc("GET /api/v1/extensions/-/triage-templates", func(w http.ResponseWriter, r *http.Request) {
		out := []TriageTemplateView{}
		h.extensions.declared(func(p *extension.Extension) {
			for _, it := range p.Manifest.TriageTemplates() {
				out = append(out, TriageTemplateView{Extension: p.Record.ID, ExtensionName: p.Full.Name, ID: it.ID, Title: it.Title, Text: it.Text})
			}
		})
		writeJSON(w, map[string]any{"templates": out})
	})
	api.HandleFunc("GET /api/v1/extensions/-/web-shells", func(w http.ResponseWriter, r *http.Request) {
		out := []WebShellView{}
		h.extensions.declared(func(p *extension.Extension) {
			if p.Dir == "" {
				return
			}
			for _, it := range p.Manifest.WebShells() {
				if err := extension.CheckWebShellFiles(it, p.Dir); err != nil {
					continue
				}
				out = append(out, WebShellView{
					Extension:     p.Record.ID,
					ExtensionName: p.Full.Name,
					Version:       p.Record.Version,
					ID:            it.ID,
					Key:           extension.WebShellKey(p.Record.ID, it.ID),
					Name:          it.Name,
					Description:   it.Description,
				})
			}
		})
		writeJSON(w, map[string]any{"web_shells": out})
	})
}
