package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/larkhost"
)

// Posting into a todo's chat thread (#5279, docs/assistant/todos.md "Chat thread", docs/extensions/bots.md): a todo bound to a Lark topic
// tells the topic, in one short line with a link back to the todo, when
//
//   - a decision is noted (any source but the thread's own: postable),
//   - an Agent working on it finishes a turn or needs the user (the events the phone's pushes are made of), or
//   - it is done or closed.
//
// A todo gets at most one line a minute: what happens meanwhile is joined into the next one ("3 new decisions: …").
// Nothing is sent unless the bot may send into the chat (the hub's rule, FeishuStatus.Chats), a message of the topic is
// known to reply to, and the todo is still bound. What is not sent is only logged. The private chat's reports and
// pushes are as they were: both go out.

const (
	// threadPostEvery is the least time between two posts of one todo.
	threadPostEvery = time.Minute
	// threadPostSettle is how long the first thing of a quiet todo waits, so what comes right behind it (the several
	// rows of one outcome, a decision and the todo's closing) is one line.
	threadPostSettle = 3 * time.Second
	// threadPostEvents bounds what waits for one line of one to-do: events past it are dropped (not counted), so a flood
	// is one line about the first threadPostEvents things.
	threadPostEvents = 50
	// threadPostShown is how many decisions a line quotes.
	threadPostShown = 3
)

type threadEventKind int

// The kinds are the outbound port's (larkhost.ThreadKind), which core code posts through.
const (
	threadDecision      = threadEventKind(larkhost.ThreadDecision)
	threadAgentFinished = threadEventKind(larkhost.ThreadAgentFinished)
	threadAgentNeeds    = threadEventKind(larkhost.ThreadAgentNeeds)
	threadTodoAccepted  = threadEventKind(larkhost.ThreadTodoAccepted)
	threadTodoDone      = threadEventKind(larkhost.ThreadTodoDone)
	threadTodoFailed    = threadEventKind(larkhost.ThreadTodoFailed)
)

// threadEvent is one thing to tell the topic of a todo; text is what the line quotes (a decision, the agent's name
// and what it asks, why the todo closed).
type threadEvent struct {
	kind  threadEventKind
	agent string
	text  string
	dm    bool // threadAgentNeeds: its card is in the owner's DM
}

// threadPoster holds what is waiting to be said, by todo. now and after are the clock and the timer; tests replace them.
type threadPoster struct {
	mu    sync.Mutex
	sent  uint64 // lines sent so far: part of each line's key
	todos map[string]*threadQueue
	now   func() time.Time
	after func(d time.Duration, f func())
	// asked, when set, is called when threadAgentAsks has decided (tests: the decision runs on its own goroutine).
	asked func()
}

type threadQueue struct {
	last   time.Time // the last time a line was due (sent or left out)
	events []threadEvent
	armed  bool // a flush is scheduled
}

// threadPost queues ev for todo id's topic: nothing for a todo bound to none. The line goes out when the minute since
// the last one is over, and no sooner than threadPostSettle from now.
func (s *Service) threadPost(id string, ev threadEvent) {
	if id == "" || s.threadOf(id) == nil {
		return
	}
	p := &s.tposts
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.todos == nil {
		p.todos = map[string]*threadQueue{}
	}
	now := p.clock()
	for k, q := range p.todos { // the quiet ones are forgotten
		if !q.armed && now.Sub(q.last) >= threadPostEvery {
			delete(p.todos, k)
		}
	}
	q := p.todos[id]
	if q == nil {
		q = &threadQueue{}
		p.todos[id] = q
	}
	if len(q.events) < threadPostEvents {
		q.events = append(q.events, ev)
	}
	if q.armed {
		return
	}
	q.armed = true
	wait := threadPostSettle
	if left := q.last.Add(threadPostEvery).Sub(now); left > wait {
		wait = left
	}
	p.later(wait, func() { s.threadFlush(id) })
}

// threadFlush says what has gathered for todo id in the topic.
func (s *Service) threadFlush(id string) {
	p := &s.tposts
	p.mu.Lock()
	q := p.todos[id]
	if q == nil {
		p.mu.Unlock()
		return
	}
	events := q.events
	q.events, q.armed, q.last = nil, false, p.clock()
	p.mu.Unlock()
	if len(events) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	th := s.threadOf(id)
	if th == nil || th.Channel != channel.Feishu {
		return // unbound since
	}
	st, err := s.feishuStatusKept(ctx)
	if err != nil || !st.Ready {
		s.logf("todo %s: not posting to its thread: the bot is not ready (%v)", id, err)
		return
	}
	if !slices.Contains(st.Chats, th.ChatID) {
		// What an agent bound (todo_bind_thread) is its own word: only the chats the owner's private chat and opt-in
		// name are ever written to.
		s.logf("todo %s: not posting to its thread: its chat is not one the bot may send into", id)
		return
	}
	anchor := s.topicMessage(ctx, th.ChatID, th.ThreadID)
	if anchor == "" {
		s.logf("todo %s: not posting to its thread: no message of the topic is known to reply to", id)
		return
	}
	text := threadPostText(s.botLang(ctx), events, todoLink(strings.TrimRight(st.PublicURL, "/"), id))
	if text == "" {
		return
	}
	to := feishuTarget{ChatID: th.ChatID, InThread: true, NoDetour: true}
	// Its own key for every line: the reply's usual key is made of the parent and the words, so two lines that read the
	// same (two finished turns) under one anchor would be one to Feishu, and the second silently dropped.
	err = s.appCall(ctx, AppFeishu, FeishuText, targetParams(to, map[string]any{"reply_to": anchor, "text": text, "uuid": s.threadPostKey(id)}), nil)
	switch {
	case err == nil:
	case botcard.MessageGone(err):
		// The anchor was withdrawn or deleted: the topic has no such message to reply under. This line is lost (a failed
		// post is not tried again); the next message the owner writes there is the new anchor.
		s.logf("todo %s: post to its thread: its anchor message is gone; forgetting it", id)
		s.forgetTopicMessage(ctx, th.ChatID, th.ThreadID, anchor)
	default:
		s.logf("todo %s: post to its thread: %v", id, err)
	}
}

// threadPostKey is the idempotency key of the next line for todo id: unique to the line, within Feishu's 50 characters.
func (s *Service) threadPostKey(id string) string {
	p := &s.tposts
	p.mu.Lock()
	p.sent++
	n := p.sent
	now := p.clock()
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%s", id, n, now.UnixNano(), randomHex(4))))
	return "tp:" + hex.EncodeToString(sum[:])[:32]
}

// threadPostText is the one line for events, with the link to the todo at the end when there is one.
func threadPostText(l i18n.Lang, events []threadEvent, link string) string {
	var decisions, finished, parts []string
	anonFinished := false
	var needs []string
	var closing *threadEvent
	accepted := false
	counted := 0
	for i := range events {
		e := events[i]
		switch e.kind {
		case threadDecision:
			counted++
			if len(decisions) < threadPostShown {
				decisions = append(decisions, e.text)
			}
		case threadAgentFinished:
			if e.agent == "" {
				anonFinished = true
			} else if !slices.Contains(finished, e.agent) {
				finished = append(finished, e.agent)
			}
		case threadAgentNeeds:
			who := i18n.T(l, "assistant-feishu.thread_post_who_anon")
			if e.agent != "" {
				who = i18n.T(l, "assistant-feishu.thread_post_who", i18n.Args{"name": e.agent})
			}
			key := "assistant-feishu.thread_post_needs"
			switch {
			case e.dm:
				key += "_dm" // the card is in the DM: the topic is only told that, not what it asks
			case e.text == "":
				key += "_bare"
			}
			if line := i18n.T(l, key, i18n.Args{"who": who, "ask": e.text}); !slices.Contains(needs, line) {
				needs = append(needs, line)
			}
		case threadTodoAccepted:
			accepted = true
		case threadTodoDone, threadTodoFailed:
			closing = &events[i]
		}
	}
	sep := i18n.T(l, "assistant-feishu.thread_post_sep")
	if counted > 0 {
		list := strings.Join(decisions, sep)
		if counted > len(decisions) {
			list += "…"
		}
		parts = append(parts, i18n.T(l, "assistant-feishu.thread_post_decisions", i18n.Args{"count": counted, "list": list}))
	}
	if len(finished) > 0 {
		parts = append(parts, i18n.T(l, "assistant-feishu.thread_post_finished", i18n.Args{"agents": strings.Join(finished, sep)}))
	} else if anonFinished {
		parts = append(parts, i18n.T(l, "assistant-feishu.thread_post_finished_anon"))
	}
	parts = append(parts, needs...)
	if accepted && closing == nil { // closing says it too
		parts = append(parts, i18n.T(l, "assistant-feishu.thread_post_accepted"))
	}
	if closing != nil {
		key := "assistant-feishu.thread_post_done"
		if closing.kind == threadTodoFailed {
			key = "assistant-feishu.thread_post_failed"
		}
		if closing.text != "" {
			key += "_reason"
		}
		parts = append(parts, i18n.T(l, key, i18n.Args{"reason": closing.text}))
	}
	if len(parts) == 0 {
		return ""
	}
	text := strings.Join(parts, sep)
	if link != "" {
		text += " " + link
	}
	return text
}
