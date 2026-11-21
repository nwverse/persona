package assistant

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestLocalizedScheduleSourceAndCopiedInstructions(t *testing.T) {
	const title = "Review α / 日程"
	const instructions = "  owner's request: 秋\n\nkeep <project>Atlas</project> as written  \n"
	for _, tc := range []struct{ lang, repeat, state, heading, source string }{
		{"en", repeatOnce, "once", "Your scheduled follow-up", proto.SourceFollowup},
		{"ja", repeatOnce, "once", "以前に予約したフォローアップ", proto.SourceFollowup},
		{"en", repeatInterval, "interval", "every 30 min", proto.SourcePatrol},
		{"ja", repeatInterval, "interval", "30分ごと", proto.SourcePatrol},
	} {
		t.Run(tc.lang+"_"+tc.state, func(t *testing.T) {
			f := newFakeHub().withMachine()
			f.hello.Locale = tc.lang
			s := newStorelessService(t, f)
			now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
			task := Task{ID: "schedule-example", Title: title, Prompt: instructions, Repeat: tc.repeat, EveryMinutes: 30, At: now, Enabled: true, Push: pushAsked, Inbox: "worthy", StateNote: &TaskStateNote{Text: "cursor = 先週"}}
			setTasks(s, task)
			if _, err := s.runTask(task, now, now.Add(5*time.Minute), false, false); err != nil {
				t.Fatal(err)
			}
			text := f.lastSend(t)["text"].(string)
			it := proto.Item{Role: "user", Kind: proto.KindText, Text: text}
			source := prompts.ItemSource(it)
			in := prompts.ParseInjected(text)
			if source == nil || source.Kind != tc.source || source.Title != title || in == nil || in.Kind != proto.InjectedFollowup || in.State != tc.state || in.Title != title || in.Detail != instructions {
				t.Fatalf("captured source and schedule identity: %+v, %+v", source, in)
			}
			for _, word := range []string{tc.heading, "notify_user", "inbox_post", instructions} {
				if !strings.Contains(text, word) {
					t.Errorf("missing %q", word)
				}
			}
			body := text[strings.Index(text, prompts.ScheduleMarkerPrefix):]
			if stripScheduleHeader(body) != instructions || !copiesRun(ScheduleRun{Title: title}, "", body) {
				t.Error("copying this scheduled turn did not recover the original task")
			}
			key := runKeyIn(it)
			if tc.repeat == repeatInterval {
				if key == "" || !strings.Contains(text, "cursor = 先週") || !strings.Contains(text, "schedule_state_set") {
					t.Error("missing recurring state or its run key")
				}
			} else if key != "" {
				t.Error("one-off follow-up has a recurring state key")
			}
			if in := prompts.ParseInjected(prompts.ProtectUserMessage(body)); in != nil {
				t.Errorf("owner's paste classified as system turn: %+v", in)
			}
		})
	}
}

func TestScheduledNorthStarReportLanguage(t *testing.T) {
	f := newFakeHub()
	s := newStorelessService(t, f)
	s.store = func() Store { return failingNorthStarStore{Store: &memStore{}} }
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	for _, test := range []struct{ locale, lead string }{
		{"", "Persona 算好的北极星指标"}, {"zh-Hans", "Persona 算好的北极星指标"}, {"en", "North star metrics calculated by Persona"}, {"ja", "今回の実行開始時に Persona が集計したノーススター指標"},
	} {
		f.mu.Lock()
		f.hello.Locale = test.locale
		f.mu.Unlock()
		facts := s.runFacts(t.Context(), Task{ID: "daily-report", Report: true}, at)
		if !strings.HasPrefix(facts, test.lead) || facts != s.northStarRunFacts(t.Context(), at) {
			t.Errorf("%q report start and north-star helper diverged: %q", test.locale, facts)
		}
	}
}

func TestScheduledFactsRetainOriginalWorkAndRecording(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.En, i18n.Ja, i18n.ZhHans} {
		f := briefFacts{l: l, Card: true, WaitingUnknown: true, LaterTotal: 2,
			Leaders:    []prompts.BriefLeader{{Name: "Atlas", Open: 3, Stuck: 1, Reached: "Review completed"}},
			Briefs:     []prompts.BriefTask{{Words: "Owner’s original 中文", Now: "Version 2", Status: "blocked", Need: "Owner action", Scope: []string{"scope Δ"}}},
			BriefsDone: []prompts.BriefTask{{Goal: "original goal", By: "leader", Result: []prompts.BriefCriterion{{Criterion: "Criterion 甲", State: "dropped", Change: "Original change"}}, AlsoDone: []string{"Extra work"}}},
			InboxDue:   []prompts.BriefFollow{{Title: "Check again", Who: "Example team", Next: "Review evidence"}},
			Day:        &prompts.BriefDay{Date: "2026-11-02", From: "09:00", To: "10:00", Transcribed: "09:55", Parts: []prompts.BriefDayPart{{Made: "10:01", Summary: "Recording says こんにちは"}}, Actions: []prompts.BriefDayAction{{What: "Call Sam"}}},
		}
		got := f.modelText()
		for _, literal := range []string{"Owner’s original 中文", "Review completed", "scope Δ", "Criterion 甲", "Original change", "Extra work", "Example team", "Review evidence", "09:55", "Recording says こんにちは", "Call Sam"} {
			if !strings.Contains(got, literal) {
				t.Errorf("%s facts lost %q", l, literal)
			}
		}
	}
}

func TestScheduledKnowledgeHintUsesTheRunLanguage(t *testing.T) {
	const dir = "/example/notes"
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		lang i18n.Lang
		hint string
	}{
		{i18n.En, "Knowledge base at"}, {i18n.Ja, "ナレッジベース"}, {i18n.ZhHans, "知识库"}, {"", "知识库"},
	} {
		task := Task{ID: "daily", Title: "Atlas", Prompt: "Project update", Repeat: repeatDaily, kbDir: dir, runLang: tc.lang}
		for _, continued := range []bool{false, true} {
			got := taskPrompt(task, at, at, time.UTC, true, true, continued)
			for _, word := range []string{tc.hint, dir, "kb_list projects", "kb_get", "kb_update"} {
				if strings.Contains(got, word) == continued {
					t.Errorf("%s continued=%v: unexpected presence of %q", tc.lang, continued, word)
				}
			}
			in := prompts.ParseInjected(got)
			if in == nil || in.Detail != task.Prompt {
				t.Fatalf("knowledge hint leaked into transcript detail: %+v", in)
			}
		}
	}
}
