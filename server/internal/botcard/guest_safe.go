package botcard

import (
	"net"
	"net/url"
	"strings"
	"unicode"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// GuestSafeText is text as it may be shown to people who are not the owner: local paths and links into the owner's
// hub are replaced, control characters dropped. Idempotent.
func GuestSafeText(text string, privateHosts []string) string {
	if text == "" {
		return ""
	}
	text = guestPath.ReplaceAllString(text, "[path]")
	text = guestURL.ReplaceAllStringFunc(text, func(m string) string {
		if guestURLPrivate(m, privateHosts) {
			return "[link]"
		}
		return m
	})
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
}

// GuestSafeToolName is a tool's name as a guest may see it: lower-case letters, digits and "_", at most 40.
func GuestSafeToolName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if b.Len() >= 40 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			continue // anything else is dropped, never shown
		default:
			continue
		}
	}
	out := b.String()
	if out == "" {
		return "tool"
	}
	return out
}

// guestPath matches an absolute path under a home or temp root, up to the next whitespace or closing bracket or quote.
var guestPath = lazyre.New(`(?:~|(?:[A-Za-z]:)?(?:\\|/)+(?:Users|home|root|private|var[/\\]+folders|tmp)[/\\])[^\s"'\])>]*`)

// guestURL matches a URL with a scheme. Its host is checked afterwards: only the owner's own are replaced.
var guestURL = lazyre.New(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'\])>]*`)

// guestURLPrivate says whether the URL is one a guest must not see: the owner's hub, a machine on the local network,
// or the app's own deep link.
func guestURLPrivate(raw string, privateHosts []string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" && u.Scheme == "" {
		return false
	}
	if compat.IsAppScheme(strings.ToLower(u.Scheme)) {
		return true
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	for _, h := range privateHosts {
		if strings.EqualFold(host, hostOnly(h)) {
			return true
		}
	}
	if strings.HasSuffix(host, ".local") || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// hostOnly is a host with its port taken off, as the hub's public URL may carry one.
func hostOnly(h string) string {
	if u, err := url.Parse(h); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	host, _, err := net.SplitHostPort(h)
	if err != nil {
		return h
	}
	return host
}
