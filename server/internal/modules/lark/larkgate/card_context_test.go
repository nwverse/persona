package larkgate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestFetchCardContextFromReadOnlyCLI(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lark-cli")
	script := `#!/bin/sh
case "$*" in
  *"/chats/oc_1"*) printf '%s\n' '{"chat":{"name":"Release Room"}}' ;;
  *"/messages/om_1"*) printf '%s\n' '{"message":{"chat_id":"oc_1","thread_id":"omt_1","sender":{"name":"Example User"},"body":{"content":"{\"text\":\"Can we ship on Friday?\"}"}}}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := New()
	cfg := proto.AssistantLark{CLI: &proto.LarkCLI{Bin: bin}}
	chat, sender, replyText, inTopic, ok := g.FetchCardContext(context.Background(), cfg, "reply", "", "om_1")
	if !ok || chat != "Release Room" || sender != "Example User" || replyText != "Can we ship on Friday?" || !inTopic {
		t.Fatalf("unexpected context: ok=%v chat=%q sender=%q reply=%q topic=%v", ok, chat, sender, replyText, inTopic)
	}
	summary, formatted := g.FormatMessageSummary("reply", `{"text":"yes","clientId":"internal"}`, chat, sender, replyText, inTopic, ok, i18n.En)
	if !formatted || !strings.Contains(summary, "Release Room") || !strings.Contains(summary, "Can we ship on Friday?") || strings.Contains(summary, "clientId") {
		t.Fatalf("summary: formatted=%v %q", formatted, summary)
	}
}

func TestFetchCardContextFailureFallsBack(t *testing.T) {
	g := New()
	cfg := proto.AssistantLark{CLI: &proto.LarkCLI{Bin: filepath.Join(t.TempDir(), "missing-lark-cli")}}
	_, _, _, _, ok := g.FetchCardContext(context.Background(), cfg, "send", "oc_1", "")
	if ok {
		t.Fatal("missing CLI unexpectedly produced metadata")
	}
	if _, formatted := g.FormatMessageSummary("send", `{"text":"hello"}`, "", "", "", false, false, i18n.En); formatted {
		t.Fatal("expected FormatMessageSummary to decline when ctxOK is false")
	}
}
