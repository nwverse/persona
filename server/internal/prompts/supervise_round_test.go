package prompts

import (
	"strings"
	"testing"
)

// The rules' bullets as supervise.md writes them, by what they are for. i18n-ignore: exact generated prompt text.
const (
	ruleCloseout   = "- 带收尾记录的："
	ruleReview     = "- 等你验收的："
	ruleDecide     = "要用户拍板的 needs_user"
	ruleDecideHead = "- 要给结论时：要用户拍板的 needs_user"
	ruleCard       = "accepted 和 needs_user 会给用户发一张结论卡"
	ruleUserCheck  = "才加 user_check"
	ruleBrief      = "- 验收按任务简报："
	ruleUnjudged   = "- hub 没判断出来是不是做完了的："
	ruleStopped    = "- 停下没做完 / 在等回答的："
	ruleReport     = "- Agent 主动汇报的："
	ruleInterrupt  = "- 方向错了、在空等"
	ruleStalled    = "- 没有进展的："
	ruleOrphaned   = "- 没有 Agent 在做了的："
	ruleCut        = "- 被打断的："
	ruleFailed     = "- 没开起来的："
	ruleDownstream = "- 做完还有下游的事"
	ruleOnTask     = "- 先确认 Agent 最后这一轮在推进本任务"
	ruleFinish     = "- 只把要用户决定的事和已验收的结果告诉用户。"
	freshHeader    = "这一轮通常从干净的上下文开始"
	lastReview     = "上次结论（"
)

var allRules = []string{ruleCloseout, ruleReview, ruleDecide, ruleCard, ruleUserCheck, ruleBrief, ruleUnjudged, ruleStopped,
	ruleReport, ruleInterrupt, ruleStalled, ruleOrphaned, ruleCut, ruleFailed, ruleDownstream, ruleOnTask, ruleFinish}

func background(fresh bool, entries ...SuperviseEntry) Supervise {
	return Supervise{Leader: true, Background: true, Fresh: fresh, Entries: entries}
}

func checkRules(t *testing.T, text string, want, not []string) {
	t.Helper()
	for _, w := range want {
		if strings.Count(text, w) != 1 {
			t.Errorf("%q occurs %d times, want once:\n%s", w, strings.Count(text, w), text)
		}
	}
	for _, n := range not {
		if strings.Contains(text, n) {
			t.Errorf("%q is in the message:\n%s", n, text)
		}
	}
}

// A Fresh round carries only the rules for the kinds of its entries; the ones every round needs stay.
func TestSuperviseFreshKeepsOnlyTheRoundsSections(t *testing.T) {
	always := []string{ruleInterrupt, ruleOnTask, ruleFinish}
	idle := RenderSupervise(background(true, SuperviseEntry{ID: "t1", Title: "Example task", Kind: "idle"}))
	checkRules(t, idle, append([]string{ruleStopped, ruleDecideHead, ruleCard}, always...),
		[]string{ruleCloseout, ruleReview, ruleUserCheck, ruleBrief, ruleUnjudged, ruleReport, ruleStalled, ruleOrphaned, ruleCut, ruleFailed, ruleDownstream})

	review := RenderSupervise(background(true, SuperviseEntry{ID: "t1", Title: "Example task", Kind: "review"}))
	checkRules(t, review, append([]string{ruleReview, ruleDecide, ruleCard, ruleUserCheck, ruleBrief, ruleDownstream}, always...),
		[]string{ruleDecideHead, ruleCloseout, ruleUnjudged, ruleStopped, ruleReport, ruleStalled, ruleOrphaned, ruleCut, ruleFailed})
	if len(review) >= len(RenderSupervise(background(false, SuperviseEntry{ID: "t1", Title: "Example task", Kind: "review"})))-len(SuperviseRoundMarker) {
		t.Error("a review-only round is not shorter than the whole message")
	}

	done := RenderSupervise(background(true, SuperviseEntry{ID: "t1", Title: "Example task", Kind: "report", Report: "done"}))
	checkRules(t, done, append([]string{ruleReview, ruleDecide, ruleBrief, ruleReport, ruleDownstream}, always...),
		[]string{ruleDecideHead, ruleStopped, ruleUnjudged})
	progress := RenderSupervise(background(true, SuperviseEntry{ID: "t1", Title: "Example task", Kind: "report", Report: "progress"}))
	checkRules(t, progress, append([]string{ruleReport, ruleDecideHead}, always...), []string{ruleReview, ruleBrief, ruleDownstream})

	closeout := RenderSupervise(background(true, SuperviseEntry{ID: "t1", Title: "Example task", Kind: "idle", Closeout: &SuperviseCloseout{Status: "done"}}))
	checkRules(t, closeout, []string{ruleCloseout}, nil)

	for kind, rule := range map[string]string{"stalled": ruleStalled, "orphaned": ruleOrphaned, "interrupted": ruleCut, "failed": ruleFailed, "started": ruleFailed, "unjudged": ruleUnjudged} {
		text := RenderSupervise(background(true, SuperviseEntry{ID: "t1", Title: "Example task", Kind: kind}))
		checkRules(t, text, append([]string{rule}, always...), nil)
	}
	// Every Fresh message is still one ParseInjected reads as supervision, at its level.
	for _, text := range []string{idle, review, done, progress, closeout} {
		if in := ParseInjected(text); in == nil || in.State != "supervise" || in.Level != "triage" {
			t.Fatalf("not read back as a supervision message: %+v", in)
		}
	}
}

// Not Fresh, every rule goes in whatever the entries are, as before #7315 (the goldens in internal/assistant/testdata
// hold the exact bytes), with no header line, marker or last review.
func TestSuperviseNotFreshIsUnchanged(t *testing.T) {
	entry := SuperviseEntry{ID: "t1", Title: "Example task", Kind: "idle", LastReview: &SuperviseLastReview{Verdict: "rework", At: "11-14 10:20", Note: "add the tests"}}
	for _, role := range []struct {
		name string
		data Supervise
	}{
		{"main", Supervise{}},
		{"side", Supervise{Side: true}},
		{"leader", Supervise{Leader: true}},
		{"background", Supervise{Leader: true, Background: true}},
	} {
		t.Run(role.name, func(t *testing.T) {
			data := role.data
			data.Entries = []SuperviseEntry{entry}
			text := RenderSupervise(data)
			checkRules(t, text, allRules, []string{ruleDecideHead, freshHeader, SuperviseRoundMarker, lastReview})
			data.Entries[0].LastReview = nil
			if again := RenderSupervise(data); again != text {
				t.Fatal("the last review changed a message that is not Fresh")
			}
			if IsSuperviseRound(text) {
				t.Fatal("a message that is not Fresh reads as a round")
			}
		})
	}
}

// A Fresh message says where earlier rounds' decisions are, carries the marker line after its level tag and each
// entry's last review; IsSuperviseRound is true only for such a supervision message.
func TestSuperviseRoundMarker(t *testing.T) {
	text := RenderSupervise(Supervise{Level: "info", Leader: true, Background: true, Fresh: true, Entries: []SuperviseEntry{
		{ID: "t1", Title: "Example task", Kind: "review", LastReview: &SuperviseLastReview{Verdict: "rework", At: "11-14 10:20", Note: "add the tests"}},
		{ID: "t2", Title: "Other task", Kind: "idle"},
	}})
	lines := strings.Split(text, "\n")
	if !strings.HasPrefix(lines[2], freshHeader) || lines[3] != `<persona-supervision level="info" />` || lines[4] != SuperviseRoundMarker {
		t.Fatalf("header, level tag and marker:\n%s", strings.Join(lines[:6], "\n"))
	}
	if !strings.Contains(text, "\n   "+lastReview+"11-14 10:20）：rework，add the tests\n") || strings.Count(text, lastReview) != 1 {
		t.Fatalf("the last review:\n%s", text)
	}
	if in := ParseInjected(text); in == nil || in.State != "supervise" || in.Level != "info" {
		t.Fatalf("ParseInjected: %+v", in)
	}
	if !IsSuperviseRound(text) || !IsSuperviseRound("<persona-context>\n- 现在：x\n</persona-context>\n"+text) {
		t.Fatal("a round is not read as one")
	}
	for _, other := range []string{
		"hello " + SuperviseRoundMarker,
		"Look:\n" + SuperviseRoundMarker + "\nis a tag",
		RenderSupervise(Supervise{Entries: []SuperviseEntry{{ID: "t1", Title: "Example task", Kind: "idle"}}}),
	} {
		if IsSuperviseRound(other) {
			t.Fatalf("read as a round: %q", other)
		}
	}
}
