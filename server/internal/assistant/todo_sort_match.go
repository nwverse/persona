package assistant

// Matching helpers for the sorting rules (#13026): a project named in a title only as a whole word, and an Agent's
// work folder tried in the hub's canonical form so Windows folders match.

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/kb"
)

// wholeWord: word is in text with no letter or digit right before or after it. A word that starts or ends with a
// character of a script written without spaces (Chinese, Japanese) needs no boundary on that side.
func wholeWord(text, word string) bool {
	if word == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(word)
	last, _ := utf8.DecodeLastRuneInString(word)
	for from := 0; from <= len(text)-len(word); {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return false
		}
		i += from
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after, _ := utf8.DecodeRuneInString(text[i+len(word):])
		if (i == 0 || !wordRune(before) || unspaced(first)) && (i+len(word) == len(text) || !wordRune(after) || unspaced(last)) {
			return true
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		from = i + size
	}
	return false
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// unspaced: r belongs to a script written without spaces between words.
func unspaced(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai)
}

// projectsForWork is kb.ProjectsForWork for a work folder as an Agent reports it: a folder the hub keeps in its
// canonical form (workspaceDirectory: a Windows drive lower-cased, forward slashes) matches as well as one written
// as the computer reports it.
func projectsForWork(e *kb.Engine, machine, dir string) []*kb.Project {
	canonical := workspaceDirectory(dir)
	if canonical == dir {
		return e.ProjectsForWork(machine, dir)
	}
	out := e.ProjectsForDir(machine, canonical)
	for _, p := range e.ProjectsForDir(machine, dir) {
		if !slices.ContainsFunc(out, func(q *kb.Project) bool { return q.Slug == p.Slug }) {
			out = append(out, p)
		}
	}
	if len(out) > 0 {
		return out
	}
	return e.ProjectsForWork(machine, dir)
}
