package hub

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"
)

// The first-run setup code (docs/operations/admin.md "Admin password and sessions"). A request from this machine's loopback may be a browser here, or
// the internet through a reverse proxy on this machine that adds no forwarding header (nginx's proxy_pass by
// default): both look the same. So setting the first password from loopback takes a one-time code that only someone
// who can work on the hub's machine sees: the hub logs one when it starts without a password, and
// `personad hub admin setup-code` gets a fresh one. LAN sources do not need it, unless hub.json says admin.first_setup
// "code" (new installs from `personad hub setup`: on a shared LAN anyone could otherwise set the password first);
// the installer prints the admin URL with the code in its fragment (#setup=…), which the page reads.

const (
	setupCodeLifetime = 10 * time.Minute
	setupCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0 / O, 1 / I: read off a terminal
)

type setupCode struct {
	hash    string
	expires time.Time
}

// newSetupCode replaces any earlier code: XXXX-XXXX, 40 random bits, ten minutes, one use.
func (h *Hub) newSetupCode() (string, time.Time) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	var code strings.Builder
	for i, c := range b {
		if i == 4 {
			code.WriteByte('-')
		}
		code.WriteByte(setupCodeAlphabet[int(c)%len(setupCodeAlphabet)])
	}
	expires := h.adminNow().Add(setupCodeLifetime)
	h.admin.mu.Lock()
	h.admin.setup = &setupCode{hash: sha256Hex(normalizeSetupCode(code.String())), expires: expires}
	h.admin.mu.Unlock()
	return code.String(), expires
}

func normalizeSetupCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
}

// setupCodeRequired: setting the first password from loopback (the connection's own address, whatever headers say),
// or from anywhere when admin.first_setup is "code".
func (h *Hub) setupCodeRequired(r *http.Request) bool {
	return remoteIP(r).IsLoopback() || h.cfg.Admin.FirstSetup == firstSetupCode
}

// useSetupCode checks a code and spends it when it is right.
func (h *Hub) useSetupCode(code string) bool {
	h.admin.mu.Lock()
	defer h.admin.mu.Unlock()
	c := h.admin.setup
	if c == nil || !h.adminNow().Before(c.expires) || code == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(sha256Hex(normalizeSetupCode(code))), []byte(c.hash)) != 1 {
		return false
	}
	h.admin.setup = nil
	return true
}

// setupCodeLive is whether an unspent, unexpired code exists.
func (h *Hub) setupCodeLive() bool {
	h.admin.mu.Lock()
	defer h.admin.mu.Unlock()
	return h.admin.setup != nil && h.adminNow().Before(h.admin.setup.expires)
}

func (h *Hub) logSetupCode(code string, expires time.Time) {
	where := "from this machine (loopback)"
	if h.cfg.Admin.FirstSetup == firstSetupCode {
		where = "(admin.first_setup is \"code\": from any source)"
	}
	h.logf("admin: first-run setup code %s (10 minutes, until %s): needed to set the admin password %s; "+
		"personad hub admin setup-code prints a new one", code, expires.UTC().Format(time.RFC3339), where)
}

// adminStartup logs a setup code when the hub starts without an administrator password.
func (h *Hub) adminStartup(ctx context.Context) {
	store, ok := h.adminDB()
	if !ok {
		return
	}
	hash, err := store.AdminPasswordHash(ctx)
	if err != nil || hash != "" {
		return
	}
	h.logSetupCode(h.newSetupCode())
}

// setupCodeRoute is `personad hub admin setup-code`: a fresh code, for the master token over loopback only.
func (h *Hub) setupCodeRoute(open *http.ServeMux) {
	open.HandleFunc("POST /api/v1/admin/setup-code", func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !remoteIP(r).IsLoopback() || tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(h.cfg.Token)) != 1 {
			writeErrCode(w, http.StatusForbidden, "forbidden", errors.New("setup codes need the master token over loopback"))
			return
		}
		store, ok := h.adminDB()
		if !ok {
			writeErr(w, http.StatusServiceUnavailable, errors.New("no database"))
			return
		}
		hash, err := store.AdminPasswordHash(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if hash != "" {
			writeErrCode(w, http.StatusConflict, "already_set", errors.New("the administrator password is already set: sign in, or personad hub admin reset-password"))
			return
		}
		code, expires := h.newSetupCode()
		h.logf("admin: a new first-run setup code was printed by personad hub admin setup-code")
		writeJSON(w, map[string]any{"code": code, "expires_at": expires.UTC()})
	})
}
