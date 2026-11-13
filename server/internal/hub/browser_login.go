package hub

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// Browser challenges are short-lived handshakes, like passwordLoginState. Durable
// device credentials are issued only after approval and are stored in Postgres.
type browserLoginFlow struct {
	Secret, Name, State, ApproverHash, Token, DeviceID string
	Until                                              time.Time
}
type browserLoginState struct {
	mu    sync.Mutex
	flows map[string]*browserLoginFlow
}

func (h *Hub) browserLoginRoutes(mux *http.ServeMux) {
	routes := http.NewServeMux()
	routes.HandleFunc("POST /api/v1/auth/browser-login/start", func(w http.ResponseWriter, r *http.Request) {
		if h.passwordLimited(w, r, true) {
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if !passwordJSON(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || len([]rune(in.Name)) > 80 {
			writeErr(w, 400, errors.New("invalid browser name"))
			return
		}
		s := &h.browserLogin
		s.mu.Lock()
		defer s.mu.Unlock()
		for id, f := range s.flows {
			if !time.Now().Before(f.Until) {
				delete(s.flows, id)
			}
		}
		if len(s.flows) >= passwordFlowLimit {
			writeErr(w, 429, errors.New("too many pending sign-ins"))
			return
		}
		if s.flows == nil {
			s.flows = map[string]*browserLoginFlow{}
		}
		id, secret := randomToken(), randomToken()
		f := &browserLoginFlow{Secret: hashToken(secret), Name: in.Name, State: "pending", Until: time.Now().Add(passwordFlowLifetime)}
		link := "persona://browser-login?" + url.Values{"hub": {h.hubID()}, "request": {id}}.Encode()
		qr, err := newQRImage(link)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		s.flows[id] = f
		writeJSON(w, map[string]any{"id": id, "secret": secret, "expires_at": f.Until, "qr": qr, "url": link})
	})
	routes.HandleFunc("POST /api/v1/auth/browser-login/poll", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		}
		if !passwordJSON(w, r, &in) {
			return
		}
		s := &h.browserLogin
		s.mu.Lock()
		defer s.mu.Unlock()
		f := s.flows[in.ID]
		if f == nil || !time.Now().Before(f.Until) || subtle.ConstantTimeCompare([]byte(f.Secret), []byte(hashToken(in.Secret))) != 1 {
			writeErr(w, 410, errors.New("sign-in expired"))
			return
		}
		if f.State != "approved" {
			writeJSON(w, map[string]any{"state": f.State})
			return
		}
		approver, err := h.store.DeviceTokenByHash(r.Context(), f.ApproverHash)
		if err != nil {
			writeErr(w, 503, err)
			return
		}
		if approver == nil || approver.Role != roleFull {
			writeErr(w, 403, errors.New("approving device is no longer authorized"))
			return
		}
		if f.Token == "" {
			token, id := "jd_"+randomHex(24), "dev_"+randomHex(6)
			if err := h.store.CreateDeviceToken(r.Context(), db.DeviceToken{ID: id, Name: f.Name, Kind: "web", Role: roleFull}, hashToken(token)); err != nil {
				writeErr(w, 503, err)
				return
			}
			f.Token, f.DeviceID = token, id
			h.notifyPairing(f.Name, id)
		}
		// The same browser can retry a lost response without creating another device.
		writeJSON(w, map[string]any{"state": "approved", "token": f.Token, "id": f.DeviceID})
	})
	approval := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := principalOf(r.Context())
		if !mayTrust(who) || who.ID == "" {
			writeErr(w, 403, errors.New("a signed-in full device must approve"))
			return
		}
		var in struct {
			ID    string `json:"id"`
			Hub   string `json:"hub"`
			Allow *bool  `json:"allow,omitempty"`
		}
		if !passwordJSON(w, r, &in) {
			return
		}
		if in.Hub != h.hubID() {
			writeErr(w, 400, errors.New("this code belongs to another hub"))
			return
		}
		s := &h.browserLogin
		s.mu.Lock()
		defer s.mu.Unlock()
		f := s.flows[in.ID]
		if f == nil || !time.Now().Before(f.Until) {
			writeErr(w, 410, errors.New("sign-in expired"))
			return
		}
		if in.Allow != nil {
			if f.State != "pending" && f.State != "scanned" {
				writeErr(w, 409, errors.New("sign-in already decided"))
				return
			}
			f.State = "denied"
			if *in.Allow {
				f.State = "approved"
				f.ApproverHash = hashToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			}
		} else if f.State == "pending" {
			f.State = "scanned"
		}
		writeJSON(w, map[string]any{"name": f.Name, "state": f.State, "expires_at": f.Until})
	})
	routes.Handle("POST /api/v1/auth/browser-login/approve", h.gate(h.auth(approval)))
	mux.Handle("/api/v1/auth/browser-login/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if h.memberRefuses(w) {
			return
		}
		if r.TLS == nil && !remoteIP(r).IsLoopback() {
			writeErrCode(w, 403, "tls_required", errors.New("HTTPS is required"))
			return
		}
		if code, err := adminWriteCheck(r); err != nil {
			writeErrCode(w, 403, code, err)
			return
		}
		if h.store == nil {
			writeErr(w, 503, errors.New("database unavailable"))
			return
		}
		routes.ServeHTTP(w, r)
	}))
}
