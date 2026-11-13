package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
)

// chatTags is the tag a chat connector marks a thing with: its chat and the name people read (a group's name, the
// other person of a direct chat).
func chatTags(name string) []connector.Tag {
	return []connector.Tag{{Key: connector.TagChat, Value: "oc_chat", Name: name}}
}

// fillMigrated takes where a chat thing happens from its chat tag's name, on every write: a group's name or the other
// person of a direct chat, trimmed. A thing that names no chat (a document), a chat tag with a blank name, and any
// other tag leave the item's where as it was; a renamed chat follows on the next move.
func TestFillMigratedWhere(t *testing.T) {
	for _, c := range []struct {
		name  string
		start string
		tags  []connector.Tag
		want  string
	}{
		{"a group", "", chatTags("Example Group"), "Example Group"},
		{"a direct chat's other person", "", chatTags("Alice"), "Alice"},
		{"trimmed", "", chatTags("  Example Group \n"), "Example Group"},
		{"renamed", "Old Name", chatTags("New Name"), "New Name"},
		{"no tags keeps it", "Example Group", nil, "Example Group"},
		{"a blank name keeps it", "Example Group", chatTags(""), "Example Group"},
		{"a spaces-only name keeps it", "Example Group", chatTags("   "), "Example Group"},
		{"a chat tag without a name keeps it", "Example Group", []connector.Tag{{Key: connector.TagChat, Value: "oc_chat"}}, "Example Group"},
		{"a non-chat tag keeps it", "Example Group", []connector.Tag{{Key: connector.TagRepo, Value: "example-org/example-repo",
			Name: "example-org/example-repo"}}, "Example Group"},
	} {
		t.Run(c.name, func(t *testing.T) {
			it := &InboxItem{ID: "lark:10:5", Source: "lark", Where: c.start}
			fillMigrated(it, connector.InboxThing{Thread: "chat:10", Kind: KindDM, Title: "Latest message", Last: "5", Tags: c.tags,
				Extra: &connector.ThingExtra{Source: "lark", Count: 1}})
			if it.Where != c.want {
				t.Fatalf("where %q, want %q", it.Where, c.want)
			}
		})
	}
}

// A chat a plugin took over comes in with the chat's name as where; a rename follows it on the next move; a document's
// new item has none. Where is listed (an app reads it) and absent from the JSON when empty, so items from before and
// mail keep the shape they had.
func TestMigratedInboxWhere(t *testing.T) {
	ctx := context.Background()
	h := newInboxHub(t)
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	p := &extensionLark{}
	h.addConnector(p, false)
	now := time.Now()
	larkExtra := func(last string) *connector.ThingExtra {
		return &connector.ThingExtra{Source: "lark", Account: "feishu", AccountName: "飞书", LegacyID: "lark:10:" + last, Count: 1,
			StartedAt: now, Lark: rawJSON(InboxLark{AccountID: 1, FeedID: "10", StartPosition: last})}
	}
	chat := func(last, name string) connector.InboxThing {
		return connector.InboxThing{Thread: "chat:10", Kind: KindDM, Title: "Alice", At: now, Last: last,
			Tags: chatTags(name), Extra: larkExtra(last)}
	}
	p.things = []connector.InboxThing{chat("5", "Example Group")}
	h.collectInbox(ctx, roundPoll)
	it := h.inbox.items["lark:10:5"]
	if it == nil || it.Where != "Example Group" {
		t.Fatalf("item %+v", it)
	}
	listed, _ := json.Marshal(it.listed())
	if !strings.Contains(string(listed), `"where":"Example Group"`) {
		t.Fatalf("where is not listed: %s", listed)
	}

	// The chat is renamed in the source: the next move carries the new name.
	p.things[0] = chat("6", "Renamed Group")
	h.collectInbox(ctx, roundPoll)
	if it = h.inbox.items["lark:10:5"]; it == nil || it.Last != "6" || it.Where != "Renamed Group" {
		t.Fatalf("a rename did not follow: %+v", it)
	}

	// A document's thing names no chat: no where, and none in its JSON.
	p.things = append(p.things, connector.InboxThing{Thread: "doc:n1", Kind: KindDoc, Title: "周报", At: now, Last: "100",
		Extra: &connector.ThingExtra{Source: "lark", Account: "feishu", AccountName: "飞书", LegacyID: "doc:n1:100", Count: 1,
			StartedAt: now, Doc: rawJSON(InboxDoc{NoticeID: "n1", Type: "sheet"})}})
	h.collectInbox(ctx, roundPoll)
	doc := h.inbox.items["doc:n1:100"]
	if doc == nil || doc.Where != "" {
		t.Fatalf("doc item %+v", doc)
	}
	listed, _ = json.Marshal(doc.listed())
	if strings.Contains(string(listed), `"where"`) {
		t.Fatalf("an empty where is listed: %s", listed)
	}

	// Mail never has one (its row reads sender and subject).
	mail, _ := json.Marshal(mailItem("m1", "shop@example.com", "示例商城").listed())
	if strings.Contains(string(mail), `"where"`) {
		t.Fatalf("mail lists a where: %s", mail)
	}
}

// The mute menu's chat entry is named by where the item happens (the chat's name), the title only when there is none:
// what it mutes by (tag and value) does not change.
func TestMutesOfWhere(t *testing.T) {
	it := &InboxItem{Source: "lark", Title: "Latest message", Where: "Example Group", Lark: &InboxLark{FeedID: "f1"}}
	if got := fmt.Sprint(mutesOf(it)); got != "[{chat f1 Example Group}]" {
		t.Fatalf("mutes: %s", got)
	}
	it.Where = ""
	if got := fmt.Sprint(mutesOf(it)); got != "[{chat f1 Latest message}]" {
		t.Fatalf("without a where the title names the chat: %s", got)
	}
}
