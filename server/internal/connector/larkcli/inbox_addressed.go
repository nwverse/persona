package larkcli

import (
	"context"
	"encoding/json"
	"strings"

	adapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Bot membership and display names are not evidence that a message addresses it.
func mentionsIdentity(message adapter.Message, id string) bool {
	if id == "" {
		return false
	}
	for _, mention := range message.Mentions {
		if mention.ID == id {
			return true
		}
	}
	return false
}

func mentionsBot(message adapter.Message, auth adapter.AuthStatus) bool {
	return mentionsIdentity(message, auth.AppID) || mentionsIdentity(message, auth.BotOpenID)
}

func (c *Connector) inboxAuth(ctx context.Context) (adapter.AuthStatus, error) {
	auth, err := c.runner.AuthStatus(ctx)
	if err != nil || !auth.LoggedIn || auth.AppID == "" {
		return auth, err
	}
	c.inboxMu.Lock()
	if c.inboxBotAppID == auth.AppID {
		auth.BotOpenID = c.inboxBotOpenID
	}
	c.inboxMu.Unlock()
	if auth.BotOpenID == "" {
		if id, err := c.runner.BotOpenID(ctx); err == nil {
			auth.BotOpenID = id
			c.inboxMu.Lock()
			c.inboxBotAppID, c.inboxBotOpenID = auth.AppID, id
			c.inboxMu.Unlock()
		}
	}
	return auth, nil
}

// Role labels are source data, including the localized labels rendered by Lark.
var cardRole = lazyre.New(`(?i)^(?:on[- ]call(?: person)?|assignee|owner|responsible(?: person)?|值班人|负责人)\s*[:：]\s*(.*)$`) // i18n-ignore

func cardAddressesOwner(message adapter.Message, auth adapter.AuthStatus) bool {
	return cardOwnerMatch(message, auth, false)
}

func cardAssignsOwner(message adapter.Message, auth adapter.AuthStatus) bool {
	return cardOwnerMatch(message, auth, true)
}

func cardOwnerMatch(message adapter.Message, auth adapter.AuthStatus, requireRole bool) bool {
	if message.MsgType != "interactive" || auth.OpenID == "" {
		return false
	}
	// Structured card content retains identity even when the displayed name changes.
	var value any
	_ = json.Unmarshal(message.Content, &value)
	if text, ok := value.(string); ok {
		_ = json.Unmarshal([]byte(text), &value)
	}
	hasIdentity := false
	var addressed func(any, bool) bool
	addressed = func(value any, role bool) bool {
		switch v := value.(type) {
		case map[string]any:
			tag, _ := v["tag"].(string)
			if tag == "at" || tag == "person" {
				hasIdentity = true
			} else if cardRoleElement(v, false) {
				// A completed field ends an inherited label, even through an extra container.
				role = cardRoleElement(v, true)
			}
			// A direct label belongs to this identity, not to unrelated descendants.
			identityRole := role || cardRoleElement(v, false)
			if tag == "at" && (!requireRole || identityRole) || tag == "person" && identityRole {
				// Card user_id defaults to an open ID; generic element IDs are not user identities.
				idType, hasIDType := v["id_type"]
				userIDType, hasUserIDType := v["user_id_type"]
				if (!hasIDType || idType == "open_id") && (!hasUserIDType || userIDType == "open_id") {
					for _, key := range []string{"user_id", "open_id"} {
						if id, ok := v[key].(string); ok && len(id) > 3 && strings.HasPrefix(id, "ou_") && id == auth.OpenID {
							return true
						}
					}
				}
			}
			for _, child := range v {
				if addressed(child, role) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if addressed(child, role) {
					return true
				}
				// A role label applies to the next value in this row, not to
				// unrelated person elements elsewhere in the card.
				if element, ok := child.(map[string]any); !ok || element["tag"] != "person" && element["tag"] != "at" {
					role = cardRoleElement(child, true)
				}
			}
		}
		return false
	}
	if addressed(value, false) {
		return true
	}
	// Structured identities own the decision, even if their displayed names
	// disagree. Only identity-free CLI projections may fall back to text.
	if hasIdentity {
		return false
	}
	// Compact CLI cards can have only text: require a labeled field and an exact
	// current-account name, never an arbitrary name elsewhere in the card.
	name := strings.TrimSpace(auth.UserName)
	if name == "" {
		return false
	}
	for _, line := range strings.Split(messageText(message), "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "**", ""))
		match := cardRole.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		for _, person := range strings.FieldsFunc(match[1], func(r rune) bool {
			return r == ',' || r == ';' || r == '/' || r == '，' || r == '、' || r == '；'
		}) {
			if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(person), "@")) == name {
				return true
			}
		}
	}
	return false
}

func cardRoleElement(value any, labelOnly bool) bool {
	if element, ok := value.(map[string]any); ok {
		if text, exists := element["text"]; exists {
			return cardRoleElement(text, labelOnly)
		}
		return cardRoleElement(element["content"], labelOnly)
	}
	text, _ := value.(string)
	if labelOnly {
		match := cardRole.FindStringSubmatch(strings.TrimSpace(strings.ReplaceAll(text, "**", "")))
		return len(match) == 2 && strings.TrimSpace(match[1]) == ""
	}
	for _, line := range strings.Split(text, "\n") {
		match := cardRole.FindStringSubmatch(strings.TrimSpace(strings.ReplaceAll(line, "**", "")))
		if len(match) == 2 {
			return true
		}
	}
	return false
}
