package assistantwire

import (
	"net/http"
	"strings"
)

const (
	// ManagerHeader explicitly names the manager an API request concerns.
	ManagerHeader = "X-Persona-Manager"
	// ManagerQuery names the same target on URLs that cannot carry a header.
	ManagerQuery = "manager"
)

// NamedManager preserves the API's header-before-query routing precedence.
// An empty result means the caller did not explicitly name a target.
func NamedManager(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get(ManagerHeader)); id != "" {
		return id
	}
	return strings.TrimSpace(r.URL.Query().Get(ManagerQuery))
}
