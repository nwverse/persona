package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThreadRootReadsRawAndSuppliedRoot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rootID string
	}{
		{name: "raw lookup"},
		{name: "supplied root", rootID: "om_example_root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "args.log")
			t.Setenv("LARKCLI_FAKE_LOG", logPath)
			setFakeRoot(t, []Message{{MessageID: "om_example_root", ChatID: "oc_example_chat", ThreadID: "omt_example_thread"}})
			r := New(fakeConfig(t, "success"))
			got, err := r.ThreadRoot(context.Background(), Message{
				MessageID: "om_example_request", ChatID: "oc_example_chat", ThreadID: "omt_example_thread", RootID: tc.rootID,
			})
			if err != nil || got.MessageID != "om_example_root" || got.ChatID != "oc_example_chat" {
				t.Fatalf("root = %+v, err = %v", got, err)
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			calls := string(log)
			if !strings.Contains(calls, "--as user --format json") || !strings.Contains(calls, "im +messages-mget --message-ids om_example_root --no-reactions --as user --format json") {
				t.Fatalf("user identity or root mget missing from calls:\n%s", calls)
			}
			rawLookup := strings.Contains(calls, "/open-apis/im/v1/messages/om_example_request")
			if rawLookup == (tc.rootID != "") {
				t.Fatalf("raw lookup presence = %v for supplied root %q:\n%s", rawLookup, tc.rootID, calls)
			}
			if rawLookup && !strings.Contains(calls, "api GET /open-apis/im/v1/messages/om_example_request --as user --format json") {
				t.Fatalf("raw root lookup must read as user:\n%s", calls)
			}
		})
	}
}

func TestThreadRootRejectsUnrelatedOrDeletedMessages(t *testing.T) {
	for _, tc := range []struct {
		name string
		root Message
	}{
		{name: "different chat", root: Message{MessageID: "om_example_root", ChatID: "oc_other_chat", ThreadID: "omt_example_thread"}},
		{name: "different thread", root: Message{MessageID: "om_example_root", ChatID: "oc_example_chat", ThreadID: "omt_other_thread"}},
		{name: "deleted root", root: Message{MessageID: "om_example_root", ChatID: "oc_example_chat", ThreadID: "omt_example_thread", Deleted: true}},
		{name: "expanded extra message", root: Message{MessageID: "om_unrequested_extra", ChatID: "oc_example_chat", ThreadID: "omt_example_thread"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setFakeRoot(t, []Message{tc.root})
			r := New(fakeConfig(t, "success"))
			got, err := r.ThreadRoot(context.Background(), Message{
				MessageID: "om_example_request", ChatID: "oc_example_chat", ThreadID: "omt_example_thread",
			})
			if err != nil || got.MessageID != "" {
				t.Fatalf("unrelated root accepted: %+v, err = %v", got, err)
			}
		})
	}
}

func setFakeRoot(t *testing.T, messages []Message) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "root.json")
	body, err := json.Marshal(map[string]any{"ok": true, "data": map[string]any{"messages": messages}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_ROOT", path)
}
