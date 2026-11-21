package assistant

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// Exercise the finished card, not just its splitter: the visible Markdown and
// the collapsed detail must each retain the response's code block boundaries.
func TestAgentDoneCardFoldsOutsideCode(t *testing.T) {
	code := strings.Repeat("sample code line\n", 20)
	longFence := strings.Repeat("`", 128)
	for _, tc := range []struct {
		name, open, lookalike, close string
	}{
		{"short backticks", "````md", "```", "````"},
		{"backticks with trailing text", "````md", "````not a close", "````"},
		{"longer backticks with trailing text", "````md", "````` still code", "````"},
		{"different fence character", "````md", "~~~~", "````"},
		{"longer closing fence", "````md", "```", "``````"},
		{"closing whitespace", "````md", "```", "  ```` \t"},
		{"four space closer", "```html", "    ```", "```"},
		{"tab closer", "```html", "\t```", "```"},
		{"ordinary backticks", "```go", "", "```"},
		{"one space fence", " ```go", "", " ```"},
		{"two space fence", "  ```go", "", "  ```"},
		{"three space fence", "   ```go", "", "   ```"},
		{"ordinary tildes", "~~~go", "", "~~~"},
		{"short tildes", "~~~~md", "~~~", "~~~~"},
		{"tildes with trailing text", "~~~~md", "~~~~ still code", "~~~~"},
		{"tildes with backticks", "~~~~md", "````", "~~~~"},
		{"longer closing tildes", "~~~~md", "~~~", "~~~~~~"},
		{"long fence", longFence + "md", longFence[:127], longFence},
		{"unclosed fence", "````md", "```", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lead := tc.open + "\n" + code
			if tc.lookalike != "" {
				lead += tc.lookalike + "\n"
			}
			// The preview limit has passed. This blank line is still code.
			lead += "\n<sample>Still inside the code block.</sample>\n" + tc.close
			tail := "Further result details."
			body := lead + "\n\n" + tail
			if tc.close == "" {
				lead, tail = body, "" // no safe paragraph boundary
			} else {
				lead = strings.TrimSpace(lead)
			}
			assertAgentDoneFold(t, body, lead, tail)
		})
	}

	t.Run("paragraphs", func(t *testing.T) {
		lead := strings.Repeat("Result paragraph. ", 20)
		tail := "Further result details.\n\nOne more paragraph."
		assertAgentDoneFold(t, lead+"\n\n"+tail, strings.TrimSpace(lead), tail)
	})
	t.Run("short answer", func(t *testing.T) {
		body := "A short result.\n\nFurther details."
		assertAgentDoneFold(t, body, body, "")
	})
	for _, indent := range []string{"    ", "\t"} {
		t.Run("indented text is not an opener "+indent, func(t *testing.T) {
			lead := strings.Repeat("Result paragraph. ", 20) + "\n" + indent + "```example"
			assertAgentDoneFold(t, lead+"\n\nFurther details.", strings.TrimSpace(lead), "Further details.")
		})
	}
	t.Run("blockquote code", func(t *testing.T) {
		lead := "> ```html\n" + strings.Repeat("> sample line\n", 24) + "> <sample>literal</sample>\n> ```"
		assertAgentDoneFold(t, lead+"\n\nFurther details.", lead, "Further details.")
	})
	t.Run("list code", func(t *testing.T) {
		lead := "- top\n    ```html\n" + strings.Repeat("    sample line\n", 22) + "    <sample>literal</sample>\n    ```"
		assertAgentDoneFold(t, lead+"\n\nFurther details.", lead, "Further details.")
	})
	t.Run("inline triple backticks", func(t *testing.T) {
		lead := "```" + strings.Repeat("word", 80) + "```"
		assertAgentDoneFold(t, lead+"\n\nOutside <sample>.", lead, "Outside &#60;sample>.")
	})
	t.Run("shorter ticks inside inline code", func(t *testing.T) {
		lead := "```" + strings.Repeat("word ", 65) + "`<sample>` body```"
		assertAgentDoneFold(t, lead+"\n\nOutside <sample>.", lead, "Outside &#60;sample>.")
	})
	t.Run("backtick in info is not a fence", func(t *testing.T) {
		lead := "```go`invalid\n" + strings.Repeat("Text ", 70)
		assertAgentDoneFold(t, lead+"\n\nOutside <sample>.", strings.TrimSpace(lead), "Outside &#60;sample>.")
	})
}

func assertAgentDoneFold(t *testing.T, body, wantLead, wantRest string) {
	t.Helper()
	if len([]rune(body)) > agentDoneClip {
		t.Fatal("fixture exceeds the separate answer clipping budget")
	}
	card, fallback := agentDoneCard("en", "Finished sample", body, "Sample computer", "", proto.Session{}, nil)
	preview := card.Body.Elements[0].(map[string]any)["content"].(string)
	var rest string
	panels := 0
	for _, raw := range card.Body.Elements {
		e, ok := raw.(map[string]any)
		if !ok || e["tag"] != "collapsible_panel" {
			continue
		}
		panels++
		if e["expanded"] != false {
			t.Fatal("details must stay collapsed by default")
		}
		rest = e["elements"].([]any)[0].(map[string]any)["content"].(string)
	}
	if preview != wantLead || rest != wantRest {
		t.Fatalf("fold split a code block or changed the answer:\npreview %q\nwant    %q\ndetails %q\nwant    %q", preview, wantLead, rest, wantRest)
	}
	wantPanels := 0
	if wantRest != "" {
		wantPanels = 1
	}
	if panels != wantPanels {
		t.Fatalf("got %d detail panels, want %d", panels, wantPanels)
	}
	if !strings.Contains(fallback, fallbackText.Replace(body)) {
		t.Fatalf("plain-text fallback lost part of the response: %q", fallback)
	}
}
