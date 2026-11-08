package transcript

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// What an agent's last reply asks the user (docs/protocol/agent-states.md "Awaiting your reply", #5096): its closing question ("要不要推到
// beta？", "A 还是 B？"), or a closing sentence that hands a decision over ("你定一下用哪个方案", "Let me know which one").
// Only words: no model is asked, so it costs nothing and runs on every list. A reply that only reports ("修好了，测试都过了")
// asks nothing. The node reads the whole reply (the list's last_text is only its first 200 runes, and the question
// is usually at the end).

// askMost is how many runes of the question are kept.
const askMost = 120

// askTail is how many of the reply's last sentences may hold the question: a question followed by a line on how to
// answer ("回复 1 或 2 即可") still counts; one asked early and answered by the agent itself further down does not.
const askTail = 3

// askClosers may follow a sentence's end: closing brackets and quotes.
const askClosers = `)）」』"'”’*_`

var (
	askFence    = lazyre.New("(?s)```.*?```")
	askMarkdown = strings.NewReplacer("**", "", "__", "", "`", "", "~~", "")
	askListMark = lazyre.New(`^\s*(?:[-*+>#]+|\d+[.)、])\s*`)
	// Sentences that say nothing is asked ("没有需要你拍板的问题").
	askNone = lazyre.New(`没有(?:需要你|要你|什么要你)|不需要你|无需你|nothing (?:for you|needs you)|no (?:questions|decisions?) for you`)
	// Inline code and links are no prose: "the `user?.name` crash", "…/pull/9?w=1" ask nothing (shield).
	askInline = lazyre.New("`[^`\n]*`|https?://\\S+")
	// Sayings that hand a decision to the user, matched on a lower-cased sentence. Only whole sayings: 要我 alone is in
	// "需要我们", 告诉我 in "日志告诉我们", 你想 in "如果你想看细节"; a sentence that ends in 吗 is a question anyway.
	askPhrases = lazyre.New(`要不要|是否(?:需要|要|继续|同意|可以)|你(?:来定|定一下|决定一下|看要不要|看行不行|看怎么样|选一个|选一下)|` +
		`请(?:确认|选择|决定|告诉我|回复|指示)|告诉我(?:你|一下|要|用|选)|回我一下|你回我|跟我说一声|和我说一声|你(?:选定|定了|确认|同意)(?:后|以后|之后)|` +
		`选定后|等你(?:决定|确认|回复|拍板)|选哪|哪个方案|哪种方案|` +
		`\bshould i\b|\bshall i\b|\bdo you want\b|\bwould you like\b|\bwant me to\b|\blet me know\b|\bwhich (?:one|option|approach) (?:do|would|should)\b|` +
		`\bplease (?:confirm|choose|pick|decide)\b|\byour call\b|` +
		`ますか|ですか|でしょうか|しましょうか|教えてください|確認してください`)
	// A Chinese question without its question mark: the sentence ends in 吗 (punctuation aside). Not 么 / 呢: "…为什么。",
	// "CI 还在跑呢。" are statements.
	askParticle = lazyre.New(`吗[。.！!~～]*$`)
)

// AskOf is the question text's last words put to the user, cleaned of Markdown and cut to a line; "" when they ask
// nothing.
func AskOf(text string) string {
	sentences := askSentences(text)
	if len(sentences) == 0 {
		return ""
	}
	tail := sentences[max(len(sentences)-askTail, 0):]
	// The last question mark wins; else the last sentence with a saying that asks.
	for i := len(tail) - 1; i >= 0; i-- {
		if endsAsking(tail[i]) && !askNone.MatchString(strings.ToLower(tail[i])) {
			return clipAsk(tail[i])
		}
	}
	if askNone.MatchString(strings.ToLower(tail[len(tail)-1])) {
		return "" // the close says there is nothing else to decide: a saying earlier is an offer, not a question
	}
	for i := len(tail) - 1; i >= 0; i-- {
		if l := strings.ToLower(tail[i]); askPhrases.MatchString(l) && !askNone.MatchString(l) {
			return clipAsk(tail[i])
		}
	}
	return ""
}

// askSentences splits the reply's prose into sentences, code blocks and blank lines left out, each line on its own (a
// list's items are separate even without full stops).
func askSentences(text string) []string {
	text = askFence.ReplaceAllString(text, "\n")
	text = askInline.ReplaceAllStringFunc(text, shield) // their '?' and '.' end no sentence; their words are kept
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(askMarkdown.Replace(askListMark.ReplaceAllString(line, "")))
		if line == "" || strings.Trim(line, "-=*_|: ") == "" { // rules, table borders
			continue
		}
		start := 0
		for i, r := range line {
			if r == '。' || r == '！' || r == '？' || (r == '!' || r == '?' || r == '.') && englishStop(line, i) {
				end := i + utf8.RuneLen(r)
				for end < len(line) {
					c, n := utf8.DecodeRuneInString(line[end:])
					if !strings.ContainsRune(askClosers, c) {
						break
					}
					end += n
				}
				if s := strings.TrimSpace(line[start:end]); s != "" {
					out = append(out, s)
				}
				start = end
			}
		}
		if s := strings.TrimSpace(line[start:]); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// englishStop: the ASCII '.', '!' or '?' at i ends a sentence: a space or the line's end after it, and not a number's point.
func englishStop(line string, i int) bool {
	return (i+1 == len(line) || line[i+1] == ' ') && i > 0 && !(line[i] == '.' && unicode.IsDigit(rune(line[i-1])))
}

// endsAsking: s ends with a question mark (closing quotes, brackets and emoji after it aside).
func endsAsking(s string) bool {
	s = strings.TrimRightFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(askClosers, r) || r > 0x2600 && !unicode.IsLetter(r) && r != '？'
	})
	return strings.HasSuffix(s, "?") || strings.HasSuffix(s, "？") || askParticle.MatchString(s)
}

// The stand-ins shield puts for the marks that end a sentence, inside inline code and links (private-use runes).
const shieldQ, shieldDot, shieldBang = '\ue000', '\ue001', '\ue002'

// shield hides the sentence-ending marks of inline code or a link; clipAsk puts them back.
func shield(s string) string {
	return strings.NewReplacer("?", string(shieldQ), ".", string(shieldDot), "!", string(shieldBang)).Replace(s)
}

func clipAsk(s string) string {
	s = strings.NewReplacer(string(shieldQ), "?", string(shieldDot), ".", string(shieldBang), "!").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= askMost {
		return s
	}
	r := []rune(s)
	return string(r[:askMost]) + "…"
}
