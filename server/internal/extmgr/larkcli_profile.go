package extmgr

import (
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	larkcliconnector "github.com/nwverse/persona/server/internal/connector/larkcli"
	larkcliadapter "github.com/nwverse/persona/server/internal/larkcli"
)

const managedLarkCLIProfile = "persona"

type larkCLIBotView struct {
	Profile string `json:"profile"`
	AppID   string `json:"app_id,omitempty"`
	SameApp bool   `json:"same_app"`
}

// effectiveLarkCLIProfile is the one profile the manager gives to both the
// connector and assistant.lark. An explicit profile remains an escape hatch;
// otherwise a bot app gets the managed profile and no bot falls back to the
// CLI's default profile.
func (h *Manager) effectiveLarkCLIProfile(ctx context.Context) string {
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil {
		return ""
	}
	if profile := strings.TrimSpace(h.cfg.Lark.CLI.Profile); profile != "" {
		return profile
	}
	if h.botAppCredentialsID(ctx) != "" {
		return managedLarkCLIProfile
	}
	return ""
}

func (h *Manager) botAppCredentials(ctx context.Context) (appID, secret, domain string) {
	if h == nil || h.cfg == nil {
		return "", "", ""
	}
	b := &h.bots
	b.mu.Lock()
	defer b.mu.Unlock()
	h.loadBotsLocked(ctx)
	appID, secret, domain, _, err := h.botCredentialsLocked()
	if err != nil {
		// The app id is still safe and authoritative for profile selection even
		// when the sealed secret cannot be opened; sync will report the failure
		// without exposing it.
		return appID, "", domain
	}
	return appID, secret, domain
}

func (h *Manager) botAppCredentialsID(ctx context.Context) string {
	id, _, _ := h.botAppCredentials(ctx)
	return id
}

// syncLarkCLIProfile binds the managed profile to the bot app without ever
// putting the app secret in argv or a returned error. It is intentionally a
// no-op for an explicit profile and for a manager without bot credentials.
func (h *Manager) syncLarkCLIProfile(ctx context.Context) error {
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil || strings.TrimSpace(h.cfg.Lark.CLI.Profile) != "" {
		return nil
	}
	appID, secret, domain := h.botAppCredentials(ctx)
	if appID == "" || secret == "" {
		return nil
	}
	cli := h.cfg.Lark.CLI
	show := larkcliadapter.New(larkcliadapter.Config{Bin: cli.Bin, Profile: managedLarkCLIProfile, Timeout: 15 * time.Second})
	showCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	current, err := show.ConfigShow(showCtx)
	cancel()
	if err == nil && current.AppID == appID {
		return nil
	}
	brand := larkCLIBrand(domain)
	initRunner := larkcliadapter.New(larkcliadapter.Config{Bin: cli.Bin, Timeout: 15 * time.Second})
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = initRunner.InitProfile(initCtx, managedLarkCLIProfile, appID, secret, brand)
	cancel()
	if err != nil {
		return err
	}
	return nil
}

func larkCLIBrand(domain string) string {
	d := strings.ToLower(strings.TrimSpace(domain))
	if d == "lark" || d == "larksuite" || strings.Contains(d, "larksuite.com") {
		return "lark"
	}
	return "feishu"
}

// newLarkCLIConnector builds the connector for an effective profile with its
// Inbox wiring (state store, gateway preference), so a rebuild after a profile
// change keeps the chat Inbox working.
func (h *Manager) newLarkCLIConnector(profile string) *larkcliconnector.Connector {
	cli := h.cfg.Lark.CLI
	lc := larkcliconnector.New(larkcliadapter.Config{Bin: cli.Bin, Profile: profile,
		InboxPoll: cli.InboxPoll(), InboxPageLimit: cli.InboxPageLimit})
	lc.SetStateStore(h.store)
	lc.SetInboxSource(h.cfg.Lark.Prefer, func(ctx context.Context) bool {
		gateway := h.connectors.Get(connector.Lark)
		if gateway == nil {
			return false
		}
		return connector.StatusOf(ctx, gateway).State == connector.StateOK
	})
	return lc
}

// refreshLarkCLIConnector applies a profile change to the live registry. The
// registry's Remove/Add pair preserves the user's enabled switch and cancels a
// pending login through the connector lifecycle.
func (h *Manager) refreshLarkCLIConnector(ctx context.Context) {
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil || h.connectors == nil {
		return
	}
	profile := h.effectiveLarkCLIProfile(ctx)
	if existing := h.connectors.Get(larkcliconnector.ID); existing != nil {
		if c, ok := existing.(*larkcliconnector.Connector); ok && c.Profile() == profile {
			return
		}
		h.connectors.Remove(larkcliconnector.ID)
	}
	if err := h.addConnector(h.newLarkCLIConnector(profile)); err != nil {
		h.logf("larkcli connector: %v", err)
	}
}

// larkCLIView is the small binding summary consumed by the bot settings page.
// The app id is read from the selected profile's non-secret auth status, then
// compared with the bot app id held by the manager.
func (h *Manager) larkCLIView(ctx context.Context, botAppID string) *larkCLIBotView {
	if h == nil || h.cfg == nil || h.cfg.Lark.CLI == nil {
		return nil
	}
	profile := h.effectiveLarkCLIProfile(ctx)
	appID := ""
	cli := h.cfg.Lark.CLI
	r := larkcliadapter.New(larkcliadapter.Config{Bin: cli.Bin, Profile: profile, Timeout: 5 * time.Second})
	if auth, err := r.AuthStatus(ctx); err == nil {
		appID = auth.AppID
	}
	return &larkCLIBotView{Profile: profile, AppID: appID, SameApp: botAppID != "" && appID != "" && botAppID == appID}
}
