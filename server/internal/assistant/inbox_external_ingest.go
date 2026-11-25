package assistant

import (
	"context"
	"encoding/json"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
)

type externalInboxStore interface {
	ConfiguredExternalRefs(context.Context, []assistantwire.ExternalRef) ([]assistantwire.ExternalRef, error)
	SaveExternalInboxItems(context.Context, []db.InboxRow, []assistantwire.ExternalInboxEvent, time.Time) error
	ExternalRoutingConfiguration(context.Context, []assistantwire.ExternalRef) (assistantwire.ExternalRoutingConfiguration, error)
	WithExternalInboxSweep(context.Context, func() error) error
	ReadyExternalInboxEvents(context.Context, time.Time, int) ([]assistantwire.ExternalInboxWork, error)
	LoadExternalInboxItem(context.Context, assistantwire.ExternalInboxItemID) (json.RawMessage, error)
	RecentExternalInboxOwners(context.Context, assistantwire.ExternalRef, string) ([]assistantwire.ExternalInboxDelivery, error)
	SaveExternalInboxProgress(context.Context, string, time.Time, *assistantwire.ExternalInboxPlan, bool) error
}

func (s *Service) externalInboxDB() externalInboxStore {
	store, _ := s.store().(externalInboxStore)
	return store
}

// externallyRouted reserves this source version for scoped routing. Inbox
// display and owner actions can still operate on it.
func (it *InboxItem) externallyRouted() bool {
	return it.Last != "" && (it.ExternalRouteOf == it.Last || it.externalChecking)
}

func (b *inboxBook) captureExternalInbox(it *InboxItem, sourceEvent []string) {
	if !b.externalCapture || len(it.ExternalPath) == 0 || !it.open() || it.Read || it.Kind == "bot_handoff" {
		return
	}
	event := assistantwire.ExternalInboxEvent{
		Path: it.ExternalPath, InboxID: assistantwire.ExternalInboxItemID(it.ID), Cursor: it.Last,
		Title: cleanExternalText(it.Title, 120), Summary: cleanExternalText(it.Preview, 600),
		Senders: []string{cleanExternalText(it.Sender, 256)},
	}
	if it.Connector != nil {
		event.URL = inboxTaskScopeURL(it.Connector.URL)
		if event.URL == "" {
			event.URL = inboxTaskScopeURL(it.Connector.OpenURL)
		}
		event.Senders = append(event.Senders, cleanExternalText(inboxExternalTag(it.Connector.Tags, connector.TagSender), 256))
		if refURL := inboxExternalTag(it.Connector.Tags, connector.TagExternalObject); refURL != "" {
			event.URL = inboxTaskScopeURL(refURL)
		}
	}
	if it.Mail != nil {
		event.Senders = append(event.Senders, cleanExternalText(it.Mail.From.Email, 256))
	}
	if it.Lark != nil {
		event.Senders = append(event.Senders, cleanExternalText(it.Lark.SenderID, 256))
		if it.Lark.AtMe {
			event.Mentions = append(event.Mentions, "me")
		}
		if it.Lark.AtAll {
			event.Mentions = append(event.Mentions, "all")
		}
	}
	if len(sourceEvent) == 0 {
		sourceEvent = inboxExternalEventIDs(it)
	}
	for _, id := range sourceEvent {
		if id == "" {
			continue
		}
		event.Ref, event.EventID = it.ExternalPath[0], assistantwire.ExternalEventID(id)
		if it.Mail != nil && strings.HasPrefix(id, "message:") {
			// A Message-ID is stable across mailbox labels. IMAP UIDs are mailbox-local.
			event.Ref.Container = assistantwire.ExternalRefNode{}
		}
		if len(id) > 512 || strings.ContainsRune(id, 0) {
			event.EventID = assistantwire.ExternalEventID("cursor:" + namedClientID(id))
		}
		ref, _ := json.Marshal(event.Ref)
		event.Key = namedClientID("external inbox\n" + string(ref) + "\n" + string(event.EventID))
		it.externalMoves = append(it.externalMoves, event)
		it.externalChecking = true
	}
}

func cleanExternalText(text string, limit int) string {
	return clip(oneLine(strings.ReplaceAll(text, "\x00", "")), limit)
}

// prepareExternalInbox checks opt-in in one read per Inbox batch. The source
// snapshot is held out of the old automatic paths while that decision is pending.
func (s *Service) prepareExternalInbox(ctx context.Context, changed []*InboxItem) {
	store := s.externalInboxDB()
	if store == nil {
		return
	}
	b := &s.inbox
	refs := map[assistantwire.ExternalRef]bool{}
	b.mu.Lock()
	for _, it := range changed {
		if it.externalChecking {
			for _, event := range it.externalMoves {
				for _, ref := range event.Path {
					refs[ref] = true
				}
			}
		}
	}
	b.mu.Unlock()
	if len(refs) == 0 {
		return
	}
	found, err := store.ConfiguredExternalRefs(ctx, slices.Collect(maps.Keys(refs)))
	if err != nil {
		// Journal the events for revalidation when the store recovers.
		s.logf("inbox: checking external source configuration: %v", err)
	} else {
		clear(refs)
		for _, ref := range found {
			refs[ref] = true
		}
	}
	if len(refs) == 0 {
		b.mu.Lock()
		for _, it := range changed {
			it.externalMoves, it.externalChecking = nil, false
		}
		b.mu.Unlock()
		return
	}

	owner := s.hello(ctx).Owner
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, it := range changed {
		if !it.externalChecking {
			continue
		}
		it.externalMoves = slices.DeleteFunc(it.externalMoves, func(event assistantwire.ExternalInboxEvent) bool {
			return !slices.ContainsFunc(event.Path, func(ref assistantwire.ExternalRef) bool { return refs[ref] })
		})
		it.externalChecking = false
		if len(it.externalMoves) == 0 {
			continue
		}
		if slices.ContainsFunc(it.ExternalPath, func(ref assistantwire.ExternalRef) bool { return refs[ref] }) {
			it.ExternalRouteOf, it.TriagedAt = it.Last, it.Last
			if it.Tier == "" {
				it.Tier = TierFYI
			}
		}
		if !it.open() || it.VisibilityExcluded || isSelfAuthoredInboxItem(it, owner, nil) {
			it.externalMoves = nil
		}
	}
}

func inboxTaskScopeURL(raw string) string {
	// A URL goes to a recipient only after the routing decision. The model sees
	// the summary. Keep the usual source link boundary for clickable URLs.
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || u.Scheme != "https" || len(raw) > 2048 {
		return ""
	}
	return u.String()
}

// A scoped route owns the only automatic interpretation of the source batch.
// Keep the Inbox grouping structural and leave work classification to that route.
func (s *Service) externalGroupingConfigured(ctx context.Context, source assistantwire.InboxSource, thing connector.InboxThing) bool {
	store := s.externalInboxDB()
	if store == nil {
		return false
	}
	it := inboxVisibilityThingItem(source.ID, thing)
	setInboxExternalPath(it, source.Via, thing)
	if len(it.ExternalPath) == 0 {
		return false
	}
	configured, err := store.ConfiguredExternalRefs(ctx, it.ExternalPath)
	return err == nil && len(configured) > 0
}
