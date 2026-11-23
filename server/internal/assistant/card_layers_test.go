package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The card ledger (card_layers.go): each push to the owner counted by day with the layer its source gives it.

// cardsToday is the counts of the day it is now in s's zone, by source.
func cardsToday(t *testing.T, s *Service) map[string]cardCount {
	t.Helper()
	ctx := context.Background()
	got := map[string]cardCount{}
	for _, r := range s.cardsOn(ctx, time.Now().In(s.location(ctx)).Format(time.DateOnly)) {
		c := got[r.Source]
		c.Source, c.Layer, c.N = r.Source, r.Layer, c.N+r.N
		got[r.Source] = c
	}
	return got
}

// A whitelisted source counts as layer 4, one off the whitelist with its own layer and name, a push no source was
// named for as layer 0, untagged; and the pushes themselves go out as before.
func TestCardLedgerCountsPushesByLayerAndSource(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	s.noteCard(ctx, cardConfirmOutbound, Notification{SessionID: "conv1"})
	s.push(withCard(ctx, cardPlanWaiting), Notification{Title: "a plan waits", SessionID: "conv1"})
	s.push(withCard(ctx, cardPlanWaiting), Notification{Title: "another plan waits", SessionID: "conv1"})
	s.push(ctx, Notification{Title: "from nowhere"})

	sent := 0
	for _, n := range f.pushed() {
		if strings.Contains(n.Title, "plan waits") || n.Title == "from nowhere" {
			sent++
		}
	}
	if sent != 3 {
		t.Fatalf("%d of the pushes went out, want all 3: the ledger only records", sent)
	}
	got := cardsToday(t, s)
	if c := got[cardConfirmOutbound]; c.Layer != cardLayerOwner || c.N != 1 {
		t.Errorf("confirm_outbound = %+v, want layer 4 once", c)
	}
	if c := got[cardPlanWaiting]; c.Layer != cardLayerNone || c.N != 2 {
		t.Errorf("plan_waiting = %+v, want layer 1 twice", c)
	}
	if c := got[cardUntagged]; c.Layer != cardLayerUnclassified || c.N != 1 {
		t.Errorf("untagged = %+v, want layer 0 once", c)
	}
	if line := cardLedgerText(i18n.ZhHans, s.cardsOn(ctx, time.Now().In(s.location(ctx)).Format(time.DateOnly)), false); line != "今日推给你 4 张，其中第 1–3 层 3 张（来源 plan_waiting×2、untagged×1）" {
		t.Errorf("line = %q", line)
	}
}

// The counts are kept in the database: a manager started again reads them.
func TestCardLedgerSurvivesARestart(t *testing.T) {
	st := newFleetMem()
	s, _ := newTaskServiceOn(t, st)
	s.noteCard(context.Background(), cardLeaderNeedsUser, Notification{SessionID: "lead1"})
	again, _ := newTaskServiceOn(t, st)
	if c := cardsToday(t, again)[cardLeaderNeedsUser]; c.Layer != cardLayerOwner || c.N != 1 {
		t.Fatalf("after a restart leader_needs_user = %+v, want layer 4 once", c)
	}
}

// An Agent's card takes its source from its kind, the manager's own from what ask was told.
func TestInputCardSource(t *testing.T) {
	s, _ := newQuietService(t)
	s.cardAsks.Store("ix_own", cardConfirmOutbound)
	for _, tc := range []struct {
		ix        proto.Interaction
		escalated bool
		want      string
	}{
		{proto.Interaction{ID: "ix_own", Kind: proto.IxPermission, ToolName: "agent_send"}, false, cardConfirmOutbound},
		{proto.Interaction{ID: "ix_a", Kind: proto.IxElicitation}, true, cardAgentSecretForm},
		{proto.Interaction{ID: "ix_b", Kind: proto.IxQuestion, Secret: true}, false, cardAgentSecretForm},
		{proto.Interaction{ID: "ix_c", Kind: proto.IxPermission, ToolName: "Bash"}, true, cardAskEscalated},
		{proto.Interaction{ID: "ix_d", Kind: proto.IxPermission, ToolName: "Bash"}, false, cardAgentPermission},
		{proto.Interaction{ID: "ix_e", Kind: proto.IxPlan}, false, cardAgentPlan},
		{proto.Interaction{ID: "ix_f", Kind: proto.IxQuestion, ToolName: "ask_user"}, false, cardAskUser},
		{proto.Interaction{ID: "ix_g", Kind: proto.IxScreen}, false, cardAgentQuestion},
	} {
		if got := s.inputCardSource(tc.ix, tc.escalated); got != tc.want {
			t.Errorf("%s: source %q, want %q", tc.ix.ID, got, tc.want)
		}
	}
	for _, src := range []string{cardConfirmOutbound, cardAgentSecretForm, cardAskEscalated, cardAgentPermission, cardAgentPlan, cardAskUser, cardAgentQuestion} {
		if cardLayerOf(src) == cardLayerUnclassified {
			t.Errorf("source %s has no layer in cardLayers", src)
		}
	}
}

// The reconciliation line: the morning briefing reconciles yesterday, the evening one today so far; all layer 4 says
// so; nothing pushed says nothing.
func TestCardLedgerLine(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	if day, yesterday := cardLedgerDay(time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC), loc); day != "2026-11-22" || !yesterday {
		t.Errorf("09:00 local reconciles %s (yesterday %v), want 2026-11-22", day, yesterday)
	}
	if day, yesterday := cardLedgerDay(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC), loc); day != "2026-11-23" || yesterday {
		t.Errorf("20:00 local reconciles %s (yesterday %v), want 2026-11-23", day, yesterday)
	}
	rows := []cardCount{
		{Source: cardDecision, Layer: cardLayerOwner, Owner: "lead1", N: 3},
		{Source: cardTaskFailed, Layer: cardLayerLead, Owner: "lead1", N: 1},
		{Source: cardAgentPermission, Layer: cardLayerLead, Owner: "lead1", N: 2},
		{Source: cardTaskFailed, Layer: cardLayerLead, Owner: "conv1", N: 1},
	}
	if got := cardLedgerText(i18n.ZhHans, rows, true); got != "昨日推给你 7 张，其中第 1–3 层 4 张（来源 agent_permission×2、task_failed×2）" {
		t.Errorf("zh = %q", got)
	}
	if got := cardLedgerText(i18n.En, rows, false); got != "Pushed to you today: 7, of which layers 1–3: 4 (sources agent_permission×2, task_failed×2)" {
		t.Errorf("en = %q", got)
	}
	if got := cardLedgerText(i18n.ZhHans, rows[:1], false); got != "今日推给你 3 张，都是第 4 层" {
		t.Errorf("all layer 4 = %q", got)
	}
	if got := cardLedgerText(i18n.ZhHans, nil, false); got != "" {
		t.Errorf("nothing pushed = %q, want no line", got)
	}
}

// The briefing card carries the line when there is one, and nothing of it when nothing was pushed.
func TestBriefCardCardLedgerLine(t *testing.T) {
	line := "今日推给你 4 张，其中第 1–3 层 3 张（来源 plan_waiting×2、untagged×1）"
	at := time.Date(2026, 11, 23, 20, 0, 0, 0, time.UTC)
	_, plain, _ := (&Service{}).briefCard(&briefFacts{l: i18n.ZhHans, loc: time.UTC, At: at, CardLedger: line}, "晚报", "")
	if !strings.Contains(plain, line) {
		t.Errorf("the card does not carry the ledger line:\n%s", plain)
	}
	_, plain, _ = (&Service{}).briefCard(&briefFacts{l: i18n.ZhHans, loc: time.UTC, At: at}, "晚报", "")
	if strings.Contains(plain, "推给你") {
		t.Errorf("a day with nothing pushed has a ledger line:\n%s", plain)
	}
}
