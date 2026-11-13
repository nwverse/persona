package larkcli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

// ReplayMessages reads only an explicit set of source messages back into the
// normal Inbox routing. It does not change the live cursor, seen ledger, topic
// set or Collect snapshot; unrelated history is context, never new activity.
func (c *Connector) ReplayMessages(ctx context.Context, chatID, start, end string, ids []string) ([]connector.InboxThing, error) {
	from, fromErr := time.Parse(time.RFC3339, start)
	to, toErr := time.Parse(time.RFC3339, end)
	if !strings.HasPrefix(chatID, "oc_") || strings.ContainsAny(chatID, " ,:\r\n\t") ||
		fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > time.Hour ||
		len(ids) == 0 || len(ids) > 10 {
		return nil, fmt.Errorf("replay requires a chat, a time window of at most one hour and 1-10 message IDs")
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !strings.HasPrefix(id, "om_") || len(id) <= 3 || strings.ContainsAny(id, " ,:\r\n\t") || wanted[id] {
			return nil, fmt.Errorf("replay requires distinct message IDs")
		}
		wanted[id] = true
	}
	if !c.inboxShouldCollect(ctx) {
		return nil, fmt.Errorf("lark-cli is not the active Inbox source")
	}
	auth, err := c.inboxAuth(ctx)
	if err != nil {
		return nil, err
	}
	if !auth.LoggedIn || auth.OpenID == "" {
		return nil, &adapter.Error{Kind: adapter.KindNotLoggedIn}
	}
	c.inboxMu.Lock()
	if err := c.loadInboxStateLocked(ctx); err != nil {
		c.inboxMu.Unlock()
		return nil, err
	}
	topics := make(map[string]struct{}, len(c.inboxTopics))
	for id := range c.inboxTopics {
		topics[id] = struct{}{}
	}
	c.inboxMu.Unlock()
	found := make(map[string]adapter.Message, len(wanted))
	token := ""
	for pageNo := 0; pageNo < inboxPageLimit && len(found) < len(wanted); pageNo++ {
		page, err := c.runner.SearchMessages(ctx, "", adapter.SearchOptions{
			ChatID: chatID, Start: start, End: end, PageToken: token, Limit: 50,
		})
		if err != nil {
			return nil, err
		}
		for _, message := range page.Messages {
			if wanted[message.MessageID] && message.ChatID == chatID {
				at := messageTime(message)
				if at.IsZero() || at.Before(from.Truncate(time.Minute)) || !at.Before(to) {
					return nil, fmt.Errorf("replay message has no valid timestamp inside the requested window")
				}
				found[message.MessageID] = message
			}
		}
		if !page.HasMore || page.PageToken == "" {
			break
		}
		token = page.PageToken
	}
	if len(found) != len(wanted) {
		return nil, fmt.Errorf("replay found %d of %d requested messages; narrow the time window", len(found), len(wanted))
	}
	messages := make([]adapter.Message, 0, len(found))
	for _, message := range found {
		if !isBoundAppMessage(message, auth.AppID) {
			messages = append(messages, message)
		}
	}
	sort.SliceStable(messages, func(i, j int) bool { return messageLess(messages[i], messages[j]) })
	things := []connector.InboxThing{}
	for _, group := range c.groupMessages(messages, auth, topics) {
		group.pinned = true
		if thing := c.thingForGroup(ctx, group, auth); thing.Thread != "" {
			things = append(things, thing)
		}
	}
	return things, nil
}
