package lazyre

import (
	"regexp/syntax"
	"slices"
	"strings"
	"unicode/utf8"
)

// A gated pattern (NewGated) answers MatchString by first looking for the literals every match starts with: a large
// unanchored alternation of sayings costs the regexp engine every branch at every position of the text, about half a
// microsecond a byte, while a text holding none of the literals cannot match at all. The literals come from the
// pattern itself, so the gate never disagrees with the pattern.

// gateMost is how many literals a gate looks for at most; a pattern with more is matched without one.
const gateMost = 256

// NewGated is New for a pattern MatchString runs often on texts it seldom matches: MatchString first checks the text
// holds one of the literals every match starts with (zero-width assertions aside). A pattern some match of which may
// start with anything (a class, a repeat, case folding) has no gate and matches as New's.
func NewGated(src string) *Regexp {
	r := &Regexp{src: src, gated: true}
	if inTest {
		r.Regexp()
	}
	return r
}

// Gated reports whether r has a gate: it was made with NewGated and every match of it starts with a literal.
func (r *Regexp) Gated() bool {
	r.Regexp()
	return r.gate != nil
}

// gateLiterals is the gate of src: strings one of which every match starts with, none inside another; nil when
// there is none to look for.
func gateLiterals(src string) []string {
	re, err := syntax.Parse(src, syntax.Perl)
	if err != nil {
		return nil
	}
	set, _ := prefixes(re.Simplify())
	if len(set) == 0 || len(set) > gateMost || slices.Contains(set, "") {
		return nil
	}
	// Shortest first, and one that holds a shorter one dropped: a text holding it holds the shorter one too.
	slices.SortFunc(set, func(a, b string) int { return len(a) - len(b) })
	var out []string
	for _, s := range set {
		if !slices.ContainsFunc(out, func(o string) bool { return strings.Contains(s, o) }) {
			out = append(out, s)
		}
	}
	return out
}

// prefixes is a set of strings one of which every match of re starts with, and whether each is a whole match of re
// (so what follows re in a concatenation may lengthen them). A part it cannot tell about ends the strings there.
func prefixes(re *syntax.Regexp) ([]string, bool) {
	switch re.Op {
	case syntax.OpLiteral:
		if re.Flags&syntax.FoldCase != 0 {
			return []string{""}, false
		}
		return []string{string(re.Rune)}, true
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return []string{""}, true
	case syntax.OpCapture:
		return prefixes(re.Sub[0])
	case syntax.OpCharClass:
		var out []string
		for i := 0; i+1 < len(re.Rune); i += 2 {
			for c := re.Rune[i]; c <= re.Rune[i+1]; c++ {
				if len(out) == 8 {
					return []string{""}, false
				}
				out = append(out, string(c))
			}
		}
		if len(out) == 0 {
			return []string{""}, false
		}
		return out, true
	case syntax.OpAlternate:
		var out []string
		whole := true
		for _, sub := range re.Sub {
			set, w := prefixes(sub)
			out, whole = append(out, set...), whole && w
		}
		return out, whole
	case syntax.OpConcat:
		out := []string{""}
		for _, sub := range re.Sub {
			set, whole := prefixes(sub)
			if len(out)*len(set) > gateMost {
				return out, false
			}
			next := make([]string, 0, len(out)*len(set))
			for _, a := range out {
				for _, b := range set {
					next = append(next, a+b)
				}
			}
			if out = next; !whole {
				return out, false
			}
		}
		return out, true
	}
	return []string{""}, false
}

// gate is what a gated pattern looks for in a text before matching it: one of lits.
type gate struct {
	lits []string
	// ascii and other are lits by their first rune, for a gate with many: one pass over the text, rather than one per
	// literal.
	ascii [utf8.RuneSelf][]string
	other map[rune][]string
}

// fewLits is how many literals a gate looks for one at a time (strings.Contains is fast for each).
const fewLits = 8

// newGate is src's gate; nil when it has none.
func newGate(src string) *gate {
	lits := gateLiterals(src)
	if lits == nil {
		return nil
	}
	g := &gate{lits: lits}
	if len(lits) > fewLits {
		g.other = map[rune][]string{}
		for _, l := range lits {
			if c, _ := utf8.DecodeRuneInString(l); c < utf8.RuneSelf {
				g.ascii[c] = append(g.ascii[c], l)
			} else {
				g.other[c] = append(g.other[c], l)
			}
		}
	}
	return g
}

// open: s holds one of the gate's literals, so the pattern may match it.
func (g *gate) open(s string) bool {
	if g.other == nil {
		for _, l := range g.lits {
			if strings.Contains(s, l) {
				return true
			}
		}
		return false
	}
	for i, c := range s {
		var ls []string
		if c < utf8.RuneSelf {
			ls = g.ascii[c]
		} else {
			ls = g.other[c]
		}
		for _, l := range ls {
			if strings.HasPrefix(s[i:], l) {
				return true
			}
		}
	}
	return false
}
