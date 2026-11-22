package prompts

import (
	"encoding/json"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// Keep the clock prefix for old conversation titles and briefing delivery receipts. The payload
// identifies the run independently of its translated heading or the language of the owner's text.
const ScheduleMarkerPrefix = "⏰ <persona-schedule>"

type scheduleMetadata struct {
	State       string `json:"state"`
	Title       string `json:"title"`
	PromptBytes int    `json:"prompt_bytes"`
	RunKey      string `json:"run_key,omitempty"`
}

func (t TaskRun) Marker() string {
	state := "daily"
	if t.Once {
		state = "once"
	} else if t.Interval {
		state = "interval"
	}
	raw, _ := json.Marshal(scheduleMetadata{State: state, Title: t.Label, PromptBytes: len(t.Prompt), RunKey: t.RunKey})
	return string(raw)
}

// ScheduledRun is the identity and the exact instructions in a localized scheduled turn.
type ScheduledRun struct {
	State, Title, Prompt, RunKey string
}

// ReadScheduledRun recovers the prompt without copying it into model-visible metadata. Its byte count
// distinguishes the actual boundary even when a quoted instruction contains the same delimiter.
func ReadScheduledRun(text string) *ScheduledRun {
	if UserAuthoredContext(text) {
		return nil
	}
	for {
		loc := leadContextRe.FindStringIndex(text)
		if loc == nil {
			break
		}
		text = text[loc[1]:]
	}
	head, body, _ := strings.Cut(strings.TrimSpace(text), "\n")
	raw, ok := strings.CutPrefix(head, ScheduleMarkerPrefix)
	if !ok {
		return nil
	}
	raw, ok = strings.CutSuffix(raw, "</persona-schedule>")
	var m scheduleMetadata
	if !ok || json.Unmarshal([]byte(raw), &m) != nil || m.Title == "" ||
		(m.State != "once" && m.State != "daily" && m.State != "interval") {
		return nil
	}
	canonical, _ := json.Marshal(m)
	if raw != string(canonical) {
		return nil
	}
	body, ok = strings.CutSuffix(body, "\n</persona-schedule-instructions>")
	if !ok || m.PromptBytes < 0 || m.PromptBytes > len(body) {
		return nil
	}
	start := len(body) - m.PromptBytes
	if !strings.HasSuffix(body[:start], "\n\n<persona-schedule-instructions>\n") {
		return nil
	}
	return &ScheduledRun{State: m.State, Title: m.Title, Prompt: body[start:], RunKey: m.RunKey}
}

func scheduleInjected(state, title, detail string) *proto.Injected {
	line := "定时任务「" + title + "」开始运行"
	if state == "once" {
		line = "跟进「" + title + "」到时间了"
	}
	return &proto.Injected{Kind: proto.InjectedFollowup, State: state, Title: title, Detail: detail, Line: line}
}
