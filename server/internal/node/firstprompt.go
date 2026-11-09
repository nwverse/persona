package node

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
)

// A new Claude's first message (start in control.go) is typed into its input box once the box shows. Claude Code is
// not always done drawing then: one that updated itself on start ("Updated to latest…") and showed the auto mode
// notice took the typed message and dropped it, and the agent sat idle with an empty box for an hour and a half
// (#2423). So the first message counts as delivered only once the transcript has it. Until then it is entered (still
// in the box) or typed again (gone) once the box is ready again, a few times; when it still does not go in, or the
// screen is not one to type into, the session is listed as needing the user, with why, instead of idle.

// firstPromptWait is how long the transcript has to show the first message after it was typed; firstPromptRecover how
// long Claude then has to show its input box again before the message is typed once more. Tests shorten both.
var firstPromptWait, firstPromptRecover = 15 * time.Second, 30 * time.Second

// firstPromptTries is how many times the first message is typed at most.
const firstPromptTries = 3

// firstPrompt types the first message into the new Claude in pane (a is how waitReady left it) and, in the
// background, makes sure the session's transcript gets it: the start answers as it did (a sync start's caller has
// a budget of its own), and messages sent to the session meanwhile wait for the check (the typing lock), so a
// message typed again cannot mix with them. An error: the host refused the first message (nothing went in).
func (n *Node) firstPrompt(ctx context.Context, pane, id, prompt string, a *host.Agent) error {
	since := time.Now()
	if a != nil && a.AgentStatus == proto.StatusBlocked {
		n.lostFirst.set(id, lostDialog, since) // a dialog of Claude's own has the keyboard: the message would answer it
		return nil
	}
	err := n.hostPrompt(ctx, pane, prompt)
	if errors.Is(err, host.ErrAgentBlocked) {
		n.lostFirst.set(id, lostDialog, since)
		return nil
	}
	if err != nil {
		return fmt.Errorf("send first message: %w", err)
	}
	n.typing.typed(pane, prompt)
	unlock := n.typing.lock(id)
	n.firstWait.Add(1)
	go func() {
		defer n.firstWait.Done()
		defer unlock()
		bctx, cancel := context.WithTimeout(context.Background(),
			firstPromptTries*firstPromptWait+(firstPromptTries-1)*firstPromptRecover+15*time.Second)
		defer cancel()
		if why := n.confirmFirst(bctx, pane, id, prompt, since); why != "" {
			log.Printf("start %s: %s", id, why)
			n.lostFirst.set(id, why, since)
			n.notifySessions()
		}
	}()
	return nil
}

// confirmFirst waits for the transcript to get the first message typed at since, typing it again when it was
// dropped. It returns "" once the transcript has it, else why it did not go in, for the user.
func (n *Node) confirmFirst(ctx context.Context, pane, id, prompt string, since time.Time) string {
	typedAt := since
	for try := 1; ; try++ {
		if n.firstPromptTaken(ctx, pane, id, prompt, since, typedAt) {
			return ""
		}
		if ctx.Err() != nil {
			return lostTimeout
		}
		if try == firstPromptTries {
			return fmt.Sprintf("首条消息送了 %d 次都没进去，Agent 还没开始：看看电脑上的终端，或者在这里把任务重发一遍。", firstPromptTries)
		}
		log.Printf("start %s: the first message did not reach the transcript (try %d), typing it again", id, try)
		if why := n.readyForFirstPrompt(ctx, pane, prompt); why != "" {
			return why
		}
		typedAt = time.Now()
		err := n.hostPrompt(ctx, pane, prompt)
		if errors.Is(err, host.ErrAgentBlocked) {
			return lostDialog
		}
		if err != nil {
			return fmt.Sprintf("首条消息没有送进去（%v），Agent 还没开始：在这里把任务重发一遍。", err)
		}
		n.typing.typed(pane, prompt)
	}
}

// User-facing reasons a first message did not go in.
const (
	lostDialog  = "终端上开着一个对话框，首条消息没有送进去，Agent 还没开始：先在终端里处理它，再在这里把任务重发一遍。"
	lostDraft   = "首条消息停在输入框里没有发出去，Agent 还没开始：在电脑上按回车，或者清空输入框后在这里重发。"
	lostOther   = "输入框里有别的内容，首条消息没有再送，Agent 还没开始：看看电脑上的终端，再在这里把任务重发一遍。"
	lostNoBox   = "Claude 没有回到可以输入的状态，首条消息没有送进去，Agent 还没开始：看看电脑上的终端，再在这里把任务重发一遍。"
	lostTimeout = "等首条消息送进去超时了，Agent 还没开始：看看电脑上的终端，或者在这里把任务重发一遍。"
	// lostUltracode: the start's ultracode command may still sit in the input box (ultracode.go), or the box could
	// not be read after it.
	lostUltracode = "ultracode 的开关命令可能还停在电脑上的输入框里，首条消息没有送进去，Agent 还没开始：看看电脑上的终端，再在这里把任务重发一遍。"
)

// firstPromptTaken waits for the transcript to show a message from since on, for firstPromptWait after it was typed
// (typedAt). A message still in the box after submitGrace gets one Enter (as ensureSubmitted does for other sends).
// Not in the transcript by then: taken only if Claude works on something now (typing it again would run it twice).
func (n *Node) firstPromptTaken(ctx context.Context, pane, id, prompt string, since, typedAt time.Time) bool {
	deadline := typedAt.Add(firstPromptWait)
	entered := false
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(firstPromptPoll):
		}
		if n.promptedSince(id, since) {
			return true
		}
		if time.Now().After(deadline) {
			break
		}
		if !entered && time.Since(typedAt) >= submitGrace {
			if s, err := n.readPaneANSI(ctx, pane, "visible", 60); err == nil {
				if in := screen.ClaudeInput(s); in.Box && in.Draft && stillPrompted(in.Text, prompt) {
					entered = true
					_ = n.sendKeys(ctx, pane, "enter")
				}
			}
		}
	}
	a, err := n.paneAgent(ctx, pane)
	return err == nil && a.AgentStatus == proto.StatusWorking
}

// firstPromptPoll is how often firstPromptTaken looks at the transcript.
var firstPromptPoll = 500 * time.Millisecond

// readyForFirstPrompt waits for Claude's input box to show again (accepting a trust dialog on the way) and checks the
// box is empty: "" when the message can be typed again, else why not.
func (n *Node) readyForFirstPrompt(ctx context.Context, pane, prompt string) string {
	rctx, cancel := context.WithTimeout(ctx, firstPromptRecover)
	defer cancel()
	a, err := n.waitReady(rctx, pane, nil)
	switch {
	case ctx.Err() != nil:
		return lostTimeout
	case err != nil:
		return lostNoBox
	case a.AgentStatus == proto.StatusBlocked:
		return lostDialog
	}
	s, err := n.readPaneANSI(ctx, pane, "visible", 60)
	if err != nil {
		return lostNoBox
	}
	switch code, in := screen.CheckClaude(s); {
	case code == screen.DialogOpen:
		return lostDialog
	case code == screen.InputDraft && stillPrompted(in.Text, prompt):
		return lostDraft
	case code == screen.InputDraft:
		return lostOther
	}
	return ""
}

// promptedSince: the session's transcript has a message to the agent from since on (a second earlier, for the
// transcript's own clock rounding).
func (n *Node) promptedSince(id string, since time.Time) bool {
	p := n.transcriptPath(id)
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	at := n.meta(p, st).lastPromptAt
	return !at.IsZero() && !at.Before(since.Add(-time.Second))
}

// lostPrompts are the sessions whose first message did not go in, with why: listed as needing the user until a
// message reaches the agent (sent from the phone, or typed at the computer). Kept in memory: a restarted node lists
// them as what herdr says (idle).
type lostPrompts struct {
	mu sync.Mutex
	m  map[string]lostPrompt
}

type lostPrompt struct {
	why string
	at  time.Time // when the start typed it
}

func (l *lostPrompts) set(id, why string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]lostPrompt{}
	}
	l.m[id] = lostPrompt{why, at}
}

func (l *lostPrompts) clear(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, id)
}

func (l *lostPrompts) get(id string) (lostPrompt, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.m[id]
	return p, ok
}

// lostFirstPrompt is why the session waits for the user because its first message did not go in, "" when it does not
// (any more: it works, or its transcript got a message since).
func (n *Node) lostFirstPrompt(id, status string) string {
	p, ok := n.lostFirst.get(id)
	if !ok {
		return ""
	}
	if status == proto.StatusWorking || n.promptedSince(id, p.at) {
		n.lostFirst.clear(id)
		return ""
	}
	return p.why
}
