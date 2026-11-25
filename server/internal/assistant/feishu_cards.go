package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// interactionRuns are the runs that decide where ix's card goes: the latest asked in the owner's DM and the latest
// asked in a topic, of the conversation it belongs to (either may be nil). While a /read of the conversation goes on,
// the card may be that run's, and a /read's group gets its answer and nothing else: the card goes to the owner's DM,
// so no topic is returned either (another topic's run of the same conversation would take the /read's question into
// its group, and a line in that topic would point at a card it has nothing to do with).
func (s *Service) interactionRuns(ix proto.Interaction) (private, topic *channel.Run) {
	read := false
	for _, r := range s.channelRuns(channel.Feishu) {
		if r.MachineID != ix.MachineID || r.SessionID != ix.SessionID {
			continue
		}
		if readRun(s.runExts(&r, readChatKey)) {
			read = true
			continue
		}
		if runTo(s.runExts(&r, "chat", "thread")).ChatID == "" {
			if private == nil || !r.Started.Before(private.Started) {
				private = &r
			}
		} else if topic == nil || !r.Started.Before(topic.Started) {
			topic = &r
		}
	}
	if read {
		topic = nil
	}
	return private, topic
}

// dotJoin joins the parts that are not empty with " · ".
func dotJoin(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// ixTask is the task ix's agent works on as the cards name it: its direction and title; "" when it works on none. same:
// the agent's own title is the task's, so the label is the direction alone, to go before the agent. A card closing after
// its task ended or its session moved on keeps the task it was posted for when that name is available.
func (s *Service) ixTask(ctx context.Context, ix proto.Interaction) (label string, same bool) {
	machine := s.canonical(ix.MachineID)
	t, ok := s.taskOf(ctx, machine, ix.SessionID)
	if ix.TaskTitle != "" && (!ok || t.Title != ix.TaskTitle) {
		t, ok = s.closedTaskOf(ctx, machine, ix.SessionID)
		if !ok || t.Title != ix.TaskTitle {
			return clip(oneLine(ix.TaskTitle), 70), false
		}
	}
	task := clip(oneLine(t.Title), 70)
	if !ok || task == "" {
		return "", false
	}
	direction := s.feishuDirection(ctx, t, s.feishuDirections(ctx))
	if transcript.Title(ix.SessionTitle) == t.Title {
		return direction, true
	}
	return direction + task, false
}

// jsonStrings are the string values in raw (a tool's input, its secrets already redacted), not its keys or braces.
func jsonStrings(raw string) []string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			keys := slices.Sorted(maps.Keys(v))
			for _, k := range keys {
				walk(v[k])
			}
		}
	}
	walk(v)
	return out
}

// pendingQuestion is one line saying what a pending interaction waits for; "" when it says nothing.
func pendingQuestion(ix proto.Interaction) string {
	switch ix.Kind {
	case proto.IxQuestion:
		return strings.TrimSpace(questionSummary(ix))
	case proto.IxPlan:
		return strings.TrimSpace(planTitle(ix.Plan))
	case proto.IxPermission:
		return strings.TrimSpace(ixToolName(ix) + " " + oneLine(ix.Summary))
	}
	return strings.TrimSpace(oneLine(ix.Summary))
}
