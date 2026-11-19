package node

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/screen"
)

// A terminal can display a paste as several chips with literal text between or
// after them. Chips only confirm the composer took input, never its full content:
// the startup caller still requires the complete first message in the rollout.
var codexPasteChipRE = lazyre.New(`\[Pasted Content ([0-9]+) chars\]`)

func codexFirstPromptShown(draft, prompt string) bool {
	if sameText(draft, prompt) {
		return true
	}
	matches := codexPasteChipRE.FindAllStringSubmatchIndex(draft, -1)
	if len(matches) == 0 {
		return false
	}
	compact := func(s string) string { return strings.Join(strings.Fields(s), "") }
	var parts []string
	last, hidden := 0, 0
	budget := utf8.RuneCountInString(prompt)
	for _, m := range matches {
		n, err := strconv.Atoi(draft[m[2]:m[3]])
		if err != nil || n <= 0 || n > budget-hidden {
			return false
		}
		hidden += n
		parts = append(parts, compact(draft[last:m[0]]))
		last = m[1]
	}
	parts = append(parts, compact(draft[last:]))
	// Wrapped display whitespace cannot be counted exactly. Reject impossible
	// counts and foreign/reordered visible text; do not pretend this is a hash.
	visible := 0
	for _, p := range parts {
		visible += utf8.RuneCountInString(p)
	}
	if hidden+visible > budget {
		return false
	}
	p := compact(prompt)
	first, tail := parts[0], parts[len(parts)-1]
	if !strings.HasPrefix(p, first) || !strings.HasSuffix(p, tail) || len(first)+len(tail) > len(p) {
		return false
	}
	p = p[len(first) : len(p)-len(tail)]
	for _, part := range parts[1 : len(parts)-1] {
		at := strings.Index(p, part)
		if at < 0 {
			return false
		}
		p = p[at+len(part):]
	}
	return true
}

// typeCodexFirst handles only startup, including a waiting row's first message.
// No update is installed. Skip at most one recognized offer, and replay a paste
// at most once, only into an empty unqueued composer after that skip. There has
// been no Enter at that point. Never clear or append to a different draft.
func (n *Node) typeCodexFirst(ctx context.Context, pane, prompt string) error {
	confirm, cancel := context.WithTimeout(ctx, 4*time.Second+codexDialogPause)
	defer cancel()
	skipped, replayed := false, false
	skipUpdate := func() (bool, error) {
		if skipped {
			return false, nil
		}
		text, err := n.readPane(confirm, pane, "visible", screen.DialogLines)
		if err != nil {
			return false, err
		}
		keys := codexSkipUpdateKeys(text)
		if keys == nil {
			return false, nil
		}
		if err := n.sendKeys(confirm, pane, keys...); err != nil {
			return false, err
		}
		skipped = true
		if !sleepCtx(confirm, codexDialogPause) {
			return true, confirm.Err()
		}
		return true, nil
	}
	for {
		if err := n.codexReady(confirm, pane, true); err != nil {
			ok, skipErr := skipUpdate()
			if skipErr != nil {
				return skipErr
			}
			if ok {
				continue
			}
			return err
		}
		break
	}
	paste := func() error { return n.sendText(confirm, pane, pasteStart+inertControls(prompt)+pasteEnd) }
	if err := paste(); err != nil {
		return err
	}
	var s screen.CodexScreen
	postPasteSkipped := false
	for {
		if !sleepCtx(confirm, codexStep) {
			return uncertain(errors.New(i18n.T(i18n.Default, "node-codex-start.paste_unconfirmed", i18n.Args{"view": s.View})))
		}
		var err error
		if s, err = n.codexScreen(confirm, pane); err != nil {
			return uncertain(err)
		}
		if s.View == screen.CodexComposer {
			if codexFirstPromptShown(s.Draft, prompt) {
				return uncertain(n.sendKeys(confirm, pane, "enter"))
			}
			if postPasteSkipped && !replayed && s.Draft == "" && len(s.Queued) == 0 {
				replayed = true
				if err := paste(); err != nil {
					return uncertain(err)
				}
			}
			continue
		}
		if ok, err := skipUpdate(); err != nil {
			return uncertain(err)
		} else if ok {
			postPasteSkipped = true
		}
	}
}
