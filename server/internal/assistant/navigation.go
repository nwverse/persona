package assistant

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/nwverse/persona/server/internal/apierr"
)

// The user's navigation choices (#9071, docs/protocol/protocol.md "Navigation"): the page the apps open on. The default
// manager keeps them for the hub, and a hub has one owner, so they are the user's: every device of theirs follows. Kept
// in the settings table under navigationKey (a key of its own: no migration). A field never stored is left out of the
// answer, so an app tells "never chosen" from "the default chosen" (an empty start) and may hand over a choice it kept
// while there was no record (docs/web/web.md "Start page").
const navigationKey = "navigation"

// navigation is the stored record. A PUT changes only the fields it carries.
type navigation struct {
	// Start is the page the apps open on: "" for the default (the main conversation), else a page's key as the web's
	// sidebar names it (`home`, `inbox`, `todos`, …, or `p:<extension>/<page>` for an extension's nav_page).
	Start *string `json:"start,omitempty"`
}

// startPagePattern is a start page's shape: a built-in page's key, or an extension page's `p:<extension>/<page>`. Which
// pages there are is the apps' business (an extension's page comes and goes): an app opens on the default for a key it
// does not know or a page that is not there.
var startPagePattern = regexp.MustCompile(`^(?:[a-z][a-z0-9-]{0,63}|p:[^/\x00-\x20\x7f]{1,128}/[^/\x00-\x20\x7f]{1,128})$`)

func validStartPage(s string) bool { return s == "" || startPagePattern.MatchString(s) }

func (s *Service) navigationRoutes() {
	s.handle("GET /api/v1/navigation", s.getNavigation)
	s.handle("PUT /api/v1/navigation", s.putNavigation)
}

func (s *Service) getNavigation(w http.ResponseWriter, r *http.Request) {
	var n navigation
	if st := s.store(); st != nil {
		if _, err := st.Setting(r.Context(), navigationKey, &n); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
	}
	apierr.WriteJSON(w, n)
}

func (s *Service) putNavigation(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, errors.New("only a device with full access may change the start page"))
		return
	}
	var body navigation
	if err := decodeBody(w, r, &body, 4<<10); err != nil || body.Start == nil {
		writeBodyErr(w, err, errors.New("start is required"))
		return
	}
	if !validStartPage(*body.Start) {
		apierr.Write(w, http.StatusBadRequest, errors.New("start must be empty, a page's key or p:<extension>/<page>"))
		return
	}
	st := s.store()
	if st == nil {
		apierr.Write(w, http.StatusServiceUnavailable, errors.New("the start page needs a database"))
		return
	}
	s.navMu.Lock()
	defer s.navMu.Unlock()
	var n navigation
	if _, err := st.Setting(r.Context(), navigationKey, &n); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, err)
		return
	}
	n.Start = body.Start
	if err := st.PutSetting(r.Context(), navigationKey, n); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, err)
		return
	}
	// every app follows at once: its copy decides the next opening before the hub answers
	s.event(r.Context(), map[string]any{"type": "navigation", "start": *n.Start})
	apierr.WriteJSON(w, n)
}
