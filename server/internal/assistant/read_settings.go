package assistant

import (
	"context"
	"errors"
	"net/http"

	"github.com/nwverse/persona/server/internal/apierr"
)

// Manual mark as read (#7356): one switch for the whole hub, kept in the settings table so every app agrees. Off (the
// default, and what a hub that never stored it answers), opening a conversation or an Agent session marks it seen as
// before. On, only an explicit Mark as read clears Unread: the apps stop sending their automatic seen, and the
// manager ignores any seen that does not say it is explicit, so an older app or one that does not know the switch
// cannot clear it either (docs/agent-states.md "Seen").
const readSettingsKey = "sessions.read_settings"

type readSettings struct {
	Manual bool `json:"manual"`
}

func (s *Service) readSettingsRoutes() {
	s.handle("GET /api/v1/read-settings", s.getReadSettings)
	s.handle("PUT /api/v1/read-settings", s.putReadSettings)
}

// manualRead is the stored switch; without a store, or before anyone set it, it is off.
func (s *Service) manualRead(ctx context.Context) (bool, error) {
	st := s.store()
	if st == nil {
		return false, nil
	}
	var set readSettings
	_, err := st.Setting(ctx, readSettingsKey, &set)
	return set.Manual, err
}

// userSeen records an app's seen unless the switch is on and the app did not mark it explicitly. A switch that cannot
// be read counts as off: a seen is never lost to a database blip, only to the user's own choice.
func (s *Service) userSeen(ctx context.Context, machine, session string, explicit bool) bool {
	if !explicit {
		manual, err := s.manualRead(ctx)
		if err != nil {
			s.logf("read settings: %v", err)
		}
		if manual {
			return false
		}
	}
	s.markSeen(ctx, machine, session)
	return true
}

func (s *Service) writeReadSettings(w http.ResponseWriter, r *http.Request, manual bool) {
	apierr.WriteJSON(w, map[string]any{"manual": manual, "can_write": fullRole(r)})
}

func (s *Service) getReadSettings(w http.ResponseWriter, r *http.Request) {
	manual, err := s.manualRead(r.Context())
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, err)
		return
	}
	s.writeReadSettings(w, r, manual)
}

func (s *Service) putReadSettings(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, errors.New("a device that may only answer cannot change how items are marked read"))
		return
	}
	st := s.store()
	if st == nil {
		apierr.Write(w, http.StatusServiceUnavailable, errors.New("the read setting needs the database"))
		return
	}
	var body struct {
		Manual *bool `json:"manual"`
	}
	if err := decodeBody(w, r, &body, 4<<10); err != nil || body.Manual == nil {
		writeBodyErr(w, err, errors.New("manual is required"))
		return
	}
	if err := st.PutSetting(r.Context(), readSettingsKey, readSettings{Manual: *body.Manual}); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, err)
		return
	}
	s.event(r.Context(), map[string]any{"type": "read_settings", "manual": *body.Manual})
	s.writeReadSettings(w, r, *body.Manual)
}
