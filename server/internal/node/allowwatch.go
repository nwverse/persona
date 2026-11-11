package node

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/screen"
)

// An allow that Claude Code does not take (#3752). Claude Code 2.1.322 runs the PermissionRequest hooks for its own
// safety checks ("Dangerous rm operation …", asked even in bypass mode, with "Claude Code will automatically block this
// request in 1:59"), so Persona's hook makes a permission card, but it ignores an allow for them; a deny it takes. The
// card was over, herdr still called the agent working, and the dialog held the terminal with nothing on the phone to
// answer it: every message to the agent was refused as dialog_open. So once the node hands a hook an allow, it reads
// the pane a moment later. A dialog still there lists the session as blocked, the dialog's first line its reason,
// whatever herdr says, and the manager's screen card (Claude's own dialog, its warning and countdown, answered with
// keys) takes it from there. The pane is read again while the dialog stays, until it is gone or allowWatchFor ends.

// allowSettle is how long Claude has to act on an allow before its pane is read; allowFollowEvery how often the pane
// is read again while a dialog stays; allowWatchFor how long at most (Claude blocks the request itself after 2 min).
var (
	allowSettle      = 1500 * time.Millisecond
	allowFollowEvery = 3 * time.Second
	allowWatchFor    = 3 * time.Minute
)

type allowWatch struct {
	pane string
	gen  int64
	head string // the first line of the dialog that holds the terminal, "" while none is known
}

// allowWatches are the sessions whose panes are followed after an allow, by session id.
type allowWatches struct {
	mu  sync.Mutex
	gen int64
	m   map[string]*allowWatch
}

// watchAllow follows the pane of a session whose hook was just handed an allow. A newer allow for the session takes
// over from the one before.
func (n *Node) watchAllow(session, pane string) {
	if session == "" || pane == "" {
		return
	}
	w := &n.allowed
	w.mu.Lock()
	if w.m == nil {
		w.m = map[string]*allowWatch{}
	}
	w.gen++
	gen := w.gen
	w.m[session] = &allowWatch{pane: pane, gen: gen}
	w.mu.Unlock()
	go n.followAllow(session, pane, gen)
}

func (n *Node) followAllow(session, pane string, gen int64) {
	end := time.Now().Add(allowWatchFor)
	wait := allowSettle
	for {
		time.Sleep(wait)
		wait = allowFollowEvery
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		text, err := n.dialogScreen(ctx, pane)
		cancel()
		head := ""
		if err == nil {
			head = screen.DialogHead(text)
		}
		over := err != nil || head == "" || time.Now().After(end)
		w := &n.allowed
		w.mu.Lock()
		cur := w.m[session]
		if cur == nil || cur.gen != gen { // a newer allow follows the pane now
			w.mu.Unlock()
			return
		}
		was := cur.head
		if over {
			delete(w.m, session)
		} else {
			cur.head = head
		}
		w.mu.Unlock()
		if was != head || over && was != "" {
			if over {
				n.logf("allow on %s: the dialog is gone", pane)
			} else if was == "" {
				n.logf("allow on %s not taken: %q still holds the terminal", pane, clipDialog(head, 80))
			}
			if n.events != nil {
				n.events.poke(true) // a list rebuild carries it to the manager, which makes the screen card
			}
		}
		if over {
			return
		}
	}
}

// allowDialog is the first line of the dialog that holds a session's terminal after an allow its Claude did not take,
// "" when there is none.
func (n *Node) allowDialog(session string) string {
	w := &n.allowed
	w.mu.Lock()
	defer w.mu.Unlock()
	if a := w.m[session]; a != nil {
		return a.head
	}
	return ""
}

// forgetAllowDialog drops what the reads after an allow found on pane: keys went into it (the dialog may be gone).
// The pane is still followed: the next read says whether the dialog went.
func (n *Node) forgetAllowDialog(pane string) {
	w := &n.allowed
	w.mu.Lock()
	for _, a := range w.m {
		if a.pane == pane {
			a.head = ""
		}
	}
	w.mu.Unlock()
}

// allowPane is the pane a hook's session runs in: the hook's own when this machine's hosts can drive it, else the
// agent's.
func (n *Node) allowPane(req sockRequest, session string) string {
	if req.Pane != "" && paneKindOK(n, req.Pane) {
		return req.Pane
	}
	if a, ok := n.agent(session); ok {
		return a.PaneID
	}
	return ""
}
