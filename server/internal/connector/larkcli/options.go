package larkcli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// InboxOptions is the complete lark-cli connector settings object. Chat and
// document Inbox settings share one object so options.get/options.set can
// round-trip them without one poller overwriting the other.
type InboxOptions struct {
	InboxAllChats []string `json:"inbox_all_chats"`
	Documents     []string `json:"documents"`
	WikiSpaces    []string `json:"wiki_spaces"`
	// OwnedAllComments: every comment on a watched document the user owns (DocumentOptions).
	OwnedAllComments bool `json:"owned_all_comments"`
}

func (c *Connector) Options() json.RawMessage {
	c.ensureDocuments()
	c.inboxMu.Lock()
	ids := make([]string, 0, len(c.inboxAllChats))
	for id := range c.inboxAllChats {
		ids = append(ids, id)
	}
	c.inboxMu.Unlock()
	sort.Strings(ids)

	c.docs.mu.Lock()
	documents := c.docs.options.normalized()
	c.docs.mu.Unlock()
	if documents.Documents == nil {
		documents.Documents = []string{}
	}
	if documents.WikiSpaces == nil {
		documents.WikiSpaces = []string{}
	}
	b, _ := json.Marshal(InboxOptions{
		InboxAllChats:    ids,
		Documents:        documents.Documents,
		WikiSpaces:       documents.WikiSpaces,
		OwnedAllComments: documents.OwnedAllComments,
	})
	return b
}

func (c *Connector) SetOptions(raw json.RawMessage) error {
	var wire struct {
		InboxAllChats json.RawMessage `json:"inbox_all_chats"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("options: %v", err)
	}

	var idsValue []string
	if len(wire.InboxAllChats) > 0 && string(wire.InboxAllChats) != "null" {
		if err := json.Unmarshal(wire.InboxAllChats, &idsValue); err != nil {
			return fmt.Errorf("options.inbox_all_chats: must be an array of chat ids")
		}
	}
	seen := make(map[string]struct{}, len(idsValue))
	chats := make(map[string]struct{}, len(idsValue))
	for _, id := range idsValue {
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

	documents, err := parseDocumentOptions(raw)
	if err != nil {
		return err
	}
	c.ensureDocuments()
	c.inboxMu.Lock()
	c.inboxAllChats = chats
	c.inboxMu.Unlock()
	c.docs.setOptions(documents)
	return nil
}

func (c *Connector) ensureDocuments() {
	if c.docs == nil {
		c.docs = newDocumentInbox()
	}
}
