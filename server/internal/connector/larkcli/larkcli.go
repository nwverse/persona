// Package larkcli exposes the read-only Lark tools backed by the user's
// lark-cli login (docs/plans/plan-lark-cli.md, L2).
package larkcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	ID   = "larkcli"
	Name = "Lark via lark-cli"
)

// requiredScopes is the union of the scopes used by this connector's L2
// read commands. These names come from the lark-cli shortcut guidance and the
// missing_scope errors returned by lark-cli 1.0.99. Keep the list here so an
// authenticated account with an incomplete grant is reported before a tool
// call fails.
var requiredScopes = []string{
	"im:chat:read",
	"im:message:readonly",
	"im:message.group_msg:get_as_user",
	"im:message.p2p_msg:get_as_user",
	"im:message.reactions:read",
	"search:message",
	"search:docs:read",
	"contact:user:search",
	"minutes:minutes.search:read",
	"task:task:read",
	"mail:user_mailbox.message:readonly",
	"mail:user_mailbox.message.address:read",
	"mail:user_mailbox.message.subject:read",
	"mail:user_mailbox.message.body:read",
	"calendar:calendar.event:read",
}

// Connector is enabled only when manager.json configures lark.cli. It owns no
// credentials; the adapter delegates all authentication to lark-cli.
type Connector struct {
	runner  *adapter.Runner
	now     func() time.Time
	profile string

	inboxMu          sync.Mutex
	inboxThings      map[string]connector.InboxThing
	inboxSeen        map[string]struct{}
	inboxTopics      map[string]struct{}
	inboxAllChats    map[string]struct{}
	inboxState       persistedInboxState
	inboxStateLoaded bool
	inboxCollecting  bool
	inboxStore       StateStore
	inboxPrefer      string
	gatewayAvailable func(context.Context) bool
	inboxPoll        time.Duration
	inboxPageLimit   int
}

// New creates the built-in connector from the configured CLI runner.
func New(cfg adapter.Config) *Connector {
	c := &Connector{runner: adapter.New(cfg), profile: cfg.Profile, inboxPoll: cfg.InboxPoll, inboxPageLimit: cfg.InboxPageLimit}
	if c.inboxPoll <= 0 {
		c.inboxPoll = inboxPollDefault
	}
	if c.inboxPoll < inboxPollMinimum {
		c.inboxPoll = inboxPollMinimum
	}
	if c.inboxPageLimit <= 0 {
		c.inboxPageLimit = inboxPageLimit
	}
	if c.inboxPageLimit > inboxPageLimitMax {
		c.inboxPageLimit = inboxPageLimitMax
	}
	return c
}

// NewWithRunner is useful for unit tests and embedders that already constructed a
// runner. The runner still executes the real configured subprocess.
func NewWithRunner(r *adapter.Runner) *Connector {
	return &Connector{runner: r, profile: r.Profile()}
}

func (c *Connector) ID() string   { return ID }
func (c *Connector) Name() string { return Name }

func (c *Connector) Manifest() connector.Manifest {
	return connector.Manifest{Capabilities: []connector.Capability{connector.CapStatus, connector.CapTools, connector.CapEvents, connector.CapInbox, connector.CapOptions}, Kinds: []string{"message"}}
}

func (c *Connector) Tools() []proto.Tool {
	return []proto.Tool{tool("larkcli_chats"), tool("larkcli_messages"), tool("larkcli_search")}
}

func tool(name string) proto.Tool {
	t := prompts.Tool(name)
	return proto.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
}

// Status reads the CLI version and auth status. It never includes raw command
// output, message bodies, or account tokens; the configured profile appears
// only in the localized login command shown when the user must sign in.
func (c *Connector) Status(ctx context.Context) connector.Status {
	st := connector.Status{State: connector.StateError, Via: "lark-cli"}
	if c == nil || c.runner == nil {
		return c.statusError(ctx, st, nil, adapter.KindNotInstalled)
	}
	if err := c.runner.CheckVersion(ctx); err != nil {
		return c.statusForError(ctx, st, err)
	}
	auth, err := c.runner.AuthStatus(ctx)
	if err != nil {
		return c.statusForError(ctx, st, err)
	}
	st.Account = auth.UserName
	if st.Account == "" {
		st.Account = auth.OpenID
	}
	if auth.Expired {
		return c.statusExpired(ctx, st)
	}
	if !auth.LoggedIn {
		return c.statusNotSignedIn(ctx, st)
	}
	missing := append([]string(nil), auth.MissingScopes...)
	missing = append(missing, missingRequiredScopes(auth.GrantedScopes)...)
	if missing = uniqueScopes(missing); len(missing) > 0 {
		return c.statusMissingScopes(ctx, st, missing)
	}
	st.State = connector.StateOK
	now := c.currentTime()
	if auth.RefreshWindowNeedsReminder(now) {
		st.Error = i18n.T(c.lang(ctx), "hub-connectors.expiry_reminder", i18n.Args{
			"count":   auth.DaysUntilExpiry(now),
			"command": c.loginCommand(),
		})
	}
	return st
}

func (c *Connector) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if c == nil || c.runner == nil {
		return "", &adapter.Error{Kind: adapter.KindNotInstalled}
	}
	// The old gateway's field shapes are retained. The larkcli prefix avoids a
	// collision when a Lark gateway extension is installed at the same time.
	switch name {
	case "larkcli_chats", "lark_chats":
		return c.chats(ctx, args)
	case "larkcli_messages", "lark_messages":
		return c.messages(ctx, args)
	case "larkcli_search", "lark_search":
		return c.search(ctx, args)
	default:
		return "", fmt.Errorf("unknown tool %s", name)
	}
}

func (c *Connector) chats(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Filter string `json:"filter"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if a.Limit <= 0 || a.Limit > 100 {
		a.Limit = 30
	}
	switch a.Filter {
	case "", "recent", "unread":
		// Open Platform has no unread state. The CLI's active-time list is the
		// documented L2 fallback for both the old default and recent filter.
		page, err := c.runner.ListChats(ctx, a.Limit)
		return marshal(page, err)
	case "at_me":
		page, err := c.runner.ListChatsAtMe(ctx, a.Limit)
		return marshal(page, err)
	default:
		return "", fmt.Errorf("unknown chat filter %q", a.Filter)
	}
}

func (c *Connector) messages(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ChatID  string `json:"chat_id"`
		Thread  string `json:"thread"`
		Limit   int    `json:"limit"`
		Before  string `json:"before"`
		Account int    `json:"account"` // retained for gateway-compatible input; profile selects the CLI account
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if a.ChatID == "" && a.Thread == "" {
		return "", fmt.Errorf("chat_id is required")
	}
	page, err := c.runner.ListMessages(ctx, a.ChatID, a.Thread, a.Before, a.Limit)
	return marshal(page, err)
}

func (c *Connector) search(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Query string `json:"query"`
		Kind  string `json:"kind"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	raw, err := c.runner.Search(ctx, a.Kind, a.Query)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshal(value any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(value)
	return string(b), err
}

func (c *Connector) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Connector) lang(ctx context.Context) i18n.Lang { return i18n.From(ctx).Or(i18n.Default) }

func (c *Connector) statusForError(ctx context.Context, st connector.Status, err error) connector.Status {
	return c.statusError(ctx, st, err, adapter.KindOf(err))
}

func (c *Connector) statusError(ctx context.Context, st connector.Status, err error, kind adapter.ErrorKind) connector.Status {
	if e, ok := err.(*adapter.Error); ok {
		switch kind {
		case adapter.KindMissingPermission:
			return c.statusMissingScopes(ctx, st, e.MissingScopes)
		case adapter.KindNotLoggedIn:
			return c.statusNotSignedIn(ctx, st)
		case adapter.KindExpired:
			return c.statusExpired(ctx, st)
		}
	}
	st.State = connector.StateError
	switch kind {
	case adapter.KindNotInstalled:
		st.Error = i18n.T(c.lang(ctx), "hub-connectors.not_installed")
		st.Guide = []string{i18n.T(c.lang(ctx), "hub-connectors.install_guide")}
	case adapter.KindUnsupportedVersion:
		if e, ok := err.(*adapter.Error); ok {
			st.Error = i18n.T(c.lang(ctx), "hub-connectors.old_version", i18n.Args{"got": e.Version, "want": e.Minimum})
		} else {
			st.Error = i18n.T(c.lang(ctx), "hub-connectors.old_version", i18n.Args{"got": "", "want": adapter.MinimumVersion})
		}
	case adapter.KindRateLimited:
		st.State = connector.StateRateLimited
		st.Error = i18n.T(c.lang(ctx), "hub-connectors.rate_limited")
	default:
		st.Error = i18n.T(c.lang(ctx), "hub-connectors.error")
	}
	return st
}

func (c *Connector) statusNotSignedIn(ctx context.Context, st connector.Status) connector.Status {
	st.State = connector.StateNotSignedIn
	st.Error = i18n.T(c.lang(ctx), "hub-connectors.not_signed_in")
	st.Guide = []string{i18n.T(c.lang(ctx), "hub-connectors.login_guide", i18n.Args{"command": c.loginCommand()})}
	return st
}

func (c *Connector) statusExpired(ctx context.Context, st connector.Status) connector.Status {
	st.State = connector.StateNotSignedIn
	st.Error = i18n.T(c.lang(ctx), "hub-connectors.expired")
	st.Guide = []string{i18n.T(c.lang(ctx), "hub-connectors.login_guide", i18n.Args{"command": c.loginCommand()})}
	return st
}

func (c *Connector) statusMissingScopes(ctx context.Context, st connector.Status, scopes []string) connector.Status {
	st.State = connector.StateNoPermission
	args := i18n.Args{"scope": strings.Join(scopes, ", ")}
	st.Error = i18n.T(c.lang(ctx), "hub-connectors.missing_permission", args)
	st.Guide = []string{i18n.T(c.lang(ctx), "hub-connectors.login_guide", i18n.Args{"command": c.loginCommand()})}
	return st
}

func (c *Connector) loginCommand() string {
	command := "lark-cli"
	profile := c.profile
	if profile == "" && c.runner != nil {
		profile = c.runner.Profile()
	}
	if profile != "" {
		command += " --profile " + commandArg(profile)
	}
	return command + " auth login --domain all"
}

func commandArg(value string) string {
	if value != "" {
		safe := true
		for _, r := range value {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.", r)) {
				safe = false
				break
			}
		}
		if safe {
			return value
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func missingRequiredScopes(granted []string) []string {
	have := make(map[string]struct{}, len(granted))
	for _, scope := range granted {
		have[strings.TrimSpace(scope)] = struct{}{}
	}
	var missing []string
	for _, scope := range requiredScopes {
		if _, ok := have[scope]; !ok {
			missing = append(missing, scope)
		}
	}
	return missing
}

func uniqueScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	return result
}
