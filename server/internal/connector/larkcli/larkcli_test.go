package larkcli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func testConnector(t *testing.T, scenario string) *Connector {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("..", "..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", scenario)
	return &Connector{runner: adapter.New(adapter.Config{Bin: bin, Profile: "example", Timeout: time.Second, Now: func() time.Time {
		return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	}}), now: func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }}
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
	c := testConnector(t, "success")
	st := c.Status(context.Background())
	if st.State != connector.StateOK || st.Account != "Example User" || st.Via != "lark-cli" {
		t.Fatalf("status = %+v", st)
	}
	if st.Error != "" {
		t.Fatalf("unexpected reminder: %q", st.Error)
	}

	c = testConnector(t, "expiring")
	st = c.Status(context.Background())
	if st.State != connector.StateOK || !strings.Contains(st.Error, "3 天") {
		t.Fatalf("expiry status = %+v", st)
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
	c = testConnector(t, "auth_missing_scope")
	st := c.Status(context.Background())
	if st.State != connector.StateNoPermission || !strings.Contains(st.Error, "im:message:readonly") {
		t.Fatalf("missing scope status = %+v", st)
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
