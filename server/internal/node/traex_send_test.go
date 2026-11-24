package node

import (
	"context"
	"slices"
	"testing"
)

const traeEmpty = "────────────────────\n❯ \x1b[2mWrite tests for @filename\x1b[0m\n────────────────────\n  Model · ~/work\n  ? for shortcuts"
const traeBusy = "Running command… (12s • esc to interrupt)\n  Queued for next turn · alt/⌥+↑ edit last queued message\n  ❯ Earlier message\n"
const traeDraft = "────────────────────\n❯ A fresh message\n────────────────────\n  tab to queue message"

func TestTraeComposerSeparatesTheDraftFromQueuedMessages(t *testing.T) {
	box := parseTraeComposer(traeBusy + traeEmpty)
	if !box.open || !box.busy || box.draft != "" || !slices.Equal(box.queued, []string{"Earlier message"}) {
		t.Fatalf("empty composer with queue: %+v", box)
	}
	box = parseTraeComposer(traeBusy + traeDraft)
	if !box.open || !box.canQueue || box.draft != "A fresh message" {
		t.Fatalf("draft in busy composer: %+v", box)
	}
	if box := parseTraeComposer(traeBusy + "Approve command?\n❯ Yes\n  No"); box.open {
		t.Fatalf("approval mistaken for composer: %+v", box)
	}
}

func TestTraeTerminalSubmitsConfirmedDraftWithNativeKey(t *testing.T) {
	for _, tc := range []struct {
		name, before, key string
		queue             bool
	}{
		{"follow up while busy", traeBusy, "enter", false},
		{"queue while busy", traeBusy, "tab", true},
		{"agent already idle", "", "enter", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, _ := herdrNode(t)
			const pane = "w1:p1"
			f.setScreen(pane, tc.before+traeEmpty)
			f.reply("pane.process_info", foreground([2]string{"traecli", "traecli"}))
			f.after(func(c herdrCall) {
				switch c.Method {
				case "pane.send_text":
					f.setScreen(pane, tc.before+traeDraft)
				case "pane.send_keys":
					f.setScreen(pane, tc.before+traeEmpty)
				}
			})
			if err := n.sendTraeTerminal(context.Background(), pane, "A fresh message", tc.queue); err != nil {
				t.Fatal(err)
			}
			assertTrace(t, f.trace("pane.send_keys"), []string{sendKeys(pane, tc.key)})
		})
	}
}

func TestTraeTerminalDoesNotSubmitAfterSwitchToShell(t *testing.T) {
	n, f, _ := herdrNode(t)
	const pane = "w1:p1"
	f.setScreen(pane, traeEmpty)
	f.reply("pane.process_info", foreground([2]string{"traecli", "traecli"}))
	f.after(func(c herdrCall) {
		if c.Method == "pane.send_text" {
			f.setScreen(pane, traeDraft)
			f.reply("pane.process_info", foreground([2]string{"bash", "bash"}))
		}
	})
	if err := n.sendTraeTerminal(context.Background(), pane, "A fresh message", false); !maybeDelivered(err) {
		t.Fatalf("expected uncertain delivery, got %v", err)
	}
	assertTrace(t, f.trace("pane.send_keys"), nil)
}
