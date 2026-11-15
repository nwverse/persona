// Package modelhttp identifies Persona's own requests to model gateways.
package modelhttp

import (
	"net/http"
	"strings"
)

var version = "dev"

// SetVersion is called at process startup with the version injected into the binary.
func SetVersion(v string) {
	if v != "" {
		version = v
	}
}

// UserAgent returns the identity sent to model gateways.
func UserAgent() string { return "Persona/" + version }

// SetDefaultUserAgent preserves a caller-supplied header, including an empty value.
func SetDefaultUserAgent(h http.Header) {
	if _, ok := h["User-Agent"]; ok {
		return
	}
	for name, values := range h {
		if strings.EqualFold(name, "User-Agent") {
			delete(h, name)
			h["User-Agent"] = values
			return
		}
	}
	h.Set("User-Agent", UserAgent())
}
