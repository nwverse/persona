package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func testConnector(t *testing.T, scenario string) *Connector {
	return testConnectorWithProfile(t, scenario, "example")
}

func testConnectorWithProfile(t *testing.T, scenario, profile string) *Connector {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("..", "..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", scenario)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if scenario == "needs_refresh" || scenario == "ready" {
		now = time.Date(2026, 11, 10, 6, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	return &Connector{runner: adapter.New(adapter.Config{Bin: bin, Profile: profile, Timeout: time.Second, Now: func() time.Time {
		return now
	}}), now: func() time.Time { return now }, profile: profile}
}

func TestManifestAndTools(t *testing.T) {
	c := testConnector(t, "success")
	if err := connector.Check(c); err != nil {
		t.Fatal(err)
	}
	if c.ID() != "larkcli" || c.Name() != "Lark via lark-cli" {
		t.Fatalf("identity = %s / %s", c.ID(), c.Name())
	}
	var names []string
	for _, tool := range c.Tools() {
		names = append(names, tool.Name)
		if tool.Description == "" || !strings.HasPrefix(string(tool.InputSchema), `{"type":"object"`) {
			t.Fatalf("bad tool: %+v", tool)
		}
	}
	if strings.Join(names, ",") != "larkcli_chats,larkcli_messages,larkcli_search" {
		t.Fatalf("tools = %v", names)
	}
}

func TestStatusSuccessAndExpiryReminder(t *testing.T) {
	c := testConnector(t, "rolling")
	st := c.Status(context.Background())
	if st.State != connector.StateOK || st.Account != "Example User" || st.Via != "lark-cli" {
		t.Fatalf("status = %+v", st)
	}
	if st.Error != "" {
		t.Fatalf("unexpected reminder: %q", st.Error)
	}

	c = testConnector(t, "refresh_expiring")
	st = c.Status(context.Background())
	if st.State != connector.StateOK || !strings.Contains(st.Error, "1 天") ||
		!strings.Contains(st.Error, "lark-cli --profile example auth login --domain all") {
		t.Fatalf("expiry status = %+v", st)
	}
}

func TestStatusRefreshWindowLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scenario  string
		state     string
		wantError string
		wantGuide bool
	}{
		{name: "healthy rolling login", scenario: "rolling", state: connector.StateOK},
		{name: "refresh window nearly used", scenario: "refresh_expiring", state: connector.StateOK, wantError: "1 天"},
		{name: "expired login", scenario: "expired", state: connector.StateNotSignedIn, wantError: "过期", wantGuide: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := testConnectorWithProfile(t, tc.scenario, "").Status(context.Background())
			if st.State != tc.state {
				t.Fatalf("state = %q, want %q; status = %+v", st.State, tc.state, st)
			}
			if tc.wantError == "" && st.Error != "" {
				t.Fatalf("unexpected error = %q", st.Error)
			}
			if tc.wantError != "" && !strings.Contains(st.Error, tc.wantError) {
				t.Fatalf("error = %q, want %q", st.Error, tc.wantError)
			}
			if tc.wantGuide != (len(st.Guide) == 1) {
				t.Fatalf("guide = %#v", st.Guide)
			}
		})
	}
}

func TestLoginGuideUsesConfiguredProfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile string
		command string
	}{
		{name: "default profile", command: "lark-cli auth login --domain all"},
		{name: "named profile", profile: "example", command: "lark-cli --profile example auth login --domain all"},
		{name: "profile with spaces and non ASCII", profile: "工作 profile", command: "lark-cli --profile '工作 profile' auth login --domain all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := testConnectorWithProfile(t, "not_logged_in", tc.profile).Status(context.Background())
			if len(st.Guide) != 1 || !strings.Contains(st.Guide[0], tc.command) {
				t.Fatalf("guide = %#v, want command %q", st.Guide, tc.command)
			}
		})
	}
}

func TestStatusCategories(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
		state    string
	}{
		{"not signed in", "not_logged_in", connector.StateNotSignedIn},
		{"expired", "expired", connector.StateNotSignedIn},
		{"old version", "old", connector.StateError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := testConnector(t, tc.scenario).Status(context.Background())
			if st.State != tc.state || st.Error == "" {
				t.Fatalf("status = %+v", st)
			}
		})
	}
	c := testConnector(t, "success")
	// The CLI status fixture has no missing scopes; exercise the mapped state with
	// a runner error from a read call through the tool path below.
	t.Setenv("LARKCLI_FAKE_SCENARIO", "missing_scope")
	_, err := c.Call(context.Background(), "larkcli_search", json.RawMessage(`{"query":"example"}`))
	if adapter.KindOf(err) != adapter.KindMissingPermission {
		t.Fatalf("kind = %q, err = %v", adapter.KindOf(err), err)
	}
	if !strings.Contains(err.Error(), "im:chat:read") {
		t.Fatalf("missing scope = %v", err)
	}
	c = testConnector(t, "auth_missing_scope")
	st := c.Status(context.Background())
	if st.State != connector.StateNoPermission || !strings.Contains(st.Error, "search:message") || len(st.Guide) != 1 ||
		!strings.Contains(st.Guide[0], "lark-cli --profile example auth login --domain all") {
		t.Fatalf("missing scope status = %+v", st)
	}
}

func TestStatusUsesRefreshExpiryAndReportsFeatureScopes(t *testing.T) {
	for _, scenario := range []string{"needs_refresh", "ready"} {
		st := testConnector(t, scenario).Status(context.Background())
		if st.State != connector.StateNoPermission || strings.Contains(st.Error, "not signed") || len(st.Guide) != 1 ||
			!strings.Contains(st.Error, "im:chat:read") || !strings.Contains(st.Guide[0], "lark-cli --profile example auth login --domain all") {
			t.Fatalf("%s status = %+v", scenario, st)
		}
	}
}

func TestReadToolsCallTheAdapter(t *testing.T) {
	c := testConnector(t, "success")
	out, err := c.Call(context.Background(), "larkcli_chats", json.RawMessage(`{"filter":"recent","limit":2}`))
	if err != nil || !strings.Contains(out, "oc_example_group") {
		t.Fatalf("chats = %s, err = %v", out, err)
	}
	out, err = c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_group"}`))
	if err != nil || !strings.Contains(out, "om_example_older") {
		t.Fatalf("messages = %s, err = %v", out, err)
	}
	out, err = c.Call(context.Background(), "larkcli_search", json.RawMessage(`{"query":"example","kind":"messages"}`))
	if err != nil || !strings.Contains(out, "om_example_search") {
		t.Fatalf("search = %s, err = %v", out, err)
	}
}

func inboxTestConnector(t *testing.T, scenario string) *Connector {
	t.Helper()
	c := testConnector(t, scenario)
	c.SetInboxSource("cli", nil)
	c.SetPollInterval(time.Minute)
	return c
}

func inboxThings(t *testing.T, c *Connector) map[string]connector.InboxThing {
	t.Helper()
	items, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]connector.InboxThing{}
	for _, item := range items {
		out[item.Thread] = item
	}
	return out
}

func TestInboxPollKeepsRulesGroupsAndDetectsOwnReply(t *testing.T) {
	c := inboxTestConnector(t, "inbox")
	moved, err := c.Poll(context.Background())
	if err != nil || !moved {
		t.Fatalf("poll = moved=%v err=%v", moved, err)
	}
	items := inboxThings(t, c)
	if len(items) != 3 {
		t.Fatalf("items = %#v", items)
	}
	group := items["larkcli:chat:oc_example_group:thread:omt_topic_one"]
	if group.Thread == "" || !strings.Contains(group.Context, "A second line") || group.Done != connector.DoneReplied {
		t.Fatalf("group item = %+v", group)
	}
	if dm := items["larkcli:chat:oc_example_dm"]; dm.Tier != connector.TierAction || dm.Done != "" {
		t.Fatalf("direct item = %+v", dm)
	}
	if group.Kind != "mention" || group.Title != "Example Group" || len(group.Tags) != 2 ||
		group.Tags[0].Key != connector.TagChat || group.Tags[0].Name != "Example Group" ||
		group.Tags[1].Key != connector.TagSender || group.Tags[1].Value != "ou_example_peer" || group.Tags[1].Name != "Example Peer" {
		t.Fatalf("group title/tag changed = %+v", group)
	}
	if topic := items["larkcli:chat:oc_example_group:thread:omt_topic_two"]; topic.Thread == "" {
		t.Fatal("seeded topic reply was dropped")
	}
	if _, ok := items["larkcli:chat:oc_example_group:thread:omt_topic_three"]; ok {
		t.Fatal("unmentioned topic and own-only message entered the Inbox")
	}
}

func TestInboxP2PTitleUsesOtherSender(t *testing.T) {
	c := inboxTestConnector(t, "inbox_p2p")
	if _, err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	items := inboxThings(t, c)
	dm, ok := items["larkcli:chat:oc_example_dm"]
	if !ok {
		t.Fatalf("direct item missing: %#v", items)
	}
	if dm.Kind != "dm" || dm.Title != "Example Peer" || len(dm.Tags) != 2 || dm.Tags[0].Name != "Example Peer" ||
		dm.Tags[1].Key != connector.TagSender || dm.Tags[1].Value != "ou_example_peer" || dm.Tags[1].Name != "Example Peer" {
		t.Fatalf("direct title/tag = %+v", dm)
	}
	if dm.Done != connector.DoneReplied {
		t.Fatalf("own reply was not observed = %+v", dm)
	}
}

func TestInboxDropsBoundAppMessagesButKeepsOtherBots(t *testing.T) {
	c := inboxTestConnector(t, "inbox_bot_loop")
	moved, err := c.Poll(context.Background())
	if err != nil || !moved {
		t.Fatalf("poll = moved=%v err=%v", moved, err)
	}
	items := inboxThings(t, c)
	if _, ok := items["larkcli:chat:oc_example_bound_dm"]; ok {
		t.Fatalf("bound app message entered Inbox: %#v", items)
	}
	bot, ok := items["larkcli:chat:oc_example_other_dm"]
	if !ok || bot.Kind != "bot" || bot.Tier != connector.TierAction {
		t.Fatalf("other app bot = %+v", bot)
	}
	if bot.Tags[1].Key != connector.TagSender || bot.Tags[1].Value != "cli_other" {
		t.Fatalf("other app sender tag = %+v", bot.Tags)
	}
	c.inboxMu.Lock()
	_, seen := c.inboxSeen["om_example_bound_app"]
	c.inboxMu.Unlock()
	if !seen {
		t.Fatal("bound app message was not marked seen")
	}
	moved, err = c.Poll(context.Background())
	if err != nil || moved {
		t.Fatalf("second poll = moved=%v err=%v", moved, err)
	}
}

func TestP2PTitleUsesChatPartnerWhenBatchHasOnlyOwnMessages(t *testing.T) {
	partner := json.RawMessage(`{"open_id":"ou_example_peer","name":"Example Partner","sender_type":"user"}`)
	group := &inboxGroup{chatID: "oc_example_dm", messages: []adapter.Message{{
		MessageID: "om_example_mine", ChatID: "oc_example_dm", ChatType: "p2p", Sender: adapter.Sender{ID: "ou_example_user", Name: "Example User"}, ChatPartner: partner,
	}}}
	if got := p2pTitle(group, nil, "ou_example_user"); got != "Example Partner" {
		t.Fatalf("title = %q", got)
	}
}

func TestInboxCursorOverlapDedupAndPageCap(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("LARKCLI_FAKE_LOG", logPath)
	c := inboxTestConnector(t, "inbox_pages")
	c.inboxPageLimit = 1
	if _, err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := inboxThings(t, c)
	if len(first) != 1 {
		t.Fatalf("first page items = %d", len(first))
	}
	state := c.inboxStateForTests()
	if state.Cursor == "" {
		t.Fatal("cursor did not advance")
	}
	if _, err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := inboxThings(t, c)
	if len(second) != 1 {
		t.Fatalf("overlap or capped page duplicated items: %#v", second)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "+messages-search") < 3 { // bounded seed plus one page per round
		t.Fatalf("search calls were not paged: %q", b)
	}
}

func TestInboxCoexistsWithGateway(t *testing.T) {
	c := inboxTestConnector(t, "inbox")
	c.SetInboxSource("", func(context.Context) bool { return true })
	moved, err := c.Poll(context.Background())
	if err != nil || moved {
		t.Fatalf("gateway-primary poll = moved=%v err=%v", moved, err)
	}
	items, _ := c.Collect(context.Background())
	if len(items) != 0 {
		t.Fatalf("CLI collected while gateway was primary: %+v", items)
	}
	c.SetInboxSource("cli", func(context.Context) bool { return true })
	moved, err = c.Poll(context.Background())
	if err != nil || !moved {
		t.Fatalf("CLI-primary poll = moved=%v err=%v", moved, err)
	}
}

func TestInboxReplyUsesBot(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("LARKCLI_FAKE_LOG", logPath)
	c := inboxTestConnector(t, "inbox")
	if err := c.Reply(context.Background(), connector.InboxReply{Thread: "larkcli:chat:oc_example_group:thread:omt_topic_one", Last: "om_example_mention_one", Text: "Received", ClientID: "reply-1"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "+messages-reply --message-id om_example_mention_one --text Received --idempotency-key reply-1 --reply-in-thread --as bot --format json") {
		t.Fatalf("reply command = %q", b)
	}
}

func TestInboxAllChatsOptionIsOptIn(t *testing.T) {
	c := inboxTestConnector(t, "inbox")
	var defaults InboxOptions
	if err := json.Unmarshal(c.Options(), &defaults); err != nil {
		t.Fatal(err)
	}
	if len(defaults.InboxAllChats) != 0 || len(defaults.Documents) != 0 || len(defaults.WikiSpaces) != 0 {
		t.Fatalf("default options = %+v", defaults)
	}
	if err := c.SetOptions(json.RawMessage(`{"inbox_all_chats":["oc_example_group"]}`)); err != nil {
		t.Fatal(err)
	}
	var options InboxOptions
	if err := json.Unmarshal(c.Options(), &options); err != nil {
		t.Fatal(err)
	}
	if len(options.InboxAllChats) != 1 || options.InboxAllChats[0] != "oc_example_group" || len(options.Documents) != 0 || len(options.WikiSpaces) != 0 {
		t.Fatalf("options = %+v", options)
	}
	if _, err := c.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	items := inboxThings(t, c)
	if _, ok := items["larkcli:chat:oc_example_group:thread:omt_topic_three"]; !ok {
		t.Fatalf("selected chat did not admit its ordinary message: %#v", items)
	}
	if err := c.SetOptions(json.RawMessage(`{"inbox_all_chats":[""]}`)); err == nil {
		t.Fatal("empty chat id accepted")
	}
}

func TestInboxWatchSignalsChanged(t *testing.T) {
	c := inboxTestConnector(t, "inbox")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var scope connector.Scope
	err := c.Watch(ctx, func(got connector.Scope) {
		scope = got
		cancel()
	})
	if err != nil || scope != connector.ScopeInbox {
		t.Fatalf("watch = scope=%q err=%v", scope, err)
	}
}

type inboxStateStore struct{ raw json.RawMessage }

func (s *inboxStateStore) Setting(_ context.Context, _ string, out any) (bool, error) {
	if len(s.raw) == 0 {
		return false, nil
	}
	return true, json.Unmarshal(s.raw, out)
}

func (s *inboxStateStore) PutSetting(_ context.Context, _ string, value any) error {
	b, err := json.Marshal(value)
	s.raw = b
	return err
}

func TestInboxCursorAndTopicsPersistInStore(t *testing.T) {
	store := &inboxStateStore{}
	c1 := inboxTestConnector(t, "inbox")
	c1.SetStateStore(store)
	if _, err := c1.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := c1.inboxStateForTests()
	if state.Cursor == "" || len(state.Topics) == 0 {
		t.Fatalf("state was not saved: %+v", state)
	}
	c2 := inboxTestConnector(t, "inbox")
	c2.SetStateStore(store)
	if _, err := c2.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded := c2.inboxStateForTests()
	if loaded.Cursor == "" || len(loaded.Topics) == 0 || !loaded.TopicsSeeded {
		t.Fatalf("state was not restored: %+v", loaded)
	}
}
