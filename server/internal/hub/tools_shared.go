package hub

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/textfmt"
)

// What the hub's own features use of what used to be the assistant's tools (the assistant's are internal/assistant,
// tools.go): finding an agent by name (the channels, computer use), and the formatting both write with (one copy,
// internal/textfmt).

// The formatting the assistant writes with too.
var (
	clip    = textfmt.Clip
	oneLine = textfmt.OneLine
	// plainMarkdown is Markdown as the text it shows, for a push or a one-line preview.
	plainMarkdown = textfmt.PlainMarkdown
	planTitle     = textfmt.PlanTitle
)

func (h *Hub) machineByName(id string) *Machine {
	if m := h.localMachine(id); m != nil {
		return m
	}
	for _, m := range h.machinesNow() {
		if strings.EqualFold(m.name(), id) || strings.EqualFold(m.cfg.SSHHost, id) {
			return m
		}
	}
	return nil
}

// findAgent resolves a session by full id or prefix; an empty machine searches all of them.
func (h *Hub) findAgent(machine, id string) (*Machine, proto.Session, error) {
	id = strings.TrimSpace(id)
	if len(id) < 6 {
		return nil, proto.Session{}, fmt.Errorf("session_id is required (at least its first 8 characters)")
	}
	var found *Machine
	var session proto.Session
	for _, m := range h.machinesNow() {
		if machine != "" && h.machineByName(machine) != m {
			continue
		}
		for _, s := range h.decorated(m) {
			if s.SessionID == id || strings.HasPrefix(s.SessionID, id) {
				if found != nil {
					return nil, proto.Session{}, fmt.Errorf("session_id %s is ambiguous; use the full id and machine", id)
				}
				found, session = m, s
			}
		}
	}
	if found != nil {
		return found, session, nil
	}
	return nil, proto.Session{}, fmt.Errorf("no agent %s (use agents_list)", id)
}

// compactJSON drops media / avatar noise and cuts long answers so they fit in the model's context.
func compactJSON(raw json.RawMessage, max int) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return clip(string(raw), max)
	}
	v = prune(v)
	b, _ := json.Marshal(v)
	if len(b) > max {
		return string(b[:max]) + "…(cut; narrow the query or page)"
	}
	return string(b)
}

func prune(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "avatar") || strings.HasSuffix(lk, "thumbnail") || lk == "highlights" || lk == "emojikey" {
				delete(t, k)
				continue
			}
			if val == nil || val == "" || val == false {
				delete(t, k)
				continue
			}
			t[k] = prune(val)
		}
	case []any:
		for i := range t {
			t[i] = prune(t[i])
		}
	}
	return v
}
