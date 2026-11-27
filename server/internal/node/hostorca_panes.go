package node

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/orcart"
)

// Splitting a pane, closing a whole tab and renaming a tab only while it keeps a name (#17043): Orca's terminal.split,
// terminal.closeTab and terminal.rename, all on its local socket (checked live on an isolated Orca 1.4.277). An Orca
// without one of the first two answers method_not_found: the host refuses with nothing done, and stops offering it
// (orcaHost.offers) until another runtime answers.

var (
	_ host.Splitter   = (*orcaHost)(nil)
	_ host.SlotCloser = (*orcaHost)(nil)
)

// The Orca methods behind host abilities an older runtime may lack.
const (
	orcaSplitMethod    = "terminal.split"
	orcaCloseTabMethod = "terminal.closeTab"
)

// orcaSplitDirections are host.Splitter's directions in Orca's words.
var orcaSplitDirections = map[string]string{"right": orcart.SplitRight, "down": orcart.SplitDown}

// offers: as far as the host knows without asking, the running Orca has method (the runtime answering now has not
// answered method_not_found to it).
func (h *orcaHost) offers(method string) bool {
	rt := h.c.RuntimeID()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.missingIn != rt || !h.missing[method]
}

// CanSplit and CanCloseSlot are what the terminal browser's listing offers for Orca (panes.go).
func (h *orcaHost) CanSplit() bool     { return h.offers(orcaSplitMethod) }
func (h *orcaHost) CanCloseSlot() bool { return h.offers(orcaCloseTabMethod) }

// orcaLacks is the refusal of a method the running Orca does not have, nothing done.
func orcaLacks(method string) error {
	return host.Mark(fmt.Errorf("%w: this Orca has no %s; update Orca to use it", host.ErrUnsupported, method), host.ErrNotDelivered)
}

// missingErr turns Orca's method_not_found into orcaLacks, remembering it for the runtime that said it.
func (h *orcaHost) missingErr(method string, err error) error {
	if !orcart.IsCode(err, "method_not_found") {
		return orcaErr(err)
	}
	rt := h.c.RuntimeID()
	h.mu.Lock()
	if h.missing == nil || h.missingIn != rt {
		h.missing, h.missingIn = map[string]bool{}, rt
	}
	h.missing[method] = true
	h.mu.Unlock()
	return orcaLacks(method)
}

// Split splits the pane's terminal: a new shell beside it ("right") or below it ("down"), in the pane's tab. Orca
// starts it in the worktree; a pane working in another folder gets `cd <folder>` as the new shell's first command
// (orcaCdLine: Orca takes no cwd on this call), not a folder whose name a shell's line editor would act on. The terminal
// is looked up afresh first: Orca does not hold the incarnation fence on this call.
func (h *orcaHost) Split(ctx context.Context, pane host.PaneRef, direction, cwd string) (host.PaneRef, error) {
	dir := orcaSplitDirections[direction]
	if dir == "" {
		return host.PaneRef{}, fmt.Errorf("orca: no split direction %q", direction)
	}
	if !h.offers(orcaSplitMethod) {
		return host.PaneRef{}, orcaLacks(orcaSplitMethod)
	}
	t, err := h.terminal(ctx, pane.ID, true)
	if err != nil {
		return host.PaneRef{}, orcaErr(err)
	}
	if !t.IsLocal() || !t.Writable {
		return host.PaneRef{}, host.Mark(fmt.Errorf("orca: the terminal in %s cannot be split now", pane.ID), host.ErrNotDelivered)
	}
	command := ""
	if !strings.ContainsFunc(cwd, unicode.IsControl) {
		command = orcaCdLine(cwd, t.WorktreePath)
	}
	created, err := h.c.Split(ctx, t.Target(), dir, command)
	if err != nil {
		return host.PaneRef{}, h.missingErr(orcaSplitMethod, err)
	}
	return host.PaneRef{Kind: host.Orca, ID: created.PaneKey()}, nil
}

// CloseSlot closes the tab with every pane in it, whatever runs there (terminal.closeTab on one of its terminals). A
// terminal Orca no longer knows by the time of the call counts as closed when the tab is gone after it.
func (h *orcaHost) CloseSlot(ctx context.Context, slot string) error {
	if !h.offers(orcaCloseTabMethod) {
		return orcaLacks(orcaCloseTabMethod)
	}
	t, ok, err := h.slotTerminal(ctx, slot)
	if err != nil {
		return orcaErr(err)
	}
	if !ok {
		return fmt.Errorf("orca: the selected tab is no longer available")
	}
	err = h.c.CloseTab(ctx, t.Target())
	if orcart.IsCode(err, "terminal_handle_stale") || orcart.IsCode(err, "terminal_gone") || orcart.IsCode(err, "terminal_exited") {
		if _, still, lerr := h.slotTerminal(ctx, slot); lerr == nil && !still {
			err = nil
		}
	}
	if err != nil {
		return h.missingErr(orcaCloseTabMethod, err)
	}
	h.mu.Lock()
	for key, m := range h.made {
		if m.TabID == slot {
			delete(h.made, key)
			delete(h.launches, key)
		}
	}
	h.mu.Unlock()
	return nil
}

// slotTerminal is a terminal of the tab as Orca lists it now: one of this machine's, not an orphan other than one this
// host made and Orca re-keyed (as RenameSlot takes them).
func (h *orcaHost) slotTerminal(ctx context.Context, slot string) (orcart.Terminal, bool, error) {
	terms, _, err := h.terminals(ctx)
	if err != nil {
		return orcart.Terminal{}, false, err
	}
	for _, t := range terms {
		if t.TabID == slot && t.IsLocal() && (!t.Orphaned || h.rekeyedKey(t.PaneKey())) {
			return t, true, nil
		}
	}
	return orcart.Terminal{}, false, nil
}

// errTabRenamed: the tab no longer shows the name a guarded rename expected (someone renamed it meanwhile).
var errTabRenamed = errors.New("the tab name changed")

// RenameSlotIf renames the tab only while Orca's layout, read now (the cached one may be orcaLayoutMaxAge old), shows
// it under the expected name as Snapshot labels it: a rename the owner made meanwhile wins (#15674).
func (h *orcaHost) RenameSlotIf(ctx context.Context, slot, expected, name string) error {
	l, err := h.c.Terminals(ctx, true)
	if err != nil {
		return orcaErr(err)
	}
	perSpace := map[string]int{}
	shown := false
	for _, tab := range l.Tabs {
		perSpace[tab.WorktreeID]++
		if tab.ID != slot {
			continue
		}
		label := stripSpinner(tab.Title)
		if label == "" {
			label = strconv.Itoa(perSpace[tab.WorktreeID])
		}
		shown = strings.TrimSpace(label) == expected
	}
	if !shown {
		return errTabRenamed
	}
	return h.RenameSlot(ctx, slot, name)
}
