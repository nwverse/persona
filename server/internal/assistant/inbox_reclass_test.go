package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// ruleAnswers makes the phone's card answer allow (and records what it said); drafted gets each draft's end.
func ruleAnswers(h *inboxHub, allow bool) (cards chan proto.Interaction, drafted chan string) {
	cards, drafted = make(chan proto.Interaction, 4), make(chan string, 4)
	k := &h.inbox.learning
	k.mu.Lock()
	k.ask = func(_ context.Context, ix proto.Interaction) (proto.Resolution, error) {
		cards <- ix
		return proto.Resolution{Allow: &allow}, nil
	}
	k.drafted = func(rule string, approved bool) {
		if approved {
			drafted <- rule
		} else {
			drafted <- ""
		}
	}
	k.mu.Unlock()
	return cards, drafted
}

func feedbackNow(h *inboxHub) []InboxFeedback {
	k := &h.inbox.learning
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]InboxFeedback(nil), k.feedback...)
}

// Moving an item: it takes the tier at once (status, reason, urgency) and keeps it through a triage of new content and
// a policy; the move is a correction (who, where, from → to); Undo puts the tier back and takes the correction away.
func TestReclassifyAnItem(t *testing.T) {
	h := muteHub(t)
	mux := h.routes()
	a := groupItem("a", "f1", "u1", TierAction)
	a.Urgent = true
	h.inbox.items["a"] = a

	for body, want := range map[string]int{`{"tier":"urgent"}`: 400, `{`: 400} {
		if code, _ := inboxCall(t, mux, "POST", "/api/v1/inbox/a/tier", body); code != want {
			t.Errorf("%s: %d, want %d", body, code, want)
		}
	}
	if code, _ := inboxCall(t, mux, "POST", "/api/v1/inbox/nope/tier", `{"tier":"fyi"}`); code != 404 {
		t.Errorf("a missing item: %d", code)
	}
	code, out := inboxCall(t, mux, "POST", "/api/v1/inbox/a/tier", `{"tier":"fyi"}`)
	if code != 200 || out["tier"] != TierFYI || out["status"] != StatusFYI || out["user_tier"] != TierFYI || out["urgent"] != nil {
		t.Fatalf("moved: %d %v", code, out)
	}
	if _, ok := out["tier_was"]; ok {
		t.Error("tier_was is bookkeeping, not listed")
	}
	if !h.st.saved("a") {
		t.Error("the move was not saved")
	}
	fb := feedbackNow(h)
	if len(fb) != 1 || fb[0].From != TierAction || fb[0].To != TierFYI || fb[0].Tag != "sender" || fb[0].Value != "u1" ||
		fb[0].Chat != "示例项目群" || fb[0].Sender != "示例同事" {
		t.Fatalf("correction: %+v", fb)
	}

	// New content triaged as Needs you (not urgent) keeps the user's move; a policy does not override it either.
	b := &h.inbox
	b.mu.Lock()
	a.Tier, a.Reason = TierAction, "模型的理由"
	b.applyPolicyTier(a)
	if a.Tier != TierFYI || a.Reason != b.userTierReason() {
		t.Errorf("after triage: %s %q", a.Tier, a.Reason)
	}
	b.mu.Unlock()
	setPolicyFor(t, h, InboxPolicy{Source: "lark", Tag: "chat", Value: "f1", Name: "示例项目群", Tier: TierAction})
	b.mu.Lock()
	if a.Tier != TierFYI {
		t.Errorf("a policy overrode the user's move: %s", a.Tier)
	}
	// Someone needing the user urgently lifts it.
	a.Tier, a.Urgent = TierAction, true
	b.applyPolicyTier(a)
	if a.Tier != TierAction || a.UserTier != "" {
		t.Errorf("urgent: %s %q", a.Tier, a.UserTier)
	}
	a.UserTier, a.Tier, a.Urgent = TierFYI, TierFYI, false
	b.mu.Unlock()

	// Undo: the tier before comes back and the correction goes.
	code, out = inboxCall(t, mux, "POST", "/api/v1/inbox/a/tier", `{"tier":""}`)
	if code != 200 || out["tier"] != TierAction || out["user_tier"] != nil || out["urgent"] != true {
		t.Fatalf("undone: %d %v", code, out)
	}
	if fb := feedbackNow(h); len(fb) != 0 {
		t.Errorf("the undone move is still a correction: %+v", fb)
	}

	// A filed item is not moved.
	b.mu.Lock()
	b.file(a, time.Now(), "user")
	b.mu.Unlock()
	if code, _ := inboxCall(t, mux, "POST", "/api/v1/inbox/a/tier", `{"tier":"fyi"}`); code != 409 {
		t.Errorf("a done item: %d", code)
	}
}

// Ignore files the item as ignored (listed apart from Done), is a correction, and moving it back takes that away.
func TestIgnoreAnItem(t *testing.T) {
	h := muteHub(t)
	mux := h.routes()
	h.inbox.items["a"] = groupItem("a", "f1", "u1", TierFYI)
	code, out := inboxCall(t, mux, "POST", "/api/v1/inbox/a/done", `{"done":true,"ignore":true}`)
	if code != 200 || out["done_by"] != doneByIgnored || out["status"] != nil {
		t.Fatalf("ignored: %d %v", code, out)
	}
	chain, _ := out["chain"].([]any)
	if last, _ := chain[len(chain)-1].(map[string]any); last["kind"] != ChainDone || last["text"] == "" {
		t.Errorf("chain: %v", last)
	}
	if fb := feedbackNow(h); len(fb) != 1 || fb[0].To != doneByIgnored || fb[0].From != TierFYI {
		t.Fatalf("correction: %+v", fb)
	}
	code, out = inboxCall(t, mux, "POST", "/api/v1/inbox/a/done", `{"done":false}`)
	if code != 200 || out["done_by"] != nil {
		t.Fatalf("back: %d %v", code, out)
	}
	if fb := feedbackNow(h); len(fb) != 0 {
		t.Errorf("an undone ignore is still a correction: %+v", fb)
	}
	// Dismiss (Clean up all's) stays dismissed, and teaches nothing.
	if _, out := inboxCall(t, mux, "POST", "/api/v1/inbox/a/done", `{"done":true,"dismiss":true}`); out["done_by"] != "dismissed" {
		t.Errorf("dismissed: %v", out)
	}
	if fb := feedbackNow(h); len(fb) != 0 {
		t.Errorf("a dismiss is a correction: %+v", fb)
	}
}

// Two items of one sender moved to FYI draft a rule: the model writes it from the corrections, the phone's card
// (persona_settings') asks about it in the main conversation, and approved it joins inbox.rules; the corrections it
// used draft no other. Declined, nothing is written.
func TestCorrectionsDraftARule(t *testing.T) {
	h := muteHub(t)
	mux := h.routes()
	model, calls := captureModel(t, `{"rule":"示例同事同步进展的消息算知悉即可"}`)
	h.in.setModel(model)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Rules = "- 告警机器人的消息需要我" })
	cards, drafted := ruleAnswers(h, true)
	for _, id := range []string{"a", "b"} {
		h.inbox.items[id] = groupItem(id, "f1", "u1", TierAction)
	}
	if _, out := inboxCall(t, mux, "POST", "/api/v1/inbox/a/tier", `{"tier":"fyi"}`); out["rule_drafting"] != nil {
		t.Fatalf("one move drafted a rule: %v", out)
	}
	_, out := inboxCall(t, mux, "POST", "/api/v1/inbox/b/tier", `{"tier":"fyi"}`)
	if out["rule_drafting"] != true {
		t.Fatalf("two moves drafted no rule: %v", out)
	}
	var card proto.Interaction
	select {
	case card = <-cards:
	case <-time.After(5 * time.Second):
		t.Fatal("no card")
	}
	if card.Kind != proto.IxPermission || card.ToolName != "persona_settings" || !strings.Contains(card.Summary, "示例同事同步进展的消息算知悉即可") {
		t.Errorf("card: %+v", card)
	}
	if rule := <-drafted; rule != "示例同事同步进展的消息算知悉即可" {
		t.Fatalf("drafted %q", rule)
	}
	c := onlyCall(t, calls())
	if !strings.Contains(c.System, "告警机器人的消息需要我") || !strings.Contains(c.User, "示例同事") || !strings.Contains(c.User, "需要你 → 知悉即可") {
		t.Errorf("the draft's prompt:\n%s\n---\n%s", c.System, c.User)
	}
	if got := h.cfgRules(); got != "- 告警机器人的消息需要我\n- 示例同事同步进展的消息算知悉即可" {
		t.Errorf("rules: %q", got)
	}
	for _, f := range feedbackNow(h) {
		if f.Proposed == "" {
			t.Errorf("a correction drafted is still free: %+v", f)
		}
	}
	// The rules read as a list, edited and removed in Settings.
	code, got := inboxCall(t, mux, "GET", "/api/v1/inbox/rules", "")
	if rules, _ := got["rules"].([]any); code != 200 || len(rules) != 2 || rules[1] != "示例同事同步进展的消息算知悉即可" {
		t.Fatalf("listed: %d %v", code, got)
	}
	code, got = inboxCall(t, mux, "PUT", "/api/v1/inbox/rules", `{"rules":["  示例同事的消息算知悉即可 ",""]}`)
	if rules, _ := got["rules"].([]any); code != 200 || len(rules) != 1 || h.cfgRules() != "- 示例同事的消息算知悉即可" {
		t.Fatalf("edited: %d %v %q", code, got, h.cfgRules())
	}
	if code, _ := inboxCall(t, mux, "PUT", "/api/v1/inbox/rules", `{"rules":["`+strings.Repeat("长", rulesMax)+`"]}`); code != 400 {
		t.Errorf("too long: %d", code)
	}
	if code, got = inboxCall(t, mux, "PUT", "/api/v1/inbox/rules", `{"rules":[]}`); code != 200 || h.cfgRules() != "" {
		t.Errorf("removed: %d %v", code, got)
	}
}

func TestADeclinedRuleWritesNothing(t *testing.T) {
	h := muteHub(t)
	mux := h.routes()
	h.in.setModel(fakeModel(t, `{"rule":"示例同事的消息直接折叠"}`))
	_, drafted := ruleAnswers(h, false)
	for _, id := range []string{"a", "b", "c"} {
		h.inbox.items[id] = groupItem(id, "f1", "u1", TierFYI)
		inboxCall(t, mux, "POST", "/api/v1/inbox/"+id+"/done", `{"done":true,"ignore":true}`)
	}
	select {
	case rule := <-drafted:
		if rule != "" {
			t.Errorf("declined but %q was written", rule)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("three ignores drafted no rule")
	}
	if h.cfgRules() != "" {
		t.Errorf("rules: %q", h.cfgRules())
	}
}

// The latest corrections of the item's source are shown to the triage model as the user's habits; none, the prompt is
// as before.
func TestTriageReadsTheCorrections(t *testing.T) {
	plain := prompts.RenderInboxTriage(prompts.InboxTriage{})
	with := prompts.RenderInboxTriage(prompts.InboxTriage{Corrections: []prompts.InboxCorrection{
		{Sender: "示例同事", Chat: "示例项目群", About: "同步了发版进展", From: TierAction, To: TierFYI},
		{Sender: "示例商城", To: "ignore", From: TierFYI}}})
	if strings.Contains(plain, "亲手改过") {
		t.Error("no corrections, yet the prompt names some")
	}
	for _, want := range []string{"示例同事（示例项目群）：同步了发版进展：需要你 → 知悉即可", "示例商城：知悉即可 → 忽略（用户不感兴趣）"} {
		if !strings.Contains(with, want) {
			t.Errorf("missing %q in\n%s", want, with)
		}
	}
	h := muteHub(t)
	model, calls := captureModel(t, `{"tier":"fyi","summary":"示例","reason":"示例"}`)
	h.in.setModel(model)
	h.inbox.items["a"] = groupItem("a", "f1", "u1", TierAction)
	inboxCall(t, h.routes(), "POST", "/api/v1/inbox/a/tier", `{"tier":"fyi"}`)
	n := groupItem("n", "f2", "u2", "")
	n.TriagedAt = ""
	h.triage(context.Background(), n)
	if c := onlyCall(t, calls()); !strings.Contains(c.System, "示例同事（示例项目群）") {
		t.Errorf("triage did not read the correction:\n%s", c.System)
	}
}

// Handing an item to a conversation puts it In progress until the conversation hands back a result; a conclusion
// (even "done") waits for the user's OK under Needs you, and OK files it.
func TestHandedItemIsInProgress(t *testing.T) {
	it := groupItem("a", "f1", "u1", TierFYI)
	it.Progress = &InboxProgress{Kind: ProgressHandoff, Since: time.Now()}
	if it.status() != StatusWorking {
		t.Errorf("handed: %s", it.status())
	}
	it.Suggestion = &InboxSuggestion{Kind: SuggestConclusion, Text: "做完了"}
	if it.status() != StatusAction {
		t.Errorf("with its result: %s", it.status())
	}
	it.Progress = nil
	if it.status() != StatusFYI {
		t.Errorf("a conclusion of the twin's: %s", it.status())
	}
	raw, _ := json.Marshal(func() InboxItem { x := *it; x.Progress = &InboxProgress{Kind: ProgressHandoff}; return x.listed() }())
	if !strings.Contains(string(raw), `"progress":{"kind":"handoff"`) {
		t.Errorf("progress is not listed: %s", raw)
	}
}

func (h *inboxHub) cfgRules() string {
	h.in.mu.Lock()
	defer h.in.mu.Unlock()
	return h.in.cfg.Rules
}

// goldenCorrections are the corrections the learned-rule prompts are pinned with.
var goldenCorrections = []prompts.InboxCorrection{
	{Sender: "示例同事", Chat: "示例项目群", About: "同步了发版进展", From: TierAction, To: TierFYI},
	{Sender: "示例同事", Chat: "示例项目群", About: "同步了压测结果", From: TierAction, To: TierFYI},
	{Sender: "示例商城", Chat: "限时优惠", About: "商城促销邮件", From: TierFYI, To: "ignore"},
}

// The triage prompt with the user's corrections, and the rule draft's (docs/assistant/prompts.md; UPDATE_GOLDEN=1).
func TestGoldenInboxCorrections(t *testing.T) {
	golden(t, "inbox_triage_corrections.system", prompts.RenderInboxTriage(prompts.InboxTriage{Owner: "示例用户",
		Rules: "- 告警机器人的消息需要我", Corrections: goldenCorrections}))
	golden(t, "inbox_rule_draft.system", prompts.RenderInboxRuleDraft(prompts.InboxRuleDraft{Owner: "示例用户", Rules: "- 告警机器人的消息需要我"}))
	golden(t, "inbox_rule_draft.empty.system", prompts.RenderInboxRuleDraft(prompts.InboxRuleDraft{Language: "English"}))
	golden(t, "inbox_rule_draft.user", prompts.RenderInboxRuleDraftInput(prompts.InboxRuleDraftInput{Name: "示例同事", Tag: "sender",
		Corrections: goldenCorrections[:2]}))
}
