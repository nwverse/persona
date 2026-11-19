package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

const inboxReplyApprovalAfter = 4 * time.Hour

type inboxReplyApproval struct {
	Version        int    `json:"version"`
	Notified       bool   `json:"notified,omitempty"`
	PreviousUrgent bool   `json:"previous_urgent,omitempty"`
	PreviousReason string `json:"previous_reason,omitempty"`
	Explanation    string `json:"explanation"`
	NoteSuffix     string `json:"note_suffix,omitempty"`
	LegacyNote     bool   `json:"legacy_note,omitempty"`
	ChainID        string `json:"chain_id,omitempty"`
}

// A row save releases inbox.mu while storage is in flight. Undo and notification
// receipts may touch only the exact suggestion, receipt and explanation staged
// by that save, not a replacement that happens to reuse its version and time.
type inboxApprovalState struct {
	suggestion *InboxSuggestion
	agent      *InboxAgent
	receipt    *inboxReplyApproval
	value      string
}

func approvalState(it *InboxItem) inboxApprovalState {
	agentNote := ""
	if it.Agent != nil {
		agentNote = it.Agent.Note
	}
	raw, _ := json.Marshal(struct {
		Suggestion                     *InboxSuggestion
		Receipt                        *inboxReplyApproval
		Urgent                         bool
		Reason, Last, Tier, MergedInto string
		DoneAt                         *time.Time
		Chain                          []InboxChainEntry
		AgentNote                      string
	}{currentSuggestion(it), it.ReplyApproval, it.Urgent, it.Reason, it.Last, it.Tier, it.MergedInto, it.DoneAt, it.Chain, agentNote})
	return inboxApprovalState{it.Suggestion, it.Agent, it.ReplyApproval, string(raw)}
}

func (state inboxApprovalState) current(b *inboxBook, it *InboxItem) bool {
	return b.items[it.ID] == it && state == approvalState(it)
}

// Drafts already have one home in Inbox Needs you. Make an old one urgent there,
// with a source-linked explanation, and notify the owner; never create a second
// decision, change the draft text, or send it to the other person.
func (s *Service) checkInboxReplyApprovals(ctx context.Context, now time.Time) {
	b := &s.inbox
	if !b.loaded.Load() {
		return
	}
	s.clearStaleReplyApprovals(ctx)
	type reminder struct {
		item         *InboxItem
		version      int
		text, url    string
		oldUrgent    bool
		oldReason    string
		oldReceipt   *inboxReplyApproval
		oldNote      string
		chainID      string
		conversation string
		displayed    bool
		notify       bool
		state        inboxApprovalState
	}
	var reminders []reminder
	b.mu.Lock()
	for _, it := range b.items {
		g := currentSuggestion(it)
		receipt := it.ReplyApproval
		if !it.open() || it.Tier == TierNoise || g == nil || g.Kind != SuggestReply ||
			g.At.IsZero() || now.Before(g.At.Add(inboxReplyApprovalAfter)) {
			continue
		}
		notified := receipt != nil && receipt.Version == g.Version && receipt.Notified
		repairLegacy := it.Suggestion == nil && receipt != nil && receipt.Version == g.Version &&
			!strings.Contains(g.Note, receipt.Explanation)
		if notified && !repairLegacy {
			continue
		}
		since := g.At
		if !it.UpdatedAt.IsZero() && it.UpdatedAt.Before(since) {
			since = it.UpdatedAt
		}
		hours := max(0, int(now.Sub(since)/time.Hour))
		text := i18n.T(b.userLang(), "assistant-commitments.approval_overdue", i18n.Args{"hours": hours})
		source := sourceLink(it)
		link := source.URL
		if link == "" {
			link = "/inbox/" + it.ID
		}
		r := reminder{item: it, version: g.Version, text: text, url: link,
			oldUrgent: it.Urgent, oldReason: it.Reason, oldReceipt: receipt, oldNote: g.Note, notify: !notified}
		if g.Conversation != nil {
			r.conversation = g.Conversation.SessionID
		} else if it.Agent != nil {
			r.conversation = it.Agent.SessionID
		}
		if receipt == nil || receipt.Version != g.Version {
			it.ReplyApproval = &inboxReplyApproval{Version: g.Version, PreviousUrgent: it.Urgent,
				PreviousReason: it.Reason, Explanation: text, NoteSuffix: "\n" + text, LegacyNote: it.Suggestion == nil}
			it.Urgent, it.Reason = true, text
			g.Note += it.ReplyApproval.NoteSuffix
			if it.Suggestion == nil {
				it.Agent.Note = g.Note // legacySuggestion is a projection, not the stored draft
			}
			b.chainAdd(it, InboxChainEntry{Kind: ChainCheck, At: now.UTC(), Text: text, Detail: link,
				Link: firstLink(it), Via: ViaAssistant})
			r.chainID = it.Chain[len(it.Chain)-1].ID
			it.ReplyApproval.ChainID = r.chainID
			r.displayed = true
		} else if repairLegacy {
			// Older receipts may be durable even though only a temporary note was edited.
			repaired := *receipt
			repaired.NoteSuffix = "\n" + receipt.Explanation
			repaired.LegacyNote = true
			it.ReplyApproval = &repaired
			it.Agent.Note = g.Note + repaired.NoteSuffix
			r.displayed = true
		}
		r.state = approvalState(it)
		reminders = append(reminders, r)
	}
	b.mu.Unlock()
	items := make([]*InboxItem, 0, len(reminders))
	for _, r := range reminders {
		if r.displayed {
			items = append(items, r.item)
		}
	}
	failed := s.saveInboxUndo(ctx, items, func(failed map[string]bool) {
		for _, r := range reminders {
			it := r.item
			if !r.displayed || !failed[it.ID] || !r.state.current(b, it) {
				continue
			}
			it.Urgent, it.Reason, it.ReplyApproval = r.oldUrgent, r.oldReason, r.oldReceipt
			if it.Suggestion != nil {
				it.Suggestion.Note = r.oldNote
			} else {
				it.Agent.Note = r.oldNote
			}
			if r.chainID != "" {
				it.Chain = slices.DeleteFunc(it.Chain, func(e InboxChainEntry) bool { return e.ID == r.chainID })
			}
		}
	})
	for _, r := range reminders {
		if !r.notify || failed[r.item.ID] {
			continue
		}
		b.mu.Lock()
		current := r.state.current(b, r.item)
		b.mu.Unlock()
		if !current {
			continue
		}
		// The stable notification id lets the hub deduplicate a repeated receipt.
		_, err := s.hub.NotifyRequest(ctx, NotifyRequest{Notification: Notification{
			ID: fmt.Sprintf("inbox-reply-%s-%d", r.item.ID, r.version), Kind: NotifyPush,
			Title: s.outboundText(ctx, "approval_title", nil), Body: r.text + "\n" + r.url,
			SessionID: r.conversation}})
		if err != nil {
			s.logf("overdue Inbox reply notification: %v", err)
			continue
		}
		b.mu.Lock()
		current = r.state.current(b, r.item)
		previous := r.item.ReplyApproval
		var notified inboxApprovalState
		if current {
			receipt := *r.item.ReplyApproval
			receipt.Notified = true
			r.item.ReplyApproval = &receipt
			notified = approvalState(r.item)
		}
		b.mu.Unlock()
		if current {
			s.saveInboxUndo(ctx, []*InboxItem{r.item}, func(failed map[string]bool) {
				if failed[r.item.ID] && notified.current(b, r.item) {
					r.item.ReplyApproval = previous
				}
			})
		}
	}
}

func (s *Service) clearStaleReplyApprovals(ctx context.Context) {
	b := &s.inbox
	type cleared struct {
		it                      *InboxItem
		receipt                 *inboxReplyApproval
		urgent                  bool
		reason, note, agentNote string
		chain                   []InboxChainEntry
		state                   inboxApprovalState
	}
	var clearedRows []cleared
	var items []*InboxItem
	b.mu.Lock()
	for _, it := range b.items {
		r := it.ReplyApproval
		g := currentSuggestion(it)
		if r == nil || it.open() && it.Tier != TierNoise && g != nil && g.Kind == SuggestReply && g.Version == r.Version {
			continue
		}
		old := cleared{it: it, receipt: r, urgent: it.Urgent, reason: it.Reason, chain: slices.Clone(it.Chain)}
		if it.Suggestion != nil {
			old.note = it.Suggestion.Note
			if r.NoteSuffix != "" {
				it.Suggestion.Note = strings.TrimSuffix(it.Suggestion.Note, r.NoteSuffix)
			}
		}
		if it.Agent != nil {
			old.agentNote = it.Agent.Note
			if r.LegacyNote && r.NoteSuffix != "" {
				it.Agent.Note = strings.TrimSuffix(it.Agent.Note, r.NoteSuffix)
			}
		}
		if r.ChainID != "" {
			it.Chain = slices.DeleteFunc(it.Chain, func(e InboxChainEntry) bool {
				return e.ID == r.ChainID && e.Kind == ChainCheck && e.Text == r.Explanation
			})
		}
		if it.Reason == r.Explanation {
			it.Urgent, it.Reason = r.PreviousUrgent, r.PreviousReason
		}
		it.ReplyApproval = nil
		old.state = approvalState(it)
		clearedRows = append(clearedRows, old)
		items = append(items, it)
	}
	b.mu.Unlock()
	s.saveInboxUndo(ctx, items, func(failed map[string]bool) {
		for _, r := range clearedRows {
			if failed[r.it.ID] && r.state.current(b, r.it) {
				r.it.ReplyApproval, r.it.Urgent, r.it.Reason = r.receipt, r.urgent, r.reason
				r.it.Chain = r.chain
				if r.it.Suggestion != nil {
					r.it.Suggestion.Note = r.note
				}
				if r.it.Agent != nil {
					r.it.Agent.Note = r.agentNote
				}
			}
		}
	})
}
