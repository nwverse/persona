package link

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CloudflareAccess is the service token for exactly one HTTPS reverse-proxy origin.
type CloudflareAccess struct {
	Origin       string `json:"origin"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func accessHost(u *url.URL) string {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func (a CloudflareAccess) valid() bool {
	u, err := url.Parse(a.Origin)
	if err != nil || accessHost(u) == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return false
	}
	for _, v := range []string{a.ClientID, a.ClientSecret} {
		if v == "" || strings.IndexFunc(v, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
			return false
		}
	}
	return true
}

func SetCloudflareAccess(path string, a *CloudflareAccess) error {
	if a != nil && !a.valid() {
		return newTextError("link-errors.access_token_invalid", nil)
	}
	return Update(path, func(c *Config) error { c.CloudflareAccess = a; return nil })
}

type accessTransport struct {
	*http.Transport
	access CloudflareAccess
}

func (t *accessTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	origin, _ := url.Parse(t.access.Origin)
	if accessHost(r.URL) == accessHost(origin) {
		r = r.Clone(r.Context())
		r.Header.Set("CF-Access-Client-Id", t.access.ClientID)
		r.Header.Set("CF-Access-Client-Secret", t.access.ClientSecret)
	}
	return t.Transport.RoundTrip(r)
}
