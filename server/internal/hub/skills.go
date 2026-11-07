package hub

import (
	"context"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// The assistant's skills live on the machine that runs it, in the user's ~/.persona/skills (docs/operations/hub-update.md "Moving
// skills out of the binary"): the hub asks that machine's node (skills.list) to show them and to tell the assistant what it has.

var errNoAssistantMachine = i18n.Err("hub-skills.no_assistant_machine")

// skill is one skill as the node lists it (assistantskills.Skill).
type skill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"` // user | builtin
}

// machineSkills asks m's node for manager id's assistant's skills there: another manager's in its own directory (the
// connectors signed in there), the built-in's as ever (the node picks its directory).
func (h *Hub) machineSkills(ctx context.Context, m *Machine, id string) ([]skill, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out struct {
		Skills []skill `json:"skills"`
	}
	params := map[string]any{}
	if id != assistantwire.AssistantID {
		if cwd := m.assistantCwdOf(id); cwd != "" {
			params["cwd"] = cwd
		}
	}
	if err := m.call(ctx, "skills.list", params, &out); err != nil {
		return nil, err
	}
	return out.Skills, nil
}

// serveSkills is GET /api/v1/skills: the skills of the requested (or default) manager's machine, read only.
func (h *Hub) serveSkills(w http.ResponseWriter, r *http.Request) {
	id, ok := h.owner(w, r, h.byRequest)
	if !ok {
		return
	}
	m := h.localMachine(h.assistantProfileOf(id).MachineID)
	if m == nil {
		writeErr(w, http.StatusConflict, errNoAssistantMachine)
		return
	}
	list, err := h.machineSkills(r.Context(), m, id)
	if err != nil {
		callErr(w, err)
		return
	}
	if list == nil {
		list = []skill{}
	}
	writeJSON(w, map[string]any{"machine_id": m.cfg.ID, "machine": m.name(), "skills": list})
}
