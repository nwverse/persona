package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestCardFallbackPrecedesSummaryClipping(t *testing.T) {
	text := "Conclusion\n```persona-view\n{\"fallback\":\"Readable progress\",\"sections\":[]}\n```\n"
	if got := clipMarkdown(text, 600); strings.Contains(got, "sections") || !strings.Contains(got, "Readable progress") {
		t.Fatalf("summary %q", got)
	}
	if got := transcriptText("Example", "", []proto.Item{{Role: "assistant", Kind: proto.KindText, Text: text}}); strings.Contains(got, "sections") || !strings.Contains(got, "Readable progress") {
		t.Fatalf("context %q", got)
	}
}

func TestGoldenCardTitleFallback(t *testing.T) {
	f := newFakeHub()
	f.replies = []string{"研究 | 示例进度"}
	s := newTestService(t, f)
	s.makeTitle(context.Background(), time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), "看看进度", "```persona-view\n{\"fallback\":\"构建和测试已经完成\",\"sections\":[]}\n```", "")
	calls := f.modelCalls()
	if len(calls) == 0 {
		t.Fatal("no title input")
	}
	if strings.Contains(calls[0].User, "sections") {
		t.Fatal("title input leaked card JSON")
	}
	golden(t, "session_title.card_fallback", calls[0].User)
}
