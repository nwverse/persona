package node

import (
	"encoding/json"
	"testing"
	"time"
)

func handBackInput() hookInput {
	input, _ := json.Marshal(map[string]any{"questions": []map[string]any{{"question": "Which?"}}})
	return hookInput{HookEventName: "PermissionRequest", SessionID: decidedSession, ToolName: "AskUserQuestion", ToolInput: input}
}

// Once an ambiguous attempt is handed to the terminal, its original request cannot later recreate a phone wait.
func TestQuestionHandBackPreventsLaterOriginalPair(t *testing.T) {
	q := &questionCalls{}
	in := handBackInput()
	pre := in
	pre.HookEventName, pre.ToolUseID = "PreToolUse", "toolu_handback"
	now := time.Now()
	q.before(pre, now)
	if !q.handBack(in, "a1", now) {
		t.Fatal("an unpaired ambiguous attempt was not handed back")
	}
	if id := q.pair(in, "a1", false, now); id != "" {
		t.Fatalf("the original request paired %q after the attempt was handed back", id)
	}
}

// If the original request paired while the transcript was being read, the stale ambiguity cannot cancel its card.
func TestQuestionOriginalPairBeforeHandBackWins(t *testing.T) {
	q := &questionCalls{}
	in := handBackInput()
	pre := in
	pre.HookEventName, pre.ToolUseID = "PreToolUse", "toolu_original"
	now := time.Now()
	q.before(pre, now)
	if id := q.pair(in, "a1", false, now); id != "toolu_original" {
		t.Fatalf("original pair %q", id)
	}
	if q.handBack(in, "a1", now) {
		t.Fatal("a stale ambiguous read handed back an already-paired attempt")
	}
	if id := q.pair(in, "a1", true, now); id != "toolu_original" {
		t.Fatalf("the safe original pairing was lost: %q", id)
	}
}
