package assistant

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Run the saved task twice on the same node. Only the owner's hello changes.
func TestScheduledReportLanguageAtDelivery(t *testing.T) {
	for _, c := range []struct {
		id, english, japanese, chinese string
	}{
		{"morning", "morning briefing", "朝のブリーフィング", "给我今天的早报"},
		{"evening", "evening review", "夜の振り返り", "今天的晚间回顾"},
	} {
		t.Run(c.id, func(t *testing.T) {
			f := newFakeHub().withMachine()
			s := newStorelessService(t, f)
			routines := tasksFrom(defaultRoutines(i18n.En), nil)
			setTasks(s, routines...)
			saved, _ := taskByID(s, c.id)
			for _, want := range []struct{ locale, report, facts string }{
				{"en", c.english, "Facts calculated by Persona"},
				{"ja", c.japanese, "Persona が集計した事実"},
				{"zh-Hans", c.chinese, "Persona 算好的事实"},
				{"", c.chinese, "Persona 算好的事实"},
			} {
				f.mu.Lock()
				f.hello.Locale = want.locale
				f.mu.Unlock()
				if _, err := runNow(t, s, c.id); err != nil {
					t.Fatal(err)
				}
				p := f.lastSend(t)
				text := p["text"].(string)
				for _, word := range []string{want.report, want.facts} {
					if !strings.Contains(text, word) {
						t.Errorf("owner %q: delivered %s lacks %q", want.locale, c.id, word)
					}
				}
				if in := prompts.ParseInjected(text); in == nil || in.State != "daily" || in.Title != saved.Title {
					t.Errorf("owner %q: wrong scheduled message identity: %+v", want.locale, in)
				}
			}
		})
	}
}

func TestScheduledOwnerPromptVerbatim(t *testing.T) {
	const custom = "  Report this owner's wording: 会議\n\n  Keep  the spacing.  \n"
	f := newFakeHub().withMachine()
	f.hello.Locale = "ja"
	s := newStorelessService(t, f)
	task := Task{ID: "morning", Title: "Review Ω", Repeat: repeatDaily, Time: "09:00", Prompt: custom, Enabled: true}
	setTasks(s, tasksFrom(defaultRoutines(i18n.En), nil)...)
	body, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/assistant/tasks/"+task.ID, bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("edit schedule: %d %s", rec.Code, rec.Body.String())
	}
	if stripScheduleHeader(" \t\n ") != "" || stripScheduleHeader(custom) != custom {
		t.Fatal("the schedule tool must reject blank input and preserve other input")
	}
	saved, _ := taskByID(s, task.ID)
	if saved.Prompt != custom {
		t.Fatalf("the app's saved edit changed: %q", saved.Prompt)
	}
	if _, err := runNow(t, s, task.ID); err != nil {
		t.Fatal(err)
	}
	text := f.lastSend(t)["text"].(string)
	if !strings.Contains(text, custom) || !strings.Contains(text, "定期タスク") {
		t.Error("the localized task must deliver the owner's exact text, including whitespace")
	}
	in := prompts.ParseInjected(text)
	if in == nil || in.Kind != proto.InjectedFollowup || in.Title != task.Title || in.Detail != custom {
		t.Errorf("custom instructions in transcript: %+v", in)
	}
}

func TestScheduledFormerDefaultSignatures(t *testing.T) {
	for _, c := range []struct {
		id             string
		former, hashes []string
	}{
		{"morning", prompts.FormerMorningRoutines(), []string{
			"2f51d00bc4f0fc0065a528a379346d66bd39c6de1332a83188567279bb285900",
			"e69b42eb16e120ff80bc4b54f0ac4109bb29a887d638345542d3cf227213e1dc",
			"d09bea314e20c70037ec0c9138cb9c3bef3357dcef27de3399c03440d04d558b"}},
		{"evening", prompts.FormerEveningRoutines(), []string{
			"a700536875a0e83330855a5734b5ba304ed4caaffc2d0e05e7de69c31fe6f440",
			"a6436a9dcf8c1558322c8742f7201d4c08bafec2c1fb065f1bb304ff2cb5e445"}},
	} {
		if len(c.former) != len(c.hashes) {
			t.Fatalf("historical %s prompts: %d", c.id, len(c.former))
		}
		for i, old := range c.former {
			if fmt.Sprintf("%x", sha256.Sum256([]byte(old))) != c.hashes[i] {
				t.Errorf("%s signature %d changed", c.id, i+1)
			}
			tasks := []Task{{ID: c.id, Prompt: old}, {ID: c.id, Prompt: old + " "}, {ID: "custom", Prompt: old}}
			if !upgradeDefaultRoutines(tasks) || tasks[0].Prompt == old || tasks[1].Prompt != old+" " || tasks[2].Prompt != old {
				t.Fatalf("unexpected upgrade of %s history %d", c.id, i+1)
			}
		}
	}
}

func TestGoldenScheduledReportDelivery(t *testing.T) {
	for _, locale := range []string{"", "zh-Hans", "en", "ja"} {
		name := locale
		if name == "" {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			f := newFakeHub().withMachine()
			s := newStorelessService(t, f)
			f.mu.Lock()
			f.hello.Locale = locale
			f.mu.Unlock()
			tasks := tasksFrom(defaultRoutines(i18n.En), nil)
			setTasks(s, tasks...)
			var turns []string
			for _, task := range tasks {
				if _, err := runNow(t, s, task.ID); err != nil {
					t.Fatal(err)
				}
				text := f.lastSend(t)["text"].(string)
				key := runKeyIn(proto.Item{Role: "user", Kind: proto.KindText, Source: taskMessageSource(task), Text: text})
				if key == "" {
					t.Fatal("scheduled run key is missing from the captured send")
				}
				turns = append(turns, strings.ReplaceAll(stripContext(text), key, "RUN_KEY"))
			}
			fileLocale := locale
			if fileLocale == "" {
				fileLocale = "zh-Hans"
			}
			golden(t, "schedule.delivery."+fileLocale, strings.Join(turns, "\n\n---\n\n"))
		})
	}
}
