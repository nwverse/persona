package node

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/screen"
)

type traeComposer struct {
	open, busy, canQueue bool
	draft                string
	queued               []string
}

// TRAE draws its composer between two rules. Its busy queues above the box use
// the same ❯ glyph; neither a queued message nor a transcript row is a composer.
func parseTraeComposer(text string) traeComposer {
	raw := strings.Split(text, "\n")
	plain := make([]string, len(raw))
	for i, line := range raw {
		plain[i] = strings.TrimSpace(screen.Plain(line))
	}
	var box traeComposer
	for i := len(plain) - 2; i > 0; i-- {
		if !strings.HasPrefix(plain[i], "❯") || !strings.HasPrefix(plain[i-1], "────") {
			continue
		}
		var draft []string
		end := i + 1
		for ; end < len(raw) && !strings.HasPrefix(plain[end], "────"); end++ {
		}
		if end == len(raw) {
			return box
		}
		for k := i; k < end; k++ {
			line := strings.TrimSpace(screen.Undimmed(raw[k]))
			if k == i {
				line = strings.TrimSpace(strings.TrimPrefix(line, "❯"))
			}
			draft = append(draft, line)
		}
		box.open = true
		box.draft = strings.TrimSpace(strings.Join(draft, "\n"))
		box.canQueue = strings.Contains(strings.Join(plain[end+1:], "\n"), "tab to queue message")
		for k := i - 2; k >= 0 && i-k <= 30; k-- {
			if strings.Contains(plain[k], "esc to interrupt)") {
				box.busy = true
				inQueue := false
				for _, line := range plain[k+1 : i-1] {
					if strings.HasPrefix(line, "Queued for next turn ·") || strings.HasPrefix(line, "Send after tool call ·") {
						inQueue = true
					} else if inQueue && strings.HasPrefix(line, "❯ ") {
						box.queued = append(box.queued, strings.TrimSpace(strings.TrimPrefix(line, "❯")))
					}
				}
				break
			}
		}
		return box
	}
	return box
}

func (n *Node) readTraeComposer(ctx context.Context, pane string) (traeComposer, error) {
	text, err := n.readPaneANSI(ctx, pane, "visible", 80)
	if err != nil {
		return traeComposer{}, err
	}
	return parseTraeComposer(text), nil
}

func (n *Node) traeForeground(ctx context.Context, pane string) bool {
	h, ref, err := n.paneHost(pane)
	if err != nil {
		return false
	}
	f, ok := h.(host.ForegroundNamer)
	if !ok {
		return false
	}
	names, err := f.PaneForeground(ctx, ref)
	if err != nil {
		return false
	}
	for _, name := range names {
		if isTraexProgram(name) {
			return true
		}
	}
	return false
}

func (n *Node) sendTraeTerminal(ctx context.Context, pane, text string, queue bool) error {
	if !n.traeForeground(ctx, pane) {
		return refusal(notRunning, "TRAE CLI is no longer in the foreground. Nothing was sent.", nil)
	}
	box, err := n.readTraeComposer(ctx, pane)
	if err != nil || !box.open {
		return refusal(screen.DialogOpen, "TRAE CLI’s message box is not available. Nothing was sent.", nil)
	}
	if box.draft != "" {
		return refusal(screen.InputDraft, "TRAE CLI already has a draft. Send it or clear it in the terminal first.", map[string]any{"draft": box.draft})
	}
	if err := n.sendText(ctx, pane, pasteStart+text+pasteEnd); err != nil {
		return uncertain(err)
	}
	box, err = n.waitTraeComposer(ctx, pane, func(box traeComposer) bool {
		return stillPrompted(box.draft, text) || pastedRE.MatchString(box.draft)
	})
	if err != nil {
		return err
	}
	key := "enter"
	if queue && box.busy {
		if !box.canQueue {
			return uncertain(errors.New("TRAE CLI did not offer its next-turn queue; the message is still a draft"))
		}
		key = "tab"
	}
	if !n.traeForeground(ctx, pane) {
		return uncertain(errors.New("TRAE CLI left the foreground after the paste; nothing was submitted"))
	}
	if err := n.sendKeys(ctx, pane, key); err != nil {
		return uncertain(err)
	}
	_, err = n.waitTraeComposer(ctx, pane, func(box traeComposer) bool { return box.draft == "" })
	if err == nil {
		if key == "tab" {
			markSendQueued(ctx)
		}
		n.notifySessionsSoon()
	}
	return err
}

func (n *Node) waitTraeComposer(ctx context.Context, pane string, match func(traeComposer) bool) (traeComposer, error) {
	ctx, cancel := context.WithTimeout(ctx, codexConfirm)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return traeComposer{}, uncertain(errors.New("TRAE CLI did not confirm the message; check the terminal before sending again"))
		case <-time.After(codexStep):
		}
		box, err := n.readTraeComposer(ctx, pane)
		if err != nil {
			return traeComposer{}, uncertain(err)
		}
		if box.open && match(box) {
			return box, nil
		}
	}
}
