package assistant

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// sourceFinalReply reads the current task's finished answer. A shared main conversation is only
// read by the client message ID of this request. Its latest turn may belong to a different chat.
func (s *Service) sourceFinalReply(ctx context.Context, t Todo) (text string, wait time.Duration) {
	if len(t.Agents) == 0 {
		return s.sourceMainReply(ctx, t)
	}
	start := t.CreatedAt
	if t.ReopenedAt != nil {
		start = *t.ReopenedAt
	}
	agents := slices.Clone(t.Agents)
	slices.SortStableFunc(agents, func(a, b TodoAgent) int { return b.LinkedAt.Compare(a.LinkedAt) })
	for _, a := range agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		items, ok := s.lastTurnItems(ctx, a.MachineID, a.SessionID)
		if !ok || len(items) == 0 {
			wait = sourceReportWait
			continue
		}
		_, side := taskTurnSource(t, items)
		if side != "" || !sourceTurnInRound(items, start, *t.DoneAt) {
			continue
		}
		reply, pending := sourceReplyItems(items)
		if pending {
			wait = sourceReportWait
		}
		if len(reply) == 0 || reply[len(reply)-1].TS.Before(a.LinkedAt) {
			continue
		}
		if text := readSourceReply(ctx, busTranscript{s: s, machine: a.MachineID, session: a.SessionID}, reply); text != "" {
			return text, 0
		}
		wait = sourceReportWait
	}
	return "", wait
}

// A linked session can have history before the task, and can keep working after it closes.
func sourceTurnInRound(items []proto.Item, start, done time.Time) bool {
	var prompt time.Time
	for _, it := range items {
		if (it.Role != "user" || it.Kind != proto.KindText) && !it.Wake {
			continue
		}
		if !prompt.IsZero() || it.TS.IsZero() || it.TS.Before(start) || it.TS.After(done) {
			return false
		}
		prompt = it.TS
	}
	return !prompt.IsZero()
}

func (s *Service) sourceMainReply(ctx context.Context, t Todo) (text string, wait time.Duration) {
	handoff := t.Source.Handoff
	if handoff == "" {
		handoff = t.Source.Anchor // existing private-chat handoffs already kept this id
	}
	machine := s.settings(ctx).MachineID
	if handoff == "" || t.Origin == "" || machine == "" || t.ReopenedAt != nil {
		return "", 0
	}
	// A second turn can start by the time the completion is recorded. Only the handoff's own
	// exchange is eligible, even when a newer answer is present.
	var page struct{ Items []proto.Item }
	if err := s.call(ctx, machine, DeviceSessionItems, map[string]any{"session_id": t.Origin, "turns": 5}, &page); err != nil {
		return "", sourceReportWait
	}
	id := channel.ClientMsgID(channel.Feishu, handoff)
	at := slices.IndexFunc(page.Items, func(it proto.Item) bool {
		return it.Role == "user" && it.Kind == proto.KindText && it.ClientMsgID == id
	})
	if at < 0 {
		// The node can publish the prompt after the todo closes. The channel run can
		// also be registered after that close or already have been folded into another run.
		return "", sourceReportWait
	}

	heldThrough := -1
	if run, _ := s.channelRunByID("fb_" + handoff); run != nil {
		s.chans.mu.Lock()
		if run.Held {
			heldThrough = run.Base
		}
		s.chans.mu.Unlock()
	}
	reply, wait := sourceMainReplyItems(page.Items[at+1:], heldThrough)
	if len(reply) == 0 {
		return "", wait
	}
	if text := readSourceReply(ctx, busTranscript{s: s, machine: machine, session: t.Origin}, reply); text != "" {
		return text, 0
	}
	return "", sourceReportWait
}

// sourceMainReplyItems follows wakes only when their tool call belongs to the handoff or
// one of its continuations. An intervening request in the shared conversation cannot
// supply the answer. Tool results still identify background work after its run is gone.
func sourceMainReplyItems(items []proto.Item, heldThrough int) ([]proto.Item, time.Duration) {
	owned, start := true, 0
	wait := sourceReportWait
	var calls, background, reply []proto.Item
	for i, it := range items {
		if it.Task != nil && taskEnded(it.Task.Status) {
			background = slices.DeleteFunc(background, func(launch proto.Item) bool {
				return launchedIn([]proto.Item{launch}, it.Task)
			})
		}
		if it.Role == "user" || it.Wake {
			if it.Wake && launchedIn(calls, it.Task) {
				owned, start = true, i+1
				reply, wait = nil, sourceReportWait
			} else {
				if owned {
					wait = 0
				}
				owned = false
			}
			continue
		}
		if !owned {
			continue
		}
		switch it.Kind {
		case proto.KindToolUse:
			calls = append(calls, it)
		case proto.KindToolResult:
			if !it.IsError && transcript.LaunchedBackgroundID(it.Text) != "" {
				calls = append(calls, it)
				background = append(background, it)
			}
		case turnDurationKind:
			owned = false
			if len(background) != 0 || heldThrough >= 0 && (start == 0 || it.Seq <= heldThrough) {
				continue
			}
			turn := items[start : i+1]
			reply, _ = sourceReplyItems(turn)
			if len(reply) != 0 || channel.Stopped(turn) != "" {
				wait = 0
			}
		}
	}
	if len(reply) != 0 {
		return reply, 0
	}
	if len(background) != 0 || heldThrough >= 0 {
		return nil, channel.HeldMost
	}
	if owned && channel.Stopped(items[start:]) != "" {
		return nil, 0
	}
	return nil, wait
}

// Only assistant text after the last tool call or result is part of the final answer.
func sourceReplyItems(items []proto.Item) ([]proto.Item, bool) {
	if channel.Stopped(items) != "" {
		return nil, false
	}
	if _, ok := turnEndOf(items); !ok {
		return nil, true
	}
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		if it.Kind == proto.KindToolUse || it.Kind == proto.KindToolResult || it.Role == "user" || it.Wake {
			items = items[i+1:]
			break
		}
	}
	return channel.Answer(items), false
}

func readSourceReply(ctx context.Context, src channel.ItemSource, reply []proto.Item) string {
	var parts []string
	for _, it := range reply {
		text := it.Text
		if it.FullSize > 0 {
			full, err := src.Full(ctx, it.ID)
			if err != nil || full.ID != it.ID || full.Role != "assistant" || full.Kind != proto.KindText || full.FullSize > 0 || full.Text == "" {
				return "" // do not pass off a truncated item as the full answer
			}
			text = full.Text
		}
		if text = strings.TrimSpace(text); text != "" && !channel.Placeholder(text) {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}
