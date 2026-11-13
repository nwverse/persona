package voice

import (
	"math"
	"strings"
	"unicode"
)

// Choosing a window's text (docs/assistant/meeting-minutes.md "The built-in adapter"): every session's text of the
// window is compared with the captions (live) or the reference (the final pass) by normalized character edit
// distance; a session further than trDropOver is dropped for the window, and of the rest the medoid wins, the text
// closest to the others'. Nothing is merged across sessions.

// trDropOver: a session whose text is further than this from the captions, the reference or (with neither) every
// other session is not trusted for the window. Healthy sessions measured 0.25-0.3 from the captions, one that began to
// play a meeting participant 0.89.
const trDropOver = 0.5

// trNorm is s as the distance compares it: letters and digits only (spaces, punctuation and symbols dropped), case
// folded, full-width forms as half-width.
func trNorm(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			continue
		}
		out = append(out, unicode.ToLower(r))
	}
	return out
}

// trHasText: s has a letter or a digit.
func trHasText(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

// normDistance is the edit distance between a and b in characters, normalized (trNorm) and divided by the longer's
// length: 0 the same, 1 nothing in common. Two empty texts are the same.
func normDistance(a, b string) float64 {
	x, y := trNorm(a), trNorm(b)
	n := max(len(x), len(y))
	if n == 0 {
		return 0
	}
	return float64(levenshtein(x, y)) / float64(n)
}

func levenshtein(a, b []rune) int {
	if len(a) < len(b) {
		a, b = b, a
	}
	prev, cur := make([]int, len(b)+1), make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// medoid is which of keep (indexes into texts) is closest to the others: the least summed distance. A tie goes to the
// one closer to anchor when there is one (of two, the one closer to the captions or the reference), then the first.
func medoid(texts []string, keep []int, anchor *string) int {
	best, bestSum, bestAnchor := -1, 0.0, 0.0
	for _, i := range keep {
		sum := 0.0
		for _, k := range keep {
			if k != i {
				sum += normDistance(texts[i], texts[k])
			}
		}
		a := 0.0
		if anchor != nil {
			a = normDistance(texts[i], *anchor)
		}
		const eps = 1e-9
		if best < 0 || sum < bestSum-eps || (sum < bestSum+eps && a < bestAnchor-eps) {
			best, bestSum, bestAnchor = i, sum, a
		}
	}
	return best
}

// trAgree is which of keep agree with another: one further than trDropOver from every other one is left out. Of two,
// both stay when they agree and neither does when they do not.
func trAgree(texts []string, keep []int) []int {
	var out []int
	for _, i := range keep {
		for _, k := range keep {
			if k != i && normDistance(texts[i], texts[k]) <= trDropOver {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// pickLive chooses a live window's text among the verbatim sessions' (texts, of the sessions that answered for the
// window). With captions (caption has text): a session further than trDropOver from them is dropped and the medoid of
// the rest wins; with none left, pick is -1 and the captions' text is sent. Without captions: a session that agrees
// with no other is dropped (a session alone is taken: there is nothing to check it against); pick -1 sends nothing.
// dropped says which sessions were not trusted, for the rule that replaces a session dropped in two windows in a row;
// without captions only a choice among three or more counts.
func pickLive(caption string, texts []string) (pick int, dropped []bool) {
	dropped = make([]bool, len(texts))
	all := make([]int, len(texts))
	for i := range texts {
		all[i] = i
	}
	if trHasText(caption) {
		var keep []int
		for i, t := range texts {
			if normDistance(t, caption) > trDropOver {
				dropped[i] = true
			} else {
				keep = append(keep, i)
			}
		}
		if len(keep) == 0 {
			return -1, dropped
		}
		return medoid(texts, keep, &caption), dropped
	}
	switch len(texts) {
	case 0:
		return -1, dropped
	case 1:
		return 0, dropped
	}
	keep := trAgree(texts, all)
	if len(texts) >= 3 {
		for _, i := range all {
			dropped[i] = !containsInt(keep, i)
		}
	}
	if len(keep) == 0 {
		return -1, dropped
	}
	return medoid(texts, keep, nil), dropped
}

// pickReplay chooses a final-pass window's text. With a reference (ref has text): a session that stopped early or is
// further than trDropOver from it is dropped, and the medoid of the rest wins (of two, the one closer to the
// reference); with none left the reference stays (kept). Without one: of the sessions that did not stop early, one
// that agrees with no other is dropped and the medoid of the rest wins; with fewer than two left the window gets no
// text of the pass (kept: the hub keeps what it has).
func pickReplay(ref *string, texts []string, stoppedEarly []bool) (pick int, kept bool) {
	var keep []int
	for i := range texts {
		if !stoppedEarly[i] {
			keep = append(keep, i)
		}
	}
	if ref != nil && trHasText(*ref) {
		var near []int
		for _, i := range keep {
			if normDistance(texts[i], *ref) <= trDropOver {
				near = append(near, i)
			}
		}
		if len(near) == 0 {
			return -1, true
		}
		return medoid(texts, near, ref), false
	}
	keep = trAgree(texts, keep)
	if len(keep) < 2 {
		return -1, true
	}
	return medoid(texts, keep, nil), false
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// trSentence is one sentence sent to the manager, in recording seconds.
type trSentence struct {
	start, end float64
	text       string
}

// splitSentences cuts a reply into its sentences: after a full stop, question or exclamation mark (and the quotes or
// brackets that close with it), a Latin one only before a space. A reply is mostly one sentence; sometimes two said
// close together come as one. The text is kept as written, only trimmed.
func splitSentences(text string) []string {
	rs := []rune(text)
	var out []string
	from := 0
	cut := func(to int) {
		if s := strings.TrimSpace(string(rs[from:to])); s != "" {
			if !trHasText(s) && len(out) > 0 {
				out[len(out)-1] += s // punctuation alone joins the sentence before it
			} else {
				out = append(out, s)
			}
		}
		from = to
	}
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '。', '！', '？', '!', '?', '.':
		default:
			continue
		}
		j := i + 1
		for j < len(rs) && trCloser(rs[j]) {
			j++
		}
		if rs[i] == '.' && j < len(rs) && !unicode.IsSpace(rs[j]) {
			continue // 4.5, e.g.: not the end of a sentence
		}
		cut(j)
		i = j - 1
	}
	cut(len(rs))
	return out
}

// trCloser: a quote or bracket that closes with the sentence before it.
func trCloser(r rune) bool {
	switch r {
	case '"', '\'', '”', '’', ')', '）', '」', '』', '】':
		return true
	}
	return false
}

// trWeight is about how long s takes to say, in units of one Chinese character: a Latin word is a little under two.
func trWeight(s string) float64 {
	w, inWord := 0.0, false
	for _, r := range s {
		switch {
		case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if !inWord {
				w += 0.5
			}
			w += 0.25
			inWord = true
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			w++
			inWord = false
		default:
			inWord = false
		}
	}
	return max(w, 0.5)
}

// spreadSentences gives each sentence of a window its time: the window's speech (spans in recording seconds, in
// order) is shared out in the order of the sentences, by how long each takes to say (trWeight), and a boundary
// between two sentences near a pause in the speech moves to the pause (people pause between sentences). Without speech
// the window [from, to] is shared out. About a second is precise enough for the minutes and for jumping to a line.
func spreadSentences(pieces []string, speech [][2]float64, from, to float64) []trSentence {
	if len(pieces) == 0 {
		return nil
	}
	if len(speech) == 0 {
		speech = [][2]float64{{from, max(to, from)}}
	}
	total := 0.0
	var pauses []float64 // the pauses, in seconds of speech before them
	for i, s := range speech {
		total += s[1] - s[0]
		if i < len(speech)-1 {
			pauses = append(pauses, total)
		}
	}
	weights, sum := make([]float64, len(pieces)), 0.0
	for i, p := range pieces {
		weights[i] = trWeight(p)
		sum += weights[i]
	}
	// bounds[i] is where sentence i starts, in seconds of speech.
	bounds := make([]float64, len(pieces)+1)
	for i := range pieces {
		bounds[i+1] = bounds[i] + total*weights[i]/sum
	}
	used := -1
	for i := 1; i < len(pieces); i++ {
		near := min(trSnapMost, trSnapShare*(bounds[i]-bounds[i-1]), trSnapShare*(bounds[i+1]-bounds[i]))
		best := -1
		for k, p := range pauses {
			if k > used && p > bounds[i-1] && p < bounds[i+1] && math.Abs(p-bounds[i]) <= near &&
				(best < 0 || math.Abs(p-bounds[i]) < math.Abs(pauses[best]-bounds[i])) {
				best = k
			}
		}
		if best >= 0 {
			bounds[i], used = pauses[best], best
		}
	}
	// at is the moment the speech's first done seconds end (start: the next span's beginning at a pause).
	at := func(done float64, start bool) float64 {
		for _, s := range speech {
			d := s[1] - s[0]
			if done < d || (!start && done <= d) {
				return s[0] + done
			}
			done -= d
		}
		return speech[len(speech)-1][1]
	}
	out := make([]trSentence, len(pieces))
	for i, p := range pieces {
		out[i] = trSentence{start: at(bounds[i], true), end: at(bounds[i+1], false), text: p}
	}
	out[len(out)-1].end = speech[len(speech)-1][1]
	return out
}

// A boundary between sentences moves to a pause at most this far, in seconds of speech, and at most this share of
// either sentence's time.
const (
	trSnapMost  = 1.5
	trSnapShare = 0.4
)
