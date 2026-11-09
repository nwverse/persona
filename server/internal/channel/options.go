package channel

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// Answering a question's options in words, typed or spoken: an option number (2, or a full-width 2), an ordinal
// (a Chinese "the second" / "two", second, 2nd, the Japanese counter form, the last one), or the option's label (case,
// spaces and punctuation aside; "I'd go with SQLite" holds the label SQLite and nothing else, "Postgres" starts the
// label PostgreSQL). A multi-select takes several, split on commas (full-width and enumeration commas too), the
// Chinese "and" and "and". Whatever it is not sure of stays the answer itself: the agent that asked reads that as
// well as anyone, and a free answer drops nothing the user said, a wrong option or a bare label does.

// MatchOption reads text as a choice among q's options: the label (several joined with ", " for a multi-select, in the
// order said); ok false when it is not one, and text is then the answer itself (a free answer). A bare number is the
// option's number while there is such an option, unless another option's label is that number ("3" among 1 / 3 / 5
// replicas). A sentence that turns an option down ("not SQLite"), says more than the option ("SQLite, encrypted") or
// asks about it ("SQLite?") is not that option.
func MatchOption(q proto.Question, text string) (label string, ok bool) {
	if len(q.Options) == 0 {
		return "", false
	}
	picked, ok := matchOptions(q.Options, text, 0)
	if !ok {
		return "", false
	}
	var labels []string
	for _, i := range picked {
		if l := q.Options[i].Label; !slices.Contains(labels, l) {
			labels = append(labels, l)
		}
	}
	if len(labels) > 1 && !q.MultiSelect {
		return "", false
	}
	return strings.Join(labels, ", "), true
}

// optionSplits split a multi-select's answer, from the strongest: commas, then the words joining a list, then slashes,
// then spaces. A piece is split further only when it is not an option as it is (a label may hold a space, a Chinese
// "and" or a slash: "CI/CD").
var optionSplits = []func(string) []string{
	func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune(",，、;；", r) })
	},
	func(s string) []string { return listWords.Split(s, -1) },
	func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '／' })
	},
	strings.Fields,
}

// listWords join a list of options: "A and B" in Chinese, English and Japanese (and "plus", "&").
var listWords = lazyre.New(`(?i)\s+(?:and|plus)\s+|\s*(?:&|＆|和|跟|与|以及|还有|と)\s*`)

// matchOptions is text as one option, else (split from level on) as several, every piece an option.
func matchOptions(opts []proto.QuestionOption, text string, level int) ([]int, bool) {
	if i, ok := matchOne(opts, text); ok {
		return []int{i}, true
	}
	if asks(fold(text)) {
		return nil, false // what is asked about is not taken, nor the pieces before it
	}
	for ; level < len(optionSplits); level++ {
		parts := optionSplits[level](text)
		if len(parts) < 2 {
			continue
		}
		var out []int
		for _, p := range parts {
			if squash(p) == "" {
				continue // a stray full stop
			}
			got, ok := matchOptions(opts, p, level+1)
			if !ok {
				return nil, false
			}
			out = append(out, got...)
		}
		return out, len(out) > 0
	}
	return nil, false
}

// matchOne is text as one of opts, the first way that reads it: its number, its label, an ordinal, a label it holds
// with nothing else said, a label it is the start of.
func matchOne(opts []proto.QuestionOption, text string) (int, bool) {
	t := fold(text)
	if asks(t) {
		// A label that is itself a question, said as it is; nothing else that asks.
		return only(opts, func(l string) bool { return l == t })
	}
	if n, ok := number(text); ok && n <= len(opts) {
		return byNumber(opts, n)
	}
	s := squash(t)
	if s == "" {
		return 0, false
	}
	for _, key := range []func(string) string{loose, squash} {
		if i, ok := only(opts, func(l string) bool { return key(l) == key(t) }); ok {
			return i, true
		}
	}
	if n, bare, ok := ordinal(t, len(opts)); ok {
		switch {
		case n < 1 || n > len(opts):
			return 0, false
		case bare:
			return byNumber(opts, n)
		}
		return n - 1, true
	}
	if negated(t) {
		return 0, false
	}
	var held []int
	for i, o := range opts {
		if _, ok := labelIn(t, o.Label); ok {
			held = append(held, i)
		}
	}
	// One label in another (Java in "use JavaScript"): the longer is the one said.
	held = slices.DeleteFunc(held, func(i int) bool {
		li := squash(opts[i].Label)
		return slices.ContainsFunc(held, func(j int) bool {
			lj := squash(opts[j].Label)
			return len(lj) > len(li) && strings.Contains(lj, li)
		})
	})
	switch {
	case len(held) == 1:
		rest, _ := labelIn(t, opts[held[0]].Label)
		return held[0], fillerOnly(rest)
	case len(held) > 0:
		return 0, false // several
	}
	return startOf(opts, t, s)
}

// asks: t (folded) ends in a question mark. "SQLite?", "the second?" ask about an option rather than take it, as "1?" asks
// what 1 is (numberEnd); speech recognition writes one for a rising voice.
func asks(t string) bool {
	return strings.HasSuffix(strings.TrimRight(t, " 。.!"), "?")
}

// byNumber is option n (from 1), unless another option's label is that number ("3" among 1 / 3 / 5 replicas, "2"
// among 2 / 4 / 8 workers): which one was meant is then for the agent to read, from the answer itself.
func byNumber(opts []proto.QuestionOption, n int) (int, bool) {
	for j, o := range opts {
		if m, ok := leadingNumber(o.Label); ok && m == n && j != n-1 {
			return 0, false
		}
	}
	return n - 1, true
}

// What a label may start with that makes it a number: digits that are a whole number ("2 workers", "1. Fix", not
// "1.5x", "1,000 rows"), or a Chinese numeral with a measure word ("three replicas", "two machines"; not "one-click
// deploy", where the numeral is part of a word).
var (
	leadingDigits = lazyre.New(`^(\d+)(?:$|[^\d.,]|[.,](?:$|\D))`)
	leadingCount  = lazyre.New(`^(两|[一二三四五六七八九十]+)(?:个|位|台|份|次|天|周|年|月|倍|条|项|种|小时|分钟|秒)`)
)

// leadingNumber is the number label starts with; false for none.
func leadingNumber(label string) (int, bool) {
	l := strings.TrimLeftFunc(fold(label), func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) })
	if m := leadingDigits.FindStringSubmatch(l); m != nil {
		n, err := strconv.Atoi(m[1])
		return n, err == nil
	}
	if m := leadingCount.FindStringSubmatch(l); m != nil {
		if m[1] == "两" {
			return 2, true
		}
		return cjkNumber(m[1])
	}
	return 0, false
}

// labelIn is t (folded) without label when t holds it, false when not: an ASCII label as whole words ("go" is not in
// "good"), any other anywhere (Chinese and Japanese have no spaces). A label of one letter or one character is too
// easily in a sentence by chance: it is only ever matched whole.
func labelIn(t, label string) (rest string, ok bool) {
	l := squash(label)
	if !isASCII(l) {
		lr := []rune(l)
		if len(lr) < 2 {
			return "", false
		}
		// The label's characters in a row, t's spaces and punctuation between them aside.
		var rs []rune
		var at, end []int
		for i, r := range t {
			if !unicode.IsSpace(r) && !unicode.IsPunct(r) {
				rs, at, end = append(rs, r), append(at, i), append(end, i+utf8.RuneLen(r))
			}
		}
		for i := 0; i+len(lr) <= len(rs); i++ {
			if slices.Equal(rs[i:i+len(lr)], lr) {
				return t[:at[i]] + " " + t[end[i+len(lr)-1]:], true
			}
		}
		return "", false
	}
	lw := asciiWords(fold(label))
	whole := strings.Join(lw, "")
	if len(whole) < 2 {
		return "", false // A, C#, C++
	}
	spans := wordSpans(t)
	for _, sp := range spans {
		if t[sp[0]:sp[1]] == whole {
			return t[:sp[0]] + " " + t[sp[1]:], true // "nodejs" says Node.js
		}
	}
	for i := 0; i+len(lw) <= len(spans); i++ {
		if slices.EqualFunc(spans[i:i+len(lw)], lw, func(sp [2]int, w string) bool { return t[sp[0]:sp[1]] == w }) {
			return t[:spans[i][0]] + " " + t[spans[i+len(lw)-1][1]:], true
		}
	}
	return "", false
}

// What a sentence holding a label may say besides it and still be that option, nothing more: "use SQLite" in
// Chinese, "I'd go with sqlite", "SQLite, please" in Japanese. Anything else ("SQLite, encrypted", "delete after a
// backup", "approve but rename it", "MySQL 8.0") is a
// condition or an instruction, and the sentence is the answer itself.
var (
	fillerWords = []string{"i", "i'd", "i'll", "i'm", "let's", "lets", "we", "we'll", "go", "going", "for", "with", "use",
		"using", "do", "pick", "choose", "take", "want", "like", "prefer", "the", "one", "option", "choice", "please", "ok",
		"okay", "just", "then", "yes", "yeah", "sure", "that", "that's", "thanks"}
	fillerPieces = []string{"我", "就", "那", "那个", "那就", "还是", "用", "选", "选择", "要", "来", "来个", "吧", "呢", "啊", "呀",
		"嗯", "哦", "的", "好", "了", "好了", "就行", "就好", "可以", "行", "谢谢",
		"で", "を", "にします", "にする", "にしよう", "お願いします", "でお願いします", "がいい", "でいい", "です", "かな", "ね", "よ",
		"使う", "使います", "選ぶ", "選びます", "ください"}
)

// fillerOnly: s says nothing but filler (fillerWords, fillerPieces), or nothing at all.
func fillerOnly(s string) bool {
	for _, w := range tokens(s) {
		if isASCII(w) {
			if !slices.Contains(fillerWords, w) {
				return false
			}
			continue
		}
		// Chinese and Japanese have no spaces: the run must be filler pieces end to end.
		ends := make([]bool, len(w)+1)
		ends[0] = true
		for i := range w {
			if !ends[i] {
				continue
			}
			for _, p := range fillerPieces {
				if strings.HasPrefix(w[i:], p) {
					ends[i+len(p)] = true
				}
			}
		}
		if !ends[len(w)] {
			return false
		}
	}
	return true
}

// tokens are s's words: runs of ASCII letters and digits (with an apostrophe inside: i'd), and runs of other letters
// (Chinese, Japanese), the two apart where they meet.
func tokens(s string) []string {
	kind := func(r rune) int {
		switch {
		case r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\''):
			return 1
		case r > unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)):
			return 2
		}
		return 0
	}
	var out []string
	start, was := 0, 0
	for i, r := range s + " " {
		k := kind(r)
		if k == was {
			continue
		}
		if w := strings.Trim(s[start:i], "'"); was != 0 && w != "" {
			out = append(out, w)
		}
		start, was = i, k
	}
	return out
}

// Words that only say how many or which ("all" starts "All tests" but means every option): never the start of a
// label on their own.
var (
	functionWords = []string{"the", "a", "an", "all", "any", "both", "none", "either", "neither", "and", "or", "of", "it",
		"its", "every", "everything", "each", "some", "this", "that", "these", "those", "them"}
	quantifiers = []string{"全部", "所有", "全都", "都要", "全选", "都选", "全要", "全部都要", "所有的", "都行", "都可以", "两个都要",
		"すべて", "全て", "ぜんぶ", "全部で", "両方"}
)

// startOf is the one option whose label t is the start of ("Postgres" of "PostgreSQL (recommended)", "the cache"
// of "Use the cache"), in no other label anywhere: for ASCII from a word's start ("all" is not in "small", "prove"
// not in "Approve"), for Chinese and Japanese from the label's. Too short to tell, or only saying how many (all,
// in any of the three languages), it is none.
func startOf(opts []proto.QuestionOption, t, s string) (int, bool) {
	if isASCII(s) {
		tw := asciiWords(t)
		if len(s) < 3 || !slices.ContainsFunc(tw, func(w string) bool { return !slices.Contains(functionWords, w) }) {
			return 0, false
		}
	} else if len([]rune(s)) < 2 || slices.Contains(quantifiers, s) {
		return 0, false
	}
	i, ok := only(opts, func(l string) bool { return strings.Contains(squash(l), s) })
	if !ok {
		return 0, false
	}
	l := fold(opts[i].Label)
	if strings.HasPrefix(squash(l), s) {
		return i, true
	}
	return i, isASCII(s) && wordsStart(asciiWords(l), asciiWords(t))
}

// wordsStart: tw are a run of lw, the last of them only its start ("the cache" of use the cache, "post" of postgresql).
func wordsStart(lw, tw []string) bool {
	n := len(tw)
	if n == 0 {
		return false
	}
	for i := 0; i+n <= len(lw); i++ {
		if slices.Equal(lw[i:i+n-1], tw[:n-1]) && strings.HasPrefix(lw[i+n-1], tw[n-1]) {
			return true
		}
	}
	return false
}

// wordSpans are where t's ASCII words are: runs of ASCII letters and digits (a Chinese sentence with no spaces around
// sqlite still holds the word sqlite).
func wordSpans(t string) [][2]int {
	var spans [][2]int
	start := -1
	for i, r := range t + " " {
		in := r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))
		switch {
		case in && start < 0:
			start = i
		case !in && start >= 0:
			spans, start = append(spans, [2]int{start, i}), -1
		}
	}
	return spans
}

// asciiWords are t's ASCII words (wordSpans).
func asciiWords(t string) []string {
	var words []string
	for _, sp := range wordSpans(t) {
		words = append(words, t[sp[0]:sp[1]])
	}
	return words
}

// only is the one option whose label (folded) is like; false for none or several.
func only(opts []proto.QuestionOption, like func(label string) bool) (int, bool) {
	at := -1
	for i, o := range opts {
		if like(fold(o.Label)) {
			if at >= 0 {
				return 0, false
			}
			at = i
		}
	}
	return at, at >= 0
}

// fold is text lowercased, full-width letters, digits and signs as ASCII (a Chinese or Japanese keyboard's), a curly
// apostrophe straight, spaces made one.
func fold(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r >= '！' && r <= '～':
			r -= '！' - '!'
		case r == '　':
			r = ' '
		case r == '’':
			r = '\''
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// loose is s without the spaces and punctuation around it.
func loose(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) })
}

// squash is s folded, without spaces and punctuation: "Node.js" and "node js" are the same.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return r
	}, fold(s))
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// What turns an option down in a sentence: words (English, by word) and pieces (Chinese, Japanese).
var (
	negationWords  = []string{"no", "not", "don't", "dont", "never", "except", "instead", "without", "rather", "neither", "nor"}
	negationPieces = []string{"不", "别", "没", "除了", "以外", "之外", "じゃな", "ではな", "ない", "やめ"}
)

func negated(t string) bool {
	for _, w := range strings.FieldsFunc(t, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' }) {
		if slices.Contains(negationWords, w) {
			return true
		}
	}
	return slices.ContainsFunc(negationPieces, func(p string) bool { return strings.Contains(t, p) })
}

// Ordinals in the three languages, with what may come around one ("I'll pick the second one" in Chinese, "the
// second one", "number 2, please" in Japanese). A Chinese count ("1 of", "one of") is not a place: a measure word
// needs the ordinal prefix or "option" before the numeral. The "number" suffix names a place itself, so a bare
// "No. 2" is one too.
var (
	ordinalZh = lazyre.New(`^(?:我|就)?(?:选|选择|要|用)?(?:(?:第|选项|序号)(\d+|[一二三四五六七八九十]+)(?:个|项|条|种|号|款|位)?(?:选项)?|(\d+|[一二三四五六七八九十]+))(?:号)?(?:吧|呢|啊|呀|的|就行|就好|好了)?$`)
	ordinalJa = lazyre.New(`^(選択肢)?(\d+|[一二三四五六七八九十]+)(番目|番|つ目|個目)?(?:の(?:選択肢|もの|やつ))?(?:で|にします|でお願いします)?$`)
	ordinalEn = lazyre.New(`^(?:(?:i(?:'ll| will)? )?(?:pick|choose|take|go with|want) )?(?:the )?(?:(option|number|choice|#) ?)?([a-z]+|\d+(?:st|nd|rd|th)?)(?: (?:one|option|choice|answer))?(?: please)?$`)
	lastOne   = lazyre.New(`^(?:(?:the )?last(?: one| option| choice)?|最后(?:一个|一项|一种|那个)?吧?|最後(?:の(?:選択肢|もの|やつ))?で?)$`)
	enOrdinal = map[string]int{"first": 1, "second": 2, "third": 3, "fourth": 4, "fifth": 5, "sixth": 6, "seventh": 7,
		"eighth": 8, "ninth": 9, "tenth": 10}
	enCardinal = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8,
		"nine": 9, "ten": 10}
)

// ordinal reads t (folded) as an option's place among count, from 1; false when it is not one. bare: only a number
// (a Chinese or Japanese "two", two, "pick 2"), which may as well mean a label that is that number; not a Chinese or
// Japanese ordinal, second, option 2. Digits
// alone are not an ordinal: number reads those.
func ordinal(t string, count int) (n int, bare, ok bool) {
	t = strings.TrimFunc(t, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) && r != '#' })
	cjk := strings.ReplaceAll(t, " ", "")
	if lastOne.MatchString(t) || lastOne.MatchString(cjk) {
		return count, false, true
	}
	if m := ordinalZh.FindStringSubmatch(cjk); m != nil {
		if m[2] == "" {
			n, ok = cjkNumber(m[1])
			return n, false, ok
		}
		if m[0] == m[2] && digits(m[2]) {
			return 0, false, false
		}
		n, ok = cjkNumber(m[2])
		return n, true, ok
	}
	if m := ordinalJa.FindStringSubmatch(cjk); m != nil {
		n, ok = cjkNumber(m[2])
		return n, m[1] == "" && m[3] == "", ok
	}
	m := ordinalEn.FindStringSubmatch(t)
	if m == nil {
		return 0, false, false
	}
	w, marked := m[2], m[1] != ""
	if n, ok := enOrdinal[w]; ok {
		return n, false, true
	}
	if n, ok := enCardinal[w]; ok {
		return n, !marked, true
	}
	if d := strings.TrimRight(w, "stndrh"); d != w && digits(d) { // 2nd
		n, ok = cjkNumber(d)
		return n, false, ok
	}
	if digits(w) && w != t { // option 2, pick 2
		n, ok = cjkNumber(w)
		return n, !marked, ok
	}
	return 0, false, false
}

func digits(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

// cjkNumber reads digits (not with a leading zero), or a Chinese numeral up to 99 (2, 10, 12, 20, 23).
func cjkNumber(s string) (int, bool) {
	if digits(s) {
		if s[0] == '0' {
			return 0, false
		}
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	digit := func(r rune) int { return slices.Index([]rune("一二三四五六七八九"), r) + 1 } // 0: not a Chinese digit 1-9
	r := []rune(s)
	switch {
	case len(r) == 1 && r[0] == '十':
		return 10, true
	case len(r) == 1 && digit(r[0]) > 0:
		return digit(r[0]), true
	case len(r) == 2 && r[0] == '十' && digit(r[1]) > 0:
		return 10 + digit(r[1]), true
	case len(r) == 2 && r[1] == '十' && digit(r[0]) > 0:
		return digit(r[0]) * 10, true
	case len(r) == 3 && r[1] == '十' && digit(r[0]) > 0 && digit(r[2]) > 0:
		return digit(r[0])*10 + digit(r[2]), true
	}
	return 0, false
}
