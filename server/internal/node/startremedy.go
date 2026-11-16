package node

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/host"
)

// A start that ended with shell_waiting or agent_start_stalled keeps its tab (keepStartTab, #2570). Whoever remedies it
// (the assistant's main conversation, through the hub) may type into that tab while it is kept and no agent runs
// there (start.keys), and start again in it (session.start's in_pane) instead of opening another.

// startKeysText and startKeysCount bound what one start.keys types: a short answer, not a command line.
const (
	startKeysText  = 32 // runes of text
	startKeysCount = 8  // named keys
)

// keptStartTab is the kept tab of a failed start on pane while it is still kept and no agent runs there.
func (n *Node) keptStartTab(pane string) (keptStart, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.keptStartTabLocked(pane)
}

// keptStartTabLocked is keptStartTab with n.mu already held. Reusing a tab needs its expiry, binding and agent checks
// plus its deletion in one critical section, so two session.start requests cannot claim the same kept tab.
func (n *Node) keptStartTabLocked(pane string) (keptStart, bool) {
	k, ok := n.keptStarts[pane]
	if !ok || !time.Now().Before(k.until) {
		return keptStart{}, false
	}
	if _, bound := n.panes[pane]; bound {
		return keptStart{}, false
	}
	for _, a := range n.agents {
		if a.PaneID == pane {
			return keptStart{}, false
		}
	}
	return k, true
}

// errStartTabGone refuses typing into, or starting in, a pane that is not a failed start's kept tab (any more): it
// expired, an agent runs there, or it never was one.
var errStartTabGone = refusal("start_tab_gone", "that tab is not a failed start's kept tab any more (it expired, or an agent runs there)", nil)

// startKeys types text (as is, no enter) and then presses keys (Persona's key names: "enter", "esc", "ctrl+c", "y"…)
// into the kept tab of a failed start on pane. Only such a tab takes them, and only a short answer.
func (n *Node) startKeys(ctx context.Context, pane, text string, keys []string) error {
	if pane == "" || text == "" && len(keys) == 0 {
		return errors.New("start.keys needs a pane and text or keys")
	}
	if utf8.RuneCountInString(text) > startKeysText || len(keys) > startKeysCount {
		return refusal("too_large", "start.keys types at most 32 characters and 8 keys", nil)
	}
	// start.keys must not type into a pane that another request claims as a fresh start while it waits.
	unlock := n.typing.lock(workAgentPaneLockPrefix + pane)
	defer unlock()
	if _, ok := n.keptStartTab(pane); !ok {
		return errStartTabGone
	}
	h, ref, err := n.paneHost(pane)
	if err != nil {
		return err
	}
	if text != "" {
		if err := h.SendText(ctx, ref, text); err != nil {
			return err
		}
	}
	if len(keys) > 0 {
		return h.SendKeys(ctx, ref, keys...)
	}
	return nil
}

// reusedTabKey carries the kept tab a start runs in instead of a new one (withKeptTab), for newTab.
type reusedTabKey struct{}

// replacedTabKey carries the pane of a kept Orca tab the start cannot run in, for newTab to close before it opens a
// new one (replaceKeptTab).
type replacedTabKey struct{}

// reusingKeptTab is ctx for a start in pane, the kept tab of an earlier failed start, when it still is one: the start
// takes it over (it is no longer kept; this start keeps or closes it as its own, startTabFailed). Else ctx as it is,
// and the start opens a new tab. An Orca terminal takes one launch (orcaHost.Run): a kept tab whose start already typed
// its line there (agent_start_stalled) is replaced instead, once the start is checked and about to open its new tab
// (replaceKeptTab, #9534); one where nothing was typed (shell_waiting, answered with start.keys) is still started in.
func (n *Node) reusingKeptTab(ctx context.Context, pane string) context.Context {
	if pane == "" {
		return ctx
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	k, ok := n.keptStartTabLocked(pane)
	if !ok {
		return ctx
	}
	if o, isOrca := k.launch.(*orcaHost); isOrca && !o.launchable(k.created.Pane) {
		return context.WithValue(ctx, replacedTabKey{}, pane)
	}
	delete(n.keptStarts, pane)
	return context.WithValue(ctx, reusedTabKey{}, k.created)
}

// replaceKeptTab closes the kept tab on pane before a start opens the new tab that replaces it. newTab calls it after
// the start's checks, so a refused start leaves the tab kept. A tab that will not close fails the start and stays kept
// (its expiry still closes it): its stalled agent may still run, and a second one beside it would not be noticed.
func (n *Node) replaceKeptTab(ctx context.Context, pane string) error {
	n.mu.Lock()
	k, ok := n.keptStartTabLocked(pane)
	if ok {
		delete(n.keptStarts, pane)
	}
	n.mu.Unlock()
	org, closes := k.launch.(host.Organizer)
	if !ok || !closes { // expired or taken meanwhile: a new tab, as for a pane not kept
		return nil
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := org.Close(cctx, k.created.Pane, cmp.Or(k.ws, k.created.SpaceID), k.created.SlotID); err != nil {
		n.mu.Lock()
		n.keptStarts[pane] = k
		n.mu.Unlock()
		// Its own timer may have fired while it was taken; expireKeptStart acts once for this until.
		time.AfterFunc(time.Until(k.until), func() { n.expireKeptStart(pane, k.until) })
		return fmt.Errorf("the stalled start's tab did not close, so no new one was opened: %w", err)
	}
	return nil
}
