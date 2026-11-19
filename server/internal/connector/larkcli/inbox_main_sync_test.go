package larkcli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/prompts"
)

func TestInboxReplayKeepsSelectedRulesWithBoundedContext(t *testing.T) {
	for _, handoff := range []bool{false, true} {
		name := "assigned card"
		if handoff {
			name = "owner handoff"
		}
		t.Run(name, func(t *testing.T) {
			at := time.Date(2030, 1, 3, 12, 0, 0, 0, time.UTC)
			selected := addressedMessage("om_example_selected", "ou_example_peer", "Example Peer",
				`"<card title=\"On-call person: Example User\">An example alert</card>"`, "", nil)
			selected.MsgType = "interactive"
			wantKind, wantTier := "mention", connector.TierAction
			if handoff {
				selected = addressedMessage(selected.MessageID, "ou_example_user", "Example User",
					fmt.Sprintf("%q", strings.Repeat("h", contextNewRunes+40)), "",
					[]adapter.Mention{{ID: "cli_example"}})
				wantKind, wantTier = "bot_handoff", connector.TierFYI
			}
			selected.CreateTime = at.Format(time.RFC3339)
			var history []adapter.Message
			for i := range 8 {
				message := addressedMessage(fmt.Sprintf("om_example_old_%d", i), "ou_example_context", "Example Context",
					fmt.Sprintf("%q", fmt.Sprintf("old-%d ", i)+strings.Repeat("b", contextEarlierRunes+40)), "", nil)
				message.CreateTime = at.Add(-48*time.Hour + time.Duration(i)*time.Minute).Format(time.RFC3339)
				message.MessagePosition = fmt.Sprint(i + 1)
				history = append(history, message)
			}
			later := addressedMessage("om_example_later", "ou_example_later", "Example Later",
				`"Unselected later context"`, "", nil)
			later.CreateTime, later.MessagePosition = at.Add(time.Minute).Format(time.RFC3339), "21"
			history = append(history, selected, later)
			c := addressedTestConnector(t, []adapter.Message{selected}, history)
			items, err := c.ReplayMessages(context.Background(), selected.ChatID,
				at.Add(-time.Minute).Format(time.RFC3339), at.Add(5*time.Minute).Format(time.RFC3339),
				[]string{selected.MessageID})
			if err != nil || len(items) != 1 {
				t.Fatalf("selected replay failed: items=%+v err=%v", items, err)
			}
			item := items[0]
			if item.Last != selected.MessageID || item.Kind != wantKind || item.Tier != wantTier ||
				item.OwnerAssigned != !handoff || item.Done != "" ||
				!slices.ContainsFunc(item.Tags, func(tag connector.Tag) bool {
					return tag.Key == connector.TagSender && tag.Value == selected.Sender.ID
				}) {
				t.Fatalf("bounded context changed the selected activity or rules: %+v", item)
			}
			current, background, ok := strings.Cut(item.Context, "Background history (context only, not new requests):")
			if !ok || !strings.Contains(current, "Current messages (new activity):") ||
				!strings.Contains(current, at.Format(time.RFC3339)) ||
				strings.Contains(current, "Unselected later context") ||
				!strings.Contains(background, "Unselected later context") {
				t.Fatalf("selected activity and background were not kept separate: %q", item.Context)
			}
			for i := range 8 {
				// The later unselected message occupies one of the retained background slots.
				if got, want := strings.Contains(background, fmt.Sprintf("old-%d ", i)), i >= 9-contextEarlierKeep; got != want {
					t.Fatalf("background %d presence=%v, want %v: %q", i, got, want, item.Context)
				}
			}
			if strings.Contains(item.Context, strings.Repeat("b", contextEarlierRunes+1)) ||
				strings.Contains(item.Context, strings.Repeat("h", contextNewRunes+1)) {
				t.Fatalf("replay bypassed the background or new-message text budget: %q", item.Context)
			}
		})
	}
}

func TestInboxHandoffPromptKeepsContextWindowAndConfirmation(t *testing.T) {
	prompt := prompts.RenderInboxItem(prompts.InboxItem{Source: ID, Kind: "bot_handoff"})
	for _, want := range []string{
		"This is a handoff, not a new request for the owner.",
		"Any reply to Lark requires the owner's confirmation.",
		"Current messages (new activity):",
		"Background history (context only, not new requests):",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("handoff prompt lost %q: %s", want, prompt)
		}
	}
}
