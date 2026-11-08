package hub

import (
	"strings"
	"time"
)

func (c *client) recordSocketTraffic(event, action string, bytes int64, elapsed time.Duration) {
	if c.hub == nil || c.device == "" {
		return
	}
	c.mu.Lock()
	a := c.traffic
	if platform := trafficPlatform(c.meta.Platform); platform != "unknown" {
		a.platform, a.platformBasis = platform, "client-hello"
	}
	c.mu.Unlock()
	if a.platform == "" {
		a.platform = "unknown"
	}
	c.hub.recordTraffic(trafficKey{minute: time.Now(), identity: trafficIdentity(c.device), registration: "none", platform: a.platform, source: a.source, platformBasis: a.platformBasis, sourceBasis: a.sourceBasis, route: "/api/v1/ws", event: event, action: action}, bytes, elapsed)
}

// notePush records a logical send result, not each retry. Only fixed protocol
// classifications and hashes enter the journal; the APS content is never logged.
func (p *Pusher) notePush(d Device, payload map[string]any, kind, route, action string, status int, bytes int64, elapsed time.Duration) {
	if p.traffic == nil {
		return
	}
	event := trafficPushEvent(payload, kind)
	source := "app"
	if strings.HasPrefix(event, "liveactivity") {
		source = "liveactivity"
	} else if event == "background_widget" {
		source = "widget"
	}
	p.traffic(trafficKey{minute: time.Now(), identity: trafficIdentity(d.CredentialID), registration: trafficIdentity(d.DeviceID), platform: trafficPlatform(d.Kind), source: source, platformBasis: "push-registration", sourceBasis: "push-kind", route: route, event: event, action: action, status: status}, bytes, elapsed)
}

// Capture the classification before asynchronous sends, so diagnostics never
// retain or read a caller's payload after sending starts.
func trafficPushEvent(payload map[string]any, kind string) string {
	event := kind
	if kind == "liveactivity" {
		aps, _ := payload["aps"].(map[string]any)
		name, _ := aps["event"].(string)
		switch name {
		case "start", "update", "end":
			event += "_" + name
		default:
			event += "_unknown"
		}
	}
	if kind == "background" {
		wake, _ := payload["persona_wake"].(string)
		switch {
		case payload["resolved_interaction_ids"] != nil:
			event += "_withdraw"
		case payload["extension_widget"] != nil || strings.HasPrefix(wake, "widget"):
			event += "_widget"
		case payload["persona_wake"] != nil:
			event += "_wake"
		default:
			event += "_other"
		}
	}
	return event
}

func (p *Pusher) noteRelayResult(d Device, payload map[string]any, kind string, err error, gone bool, bytes int64, elapsed time.Duration) {
	action, status := "accepted", 200
	if err != nil {
		action, status = "failed", 0
	} else if gone {
		action, status = "failed", 410
	}
	p.notePush(d, payload, kind, "relay", action, status, bytes, elapsed)
}

func (p *Pusher) noteDirectResult(d Device, payload map[string]any, kind string, err error, status int, bytes int64, elapsed time.Duration) {
	action := "accepted"
	if err != nil {
		action, status = "failed", 0
	} else if status != 200 {
		action = "failed"
	}
	p.notePush(d, payload, kind, "apns", action, status, bytes, elapsed)
}
