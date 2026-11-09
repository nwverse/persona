package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/rpc"
)

// editQueue takes the queued input back out of claude (the TUI's "press up to edit queued messages" pulls every
// queued message into the input box), clears the input, and re-queues the ones listed in keep, in order.
// It returns the messages that were queued so the phone can put the edited one in its composer.
func (n *Node) editQueue(ctx context.Context, id string, keep []int) ([]string, error) {
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return nil, err
	}
	p := n.transcriptPath(id)
	if p == "" {
		return nil, fmt.Errorf("transcript for session %s not found", id)
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	queue := n.meta(p, st).queue
	if len(queue) == 0 {
		return nil, fmt.Errorf("nothing is queued")
	}
	if err := n.sendKeys(ctx, pane, "up"); err != nil {
		return nil, err
	}
	time.Sleep(300 * time.Millisecond)
	// ctrl+u only clears the current line: walk back through every line of the pulled-back text.
	lines := 0
	for _, q := range queue {
		lines += strings.Count(q, "\n") + 1
	}
	for i := 0; i < lines; i++ {
		_ = n.sendKeys(ctx, pane, "ctrl+u", "backspace")
	}
	_ = n.sendKeys(ctx, pane, "ctrl+u")
	for _, k := range keep {
		if k >= 0 && k < len(queue) {
			if err := n.prompt(ctx, pane, queue[k]); err != nil {
				return queue, fmt.Errorf("re-queue: %w", err)
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	return queue, nil
}

// sendNowHints are Claude Code's hints beside a queued message for its own "send now" (#6088), and the keys each one
// names. Claude Code 2.1.311 binds both to send-now and names ctrl+enter where the terminal reports extended keys (a
// herdr pane), ctrl+x ctrl+s inside tmux or screen or a terminal without them (#2467); 2.1.319 was seen with ctrl+x
// ctrl+s.
var sendNowHints = []struct {
	hint string
	keys []string
}{
	{"ctrl+enter to send now", []string{"ctrl+enter"}},
	{"ctrl+x ctrl+s to send now", []string{"ctrl+x", "ctrl+s"}},
}

// otherSendNowRE is a send-now hint naming keys that are not in sendNowHints, for the refusal to quote.
var otherSendNowRE = lazyre.New(`(?:\S+\+\S+ )+to send now`)

// sendNowOffer is the send-now hint the screen shows beside a queued message and the keys it names: no keys for a hint
// Persona does not know, "" for none.
func sendNowOffer(screen string) (hint string, keys []string) {
	s := strings.Join(strings.Fields(screen), " ")
	for _, h := range sendNowHints {
		if strings.Contains(s, h.hint) {
			return h.hint, h.keys
		}
	}
	return otherSendNowRE.FindString(s), nil
}

// offersSendNow: the screen shows a queued message with a send-now Persona knows how to press.
func offersSendNow(screen string) bool {
	_, keys := sendNowOffer(screen)
	return keys != nil
}

// sendNowWait is how long the node looks for the send-now hint to go once the key is pressed.
var sendNowWait = 3 * time.Second

// sendQueuedNow gives the agent what waits in Claude Code's queue now (#6088), with Claude Code's own send-now, the keys
// its hint names: a command the agent runs moves to the background and goes on, a reply it is writing is asked again
// with the message (measured on 2.1.319 and 2.1.311; Esc would stop the command). Pressed only while the screen offers
// it: that is the queue right now (the transcript says a message was taken about a second late, and a key pressed then
// would hit a turn that already has it), and an older Claude without it offers nothing.
func (n *Node) sendQueuedNow(ctx context.Context, id string) error {
	if a, ok := n.agent(id); ok && a.Agent != "claude" {
		return refusal("send_now_unsupported", "only Claude Code can send queued messages now", nil)
	}
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return err
	}
	screen, err := n.readPane(ctx, pane, "visible", 80)
	if err != nil {
		return err
	}
	hint, keys := sendNowOffer(screen)
	if keys == nil {
		// Something is queued but its hint is not one Persona knows: nothing pressed, and no guess at the keys.
		if hint != "" {
			return refusal("send_now_unrecognized", fmt.Sprintf("the screen offers send-now as %q, keys Persona does not know", hint), nil)
		}
		if p := n.transcriptPath(id); p != "" {
			if st, err := os.Stat(p); err == nil && len(n.meta(p, st).queue) > 0 {
				return refusal("send_now_unrecognized", "messages are queued but the screen shows no send-now hint Persona knows", nil)
			}
		}
		return refusal("nothing_queued", "nothing is queued for the agent any more", nil)
	}
	if err := n.sendKeys(ctx, pane, keys...); err != nil {
		return err
	}
	for deadline := time.Now().Add(sendNowWait); time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		if s, err := n.readPane(ctx, pane, "visible", 80); err == nil && !offersSendNow(s) {
			return nil
		}
	}
	// the key went in and the queue is still shown: perhaps it was taken, perhaps not (not "none", as a refusal says)
	return &rpc.Error{Code: "send_now_in_doubt", Message: "the queued messages are still shown as queued", Data: json.RawMessage(`{"delivered":"unknown"}`)}
}
