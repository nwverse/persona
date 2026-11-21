package extmgr

import (
	"context"

	"github.com/nwverse/persona/server/internal/feishubot"
)

// botConfigure retains only what the localized pairing hint needs, never the platform's raw error text.
// It belongs to the saved credentials and survives pairing-code renewal and manager restarts.
type botConfigure struct {
	Issue      feishubot.ConfigureIssue `json:"issue,omitempty"`
	Configured bool                     `json:"configured,omitempty"`
}

// configureBotForPairing runs only after an explicit credentials submission. Polls render its saved outcome.
func (h *Manager) configureBotForPairing(ctx context.Context, id, secret, domain string) {
	b := &h.bots
	b.mu.Lock()
	// A concurrent credentials save may already have replaced the app. The sealed value also identifies a new
	// save of the same credentials, so a delayed report cannot overwrite a newer configuration attempt.
	generation := b.set.SecretEnc
	currentSecret, err := h.openKey(generation)
	current := err == nil && b.set.AppID == id && b.set.Domain == domain && currentSecret == secret
	b.mu.Unlock()
	if !current {
		return
	}
	rep := feishubot.New(id, secret, domain).AutoConfigure(ctx)
	if rep.Detail != "" {
		h.logf("bots: auto-configure: %s", rep.Detail)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.set.AppID != id || b.set.Domain != domain || b.set.SecretEnc != generation || b.set.Owner != nil {
		return
	}
	b.set.Configure = nil
	if !rep.Skipped {
		b.set.Configure = &botConfigure{Issue: rep.Issue, Configured: rep.Configured}
	}
	h.saveBotSettingsLocked()
}
