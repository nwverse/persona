package assistant

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"

	"github.com/nwverse/persona/server/internal/connector"
)

// The inbox of a connector a plugin took over from the hub (the hub's connectors_migrate.go): its things carry the
// items' own blocks (connector.ThingExtra), so the items keep the shape the apps know (Feishu's lark / doc / moment
// block, no generic connector block), and the items the hub made itself before the move go on as the same items: a
// thread is found by the block (migratedThread), the same key the plugin uses.

// migratedConnectorID is the connector a migrated item's reply goes through: the plugin's that took Feishu over
// (the hub answers that it cannot be replied to while none did).
const migratedConnectorID = "lark"

// migratedThread is the thread of a Feishu item by its block: "chat:<feed>" ("chat:<account>:<feed>" for an account
// other than the primary one), "doc:<notice>", "moment:<post>"; "" for anything else.
func migratedThread(it *InboxItem) string {
	switch {
	case it.Lark != nil:
		if it.Lark.AccountID > 1 {
			return fmt.Sprintf("chat:%d:%s", it.Lark.AccountID, it.Lark.FeedID)
		}
		return "chat:" + it.Lark.FeedID
	case it.Doc != nil:
		return "doc:" + it.Doc.NoticeID
	case it.Moment != nil:
		return "moment:" + it.Moment.PostID
	}
	return ""
}

// applyMigrated is applyConnector for the things with Extra of a migrated connector, under b.mu.
func (b *inboxBook) applyMigrated(id string, things []connector.InboxThing, now time.Time) []*InboxItem {
	var changed []*InboxItem
	index := b.threads(func(it *InboxItem) (string, bool) {
		if it.Connector != nil {
			return "", false
		}
		return it.Source + "\x00" + migratedThread(it), true
	})
	for _, t := range things {
		source := id
		if t.Extra.Source != "" {
			source = t.Extra.Source
		}
		open, done := index.openFor(source + "\x00" + t.Thread)
		if t.Done != "" {
			if open != nil && b.settleThing(open, t, now) {
				changed = append(changed, open)
			}
			continue
		}
		if open != nil {
			switch {
			case open.Last != t.Last:
				prev := seenOf(open)
				b.roundWrite(open)
				fillMigrated(open, t)
				b.noteReceived(open, prev, nil)
				open.Read = false
				changed = append(changed, open)
			case blocksDiffer(open, t.Extra):
				// The same move, read further (a chat's read position) or read again (a document's rows): blocks only.
				b.roundWrite(open)
				setBlocks(open, t.Extra)
				changed = append(changed, open)
			}
			index.note(open) // its blocks may be another thread's now
			if t.Read && !open.Read {
				b.roundWrite(open)
				open.Read, open.Count = true, t.Extra.Count
				if open.Tier != "" && open.Tier != TierAction { // untriaged ones are settled by triage
					b.file(open, now, "read")
				}
				if !containsItem(changed, open) {
					changed = append(changed, open)
				}
			}
			continue
		}
		if t.Read || now.Sub(t.At) > inboxWindow || done != nil && (done.Last == t.Last || !t.At.After(done.UpdatedAt)) {
			continue
		}
		itemID := t.Extra.LegacyID
		if itemID == "" || b.has(itemID) {
			sum := sha1.Sum([]byte(t.Thread + "\n" + t.Last))
			itemID = id + ":" + hex.EncodeToString(sum[:8])
		}
		it := &InboxItem{ID: itemID, Source: source, Account: t.Extra.Account, AccountName: t.Extra.AccountName, StartedAt: t.Extra.StartedAt}
		if it.StartedAt.IsZero() {
			it.StartedAt = t.At
		}
		fillMigrated(it, t)
		b.noteReceived(it, inboxSeen{}, nil)
		b.add(id, it)
		b.roundNew(it)
		index.note(it)
		changed = append(changed, it)
	}
	return changed
}

// fillMigrated sets an item from a thing with Extra; an open chat item keeps where it started.
func fillMigrated(it *InboxItem, t connector.InboxThing) {
	it.Kind, it.Title, it.Sender, it.Preview, it.Context = t.Kind, t.Title, t.Sender, t.Preview, t.Context
	it.UpdatedAt, it.Last, it.Rules = t.At, t.Last, t.Rules
	it.Avatar, it.Count = t.Extra.Avatar, max(t.Extra.Count, 1)
	setBlocks(it, t.Extra)
}

func setBlocks(it *InboxItem, x *connector.ThingExtra) {
	start := ""
	if it.Lark != nil {
		start = it.Lark.StartPosition
	}
	it.Lark, it.Doc, it.Moment = nil, nil, nil
	if len(x.Lark) > 0 && json.Unmarshal(x.Lark, &it.Lark) == nil && it.Lark != nil && start != "" {
		it.Lark.StartPosition = start
	}
	if len(x.Doc) > 0 {
		_ = json.Unmarshal(x.Doc, &it.Doc)
	}
	if len(x.Moment) > 0 {
		_ = json.Unmarshal(x.Moment, &it.Moment)
	}
}

// blocksDiffer: the thing's block says something else than the item's (a chat's start position aside: the item keeps
// its own).
func blocksDiffer(it *InboxItem, x *connector.ThingExtra) bool {
	c := *it
	setBlocks(&c, x)
	enc := func(it *InboxItem) []byte {
		b, _ := json.Marshal([]any{it.Lark, it.Doc, it.Moment})
		return b
	}
	return !bytes.Equal(enc(it), enc(&c))
}

// replyMigrated sends the reply (and a document's status moves) of a Feishu item through the plugin. The client id
// is the one the hub would have used itself, so a retry across the move is still dropped by the Lark gateway.
func (s *Service) replyMigrated(ctx context.Context, r inboxReplyReq) (bool, error) {
	it := r.item
	b := &s.inbox
	clientID := ""
	if it.Lark != nil {
		clientID = r.clientID
		if !validClientID(clientID) {
			clientID = namedClientID(r.id + "\n" + r.text)
		}
		b.mu.Lock()
		sent := r.once.repeated(r.live, clientID)
		b.mu.Unlock()
		if sent {
			return true, nil
		}
	}
	var changes []connector.StatusChange
	for _, ch := range r.changes {
		changes = append(changes, connector.StatusChange{Ref: ch.Ref, Field: ch.Field, Value: ch.Value})
	}
	if len(changes) > 0 && it.Doc == nil {
		return false, i18n.ErrEnglish("inbox-errors.document_only")
	}
	if err := s.connectorReply(ctx, migratedConnectorID, connector.InboxReply{Thread: migratedThread(&it), ItemID: it.ID, Last: it.Last,
		Text: r.text, ClientID: clientID, Changes: changes}, r.sendID); err != nil {
		return false, err
	}
	if clientID != "" {
		r.once.clientID = clientID
	}
	return false, nil
}

// migratedDocStatus moves a document row's status through the plugin; the item's rows come back with its next things.
func (s *Service) migratedDocStatus(ctx context.Context, it *InboxItem, ch DocChange) (InboxItem, error) {
	b := &s.inbox
	b.mu.Lock()
	thread, itemID, last := migratedThread(it), it.ID, it.Last
	b.mu.Unlock()
	if err := s.connectorReply(ctx, migratedConnectorID, connector.InboxReply{Thread: thread, ItemID: itemID, Last: last,
		Changes: []connector.StatusChange{{Ref: ch.Ref, Field: ch.Field, Value: ch.Value}}}, ""); err != nil {
		return InboxItem{}, err
	}
	b.mu.Lock()
	out := it.listed()
	b.mu.Unlock()
	return out, nil
}

// The client ids a Feishu reply goes with (the same the plugin, and the hub before it, derive): the Lark gateway drops a
// repeat with the same id.
// clientIDNamespace is the UUIDv5 namespace of namedClientID. Fixed forever: changing it would let a retry of an
// already delivered message post again.
var clientIDNamespace = [16]byte{0xa5, 0x1e, 0x3f, 0x21, 0xe1, 0xd7, 0x4c, 0xcd, 0x89, 0xbf, 0xcb, 0x02, 0x7f, 0x8c, 0xb8, 0xdc}

// namedClientID is a UUIDv5 clientId derived from name: the same name always gives the same id, so the same
// message sent again (after a timeout, from another device, after a restart) posts once.
func namedClientID(name string) string {
	h := sha1.New()
	h.Write(clientIDNamespace[:])
	h.Write([]byte(name))
	b := h.Sum(nil)[:16]
	b[6], b[8] = b[6]&0x0f|0x50, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// validClientID reports whether s is a UUID the Lark gateway accepts as a clientId (version 1-8, RFC variant, any case).
func validClientID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if ch != '-' {
				return false
			}
		case !('0' <= ch && ch <= '9' || 'a' <= ch && ch <= 'f' || 'A' <= ch && ch <= 'F'):
			return false
		}
	}
	return '1' <= s[14] && s[14] <= '8' && strings.ContainsRune("89abAB", rune(s[19]))
}
