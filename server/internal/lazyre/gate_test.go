package lazyre

import (
	"regexp"
	"strings"
	"testing"
)

// A gated pattern matches and replaces exactly as regexp does; the gate only skips texts that cannot match. Patterns
// some match of which may start with anything have no gate.
func TestGatedMatchesLikeRegexp(t *testing.T) {
	texts := []string{"", "should I go", "you should i", "shoulder i", "x 你来定 y", "你来", "no decisions for you",
		"no decision", "a `b?` c", "see https://e.x/?q", "ab", "xaab", "ABC", "cat", "bat", "x ```go\nf()\n``` y"}
	for _, c := range []struct {
		src   string
		gated bool
	}{
		{`\bshould i\b|你(?:来定|定一下)|no (?:questions|decisions?) for you`, true},
		{"`[^`\n]*`|https?://\\S+", true},
		{`(c|b)at`, true},
		{`[cb]at`, true},
		{`a*b`, false},
		{`(?i)abc`, false},
		{`.at|ab`, false},
		{"(?s)```.*?```", true},
	} {
		r, want := NewGated(c.src), regexp.MustCompile(c.src)
		if r.Gated() != c.gated {
			t.Errorf("%s: gated = %v", c.src, r.Gated())
		}
		for _, s := range texts {
			if r.MatchString(s) != want.MatchString(s) {
				t.Errorf("%s: MatchString(%q) = %v", c.src, s, r.MatchString(s))
			}
			if got := r.ReplaceAllStringFunc(s, strings.ToUpper); got != want.ReplaceAllStringFunc(s, strings.ToUpper) {
				t.Errorf("%s: ReplaceAllStringFunc(%q) = %q", c.src, s, got)
			}
			if got := r.ReplaceAllString(s, "<$0>"); got != want.ReplaceAllString(s, "<$0>") {
				t.Errorf("%s: ReplaceAllString(%q) = %q", c.src, s, got)
			}
			if got := r.ReplaceAllLiteralString(s, "$0"); got != want.ReplaceAllLiteralString(s, "$0") {
				t.Errorf("%s: ReplaceAllLiteralString(%q) = %q", c.src, s, got)
			}
		}
	}
}
