package hub

import (
	"net/http"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
)

// Attribution is observational, never an authentication or authorization input.
// Keep the basis: a native UA without an extension marker cannot prove the surface.
type trafficAttribution struct {
	platform, source, platformBasis, sourceBasis string
}

func surfaceToken(value string) string {
	value = strings.ToLower(value)
	compact := strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(value)
	switch {
	case strings.Contains(compact, "liveactivity"), strings.Contains(compact, "activitykit"):
		return "liveactivity"
	case strings.Contains(compact, "widget"):
		return "widget"
	case value == "app":
		return "app"
	default:
		return ""
	}
}

func (h *Hub) requestTrafficAttribution(r *http.Request, p principal) trafficAttribution {
	a := trafficAttribution{platform: "unknown", source: "unknown", platformBasis: "unknown", sourceBasis: "unknown"}
	client := requestClientOf(r.Context())
	if compat.HeaderConflict(r.Header, "X-Persona-Client") {
		client = requestClient{}
	} else if client.Platform == "" {
		client = parseRequestClient(compat.Header(r.Header, "X-Persona-Client"))
	}
	if platform := trafficPlatform(client.Platform); platform != "unknown" {
		a.platform, a.platformBasis = platform, "client-header"
	}
	if source := surfaceToken(client.Platform); source != "" {
		a.source, a.sourceBasis = source, "client-header"
	}
	// An explicit bounded surface hint is optional; old clients need no change.
	if !compat.HeaderConflict(r.Header, "X-Persona-Source") {
		value := compat.Header(r.Header, "X-Persona-Source")
		if len(value) > 32 {
			value = ""
		}
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case "app", "widget", "liveactivity":
			a.source, a.sourceBasis = value, "source-header"
		}
	}
	ua := r.UserAgent()
	if len(ua) > 512 {
		ua = ""
	} // do not retain an oversized, client-controlled string
	ua = strings.ToLower(ua)
	diagnostic := strings.HasPrefix(ua, "curl/") || strings.HasPrefix(ua, "python-") || strings.HasPrefix(ua, "go-http-client/") || strings.HasPrefix(ua, "wget/")
	browser := strings.Contains(ua, "mozilla/") || r.Header.Get("Sec-Fetch-Mode") != "" || r.Header.Get("Sec-CH-UA") != ""
	native := strings.Contains(ua, "cfnetwork/") || strings.Contains(ua, "darwin/") || strings.Contains(ua, "dalvik/") || strings.Contains(ua, "okhttp/")
	if a.platform == "unknown" {
		switch {
		case browser:
			a.platform, a.platformBasis = "web", "user-agent"
		case strings.Contains(ua, "android"), strings.Contains(ua, "dalvik/"), strings.Contains(ua, "okhttp/"):
			a.platform, a.platformBasis = "android", "user-agent"
		case strings.Contains(ua, "watchos"):
			a.platform, a.platformBasis = "watchos", "user-agent"
		case strings.Contains(ua, "visionos"):
			a.platform, a.platformBasis = "visionos", "user-agent"
		case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"), strings.Contains(ua, "ios/"):
			a.platform, a.platformBasis = "ios", "user-agent"
		case native && p.Kind == "phone":
			a.platform, a.platformBasis = "ios", "native-credential"
		case native && p.Kind == "watch":
			a.platform, a.platformBasis = "watchos", "native-credential"
		case native && p.Kind == "other":
			a.platform, a.platformBasis = "macos", "native-credential"
		case !diagnostic && p.Kind == "web":
			a.platform, a.platformBasis = "web", "credential"
		case !diagnostic && p.Kind == "watch":
			a.platform, a.platformBasis = "watchos", "credential"
		case !diagnostic && h.iphoneCredential(p.ID):
			a.platform, a.platformBasis = "ios", "push-registration"
		}
	}
	if a.source == "unknown" {
		switch {
		case diagnostic:
			a.source, a.sourceBasis = "diagnostic", "user-agent"
		case surfaceToken(ua) != "":
			a.source, a.sourceBasis = surfaceToken(ua), "user-agent"
		case native:
			a.source, a.sourceBasis = "app", "native-default"
		case browser || a.platform == "web":
			a.source, a.sourceBasis = "app", "browser"
		case a.platform != "unknown":
			a.source, a.sourceBasis = "app", "platform-default"
		}
	}
	return a
}

// A registered iPhone binding disambiguates older native clients without a header.
// It does not merge registrations, identify a physical device, or trust its address.
func (h *Hub) iphoneCredential(id string) bool {
	if h.push == nil || id == "" {
		return false
	}
	h.push.mu.Lock()
	defer h.push.mu.Unlock()
	for _, d := range h.push.devices {
		if d.CredentialID == id && d.Kind == "iphone" && d.APNSToken != "" {
			return true
		}
	}
	return false
}
