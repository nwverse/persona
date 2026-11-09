package larkcli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// InboxOptions is deliberately small and opt-in. An empty list preserves the
// L4 rules; listed chat ids additionally admit every new message in that chat
// (the base rule still drops messages sent by the user).
type InboxOptions struct {
	InboxAllChats []string `json:"inbox_all_chats"`
}

func (c *Connector) Options() json.RawMessage {
	c.inboxMu.Lock()
	ids := make([]string, 0, len(c.inboxAllChats))
	for id := range c.inboxAllChats {
		ids = append(ids, id)
	}
	c.inboxMu.Unlock()
	sort.Strings(ids)
	b, _ := json.Marshal(InboxOptions{InboxAllChats: ids})
	return b
}

func (c *Connector) SetOptions(raw json.RawMessage) error {
	var options InboxOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return fmt.Errorf("options: %v", err)
	}
	seen := make(map[string]struct{}, len(options.InboxAllChats))
	chats := make(map[string]struct{}, len(options.InboxAllChats))
	for _, id := range options.InboxAllChats {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("options.inbox_all_chats contains an empty chat id")
		}
		if len(id) > 256 {
			return fmt.Errorf("options.inbox_all_chats chat id is too long")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("options.inbox_all_chats contains duplicate chat id %q", id)
		}
		seen[id] = struct{}{}
		chats[id] = struct{}{}
	}
	c.inboxMu.Lock()
	c.inboxAllChats = chats
	c.inboxMu.Unlock()
	return nil
}
