package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
)

// The model must distinguish a new, unreadable screenshot from the older exchange that was resolved (#9124).
// Stub answers pin input construction, rescheduling and persistence, not a real model's semantic accuracy.
func TestLarkCLIDMTriageSeparatesLatestMessageFromResolvedHistory(t *testing.T) {
	const history = "Example peer（@了我）: @Example owner can you check the filter?\n" +
		"Example owner: Here is the report.\nExample peer: Thanks, I can see it."
	for _, locale := range []string{"zh-Hans", "en", "ja"} {
		for _, tc := range []struct {
			name, latest, answer, tier string
		}{
			{"image", "[Image: img_example_new_screenshot]",
				`{"tier":"action","summary":"Check the new screenshot","reason":"A new screenshot needs review"}`, TierAction},
			{"resolved", "The new problem is fixed now, thanks.",
				`{"tier":"fyi","summary":"The new problem is resolved","reason":"Current resolution confirmed"}`, TierFYI},
			{"informational", "For your records only; no response needed. [Image: img_example_reference]",
				`{"tier":"fyi","summary":"A reference screenshot","reason":"For reference only"}`, TierFYI},
		} {
			t.Run(locale+"/"+tc.name, func(t *testing.T) {
				h := newInboxHub(t)
				h.f.hello.Owner, h.f.hello.Locale = "Example owner", locale
				now := time.Now()
				thing := func(last, preview, text string, at time.Time) connector.InboxThing {
					return connector.InboxThing{Thread: "larkcli:chat:oc_example", Kind: "dm", Title: "Example peer",
						Sender: "Example peer", Preview: preview, Context: text, At: at, Last: last, CanReply: true}
				}
				h.inbox.mu.Lock()
				h.inbox.applyConnector("larkcli", "Lark", []connector.InboxThing{
					thing("om_old", "Thanks, I can see it.", history, now.Add(-time.Hour)),
				}, now)
				it := openItems(h)[0]
				h.inbox.mu.Unlock()
				h.in.model, _ = captureModel(t, `{"tier":"fyi","summary":"The report was received","reason":"Earlier exchange resolved"}`)
				if !h.triageOne(context.Background(), it) || it.Tier != TierFYI {
					t.Fatal("setup: the earlier exchange was not triaged as FYI")
				}

				h.inbox.mu.Lock()
				h.inbox.applyConnector("larkcli", "Lark", []connector.InboxThing{
					thing("om_new", tc.latest, history+"\nExample peer: "+tc.latest, now),
				}, now)
				items := openItems(h)
				h.inbox.mu.Unlock()
				if len(items) != 1 || items[0] != it || it.Last != "om_new" || it.TriagedAt == it.Last {
					t.Fatal("the new message did not update the same FYI item for triage")
				}

				model, calls := captureModel(t, tc.answer)
				h.in.model = model
				if !h.triageOne(context.Background(), it) {
					t.Fatal("the new message was not triaged")
				}
				call := onlyCall(t, calls())
				latest := "本次新消息：" + tc.latest
				context := "会话上下文（含较早消息）：\n" + history
				if !strings.Contains(call.User, latest+"\n\n"+context) {
					t.Fatalf("the newest message is not separated from resolved history:\n%s", call.User)
				}
				if !strings.Contains(call.User, "另一方是人") || !strings.Contains(call.User, "（@了我）") {
					t.Fatalf("the DM or owner-mention identity was lost:\n%s", call.User)
				}
				for _, rule := range []string{"较早的回复不能证明本次新消息已解决", "[Image: ...] 只是未读取的图片占位"} {
					if !strings.Contains(call.System, rule) {
						t.Fatalf("the triage criteria lost %q:\n%s", rule, call.System)
					}
				}
				if it.Tier != tc.tier || it.TriagedAt != "om_new" {
					t.Fatalf("the verdict was not saved for the new message: tier %q, version %q", it.Tier, it.TriagedAt)
				}
				if tc.name == "image" {
					suffix := ""
					if locale != "zh-Hans" {
						suffix = "." + locale
					}
					golden(t, "inbox_triage.larkcli_dm_image.system"+suffix, call.System)
					golden(t, "inbox_triage.larkcli_dm_image.user"+suffix, call.User)
				}
			})
		}
	}
}

func TestOnlyLarkDMTriageNamesLatestPreview(t *testing.T) {
	h := newInboxHub(t)
	for _, tc := range []struct {
		name, source, kind string
		connector          bool
		want               string
	}{
		{"Lark DM", "larkcli", KindDM, true, "newest preview"},
		{"Lark bot", "larkcli", KindBot, true, ""},
		{"Lark group", "larkcli", KindMention, true, ""},
		{"another connector", "imessage", KindDM, true, ""},
		{"gateway DM", "", KindDM, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it := &InboxItem{Source: tc.source, Kind: tc.kind, Preview: "newest preview", Context: "conversation"}
			if tc.connector {
				it.Connector = &InboxConnector{}
			}
			got := h.inbox.promptItem(it)
			if got.Latest != tc.want || got.Context != it.Context {
				t.Fatalf("prompt item: latest %q, context %q", got.Latest, got.Context)
			}
		})
	}
}
