package lark

import (
	"context"
	"errors"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// feishuTopic is the chat and thread a message was said in (the core's feishu_route.go names it too).
type feishuTopic struct {
	Chat, Thread string
	UnboundMode  assistantwire.TopicTodoMode
	Pairing      string
}

// target is where what is sent for that topic goes: its chat, as a reply into the thread. The zero topic (nil) is the
// private chat, where nothing changes.
func (t *feishuTopic) target() feishuTarget {
	if t == nil {
		return feishuTarget{}
	}
	return feishuTarget{ChatID: t.Chat, InThread: true}
}

// runTo is where a run's messages go, from its channel values (Ext): under the owner's "/read" in its group
// (the lark module's read.go), the topic it was asked in, else the private chat.
func runTo(ext map[string]string) feishuTarget {
	if readRun(ext) {
		return feishuTarget{ChatID: ext[readChatKey], InThread: ext[readThreadKey] != "", NoDetour: true, Read: true}
	}
	if ext["chat"] == "" || ext["thread"] == "" {
		return feishuTarget{}
	}
	return (&feishuTopic{Chat: ext["chat"], Thread: ext["thread"]}).target()
}

// threadRoutes are who can get what was said in the topic of open todo t, best first: the agent it has that is running
// (the one that most needs it, pickTaskAgent; a closed one, offline or interrupted, is not counted), then the
// conversation that opened it (for a question's to-do, which has none, the conversation that asked: its record,
// ask_todo.go), then the main conversation. One the bot's switches do not allow, or that is not there,
// is left out; the error is why none is left (nil when there are routes).
func (s *Service) threadRoutes(ctx context.Context, t Todo, f FeishuFeatures) ([]botTarget, error) {
	var tried []botTarget
	var running []proto.Session
	for _, a := range s.taskAgents(ctx, t) {
		if a.Status != proto.StatusOffline && a.Status != proto.StatusInterrupted {
			running = append(running, a)
		}
	}
	if len(running) > 0 {
		c := pickTaskAgent(running)
		tried = append(tried, botTarget{Kind: targetAgent, MachineID: c.MachineID, SessionID: c.SessionID})
	}
	if t.Origin != "" {
		tried = append(tried, botTarget{Kind: targetPersona, SessionID: t.Origin})
	} else if a, ok := s.askTodoOf(ctx, t.ID); ok {
		tried = append(tried, botTarget{Kind: targetPersona, SessionID: a.SessionID})
	}
	tried = append(tried, botTarget{Kind: targetMain})
	l := s.botLang(ctx)
	var out []botTarget
	var last error
	for _, c := range tried {
		if err := botAllowed(l, f, c); err != nil {
			last = err
			continue
		}
		if c.Kind == targetPersona {
			// The conversation is looked for: a todo outlives the conversation that opened it.
			if _, ok := s.session(ctx, s.settings(ctx).MachineID, c.SessionID); !ok {
				continue
			}
		}
		if _, err := s.resolveBotTarget(ctx, c); err != nil {
			last = err
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 && last == nil {
		last = errors.New(i18n.T(l, "assistant-feishu.no_assistant_machine"))
	}
	return out, last
}

const readChatKey = "read_chat"

const readThreadKey = "read_thread"

// readRun says a run answers a /read: its answer goes under the message in that group, quietly.
func readRun(ext map[string]string) bool { return ext[readChatKey] != "" }

// openTarget is who takes a to-do opened from a topic or from elsewhere: the main conversation, which origin is the id
// of. fail says why not, in the bot's language, "" when it can.
func (s *Service) openTarget(ctx context.Context, st FeishuStatus) (target botTarget, origin, fail string) {
	l := s.botLang(ctx)
	failed := func(err error) string {
		return i18n.T(l, "assistant-feishu.thread_open_failed", i18n.Args{"error": clip(oneLine(err.Error()), 200)})
	}
	target = botTarget{Kind: targetMain}
	if err := botAllowed(l, st.Features, target); err != nil {
		return target, "", failed(err)
	}
	rt, err := s.resolveBotTarget(ctx, target)
	if err != nil {
		return target, "", failed(err)
	}
	origin = s.settings(ctx).Main
	if _, ok := s.session(ctx, rt.machine, origin); origin == "" || rt.isNew || !ok {
		return target, "", i18n.T(l, "assistant-feishu.thread_open_no_main")
	}
	return target, origin, ""
}
