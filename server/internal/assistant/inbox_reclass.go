package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Corrections and learned rules (docs/assistant/inbox.md "Corrections and learned rules"). The user moves an item from
// its menu ("Mark as FYI", "Mark as Needs you") or ignores it: the item takes that tier at once and keeps it
// (UserTier: triage, the rules and the policies leave it alone), and the move is kept as a correction: who sent it,
// where, what it was about, the tier before and after. The latest corrections are shown to the triage model as the
// user's habits; once the same sender or chat was moved to the same place often enough, the model drafts one rule in
// the user's words, which a card on the phone (persona_settings') asks about before it joins inbox.rules. Settings ›
// Inbox rules lists those rules, one a line, to edit or remove.

const (
	// doneByIgnored is done_by for an item the user ignored: listed under Ignored, apart from Done.
	doneByIgnored = "ignored"
	// ProgressHandoff: the user handed the item to a conversation (inbox_handoff.go).
	ProgressHandoff = "handoff"

	inboxFeedbackKey = "inbox_feedback"
	feedbackMax      = 200                 // corrections kept, the latest
	feedbackShown    = 8                   // corrections the triage model reads, the latest of the item's source
	feedbackWindow   = 30 * 24 * time.Hour // corrections older than this draft no rule
	// ruleAfter is how many items of one sender or chat moved to one tier make a rule worth asking about; an ignore
	// says less (the user may not care about that one thing only), so it takes ruleAfterIgnore.
	ruleAfter       = 2
	ruleAfterIgnore = 3
	ruleCardFor     = 12 * time.Hour // how long the phone's card waits
	rulesMax        = 4000           // inbox.rules' length, as the sources' rules
	ruleMax         = 200            // one drafted rule
)

// InboxProgress says the user put the AI on the item and the work is not done (status "working"); a result handed back
// waits for the user's OK (status "action").
type InboxProgress struct {
	Kind      string    `json:"kind"` // handoff
	Since     time.Time `json:"since"`
	MachineID string    `json:"machine_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
}

// inboxTierWas is an item's tier before the user moved it: "Undo" (tier "") puts it back.
type inboxTierWas struct {
	Tier     string `json:"tier,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Urgent   bool   `json:"urgent,omitempty"`
	UserTier string `json:"user_tier,omitempty"`
}

// InboxFeedback is one correction: the user moved item ItemID from From to To (action, fyi, noise, or ignored).
type InboxFeedback struct {
	ID     string    `json:"id"`
	At     time.Time `json:"at"`
	ItemID string    `json:"item_id"`
	Source string    `json:"source"`
	Kind   string    `json:"kind,omitempty"`
	// Tag / Value / Name: what the correction is about, the most specific of the item's mute options (a sender, else
	// its chat, else its repository): the class rules are drafted for.
	Tag    string `json:"tag,omitempty"`
	Value  string `json:"value,omitempty"`
	Name   string `json:"name,omitempty"`
	Sender string `json:"sender,omitempty"`
	Chat   string `json:"chat,omitempty"` // the chat's (or mail's) title
	About  string `json:"about,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to"`
	// Proposed: the rule draft it went into (it counts toward no other).
	Proposed string `json:"proposed,omitempty"`
}

// class is what corrections that count together share: the same thing moved to the same place.
func (f InboxFeedback) class() string {
	if f.Value == "" {
		return ""
	}
	return f.Source + "\x00" + f.Tag + "\x00" + f.Value + "\x00" + f.To
}

// inboxLearning is the corrections kept and the drafts under way.
type inboxLearning struct {
	mu       sync.Mutex // one read-change-save at a time
	loaded   bool
	feedback []InboxFeedback
	drafting map[string]bool // classes whose rule is being drafted or asked about
	// ask shows the rule's card and waits for it (tests answer it themselves); nil: askHub.
	ask func(ctx context.Context, ix proto.Interaction) (proto.Resolution, error)
	// drafted: tests wait for a draft to end.
	drafted func(rule string, approved bool)
}

// applyUserTier gives an item the user moved its tier back after triage, the rules or a connector changed it (done:
// the user's move decides, changed: the tier changed). Someone needing the user urgently (a triage of new content)
// lifts a move away from Needs you. Under b.mu.
func (b *inboxBook) applyUserTier(it *InboxItem) (done, changed bool) {
	if it.UserTier == "" {
		return false, false
	}
	if it.Urgent && it.Tier == TierAction && it.UserTier != TierAction {
		it.UserTier = ""
		return false, false
	}
	changed = it.Tier != it.UserTier
	it.Tier, it.Reason = it.UserTier, b.userTierReason()
	if it.Tier != TierAction {
		it.Urgent = false
	}
	return true, changed
}

func (b *inboxBook) userTierReason() string { return i18n.T(b.userLang(), "assistant-inbox.by_you") }

func validUserTier(t string) bool { return t == TierAction || t == TierFYI || t == TierNoise }

// feedbackOf is the correction moving it to to (its tier now is where from).
func feedbackOf(it *InboxItem, to string, now time.Time) InboxFeedback {
	f := InboxFeedback{ID: fmt.Sprintf("f%d", now.UnixNano()), At: now.UTC(), ItemID: it.ID, Source: it.Source, Kind: it.Kind,
		Sender: it.Sender, Chat: clip(oneLine(it.Title), 80), About: clip(oneLine(cmp.Or(it.Summary, it.Preview)), 120),
		From: it.Tier, To: to}
	opts := mutesOf(it)
	for _, tag := range policyTagRank {
		if i := slices.IndexFunc(opts, func(o MuteOption) bool { return o.Tag == tag }); i >= 0 {
			f.Tag, f.Value, f.Name = opts[i].Tag, opts[i].Value, opts[i].Name
			break
		}
	}
	if f.Tag == connector.TagChat && f.Sender == "" {
		f.Sender = f.Name
	}
	return f
}

// ---- moving an item ----

var errUserTier = i18n.ErrEnglish("inbox-errors.policy_tier") // the same words as a policy's tier

// reclassify moves item id to tier (action, fyi, noise), or with tier "" puts back what it had before the user's
// last move ("Undo"). It keeps the correction (an undo takes it back) and says whether a rule is being drafted from it.
func (s *Service) reclassify(ctx context.Context, id, tier string) (InboxItem, bool, error) {
	if tier != "" && !validUserTier(tier) {
		return InboxItem{}, false, errUserTier
	}
	b := &s.inbox
	now := time.Now()
	b.mu.Lock()
	it, ok := b.items[id]
	if !ok {
		b.mu.Unlock()
		return InboxItem{}, false, errNoInboxItem
	}
	if !it.open() {
		b.mu.Unlock()
		return InboxItem{}, false, errInboxItemDone
	}
	w := beginWrite(it)
	var fb *InboxFeedback
	l := b.userLang()
	switch {
	case tier == "":
		if was := it.TierWas; was != nil {
			it.Tier, it.Reason, it.Urgent, it.UserTier = was.Tier, was.Reason, was.Urgent, was.UserTier
			it.TierWas = nil
		} else {
			it.UserTier = ""
		}
		b.chainAdd(it, InboxChainEntry{Kind: ChainTriaged, At: now.UTC(), Text: i18n.T(l, "assistant-inbox_chain.reclassified_back",
			i18n.Args{"tier": tierWord(l, it.Tier)}), Detail: tierWord(l, it.Tier)})
	case it.UserTier == tier && it.Tier == tier:
		out := it.listed()
		b.mu.Unlock()
		return out, false, nil
	default:
		f := feedbackOf(it, tier, now)
		if f.From != tier {
			fb = &f
		}
		it.TierWas = &inboxTierWas{Tier: it.Tier, Reason: it.Reason, Urgent: it.Urgent, UserTier: it.UserTier}
		// set as the user said: the old urgency is not someone needing them now (that is triage's, applyUserTier)
		it.UserTier, it.Tier, it.Reason = tier, tier, b.userTierReason()
		if tier != TierAction {
			it.Urgent = false
		}
		b.chainAdd(it, InboxChainEntry{Kind: ChainTriaged, At: now.UTC(), Text: i18n.T(l, "assistant-inbox_chain.reclassified",
			i18n.Args{"tier": tierWord(l, tier)}), Detail: tierWord(l, tier)})
	}
	b.settle(w)
	b.mu.Unlock()
	if failed := s.saveWrites(ctx, []*inboxWrite{w}, "the user's move of its tier"); failed[id] {
		return InboxItem{}, false, errInboxNotPersisted
	}
	drafting := false
	if fb != nil {
		drafting = s.noteFeedback(ctx, *fb)
	} else if tier == "" {
		s.dropFeedback(ctx, id)
	}
	if tier != TierAction && tier != "" {
		s.handInbox(ctx) // a queued twin may be for it no more
	}
	b.mu.Lock()
	out := it.listed()
	b.mu.Unlock()
	return out, drafting, nil
}

// ignoreInbox files item id as ignored and keeps that as a correction.
func (s *Service) ignoreInbox(ctx context.Context, id string) (InboxItem, error) {
	b := &s.inbox
	b.mu.Lock()
	var f *InboxFeedback
	if it, ok := b.items[id]; ok && it.open() {
		x := feedbackOf(it, doneByIgnored, time.Now())
		f = &x
	}
	b.mu.Unlock()
	out, err := s.fileInbox(ctx, id, true, doneByIgnored)
	if err == nil && f != nil {
		s.noteFeedback(ctx, *f)
	}
	return out, err
}

// ---- the corrections kept ----

// loadInboxFeedback reads the corrections kept; false when the database could not be read.
func (s *Service) loadInboxFeedback(ctx context.Context) bool {
	k := &s.inbox.learning
	k.mu.Lock()
	defer k.mu.Unlock()
	return s.loadFeedbackLocked(ctx)
}

func (s *Service) loadFeedbackLocked(ctx context.Context) bool {
	k := &s.inbox.learning
	if k.loaded {
		return true
	}
	if st := s.store(); st != nil {
		var kept []InboxFeedback
		if _, err := st.Setting(ctx, inboxFeedbackKey, &kept); err != nil {
			s.logf("inbox: corrections: %v", err)
			return false
		}
		k.feedback = append(kept, k.feedback...)
	}
	k.loaded = true
	return true
}

// putFeedbackLocked keeps next (the latest feedbackMax) and makes it the corrections; with k.mu held.
func (s *Service) putFeedbackLocked(ctx context.Context, next []InboxFeedback) error {
	if n := len(next); n > feedbackMax {
		next = slices.Clone(next[n-feedbackMax:])
	}
	if st := s.store(); st != nil {
		if err := st.PutSetting(ctx, inboxFeedbackKey, next); err != nil {
			return err
		}
	}
	s.inbox.learning.feedback = next
	return nil
}

// noteFeedback keeps correction f: it replaces one still unused of the same item (the latest move counts), and one
// that only moves the item back where it was takes that away. When the class has enough corrections a rule is drafted
// in the background; it says whether one is.
func (s *Service) noteFeedback(ctx context.Context, f InboxFeedback) bool {
	k := &s.inbox.learning
	k.mu.Lock()
	defer k.mu.Unlock()
	if !s.loadFeedbackLocked(ctx) {
		return false
	}
	next := slices.Clone(k.feedback)
	if i := slices.IndexFunc(next, func(x InboxFeedback) bool { return x.ItemID == f.ItemID && x.Proposed == "" }); i >= 0 {
		back := next[i].From == f.To
		f.From = next[i].From
		next = slices.Delete(next, i, i+1)
		if back {
			if err := s.putFeedbackLocked(ctx, next); err != nil {
				s.logf("inbox: corrections: %v", err)
			}
			return false
		}
	}
	next = append(next, f)
	class := f.class()
	var picked []int
	if class != "" && !k.drafting[class] {
		since := f.At.Add(-feedbackWindow)
		for i, x := range next {
			if x.Proposed == "" && x.class() == class && x.At.After(since) {
				picked = append(picked, i)
			}
		}
	}
	need := ruleAfter
	if f.To == doneByIgnored {
		need = ruleAfterIgnore
	}
	draft := len(picked) >= need
	var corrections []InboxFeedback
	if draft {
		id := fmt.Sprintf("r%d", time.Now().UnixNano())
		for _, i := range picked {
			next[i].Proposed = id
			corrections = append(corrections, next[i])
		}
	}
	if err := s.putFeedbackLocked(ctx, next); err != nil {
		s.logf("inbox: corrections: %v", err)
		return false
	}
	if !draft {
		return false
	}
	if k.drafting == nil {
		k.drafting = map[string]bool{}
	}
	k.drafting[class] = true
	go s.draftRule(context.WithoutCancel(ctx), class, corrections)
	return true
}

// dropFeedback takes back the unused correction of item id (its move was undone).
func (s *Service) dropFeedback(ctx context.Context, id string) {
	k := &s.inbox.learning
	k.mu.Lock()
	defer k.mu.Unlock()
	if !s.loadFeedbackLocked(ctx) {
		return
	}
	next := slices.DeleteFunc(slices.Clone(k.feedback), func(x InboxFeedback) bool { return x.ItemID == id && x.Proposed == "" })
	if len(next) == len(k.feedback) {
		return
	}
	if err := s.putFeedbackLocked(ctx, next); err != nil {
		s.logf("inbox: corrections: %v", err)
	}
}

// corrections are the latest corrections of source, oldest first, as the triage model reads them.
func (s *Service) corrections(source string) []prompts.InboxCorrection {
	k := &s.inbox.learning
	k.mu.Lock()
	defer k.mu.Unlock()
	var out []prompts.InboxCorrection
	for i := len(k.feedback) - 1; i >= 0 && len(out) < feedbackShown; i-- {
		if f := k.feedback[i]; f.Source == source {
			out = append(out, promptCorrection(f))
		}
	}
	slices.Reverse(out)
	return out
}

func promptCorrection(f InboxFeedback) prompts.InboxCorrection {
	to := f.To
	if to == doneByIgnored {
		to = "ignore"
	}
	return prompts.InboxCorrection{Sender: f.Sender, Chat: f.Chat, About: f.About, From: f.From, To: to}
}

// ---- drafting a rule ----

// draftRule drafts one rule from corrections, asks the user about it on the phone and adds it to inbox.rules when they
// approve. The corrections are marked as used before: declined or not, they draft no other.
func (s *Service) draftRule(ctx context.Context, class string, corrections []InboxFeedback) {
	k := &s.inbox.learning
	approved := false
	rule := ""
	defer func() {
		k.mu.Lock()
		delete(k.drafting, class)
		done := k.drafted
		k.mu.Unlock()
		if done != nil {
			done(rule, approved)
		}
	}()
	rule = s.askRuleDraft(ctx, corrections)
	if rule == "" {
		return
	}
	approved = s.confirmRule(ctx, rule, corrections)
	if !approved {
		return
	}
	if err := s.addInboxRule(ctx, rule); err != nil {
		approved = false
		s.logf("inbox: add the rule: %v", err)
	}
}

// askRuleDraft has the model write the rule; without one, a plain sentence of the class's.
func (s *Service) askRuleDraft(ctx context.Context, corrections []InboxFeedback) string {
	f := corrections[len(corrections)-1]
	hello := s.hello(ctx)
	var in []prompts.InboxCorrection
	for _, c := range corrections {
		in = append(in, promptCorrection(c))
	}
	system := prompts.RenderInboxRuleDraft(prompts.InboxRuleDraft{Owner: hello.Owner, Language: answerLanguage(hello.Locale),
		Rules: strings.TrimSpace(s.inboxConfig(ctx).Rules)})
	input := prompts.RenderInboxRuleDraftInput(prompts.InboxRuleDraftInput{Name: f.Name, Tag: f.Tag, Corrections: in})
	var out struct {
		Rule string `json:"rule"`
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	err := s.inboxModelJSON(cctx, purposeTriage, system, input, 0, &out)
	cancel()
	if rule := clip(oneLine(out.Rule), ruleMax); err == nil && rule != "" {
		return rule
	}
	if err != nil {
		s.logf("inbox: draft a rule: %v", err)
	}
	to := f.To
	if to == doneByIgnored {
		to = TierNoise
	}
	return i18n.T(s.inbox.userLang(), "assistant-inbox.rule_"+to, i18n.Args{"name": f.Name})
}

// confirmRule asks about rule on the phone (persona_settings' card, in the main conversation): true when approved.
func (s *Service) confirmRule(ctx context.Context, rule string, corrections []InboxFeedback) bool {
	b := &s.inbox
	l := b.userLang()
	f := corrections[len(corrections)-1]
	to := f.To
	var where string
	if to == doneByIgnored {
		where = i18n.T(l, "assistant-inbox.rule_ignored")
	} else {
		where = tierWord(l, to)
	}
	summary := i18n.T(l, "assistant-inbox.rule_card", i18n.Args{"rule": rule, "count": len(corrections), "name": f.Name, "tier": where})
	shown, _ := json.Marshal(map[string]string{"setting": i18n.T(l, "assistant-inbox.rules_title"), "action": "add", "rule": rule})
	st := s.settings(ctx)
	now := time.Now().UTC()
	ix := proto.Interaction{MachineID: st.MachineID, SessionID: st.Main, Kind: proto.IxPermission, ToolName: "persona_settings",
		Summary: summary, ToolInput: shown, SessionTitle: "Persona", CreatedAt: now, ExpiresAt: now.Add(ruleCardFor)}
	ask := s.askHub
	b.learning.mu.Lock()
	if b.learning.ask != nil {
		ask = b.learning.ask
	}
	b.learning.mu.Unlock()
	r, err := ask(ctx, ix)
	if err != nil {
		s.logf("inbox: ask about a rule: %v", err)
		return false
	}
	return r.Allow != nil && *r.Allow
}

// ---- inbox.rules ----

// splitRules is inbox.rules one rule a line (list marks dropped, blank lines skipped).
func splitRules(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, mark := range []string{"- ", "* ", "• "} {
			line = strings.TrimSpace(strings.TrimPrefix(line, mark))
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// joinRules is inbox.rules of rules, one a line.
func joinRules(rules []string) string {
	var lines []string
	for _, r := range rules {
		if r = strings.TrimSpace(oneLine(r)); r != "" {
			lines = append(lines, "- "+r)
		}
	}
	return strings.Join(lines, "\n")
}

var errRulesLong = i18n.ErrEnglish("inbox-errors.rules_long")

// inboxRules reads inbox.rules fresh from the extension manager.
func (s *Service) inboxRules(ctx context.Context) (string, error) {
	var cfg assistantwire.InboxConfig
	if err := s.appCall(ctx, AppInbox, assistantwire.InboxConfigMethod, map[string]any{}, &cfg); err != nil {
		return "", err
	}
	return strings.TrimSpace(cfg.Rules), nil
}

// setInboxRules keeps text as inbox.rules and has the inbox read it again.
func (s *Service) setInboxRules(ctx context.Context, text string) error {
	if len([]rune(text)) > rulesMax {
		return errRulesLong
	}
	if err := s.appCall(ctx, AppInbox, assistantwire.InboxSetRules, map[string]any{"rules": text}, nil); err != nil {
		return err
	}
	b := &s.inbox
	b.cfgMu.Lock()
	b.cfgAt = time.Time{}
	b.cfgMu.Unlock()
	return nil
}

// addInboxRule adds rule at the end of inbox.rules as it is now (not as it was when the card was shown).
func (s *Service) addInboxRule(ctx context.Context, rule string) error {
	now, err := s.inboxRules(ctx)
	if err != nil {
		return err
	}
	rules := splitRules(now)
	if slices.Contains(rules, rule) {
		return nil
	}
	return s.setInboxRules(ctx, joinRules(append(rules, rule)))
}

func (s *Service) inboxReclassRoutes() {
	// Move an item ({"tier": "action" | "fyi" | "noise"}), or put back what it had before ({"tier": ""}).
	s.handle("POST /api/v1/inbox/{id}/tier", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		var in struct {
			Tier string `json:"tier"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		it, drafting, err := s.reclassify(r.Context(), r.PathValue("id"), in.Tier)
		switch {
		case errors.Is(err, errUserTier):
			apierr.Write(w, http.StatusBadRequest, err)
		case errors.Is(err, errNoInboxItem):
			apierr.Write(w, http.StatusNotFound, err)
		case errors.Is(err, errInboxItemDone):
			apierr.Write(w, http.StatusConflict, err)
		case err != nil:
			apierr.Write(w, http.StatusInternalServerError, err)
		default:
			apierr.WriteJSON(w, struct {
				InboxItem
				// RuleDrafting: enough moves like it were made: a rule is being drafted, to approve on the phone.
				RuleDrafting bool `json:"rule_drafting,omitempty"`
			}{it, drafting})
		}
	})
	s.handle("GET /api/v1/inbox/rules", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		text, err := s.inboxRules(r.Context())
		if err != nil {
			apierr.Write(w, http.StatusBadGateway, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"rules": listOrEmpty(splitRules(text))})
	})
	// Replace the rules ({"rules": ["…", …]}, one each): the user's own edit in Settings, no card.
	s.handle("PUT /api/v1/inbox/rules", func(w http.ResponseWriter, r *http.Request) {
		if !s.inboxUp(w, r) {
			return
		}
		var in struct {
			Rules []string `json:"rules"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		text := joinRules(in.Rules)
		if err := s.setInboxRules(r.Context(), text); err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, errRulesLong) {
				code = http.StatusBadRequest
			}
			apierr.Write(w, code, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"rules": listOrEmpty(splitRules(text))})
	})
}

func listOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
