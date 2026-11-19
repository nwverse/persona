package node

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/screen"
)

// agy's folder-trust dialog (#11424). In a folder it has not trusted yet (a new worktree, which task_plan makes by
// default), the Antigravity CLI first asks
//
//	Do you trust the contents of this project?
//	> Yes, I trust this folder
//	  No, exit
//
// and nothing reaches its input box until someone answers. Persona answers it for the folder the start chose, as
// Claude's start (claudeready.go trustKeys) and Codex's (codex_start.go codexTrustKeys: "the folder is trusted, they
// chose it") do. screen.ParseDialog reads the dialog (#10778) and codexTrustKeys picks the option that trusts it.

// agyTrustWait bounds how long a start watches agy's pane for the dialog before the first message goes in; agyTrustPoll
// is how often it looks (variables for tests). Past the bound the start goes on: the first-message check
// (workagent_first.go) flags a message it cannot confirm.
var (
	agyTrustWait = 15 * time.Second
	agyTrustPoll = 300 * time.Millisecond
)

// agyReadyHint is the hint under agy's input box: the box is up, so no trust dialog is coming.
const agyReadyHint = "? for shortcuts"

// agyTrustFolder answers agy's trust dialog in pane, if one shows, before the start types its first message. It returns
// once agy's input box shows with no dialog, or after agyTrustWait; errors only when ctx ends or keys cannot be sent.
func (n *Node) agyTrustFolder(ctx context.Context, pane string) error {
	deadline := time.Now().Add(agyTrustWait)
	answered := "" // the dialog screen last answered: the same screen is not answered twice
	for {
		text, err := n.readPane(ctx, pane, "visible", 40)
		if err == nil {
			if keys := codexTrustKeys(text); keys != nil {
				if text != answered {
					if err := n.sendKeys(ctx, pane, keys...); err != nil {
						return err
					}
					log.Printf("agy start in pane %s: trusted the folder the start chose (#11424)", pane)
					answered = text
				}
			} else if strings.Contains(text, agyReadyHint) && !screen.Choosing(text) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(agyTrustPoll):
		}
	}
}
