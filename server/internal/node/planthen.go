package node

import (
	"context"
	"log"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A session started in plan mode goes on in one mode once its plan is approved, whichever surface approves it
// (#3784): the mode its start named (session.start's plan_then, sessionState.PlanThen). Approved on Persona's card,
// the hook's answer sets it (hooks.go). Approved in the terminal's own dialog, Claude Code applies the option picked
// there; the node then looks at the footer and moves the session to the recorded mode when it differs.

// planThenOf is the mode to record for a start in mode with the asked-for plan_then: only for a plan-mode start, a
// mode Persona knows, and not plan itself.
func planThenOf(mode, then string) string {
	if mode != "plan" || then == "" || then == "plan" || !slices.Contains(permissionModes, then) {
		return ""
	}
	return then
}

// planThenFlags are the extra claude flags of a plan-mode start that goes on in bypassPermissions: without one of them
// that mode is not in the running session's shift+tab cycle, and neither the hook's setMode nor a later change can
// reach it (argsCanBypass).
func planThenFlags(mode, then string) string {
	if planThenOf(mode, then) == proto.ModeBypass {
		return " --allow-dangerously-skip-permissions"
	}
	return ""
}

// planThen is the mode session id goes on in after its plan, "" when its start named none.
func (n *Node) planThen(id string) string {
	if st, ok := n.state.get(id); ok {
		return st.PlanThen
	}
	return ""
}

// How long, and how often, the node looks at the terminal after a plan card ended without an answer.
var (
	planThenWatch = 90 * time.Second
	planThenEvery = 2 * time.Second
)

// planThenSet bounds pressing the session over to its mode (tuiSetMode: a read and a key per step of the cycle).
const planThenSet = 20 * time.Second

// planThenAfterTerminal follows a plan the terminal is deciding: once the footer shows a mode other than plan, the
// plan was approved there, and the session is moved to its recorded mode when the dialog picked another. A plan sent
// back (the footer stays on plan), a dialog still open (no footer) and a session with no recorded mode are left alone.
func (n *Node) planThenAfterTerminal(id string) {
	want := n.planThen(id)
	if want == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), planThenWatch)
		defer cancel()
		t := time.NewTicker(planThenEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			pane, err := n.paneFor(ctx, id)
			if err != nil {
				return // its tab is gone
			}
			screen, err := n.readPane(ctx, pane, "visible", 60)
			if err != nil {
				return
			}
			switch cur := tuiMode(screen); cur {
			case "", "plan":
				continue // the dialog is still up, or the plan was sent back
			case want:
				return
			default:
				// Its own time: the watch may be nearly over when the approval is seen.
				sctx, done := context.WithTimeout(context.Background(), planThenSet)
				err := n.tuiSetMode(sctx, pane, want)
				done()
				if err != nil {
					log.Printf("plan %s: approved in the terminal in %s, could not move to %s: %v", short(id), cur, want, err)
					return
				}
				n.noteModes(id, want, "")
				n.notifySessions()
				log.Printf("plan %s: approved in the terminal in %s, moved to %s as its start said", short(id), cur, want)
				return
			}
		}
	}()
}
