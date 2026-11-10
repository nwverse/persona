package hub

import (
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Shortcuts the push tests use. The hub itself pushes through Hub.notifyInteraction / Hub.notifyDone (notifyws.go)
// and stores settings with editPrefs; these build the same payloads and write the same rows.

// setPrefs stores a device's notification settings (also for a device that has not registered a token yet).
func (p *Pusher) setPrefs(id string, prefs NotifyPrefs) error { return p.setPrefsBy(id, "", prefs) }

func (p *Pusher) notifyInteraction(ix proto.Interaction, machine string) {
	p.sendLocalized(func(l i18n.Lang) map[string]any { return interactionPayloadIn(l, ix, machine) }, ix.ID, func(p NotifyPrefs) bool { return p.Input })
}

func (p *Pusher) notifyDone(machineID, machine, sessionID, title, text string, wants func(NotifyPrefs) bool) {
	p.sendLocalized(func(l i18n.Lang) map[string]any { return donePayloadIn(l, machineID, machine, sessionID, title, text) }, "", wants)
}

// interactionPayload is interactionPayloadIn in the default language.
func interactionPayload(ix proto.Interaction, machine string) map[string]any {
	return interactionPayloadIn(i18n.Default, ix, machine)
}

// startPayload is startPayloadIn in the default language.
func startPayload(st FleetState, now time.Time, quiet bool) map[string]any {
	return startPayloadIn(i18n.Default, st, now, quiet)
}
