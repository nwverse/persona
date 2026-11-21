package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/rpc"
)

const (
	outboundCommitmentsKey = "assistant.outbound_commitments"
	outboundFollowPeriod   = 24 * time.Hour
)

// This is an obligation ledger, not an outbox: it never sends or retries a message.
// Prepared entries are not evidence of delivery and cannot fulfill another promise.
type outboundReply struct {
	ID       string     `json:"id"`
	Topic    string     `json:"topic"`
	URL      string     `json:"url"`
	Text     string     `json:"text"`
	TodoID   string     `json:"todo_id"`
	Origin   string     `json:"origin"`
	DueAt    *time.Time `json:"due_at,omitempty"`
	Promise  *bool      `json:"promise,omitempty"`
	replayed *bool
}

type outboundCommitment struct {
	Reply       outboundReply `json:"reply"`
	Promise     bool          `json:"promise"`
	PreparedAt  time.Time     `json:"prepared_at"`
	SentAt      time.Time     `json:"sent_at,omitzero"`
	DueAt       time.Time     `json:"due_at,omitzero"`
	FulfilledAt time.Time     `json:"fulfilled_at,omitzero"`
	FulfilledBy string        `json:"fulfilled_by,omitempty"`
	OwnerAt     time.Time     `json:"owner_at,omitzero"`
}

type outboundBook struct {
	mu        sync.Mutex
	loaded    bool
	entries   []outboundCommitment
	confirmed map[string]outboundConfirmation
}

type outboundConfirmation struct {
	reply outboundReply
	at    time.Time
}

type outboundReadbackKey struct{}

func noteOutboundReadback(ctx context.Context, already bool) {
	if replay, ok := ctx.Value(outboundReadbackKey{}).(*bool); ok && already {
		*replay = true
	}
}

var (
	// i18n-ignore: literal outward message data, not user-interface copy.
	outboundPromiseZH = lazyre.New(`(?:算完再同步|稍后给结果|补完统一同步|(?:我|我们)(?:会|将)(?:再)?(?:同步|给.{0,8}结果)|(?:^|[。！\n])\s*会同步)`)
	outboundPromiseEN = lazyre.New(`(?i)\b(?:i|we)(?:'ll| will)\s+(?:send|share|post|provide|give|sync|update|follow up)\b.{0,100}\b(?:later|results?|update|tomorrow|within|in [0-9])\b`)
	outboundDuration  = lazyre.New(`(?i)\b(?:in|within)\s+([0-9]+)\s*(minutes?|hours?|days?|m|h|d)\b`)
	// i18n-ignore: literal deadline data.
	outboundDurationZH = lazyre.New(`([0-9]+)\s*(分钟|小时|天)(?:内|后)`)
	outboundTimestamp  = lazyre.New(`\b[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:Z|[+-][0-9]{2}:[0-9]{2})\b`)
	outboundClock      = lazyre.New(`\b([0-9]{1,2}):([0-9]{2})\b`)
)

func isOutboundPromise(text string) bool {
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(line, ">") || strings.HasPrefix(line, "`") {
			continue // quoted or fenced examples are not the sender's promise
		}
		if outboundPromiseZH.MatchString(line) || outboundPromiseEN.MatchString(line) {
			return true
		}
	}
	return false
}

func outboundDeadline(text string, now time.Time, loc *time.Location) time.Time {
	if raw := outboundTimestamp.FindString(text); raw != "" {
		if at, err := time.Parse(time.RFC3339, raw); err == nil {
			return at.UTC()
		}
	}
	for _, re := range []*lazyre.Regexp{outboundDuration, outboundDurationZH} {
		if m := re.FindStringSubmatch(text); len(m) == 3 {
			n, err := strconv.Atoi(m[1])
			if err == nil && n > 0 && n <= 365*24*60 {
				unit := time.Hour
				switch strings.ToLower(m[2]) {
				case "m", "minute", "minutes", "分钟": // i18n-ignore: deadline data
					unit = time.Minute
				case "d", "day", "days", "天": // i18n-ignore: deadline data
					unit = 24 * time.Hour
				}
				if time.Duration(n) <= 365*24*time.Hour/unit {
					return now.Add(time.Duration(n) * unit).UTC()
				}
			}
		}
	}
	lower := strings.ToLower(text)
	tomorrow := strings.Contains(lower, "tomorrow") || strings.Contains(text, "明天") // i18n-ignore: deadline data
	today := strings.Contains(lower, "today") || strings.Contains(text, "今天")       // i18n-ignore: deadline data
	if m := outboundClock.FindStringSubmatch(text); (today || tomorrow) && len(m) == 3 {
		h, _ := strconv.Atoi(m[1])
		minute, _ := strconv.Atoi(m[2])
		if h < 24 && minute < 60 {
			local := now.In(loc)
			if tomorrow {
				local = local.AddDate(0, 0, 1)
			}
			return time.Date(local.Year(), local.Month(), local.Day(), h, minute, 0, 0, loc).UTC()
		}
	}
	return now.Add(outboundFollowPeriod).UTC()
}

func outboundID(topic, delivery string) string {
	sum := sha256.Sum256([]byte(topic + "\n" + delivery))
	return hex.EncodeToString(sum[:16])
}

func (s *Service) loadOutboundLocked(ctx context.Context) error {
	b := &s.outbound
	if b.loaded {
		return nil
	}
	var entries []outboundCommitment
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, outboundCommitmentsKey, &entries); err != nil {
			return err
		}
	}
	b.entries, b.loaded = entries, true
	return nil
}

func (s *Service) putOutboundLocked(ctx context.Context, entries []outboundCommitment) error {
	if st := s.store(); st != nil {
		if err := st.PutSetting(ctx, outboundCommitmentsKey, entries); err != nil {
			return err
		}
	}
	s.outbound.entries = entries
	return nil
}

// A durable intent precedes a send that may create or fulfill an obligation.
// A failed intent write prevents that send; an uncertain send is never retried here.
func (s *Service) prepareOutbound(ctx context.Context, r outboundReply, now time.Time) error {
	if r.ID == "" || r.Topic == "" || strings.TrimSpace(r.Text) == "" {
		return nil
	}
	b := &s.outbound
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.loadOutboundLocked(ctx); err != nil {
		return err
	}
	promise := isOutboundPromise(r.Text)
	if r.Promise != nil {
		promise = *r.Promise
	}
	follows := false
	for _, e := range b.entries {
		if e.Reply.ID == r.ID && e.Reply.Topic == r.Topic {
			return nil
		}
		follows = follows || e.Promise && e.Reply.Topic == r.Topic && !e.SentAt.IsZero() && e.FulfilledAt.IsZero()
	}
	if !promise && !follows {
		return nil
	}
	entries := slices.Clone(b.entries)
	entries = append(entries, outboundCommitment{Reply: r, Promise: promise, PreparedAt: now.UTC()})
	return s.putOutboundLocked(ctx, entries)
}

// Delivery success is separate from bookkeeping success. Never turn a sent
// message into a retryable send failure because its receipt could not be saved.
func (s *Service) finishOutbound(ctx context.Context, r outboundReply, now time.Time) error {
	if r.ID == "" || r.Topic == "" {
		return nil
	}
	b := &s.outbound
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.confirmed == nil {
		b.confirmed = map[string]outboundConfirmation{}
	}
	receiptID := outboundID(r.Topic, r.ID)
	if receipt, ok := b.confirmed[receiptID]; ok {
		r, now = receipt.reply, receipt.at
	} else {
		b.confirmed[receiptID] = outboundConfirmation{r, now}
	}
	if err := s.loadOutboundLocked(ctx); err != nil {
		return err
	}
	entries := slices.Clone(b.entries)
	index := slices.IndexFunc(entries, func(e outboundCommitment) bool {
		return e.Reply.ID == r.ID && e.Reply.Topic == r.Topic
	})
	if index < 0 {
		delete(b.confirmed, receiptID)
		return nil
	}
	e := &entries[index]
	if e.SentAt.IsZero() {
		if r.replayed != nil && *r.replayed {
			// A once-only source readback proves an older send, not a new update.
			// Its intent is the conservative ordering bound when provider time is absent.
			now = e.PreparedAt
		}
		e.SentAt = now.UTC()
		if e.Promise {
			e.DueAt = outboundDeadline(e.Reply.Text, now, s.location(ctx))
			if e.Reply.DueAt != nil {
				e.DueAt = e.Reply.DueAt.UTC()
			}
		}
		for i := range entries {
			previous := &entries[i]
			// Late delivery cannot make an earlier intent answer a newer promise.
			if r.replayed != nil && *r.replayed || i == index || !previous.Promise || previous.Reply.Topic != r.Topic || previous.SentAt.IsZero() ||
				!previous.SentAt.Before(now) || !e.PreparedAt.After(previous.SentAt) || !previous.FulfilledAt.IsZero() {
				continue
			}
			previous.FulfilledAt, previous.FulfilledBy = now.UTC(), r.ID
		}
		if err := s.putOutboundLocked(ctx, entries); err != nil {
			return err
		}
	}
	if e.Promise {
		entries = slices.Clone(entries)
		e = &entries[index]
		if err := s.attachOutboundTodo(ctx, e); err != nil {
			return err
		}
		if err := s.putOutboundLocked(ctx, entries); err != nil {
			return err
		}
	}
	// Keep the original confirmation until both its receipt and task attachment
	// are durable. A retry repairs bookkeeping, never re-dates a known send.
	delete(b.confirmed, receiptID)
	return nil
}

func (s *Service) outboundTracksTopic(r outboundReply) bool {
	b := &s.outbound
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.ContainsFunc(b.entries, func(e outboundCommitment) bool {
		return r.ID != "" && e.Reply.ID == r.ID && e.Reply.Topic == r.Topic
	})
}

func (s *Service) deliverOutbound(ctx context.Context, r outboundReply, send func() error) error {
	if err := s.prepareOutbound(ctx, r, time.Now()); err != nil {
		return &rpc.Error{Code: assistantwire.CodeInboxSendUnrecorded,
			Message: i18n.T(i18n.En, "inbox-errors.send_unrecorded")}
	}
	if err := send(); err != nil {
		return err
	}
	if err := s.finishOutbound(ctx, r, time.Now()); err != nil {
		s.logf("outward reply delivered; commitment receipt not saved: %v", err)
	}
	return nil
}

func (s *Service) outboundText(ctx context.Context, key string, args i18n.Args) string {
	return i18n.T(s.lang(ctx), "assistant-commitments."+key, args)
}

func (s *Service) attachOutboundTodo(ctx context.Context, e *outboundCommitment) error {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}
	r := &e.Reply
	marker := "<!-- outward-promise:" + r.ID + " -->"
	if e.SentAt.IsZero() {
		marker = "<!-- outward-attempt:" + r.ID + " -->"
	}
	t, ok := s.todoNow(r.TodoID)
	if ok && t.DoneAt != nil && strings.Contains(t.Notes, marker) {
		return nil // recording the same receipt cannot reopen its completed task
	}
	if !ok || t.DoneAt != nil {
		id := "todo_" + outboundID(r.Topic, r.ID)
		if existing, found := s.todoNow(id); found {
			t = existing
		} else {
			origin := r.Origin
			if main, known := s.knownMain(ctx); known && main != "" {
				origin = main
			}
			var err error
			title := "title"
			if e.SentAt.IsZero() {
				title = "unconfirmed_title"
			}
			t, err = s.newTodoAs(ctx, id, s.outboundText(ctx, title, nil), "", origin, "", false)
			if err != nil {
				return err
			}
		}
		r.TodoID = t.ID
	}
	if !strings.Contains(t.Notes, marker) {
		key := "record"
		if e.SentAt.IsZero() {
			key = "unconfirmed_record"
		}
		note := s.outboundText(ctx, key, i18n.Args{"quote": r.Text, "url": r.URL, "due": e.DueAt.Format(time.RFC3339)})
		notes := strings.TrimSpace(t.Notes + "\n\n" + marker + "\n" + note)
		if _, err := s.editTodo(ctx, t.ID, db.TodoEdit{Notes: &notes}); err != nil {
			return err
		}
	}
	_, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if v.Supervisor != "" {
			return false
		}
		v.Supervisor = t.Origin
		if v.Supervisor == "" {
			v.Supervisor, _ = s.knownMain(ctx)
		}
		v.DoneWhen = []string{s.outboundText(ctx, "criterion", nil)}
		return true
	})
	return err
}

func outboundTopicURL(chat, thread, message string) string {
	q := url.Values{"open_chat_id": {chat}, "open_thread_id": {thread}}
	if message != "" {
		q.Set("message_id", message)
	}
	return "https://applink.feishu.cn/client/thread/open?" + q.Encode()
}

// Resolve only the actual source message, never the task's separate work topic.
// A single bounded readback bridges migrated Inbox and old source reports to
// native bot topics. Opaque/unreadable sources retain their exact message key.
func (s *Service) outboundFeishuSourceTopic(ctx context.Context, chat, message string) string {
	fallback := "lark-message/" + chat + "/" + message
	if !strings.HasPrefix(chat, "oc_") || !strings.HasPrefix(message, "om_") {
		return fallback
	}
	lookup, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	items, err := s.botMessages(lookup, message)
	if err == nil {
		for _, m := range items {
			if m.MessageID == message && m.ChatID == chat && m.ThreadID != "" && !m.Deleted && m.UpperMessageID == "" {
				return "lark-bot/" + chat + "/" + m.ThreadID
			}
		}
	}
	return fallback
}

func (s *Service) outboundInboxReply(ctx context.Context, it InboxItem, text, delivery string) outboundReply {
	r := outboundReply{ID: delivery, Text: text, Topic: it.Source + "/" + it.Account + "/" + it.ID}
	if it.Agent != nil {
		r.TodoID, r.Origin = it.Agent.TodoID, it.Agent.SessionID
	}
	if it.Connector != nil {
		r.Topic = it.Source + "/" + it.Account + "/" + it.Connector.Thread
		r.URL = it.Connector.URL
		if topic := outwardURLTopic(r.URL); topic != "" {
			r.Topic = topic
		}
	} else if it.Source == "lark" && it.Lark != nil && it.Doc == nil && it.Moment == nil && it.Lark.FeedID != "" {
		// migratedThread includes the legacy account; item IDs change after each
		// filed event and cannot identify a conversation. A topic anchor narrows
		// that identity when the connector has not supplied a native thread.
		r.Topic = it.Source + "/" + it.Account + "/" + migratedThread(&it)
		if message := it.Lark.ReplyTo; message != "" {
			topic := s.outboundFeishuSourceTopic(ctx, it.Lark.FeedID, message)
			if it.Lark.AccountID <= 1 || strings.HasPrefix(topic, "lark-bot/") {
				r.Topic = topic
			} else {
				r.Topic += "/" + url.PathEscape(message)
			}
		} else if it.Lark.IsTopic || it.Lark.ThreadKind != "" {
			// A topic-group feed alone does not prove which topic was answered.
			r.Topic += "/item/" + url.PathEscape(it.ID)
		}
	}
	// Legacy sources have a Persona original-message view rather than a provider URL.
	if r.URL == "" {
		r.URL = "/inbox/" + url.PathEscape(it.ID)
	}
	return r
}

// Call only after source validation and local duplicate checks, immediately
// before a provider attempt. Only that provider's readback can mark a replay.
func (s *Service) deliverInboxReply(ctx context.Context, r inboxReplyReq, send func(context.Context) error) error {
	replayed := false
	ctx = context.WithValue(ctx, outboundReadbackKey{}, &replayed)
	outward := s.outboundInboxReply(ctx, r.item, r.text, r.sendID)
	outward.replayed = &replayed
	return s.deliverOutbound(ctx, outward, func() error { return send(ctx) })
}

// An explicit sender receipt covers other send tools without guessing their
// schemas or intercepting terminal commands. It records; it never performs a send.
type outwardAnnotation struct {
	TopicURL string     `json:"topic_url"`
	SentURL  string     `json:"sent_url"`
	Text     string     `json:"text"`
	SentAt   time.Time  `json:"sent_at"`
	DueAt    *time.Time `json:"due_at,omitempty"`
	Promise  *bool      `json:"promise,omitempty"`
}

func outwardURLTopic(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	if u.Host == "github.com" {
		refs := inboxTaskRefs(raw)
		if len(refs) == 1 {
			return "github/" + refs[0]
		}
		return ""
	}
	if (u.Host == "applink.feishu.cn" || u.Host == "applink.larksuite.com") && u.Path == "/client/thread/open" {
		chat, thread := u.Query().Get("open_chat_id"), u.Query().Get("open_thread_id")
		if chat != "" && thread != "" {
			prefix := "lark-bot/"
			if u.Host == "applink.larksuite.com" {
				prefix = "lark/"
			}
			return prefix + chat + "/" + thread
		}
	}
	u.Fragment = ""
	return "url/" + u.String()
}

func (a *outwardAnnotation) reply(t Todo, now time.Time) (outboundReply, error) {
	topic := outwardURLTopic(a.TopicURL)
	receiptTopic := outwardURLTopic(a.SentURL)
	if topic == "" || receiptTopic == "" || strings.TrimSpace(a.Text) == "" ||
		a.SentAt.IsZero() || a.SentAt.After(now.Add(time.Minute)) {
		return outboundReply{}, errors.New("outward_reply requires HTTPS topic_url and sent_url, exact text and a non-future RFC3339 sent_at")
	}
	if topic != receiptTopic {
		return outboundReply{}, errors.New("outward_reply sent_url must belong to the original topic")
	}
	if a.DueAt != nil && a.DueAt.IsZero() {
		return outboundReply{}, errors.New("outward_reply due_at must be an RFC3339 deadline")
	}
	return outboundReply{ID: "receipt:" + outboundID(topic, a.SentURL), Topic: topic, URL: a.TopicURL,
		Text: a.Text, TodoID: t.ID, Origin: t.Origin, DueAt: a.DueAt, Promise: a.Promise}, nil
}

func (s *Service) watchOutboundCommitments(ctx context.Context, now time.Time) {
	b := &s.outbound
	// Retry bookkeeping only. No provider call is repeated, even if its original
	// success arrived while Postgres was unavailable.
	b.mu.Lock()
	confirmed := make([]outboundConfirmation, 0, len(b.confirmed))
	for _, receipt := range b.confirmed {
		confirmed = append(confirmed, receipt)
	}
	b.mu.Unlock()
	for _, receipt := range confirmed {
		if err := s.finishOutbound(ctx, receipt.reply, receipt.at); err != nil {
			s.logf("outward commitment receipt retry: %v", err)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.loadOutboundLocked(ctx); err != nil {
		s.logf("outward commitments: %v", err)
		return
	}
	for i := range b.entries {
		e := b.entries[i]
		if !e.Promise {
			continue
		}
		if e.SentAt.IsZero() {
			if now.Before(e.PreparedAt.Add(outboundFollowPeriod)) {
				continue
			}
			// A restart cannot infer delivery from an intent. Preserve the source
			// for readback instead of resending or silently forgetting it.
			e.DueAt = e.PreparedAt.Add(outboundFollowPeriod)
			if err := s.attachOutboundTodo(ctx, &e); err != nil {
				continue
			}
			next := slices.Clone(b.entries)
			next[i] = e
			if err := s.putOutboundLocked(ctx, next); err != nil {
				continue
			}
			detail := s.outboundText(ctx, "unconfirmed_record", i18n.Args{"quote": e.Reply.Text,
				"url": e.Reply.URL, "due": e.DueAt.Format(time.RFC3339)})
			s.signal(superviseSignal{Todo: e.Reply.TodoID, Kind: sigReport, Report: upReportBlocked,
				Key: "promise:" + e.Reply.ID, At: e.DueAt, Detail: detail, Evidence: []string{e.Reply.URL}})
			continue
		}
		if !e.FulfilledAt.IsZero() {
			s.clearOutboundDecision(ctx, e)
			continue
		}
		if current, ok := s.todoNow(e.Reply.TodoID); ok && current.DoneAt != nil &&
			current.DoneBy != db.DoneByAuto && current.DoneBy != db.DoneByReview {
			continue // an explicit owner completion or cancellation remains authoritative
		}
		if err := s.attachOutboundTodo(ctx, &e); err != nil {
			continue
		}
		if e.Reply.TodoID != b.entries[i].Reply.TodoID {
			next := slices.Clone(b.entries)
			next[i] = e
			if err := s.putOutboundLocked(ctx, next); err != nil {
				continue
			}
		}
		t, ok := s.todoNow(e.Reply.TodoID)
		if !ok {
			continue
		}
		if t.DoneAt != nil || e.DueAt.After(now) {
			continue
		}
		detail := s.outboundText(ctx, "overdue", i18n.Args{"quote": e.Reply.Text, "url": e.Reply.URL,
			"due": e.DueAt.Format(time.RFC3339)})
		s.signal(superviseSignal{Todo: t.ID, Kind: sigReport, Report: upReportBlocked, Key: "promise:" + e.Reply.ID,
			Detail: detail, Evidence: []string{e.Reply.URL}, At: e.DueAt})
		if now.Before(e.DueAt.Add(outboundFollowPeriod)) || !e.OwnerAt.IsZero() {
			continue
		}
		v, changed, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
			if v.State == reviewNeedsUser {
				return false // never replace an unrelated owner decision
			}
			v.State = reviewNeedsUser
			v.Reviews = append(v.Reviews, TaskReview{At: now.UTC(), Verdict: reviewNeedsUser, Round: v.Round,
				Note: "outward-promise:" + e.Reply.ID, WhyOwner: whyVisible, Reason: detail,
				Recommend:     s.outboundText(ctx, "review_update", nil),
				CostOfWaiting: s.outboundText(ctx, "cost", nil),
				Options: []DecisionOption{
					{Label: s.outboundText(ctx, "review_update", nil), LeadsTo: s.outboundText(ctx, "review_effect", nil)},
					{Label: s.outboundText(ctx, "keep_waiting", nil), LeadsTo: s.outboundText(ctx, "wait_effect", nil)},
				}})
			return true
		})
		ownDecision := len(v.Reviews) > 0 && v.State == reviewNeedsUser &&
			v.Reviews[len(v.Reviews)-1].Note == "outward-promise:"+e.Reply.ID
		if err != nil || !changed && !ownDecision {
			continue
		}
		t, _ = s.todoNow(t.ID)
		review := v.Reviews[len(v.Reviews)-1]
		if _, present := s.askCopy(t.ID); !present {
			s.askConclusion(ctx, t, conclusionInput{Outcome: outcomeDecide, Reason: detail, Link: e.Reply.URL,
				Recommend: review.Recommend, Decision: reviewDecision{WhyOwner: whyVisible, Reason: detail,
					Recommend: review.Recommend, Options: review.Options, CostOfWaiting: review.CostOfWaiting}})
		}
		if _, err := s.pushDecisionErr(ctx, t.ID); err != nil {
			continue
		}
		next := slices.Clone(b.entries)
		next[i].OwnerAt = now.UTC()
		if err := s.putOutboundLocked(ctx, next); err != nil {
			s.logf("outward commitment escalation receipt: %v", err)
		}
		s.signal(superviseSignal{Todo: t.ID, Kind: sigDecide, Key: "promise-owner:" + e.Reply.ID, At: now, Detail: detail})
	}
}

func (s *Service) outboundRunReply(ctx context.Context, r *channel.Run, text string) outboundReply {
	chat, thread := r.Ext["chat"], r.Ext["thread"]
	if chat == "" || thread == "" {
		return outboundReply{} // the owner's private bot chat is not an outward topic
	}
	out := outboundReply{ID: r.ID + ":" + outboundID("", text), Topic: "lark-bot/" + chat + "/" + thread,
		URL: outboundTopicURL(chat, thread, r.ReplyTo), Text: text, Origin: r.SessionID}
	if t, ok := s.taskOf(ctx, r.MachineID, r.SessionID); ok {
		out.TodoID = t.ID
	} else if t, ok := s.todoOfThread(TodoThread{Channel: channel.Feishu, ChatID: chat, ThreadID: thread}); ok {
		out.TodoID = t.ID
	}
	return out
}

func (s *Service) clearOutboundDecision(ctx context.Context, e outboundCommitment) {
	t, ok := s.todoNow(e.Reply.TodoID)
	if !ok {
		return
	}
	marker := "<!-- outward-fulfilled:" + e.Reply.ID + " -->"
	if !strings.Contains(t.Notes, marker) {
		notes := strings.TrimSpace(t.Notes + "\n\n" + marker + "\n" +
			s.outboundText(ctx, "fulfilled", i18n.Args{"url": e.Reply.URL, "at": e.FulfilledAt.Format(time.RFC3339)}))
		if _, err := s.editTodo(ctx, t.ID, db.TodoEdit{Notes: &notes}); err != nil {
			s.logf("fulfilled outward commitment note: %v", err)
			return
		}
	}
	_, changed, err := s.updateSupervision(ctx, e.Reply.TodoID, func(v *Supervision) bool {
		if v.State != reviewNeedsUser || len(v.Reviews) == 0 ||
			v.Reviews[len(v.Reviews)-1].Note != "outward-promise:"+e.Reply.ID {
			return false
		}
		v.State = reviewPending
		return true
	})
	if err != nil && !errors.Is(err, db.ErrTodoNotFound) {
		s.logf("fulfilled outward commitment: %v", err)
	}
	if changed {
		s.dropDecision(ctx, e.Reply.TodoID)
	}
}

func (s *Service) outboundSignalHolds(ctx context.Context, g superviseSignal) bool {
	b := &s.outbound
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.loadOutboundLocked(ctx); err != nil {
		return false
	}
	return slices.ContainsFunc(b.entries, func(e outboundCommitment) bool {
		return e.Promise && e.Reply.TodoID == g.Todo && "promise:"+e.Reply.ID == g.Key &&
			e.FulfilledAt.IsZero()
	})
}

func (s *Service) outboundCompletionError(ctx context.Context, todo string) error {
	b := &s.outbound
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.loadOutboundLocked(ctx); err != nil {
		return fmt.Errorf("cannot verify outward commitments: %w", err)
	}
	for _, e := range b.entries {
		if e.Promise && e.Reply.TodoID == todo && !e.SentAt.IsZero() && e.FulfilledAt.IsZero() {
			return fmt.Errorf("task %s still owes the promised update in %s; send that update before accepting the task", todo, e.Reply.URL)
		}
	}
	return nil
}
