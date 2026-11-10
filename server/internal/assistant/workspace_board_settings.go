package assistant

import (
	"context"
	"errors"
	"net/http"

	"github.com/nwverse/persona/server/internal/apierr"
)

// The workflow board is off unless turned on (#2475): one switch for the whole hub (the manager keeps it), kept in the settings table so every
// browser agrees. Turning it off only hides it; the board and the workspace snapshots stay as they were.
const workspaceBoardSettingsKey = "workspace_board.settings"

type workspaceBoardSettings struct {
	Enabled bool `json:"enabled"`
}

// workspaceBoardUsage says whether the board was ever changed. Only a store that keeps the board can tell.
type workspaceBoardUsage interface {
	WorkspaceBoardInUse(context.Context) (bool, error)
}

func (s *Service) workspaceBoardSettingsRoutes() {
	s.handle("GET /api/v1/workspace-board/settings", s.getWorkspaceBoardSettings)
	s.handle("PUT /api/v1/workspace-board/settings", s.putWorkspaceBoardSettings)
}

// workspaceBoardEnabled is the stored switch; before anyone set it, a board that was already used counts as on, so an
// upgrade does not take away a board someone keeps (docs/web/workspace-board.md "Toggle"). It is not written back: the first
// explicit change is what gets stored.
func (s *Service) workspaceBoardEnabled(ctx context.Context) (bool, error) {
	st := s.store()
	if st == nil {
		return false, errors.New("工作流看板的开关需要数据库")
	}
	var set workspaceBoardSettings
	found, err := st.Setting(ctx, workspaceBoardSettingsKey, &set)
	if err != nil || found {
		return set.Enabled, err
	}
	if u, ok := st.(workspaceBoardUsage); ok {
		return u.WorkspaceBoardInUse(ctx)
	}
	return false, nil
}

func (s *Service) writeWorkspaceBoardSettings(w http.ResponseWriter, r *http.Request, enabled bool) {
	apierr.WriteJSON(w, map[string]any{"enabled": enabled, "can_write": fullRole(r)})
}

func (s *Service) getWorkspaceBoardSettings(w http.ResponseWriter, r *http.Request) {
	on, err := s.workspaceBoardEnabled(r.Context())
	if err != nil {
		apierr.Write(w, 503, err)
		return
	}
	s.writeWorkspaceBoardSettings(w, r, on)
}

func (s *Service) putWorkspaceBoardSettings(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, 403, errors.New("只读设备不能修改工作流看板的开关"))
		return
	}
	st := s.store()
	if st == nil {
		apierr.Write(w, 503, errors.New("工作流看板的开关需要数据库"))
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeBody(w, r, &body, 4<<10); err != nil || body.Enabled == nil {
		writeBodyErr(w, err, errors.New("需要 enabled"))
		return
	}
	if err := st.PutSetting(r.Context(), workspaceBoardSettingsKey, workspaceBoardSettings{Enabled: *body.Enabled}); err != nil {
		apierr.Write(w, 503, err)
		return
	}
	s.event(r.Context(), map[string]any{"type": "workspace_board_settings", "enabled": *body.Enabled})
	s.writeWorkspaceBoardSettings(w, r, *body.Enabled)
}
