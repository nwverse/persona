package node

import (
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Answering a start's startup question from the app (#10365). An async start whose new tab stopped on a question
// (shell_waiting) keeps its tab and lists the question with the answers it may be given (questionChoices). The owner
// picks one on a full device; start.continue types it into that tab, only while the same question is still on its
// last line, and starts the session again in the same tab under the same id: the record goes back to starting
// (startLog.restart), so a message sent meanwhile, or sent again, waits for this start instead of getting the old
// error. A question still there then (or a new one) ends that start the same way, with its answers, and the tab is
// kept again.

// The refusals of start.continue; each typed nothing.
var (
	// errStartNotWaiting: the session has no failed start waiting on a startup question (it started, expired, or
	// another answer already started it again).
	errStartNotWaiting = refusal("start_not_waiting", "this session's start is not waiting on a question any more", nil)
	// errChoiceNotOffered: the answer is not one the question was offered (questionChoices).
	errChoiceNotOffered = refusal("choice_not_offered", "that answer is not offered for this question", nil)
	// errPromptChanged: the tab's last line is not the question any more: someone answered it at the computer, or it
	// went by itself. An answer typed now could run at a prompt.
	errPromptChanged = refusal("prompt_changed", "the tab's question is not on its screen any more; nothing was typed", nil)
)

// echoWait is how long start.continue watches the question's line after typing an answer: a question that reads a
// whole line shows the answer after it and waits for Enter.
const echoWait = 600 * time.Millisecond

// startContinue answers id's startup question with choice (an id questionChoices offered) and starts id again in its
// kept tab. It returns the session as it starts.
func (n *Node) startContinue(ctx context.Context, id, choice string) (*proto.Session, error) {
	failed, listed := n.starts.failed(id)
	if failed == nil || failed.spec == nil || listed.StartErrorCode != proto.CodeShellWaiting {
		return nil, errStartNotWaiting
	}
	if failed.spec.execution.SecretTicket != "" { // its values were for one start, and that one took them (secretenv.go)
		return nil, refusal(proto.CodeSecretEnvGone, "this start's secret values were used up; start it again", nil)
	}
	pane, line := listed.StartErrorArgs["pane"], listed.StartErrorArgs["line"]
	ask := questionChoices(line)
	c, ok := ask.choice(choice)
	if !ok || ask.Secret && c.ID != choiceCancel.ID { // a password prompt never gets anything typed from the app
		return nil, errChoiceNotOffered
	}
	// One answer at a time per tab, and none while another request takes the tab for a start of its own (start.keys).
	unlock := n.typing.lock(workAgentPaneLockPrefix + pane)
	defer unlock()
	if again, _ := n.starts.failed(id); again != failed { // an answer before this one started it again
		return nil, errStartNotWaiting
	}
	if pane == "" {
		return nil, errStartTabGone
	}
	if _, ok := n.keptStartTab(pane); !ok {
		return nil, errStartTabGone
	}
	shown, err := n.readPane(ctx, pane, "visible", 60)
	if err != nil {
		return nil, err
	}
	if shellQuestion(shown) != line {
		return nil, errPromptChanged
	}
	if err := n.sendKeys(ctx, pane, c.Keys...); err != nil {
		return nil, err
	}
	n.enterAfterEcho(ctx, pane, line, c, ask.LineMode)
	n.logf("start %s: answered its tab's question %q with %s", id, line, c.Label)
	spec := *failed.spec
	spec.execution.InPane = pane // the kept tab, not a new one (claimKeptTab)
	r, sctx := n.newStartRec(id, &spec, time.Now().UTC())
	if err := n.starts.restart(id, failed, r); err != nil {
		r.cancel()
		return nil, err
	}
	return n.runAsyncStart(context.WithValue(sctx, answeredKey{}, true), id, r), nil
}

// enterAfterEcho presses Enter after c, a one-key answer, when the question reads a whole line: its line then shows
// the key after the question and waits. One that read the key alone has moved on (oh-my-zsh's read -k), and gets
// nothing more. A line-reading question (lineMode) whose screen did not change within echoWait gets Enter too.
func (n *Node) enterAfterEcho(ctx context.Context, pane, line string, c shellChoice, lineMode bool) {
	if len(c.Keys) != 1 || len([]rune(c.Keys[0])) != 1 {
		return // a named key, or an answer that ends with its own Enter
	}
	key := c.Keys[0]
	deadline := time.Now().Add(echoWait)
	for {
		shown, err := n.readPane(ctx, pane, "visible", 60)
		if err != nil {
			return
		}
		last := lastLine(shown)
		if last != line {
			if rest, ok := strings.CutPrefix(last, line); ok && strings.EqualFold(strings.TrimSpace(rest), key) {
				_ = n.sendKeys(ctx, pane, "enter")
			}
			return
		}
		if !time.Now().Before(deadline) {
			if lineMode {
				_ = n.sendKeys(ctx, pane, "enter")
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(shellSettlePoll / 2):
		}
	}
}

// answeredKey marks the start startContinue runs after typing an answer (answeredOf).
type answeredKey struct{}

// answeredOf: ctx is the start startContinue runs right after answering its tab's question. Its question stays the
// last line written until the shell prints something more (oh-my-zsh only moves the cursor down after the key), however
// long the rest of the startup files take, so it is waited out as in a new tab, not refused at once (keptQuestionWait).
func answeredOf(ctx context.Context) bool {
	answered, _ := ctx.Value(answeredKey{}).(bool)
	return answered
}
