package hub

// PartnersConfig tunes partner hubs (docs/server/partner-hubs.md, partner_*.go): independent hubs paired by their
// owners so their assistants can message each other. The pairings and their dial settings are in Postgres
// (partner_hubs); this is the switch and the outbound proxy, read at start.
type PartnersConfig struct {
	// Enabled: false turns partner hubs off on this hub (no links, no pairing; the pairings are kept). Default on.
	Enabled *bool `json:"enabled,omitempty"`
	// Proxy is how this hub dials its partners: "off" (direct, not even the environment's proxy), an http:// or
	// https:// CONNECT proxy, a socks5:// or socks5h:// one (user:pass@ for credentials), or "" for the environment's
	// (https_proxy, HTTPS_PROXY, all_proxy, ALL_PROXY; NO_PROXY applies). One that does not read fails every dial.
	Proxy string `json:"proxy,omitempty"`
	// ProxyAlways: dial only through the proxy, never direct.
	ProxyAlways bool `json:"proxy_always,omitempty"`
}

func (c PartnersConfig) enabled() bool { return c.Enabled == nil || *c.Enabled }
