package node

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/screen"
)

func TestCodexApprovalOccurrenceTimestampSurvivesScans(t *testing.T) {
	cards := &codexCards{}
	agent := codexAgent{kind: "codex", cwd: "/work/example"}
	dialog := &screen.CodexApprovalDialog{Rev: "rev-1", Title: "Run command", Command: "printf ok"}
	first := cards.approvalOccurrenceCard("s1", agent, "Conversation", dialog)
	time.Sleep(time.Millisecond)
	second := cards.approvalOccurrenceCard("s1", agent, "Conversation", dialog)
	if first.ID == "" || second.ID != first.ID || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("unchanged dialog occurrence: first=%+v second=%+v", first, second)
	}
	cards.approvalGone("s1")
	time.Sleep(time.Millisecond)
	third := cards.approvalOccurrenceCard("s1", agent, "Conversation", dialog)
	if third.ID != first.ID || third.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("later dialog must be a new occurrence: first=%+v third=%+v", first, third)
	}
}
