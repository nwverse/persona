package node

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestEchoWordsIgnoreInterject(t *testing.T) {
	text := interjectNote(false)["text"].(string) + "Inspect the test diagram."
	if got := string(echoWords(text)); got != "Inspectthetestdiagram." {
		t.Fatalf("raw runtime input includes model instructions in echo words: %q", got)
	}
	if got := echoClip(text); got != "Inspect the test diagram." {
		t.Fatalf("saved echo record includes model instructions: %q", got)
	}
	const picked = `<persona-context kind="context" title="Notes">keep this source</persona-context>`
	if !strings.Contains(echoClip(picked), "keep this source") {
		t.Fatal("ordinary picked context was removed from the echo record")
	}
}

// Without the client ID, apps keep the optimistic send beside the transcript
// copy and append it after newer messages. The injected note is not sent text.
func TestEchoInterjectionWithImageAndFollowingMessage(t *testing.T) {
	for _, queued := range []bool{false, true} {
		for _, photo := range []bool{false, true} {
			t.Run(fmt.Sprintf("queued=%v/image=%v", queued, photo), func(t *testing.T) {
				at := time.Now()
				var log echoLog
				images := 0
				if photo {
					images = 1
				}
				log.expect(echoRecord{SessionID: "s", ClientMsgID: "picture-send", Text: "Inspect the test diagram.", Images: images, SentAt: at})
				log.expect(echoRecord{SessionID: "s", ClientMsgID: "followup-send", Text: "Use the blue label.", SentAt: at.Add(time.Second)})
				blocks := []map[string]any{{"type": "text", "text": "<persona-context>snapshot</persona-context>"}, interjectNote(false)}
				if photo {
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo="}})
				}
				blocks = append(blocks, map[string]any{"type": "text", "text": "Inspect the test diagram."})
				entry := map[string]any{"type": "user", "uuid": "picture", "timestamp": at.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": blocks}}
				if queued {
					entry["type"] = "attachment"
					delete(entry, "message")
					entry["attachment"] = map[string]any{"type": "queued_command", "commandMode": "prompt", "origin": map[string]string{"kind": "human"}, "prompt": blocks}
				}
				raw, _ := json.Marshal(entry)
				items := echoItems(t, string(raw), userLine("followup", at.Add(time.Second), "Use the blue label."))
				found := log.match("s", echoInTranscript, items)
				if len(found) != 2 || found[0].Line != "picture" || found[1].Line != "followup" {
					t.Fatalf("interjection and follow-up must each reconcile in send order: %+v", found)
				}
				tagged := log.tag("s", echoInTranscript, items)
				for _, it := range tagged {
					if it.Seq == 1 && (it.Kind == proto.KindText || it.Kind == proto.KindImage) && it.ClientMsgID != "picture-send" {
						t.Errorf("picture item %s has no authoritative send ID", it.ID)
					}
				}
				if _, waiting := log.waitingFrom("s", echoInTranscript); waiting {
					t.Fatal("delivered interjection still waiting for its transcript echo")
				}
			})
		}
	}
}
