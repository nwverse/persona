package hub

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

// The routes of partner hubs (docs/server/partner-hubs.md "Routes"): the two between hubs (the link, pairing), and
// the apps' (full credentials only): the list, a code, pairing with a code, the dial setting, unpairing. The command
// line has the apps' under /api/v1/admin/cli/partners (partnerCLIRoutes).

func (h *Hub) partnerRoutes(api *http.ServeMux) {
	api.HandleFunc("GET "+partnerLinkPath, h.servePartnerLink)
	api.HandleFunc("POST "+partnerPairPath, h.servePartnerPair)
	api.HandleFunc("GET /api/v1/partner-hubs", h.servePartnerHubs)
	api.HandleFunc("POST /api/v1/partner-hubs/code", h.servePartnerCode)
	api.HandleFunc("POST /api/v1/partner-hubs", h.servePartnerAdd)
	api.HandleFunc("PATCH /api/v1/partner-hubs/{hub_id}", h.servePartnerDial)
	api.HandleFunc("DELETE /api/v1/partner-hubs/{hub_id}", h.servePartnerRemove)
}

// partnerCLIRoutes are the apps' routes for `personad hub partner …`, on peerCLIRoutes' mux (/api/v1/admin/cli/ becomes
// /api/v1/admin/ there).
func (h *Hub) partnerCLIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/partners", h.servePartnerHubs)
	mux.HandleFunc("POST /api/v1/admin/partners/code", h.servePartnerCode)
	mux.HandleFunc("POST /api/v1/admin/partners", h.servePartnerAdd)
	mux.HandleFunc("PATCH /api/v1/admin/partners/{hub_id}", h.servePartnerDial)
	mux.HandleFunc("DELETE /api/v1/admin/partners/{hub_id}", h.servePartnerRemove)
}

// partnerHubView is a partner as the apps see it.
type partnerHubView struct {
	HubID string `json:"hub_id"`
	Name  string `json:"name"`
	FP    string `json:"fp"`
	// State: connected (a link is up), refused (the partner does not take this hub's credential), connecting (this hub
	// dials it), offline (it waits for the partner to dial).
	State    string     `json:"state"`
	Dial     string     `json:"dial"`
	Since    *time.Time `json:"since,omitempty"`     // the link's
	LastSeen *time.Time `json:"last_seen,omitempty"` // when a link was last up
	Error    string     `json:"error,omitempty"`     // why the last dial failed, in the request's language
	DialedBy string     `json:"dialed_by"`           // us, them, or "" without a link
	RTTMS    *int       `json:"rtt_ms,omitempty"`
	Created  time.Time  `json:"created_at"`
}

func (h *Hub) partnerView(p db.PartnerHub, l i18n.Lang) partnerHubView {
	v := partnerHubView{HubID: p.HubID, Name: p.Name, FP: p.FP, Dial: partnerDialOf(p), LastSeen: p.SeenAt, Created: p.CreatedAt}
	ps := &h.partners
	ps.mu.Lock()
	link := ps.links[p.HubID]
	fail := ps.dial[p.HubID]
	ps.mu.Unlock()
	switch {
	case link != nil && link.ctx.Err() == nil:
		since := link.since
		v.State, v.Since, v.DialedBy = "connected", &since, "them"
		if link.dialer == h.hubID() {
			v.DialedBy = "us"
		}
		if ms := link.rttMS(); ms > 0 {
			v.RTTMS = &ms
		}
		return v
	case fail.refused:
		v.State = "refused"
	case partnerDialable(p) && h.partnersOn():
		v.State = "connecting"
	default:
		v.State = "offline"
	}
	if fail.err != nil {
		v.Error = partnerErrText(fail.err, l)
	} else if v.Dial == assistantwire.PartnerDialAuto && !partnerDialable(p) {
		v.Error = i18n.T(l, "hub-partner_dial.no_tls_address")
	}
	return v
}

// partnerViews are the partners, by name.
func (h *Hub) partnerViews(l i18n.Lang) []partnerHubView {
	ps := &h.partners
	ps.mu.Lock()
	rows := make([]db.PartnerHub, 0, len(ps.rows))
	for _, p := range ps.rows {
		rows = append(rows, p)
	}
	ps.mu.Unlock()
	slices.SortFunc(rows, func(a, b db.PartnerHub) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.HubID, b.HubID))
	})
	out := make([]partnerHubView, 0, len(rows))
	for _, p := range rows {
		out = append(out, h.partnerView(p, l))
	}
	return out
}

// writePartnerErr answers a partner route's refusal with its status and code, the text in the request's language.
func writePartnerErr(w http.ResponseWriter, err error) {
	var pe *partnerError
	if errors.As(err, &pe) {
		writeErrCode(w, pe.status, pe.code, pe.err)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

// partnersAvailable answers the refusal when partner hubs cannot be used here now (off, a mesh member).
func (h *Hub) partnersAvailable(w http.ResponseWriter) bool {
	if err := h.partnersReady(); err != nil {
		writePartnerErr(w, err)
		return false
	}
	return true
}

func (h *Hub) servePartnerHubs(w http.ResponseWriter, r *http.Request) {
	on := h.partnersOn()
	writeJSON(w, map[string]any{"enabled": on, "member": h.isMember(), "can_issue": on && h.selfPartnerInfo().canIssue(),
		"hubs": h.partnerViews(h.requestLang(r))})
}

func (h *Hub) servePartnerCode(w http.ResponseWriter, r *http.Request) {
	code, err := h.newPartnerCode(r.Context())
	if err != nil {
		writePartnerErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, code)
}

func (h *Hub) servePartnerAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSONBody(w, r, &req, 16<<10); err != nil || strings.TrimSpace(req.Code) == "" {
		writeBodyErr(w, err, i18n.Err("hub-partner_pair.not_a_code"))
		return
	}
	// Not the request's: an app that goes away mid-pairing must not leave the other hub paired and this one not.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	p, err := h.joinPartner(ctx, req.Code)
	if err != nil {
		writePartnerErr(w, err)
		return
	}
	// The link comes up within moments: answer with it when it does.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for h.partnerLinkTo(p.HubID) == nil {
		select {
		case <-ctx.Done():
		case <-deadline.C:
		case <-h.partnerWait():
			continue
		}
		break
	}
	row, _ := h.partnerRow(p.HubID)
	writeJSON(w, h.partnerView(row, h.requestLang(r)))
}

func (h *Hub) servePartnerDial(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dial string `json:"dial"`
	}
	if err := decodeJSONBody(w, r, &req, 4<<10); err != nil || (req.Dial != assistantwire.PartnerDialAuto && req.Dial != assistantwire.PartnerDialNever) {
		writeBodyErr(w, err, i18n.Err("hub-partner_routes.bad_dial"))
		return
	}
	id := r.PathValue("hub_id")
	st := h.partnerStore()
	if st == nil {
		writePartnerErr(w, errPartnerNoStore)
		return
	}
	if !h.partnersLoaded() { // not "not found": the pairings are not read yet
		writePartnerErr(w, errPartnerLoading)
		return
	}
	ok, err := st.SetPartnerDial(r.Context(), id, req.Dial)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	ps := &h.partners
	ps.mu.Lock()
	row, paired := ps.rows[id]
	if paired && ok {
		row.Dial = req.Dial
		ps.rows[id] = row
		delete(ps.dial, id) // what dialing it said is of the old setting
	}
	ps.mu.Unlock()
	if !ok || !paired {
		writePartnerErr(w, errPartnerNotFound)
		return
	}
	// Never: the dialer ends when it would dial next (a link it has stays up until it goes); auto: one starts.
	h.startPartnerDialer(id)
	h.partnersChanged()
	writeJSON(w, h.partnerView(row, h.requestLang(r)))
}

func (h *Hub) servePartnerRemove(w http.ResponseWriter, r *http.Request) {
	// Not the request's context: an unpairing half done (the pairing forgotten, its credential not revoked) is worse
	// than one that finishes after the app went away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	if h.partnerStore() != nil && !h.partnersLoaded() {
		writePartnerErr(w, errPartnerLoading)
		return
	}
	ok, err := h.unpairPartner(ctx, r.PathValue("hub_id"), true)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writePartnerErr(w, errPartnerNotFound)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
