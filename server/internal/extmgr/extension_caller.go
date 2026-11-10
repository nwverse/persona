package extmgr

import (
	"net/http"
	"strings"

	"github.com/nwverse/persona/server/internal/extwire"
)

// Who is calling an extension (docs/extensions/extensions.md "The caller on forwarded requests"): a request or WebSocket the manager
// forwards to an extension's own HTTP says which credential is calling, in what role and language, as the hub's auth
// found them, and nothing else of the hub's headers (scopes, address, owner): the extension decides what to open by
// these three. The hub drops whatever the client sent under X-Persona-* before it relays; the manager drops the hub's
// own here.

// dropPersonaHeaders removes every X-Persona-* header from hd.
func dropPersonaHeaders(hd http.Header) {
	for k := range hd {
		if strings.HasPrefix(strings.ToLower(k), "x-persona-") {
			delete(hd, k)
		}
	}
}

// setCallerHeaders tells the extension r is forwarded to who is calling: the credential's id ("" for the master
// token), its role (full or extension; answer never gets this far; the master token is full, as on the api and
// messaging points and as before) and the language the hub answers this request in.
func (h *Manager) setCallerHeaders(r *http.Request) {
	p := principalOf(r.Context())
	lang := h.requestLang(r)
	dropPersonaHeaders(r.Header)
	r.Header.Set(extwire.HeaderDevice, p.ID)
	r.Header.Set(extwire.HeaderRole, p.Role)
	r.Header.Set(extwire.HeaderLang, string(lang))
}
