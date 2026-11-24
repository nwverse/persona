package assistant

import "time"

// The Inbox as the Lark workbench reads it (the lark module's workbench.go): one item a card's button names.

// workbenchItem is the item a card's button names, as listed, while it is open or done within inboxKeepDone (the list
// carries only the newest of those, inbox_list_done.go, so it is looked up by id rather than in a list).
func (s *Service) workbenchItem(id string) (InboxItem, bool) {
	b := &s.inbox
	b.mu.Lock()
	defer b.mu.Unlock()
	it, ok := b.items[id]
	if !ok || it.DoneAt != nil && it.DoneAt.Before(time.Now().Add(-inboxKeepDone)) {
		return InboxItem{}, false
	}
	return it.listed(), true
}
