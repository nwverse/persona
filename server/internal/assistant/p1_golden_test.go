package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

// The prompts of P1 (#3609): the main conversation's directions and the leader's own part of its context, a leader's
// stuck task in the main conversation's supervision message, and one line per direction in the briefings' facts.
// All names are placeholders.

func TestGoldenP1Context(t *testing.T) {
	golden(t, "context.main_directions", prompts.RenderAssistantContext(prompts.AssistantContext{Main: true, Owner: "Example user",
		Snapshot: "（快照）", Directions: []prompts.ContextDirection{{ID: "lead_a", Name: "Persona 产品"}, {ID: "lead_b", Name: "基建"}}}))
	golden(t, "context.leader", prompts.RenderAssistantContext(prompts.AssistantContext{Leader: true, Owner: "Example user",
		Snapshot: "（快照）", LeaderName: "基建"}))
	golden(t, "context.main", prompts.RenderAssistantContext(prompts.AssistantContext{Main: true, Owner: "Example user", Snapshot: "（快照）"}))
}

func TestGoldenP1SuperviseLeaderStuck(t *testing.T) {
	golden(t, "supervise.leader_stuck", prompts.RenderSupervise(prompts.Supervise{LeaderStuck: true, Entries: []prompts.SuperviseEntry{{
		ID: "todo_1", Title: "平板布局", Kind: sigLeaderStuck, Detail: "基建 · 卡住超过一天，停着没做完：平板布局"}}}))
}

func TestGoldenP1BriefLeaders(t *testing.T) {
	golden(t, "routine_facts.leaders", prompts.RenderBriefFacts(prompts.BriefFacts{Hours: 12, Leaders: []prompts.BriefLeader{
		{Name: "Persona 产品", Open: 3, Waiting: 1, Stuck: 1, Reached: "侧栏分组上线并实测", Doing: "平板布局，卡在模拟器排队", NeedsYou: "确认平板要不要双栏"},
		{Name: "基建", Open: 1}}}))
}

// A leader's task stuck over a day: the extensions get leader.report stuck, and the main conversation one supervision
// entry naming the direction (no push).
func TestLeaderStuckTellsTheMainConversation(t *testing.T) {
	st := &memStore{}
	e := directionEnv(t, st)
	setSettings(t, e.s, func(v *Settings) { v.Main = "conv_main" })
	e.title("conv_lead_a", "1110 | 探索 | x")
	e.leaders("conv_lead_a")
	e.syncLeaders()
	if err := e.s.setDirectionName(context.Background(), "conv_lead_a", "基建", directionByUser); err != nil {
		t.Fatal(err)
	}
	id := e.leaderTask("平板布局", "conv_lead_a")
	later := time.Now().Add(3 * 24 * time.Hour)
	var got []assistantwire.LeaderReportEvent
	for _, r := range e.reports(later, true) {
		if r.Kind == assistantwire.LeaderStuck {
			got = append(got, r)
		}
	}
	if len(got) != 1 || got[0].Todo != id {
		t.Fatalf("stuck reports: %+v", got)
	}
	b := &e.s.supervise
	waitFor(t, "the main conversation's stuck entry", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, g := range b.pending {
			if g.Kind == sigLeaderStuck && g.Todo == id && strings.HasPrefix(g.Detail, "基建 · ") {
				return true
			}
		}
		return false
	})
	if to := e.s.digestTo(e.s.settings(context.Background()), superviseSignal{Todo: id, Kind: sigLeaderStuck}); to != "conv_main" {
		t.Fatalf("goes to %q", to)
	}
}
