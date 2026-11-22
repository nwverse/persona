package feishubot

import (
	"strings"
	"testing"
)

func TestCardMarkdownKeepsFenceLookalikesVerbatim(t *testing.T) {
	for _, tc := range []struct{ name, open, lookalike, close string }{
		{"short backticks", "````html", "```", "````"},
		{"trailing text", "````html", "```` still code", "````"},
		{"indented closer", "```html", "    ```", "```"},
		{"short tildes", "~~~~html", "~~~", "~~~~"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := tc.open + "\nfirst line\n" + tc.lookalike + "\n\n<sample>literal code</sample>\n" + tc.close
			got := CardMarkdown(code + "\n\nOutside <sample>.")
			want := code + "\n\nOutside &#60;sample>."
			if got != want {
				t.Fatalf("code changed or prose stopped escaping:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestCardMarkdownIndentedFenceTextIsNotAnOpener(t *testing.T) {
	for _, indent := range []string{"    ", "\t"} {
		in := indent + "```example\n\nOutside <sample>."
		if got := CardMarkdown(in); !strings.Contains(got, "Outside &#60;sample>.") {
			t.Fatalf("indented text swallowed prose: %q", got)
		}
	}
}

func TestCardMarkdownFenceContainerCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"blockquote", "> ```html\n> <sample>quoted</sample>\n> ```\n\nOutside <sample>.", "> ```html\n> <sample>quoted</sample>\n> ```\n\nOutside &#60;sample>."},
		{"list", "- top\n    ```html\n    <sample>nested</sample>\n    ```\n\nOutside <sample>.", "- top\n    ```html\n    <sample>nested</sample>\n    ```\n\nOutside &#60;sample>."},
		{"nested list", "- top\n  - child\n      ```html\n      <sample>nested</sample>\n      ```\n\nOutside <sample>.", "- top\n    - child\n      ```html\n      <sample>nested</sample>\n      ```\n\nOutside &#60;sample>."},
		{"list item opener", "- ```html\n  <sample>nested</sample>\n  ```\n\nOutside <sample>.", "- ```html\n  <sample>nested</sample>\n  ```\n\nOutside &#60;sample>."},
		{"nested list item opener", "- top\n  - ```html\n    <sample>nested</sample>\n    ```\n\nOutside <sample>.", "- top\n    - ```html\n    <sample>nested</sample>\n    ```\n\nOutside &#60;sample>."},
		{"inline triple backticks", "```<sample>inline</sample>```\n\nOutside <sample>.", "```<sample>inline</sample>```\n\nOutside &#60;sample>."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CardMarkdown(tc.in); got != tc.want {
				t.Fatalf("CardMarkdown compatibility:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestCardMarkdownLongInlineCodeCompatibility(t *testing.T) {
	for _, marker := range []string{"```", "````"} {
		in := marker + "a `<sample>` b" + marker + " followed by <sample>."
		want := marker + "a `<sample>` b" + marker + " followed by &#60;sample>."
		if got := CardMarkdown(in); got != want {
			t.Fatalf("long code span:\n got %q\nwant %q", got, want)
		}
	}
}

func TestCardMarkdownSingleAndUnmatchedTicks(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"`<sample>` followed by <sample>.", "`<sample>` followed by &#60;sample>."},
		{"`<sample> followed by <sample>.", "`&#60;sample> followed by &#60;sample>."},
		{"Prose ```a `<sample>` b followed by <sample>.", "Prose ```a `&#60;sample>` b followed by &#60;sample>."},
	} {
		if got := CardMarkdown(tc.in); got != tc.want {
			t.Fatalf("single/unmatched ticks:\n got %q\nwant %q", got, tc.want)
		}
	}
}
