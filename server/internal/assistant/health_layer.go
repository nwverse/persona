package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/healthhost"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// The Health module (internal/modules/health, docs/architecture/core-and-modules.md "Health"): Health intake, the water
// reminders, the intake page, the legacy water import and the health_intake tool. The core reaches it only through
// internal/healthhost; s.healthMod is nil when the module is left out of the build (persona_without_health,
// persona_core_only) or switched off (the modules row), and then the core serves no intake route, announces no
// health_intake or health_items capability, offers no health_intake tool and sends no water reminder. The Health space (health.go)
// stays in the core.

// healthRoutes makes the module's layer, unless the build has no module or the modules row switches it off (the port
// asks the switch, moduleswitch, as every provider seam does), and registers its routes in the core's order.
func (s *Service) healthRoutes() {
	if l, ok := healthhost.New(s.moduleSwitch, healthHost{s}); ok {
		s.healthMod = l
		l.Routes(s.handle)
	}
}

// healthOn says whether the module is loaded.
func (s *Service) healthOn() bool { return s.healthMod != nil }

// healthReminders runs the module's water reminders until ctx ends (none without the module).
func (s *Service) healthReminders(ctx context.Context) {
	if s.healthMod != nil {
		s.healthMod.ReminderLoop(ctx)
	}
}

// healthTool runs the health_intake tool.
func (s *Service) healthTool(ctx context.Context, args json.RawMessage) (string, error) {
	if s.healthMod == nil {
		return "", errors.New("unknown tool " + healthhost.Tool)
	}
	return s.healthMod.Tool(ctx, args)
}

// withHealth removes the intake and items capabilities when svc has no Health layer (or, before construction,
// when the build has no Health module), so the hub announces neither feature.
func withHealth(caps []string, svc *Service) []string {
	on := healthhost.InBuild(moduleswitch.Set{})
	if svc != nil {
		on = svc.healthOn()
	}
	if on {
		return caps
	}
	return slices.DeleteFunc(caps, func(c string) bool {
		return c == assistantwire.CapabilityHealthIntake || c == assistantwire.CapabilityHealthItems
	})
}

// healthHost is the core as the Health module reaches it (healthhost.Host).
type healthHost struct{ s *Service }

func (h healthHost) Logf(format string, args ...any) { h.s.logf(format, args...) }

func (h healthHost) Settings() healthhost.Settings {
	if st := h.s.store(); st != nil {
		return st
	}
	return nil
}

func (h healthHost) IntakeStore() healthhost.IntakeStore {
	st, _ := h.s.store().(healthhost.IntakeStore)
	return st
}

func (h healthHost) ImportStore() healthhost.ImportStore {
	st, _ := h.s.store().(healthhost.ImportStore)
	return st
}

func (h healthHost) WaterExtensions() healthhost.WaterExtensions {
	e, _ := h.s.hub.(waterExtensions)
	return e
}

func (h healthHost) Event(ctx context.Context, ev map[string]any) { h.s.event(ctx, ev) }

func (h healthHost) Lang(ctx context.Context) i18n.Lang { return h.s.lang(ctx) }

func (h healthHost) Timezone(ctx context.Context) string { return h.s.settings(ctx).Timezone }

func (h healthHost) Location(ctx context.Context) *time.Location { return h.s.location(ctx) }

func (h healthHost) Standby(ctx context.Context) bool { return h.s.hello(ctx).Assistant.Standby }

func (h healthHost) QuietPush(ctx context.Context) bool { return h.s.hello(ctx).QuietPush }

func (h healthHost) Notify(ctx context.Context, n assistantwire.Notification) (assistantwire.NotifyResult, error) {
	h.s.noteCard(ctx, cardHealthWater, n)
	return h.s.hub.NotifyRequest(ctx, NotifyRequest{Notification: n})
}

func (h healthHost) FitPage(view *wire.View, version int, l i18n.Lang) error {
	slot, _ := extension.Slot("extension_page")
	_, err := extension.Fit(view, slot, version, l)
	return err
}

func (h healthHost) UIVersion() int { return extension.UIVersion }
