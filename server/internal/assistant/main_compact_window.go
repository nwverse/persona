package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nwverse/persona/server/internal/i18n"
)

// assistant.main_compact_window caps main, direction-lead and background-supervision conversations on managed
// launchers. Working Agents and ordinary conversations keep their launcher's window. The wire key retains its name
// for rolling upgrades; older nodes apply it only to main conversations.
const (
	defaultMainCompactWindow = 400_000
	// minMainCompactWindow: the main conversation's fixed part (instructions, tools, the snapshot) is tens of thousands of
	// tokens and Claude Code compacts about 33K short of the window, so a smaller window would compact every few turns.
	minMainCompactWindow = 100_000
	// maxMainCompactWindow only catches a mistyped number: a window at or above the model's changes nothing.
	maxMainCompactWindow = 10_000_000
)

func validMainCompactWindow(w int) bool {
	return w == 0 || w >= minMainCompactWindow && w <= maxMainCompactWindow
}

// mainCompactWindowOf is the window in effect: the saved one, else defaultMainCompactWindow; 0 follows the launcher.
func mainCompactWindowOf(v Settings) int {
	if w := v.MainCompactWindow; w != nil && validMainCompactWindow(*w) {
		return *w
	}
	return defaultMainCompactWindow
}

// putMainCompactWindow authoritatively scopes the existing cap, including a supervisor's first start before its id
// exists. A shared launcher must retain its larger window for working Agents.
func (s *Service) putMainCompactWindow(ctx context.Context, machine, id string, p map[string]any) {
	cfg := s.settings(ctx)
	main, _ := p["is_main"].(bool)
	supervisor := machine == cfg.MachineID && (s.isLeader(ctx, id) || s.isBackground(ctx, id) ||
		id == "" && ctx.Value(supervisionStartup{}) == true && p["guest"] == nil)
	if main || supervisor {
		p["main_compact_window"] = mainCompactWindowOf(cfg)
	} else {
		delete(p, "main_compact_window")
	}
}

func (s *Service) setMainCompactWindow(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	var n *int // null is refused, not read as 0: that would turn the cap off
	if err := json.Unmarshal(v, &n); err != nil || n == nil || !validMainCompactWindow(*n) {
		return nil, errors.New("value must be 0 (follow the launcher) or a whole number of tokens, 100000–10000000")
	}
	w := *n
	before := mainCompactWindowOf(s.settings(ctx))
	if w == before {
		return &ownChange{summary: "assistant.main_compact_window is unchanged"}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	label := func(l i18n.Lang, w int) string {
		if w == 0 {
			return i18n.T(l, "assistant-settings_tool.main_compact_window_launcher")
		}
		return i18n.T(l, "assistant-settings_tool.main_compact_window_tokens", i18n.Args{"tokens": w})
	}
	return &ownChange{cardText: func(l i18n.Lang) string {
		return i18n.T(l, "assistant-settings_tool.change_main_compact_window", i18n.Args{"before": label(l, before), "after": label(l, w)})
	},
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { v.MainCompactWindow = &w; return nil }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			return fmt.Sprintf("assistant.main_compact_window is %d from the main conversation's next message while it is idle.", w), nil
		}}, nil
}
