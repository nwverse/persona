package screen

import (
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// A rewind done in Claude Code's terminal (/rewind or Esc Esc, then "Restore conversation") writes nothing to the
// transcript on 2.1.318 and puts the prompt gone back over into the input box (#5396). What it leaves on the screen
// tells it from the screens that look alike, recorded on 2.1.318:
//   - after the rewind the conversation is redrawn without the prompt and its answer: the last ❯ row above the box is
//     the prompt before it (nothing of the agent's when it was the first);
//   - the same words typed again as a new message leave that prompt's own ❯ row above the box;
//   - ↑ recalls the last prompt into the box too, under a rule labeled `─── History 100/100 ───`, and its row stays;
//   - Esc before the reply (#3036) puts the prompt back as well, but it has no answer (the caller's to check).

// historyRuleRE is the rule over the box while ↑ walks the prompt history.
var historyRuleRE = lazyre.New(`^─{2,}\s+History\s+\d+/\d+\s+─`)

// promptRowRE starts a row of the conversation: a prompt (❯), the agent's words (⏺, ● as herdr draws it), a tool's
// output (⎿), a finished turn (✻).
var promptRowRE = lazyre.New(`^(?:❯ |\s*[⏺●⎿✻])`)

// agentRowRE is a row of the agent's words.
var agentRowRE = lazyre.New(`^\s*[⏺●]`)

// TerminalRewound says whether a resting Claude's screen is what a rewind in its terminal to before prompt leaves:
// ansi is the visible screen read with colors (only they tell a draft from Claude's grey suggestion: a plain read is
// never one), recent the plain screen reaching back into the pane's history. before is the prompt before it in the
// conversation, first that there is none. A miss is safe (the turn is left out once Claude writes the next line,
// transcript/branch.go); a wrong guess is undone when Claude goes on from that turn, but costs the clients a reload.
func TerminalRewound(ansi, recent, prompt, before string, first bool) bool {
	want := squash(prompt)
	if want == "" || colorless(ansi) || (!first && (squash(before) == "" || squash(before) == want)) {
		return false
	}
	in := ClaudeInput(ansi)
	if !in.Box || !in.Draft || placeholderRE.MatchString(in.Text) || squash(in.Text) != want {
		return false
	}
	if Choosing(Plain(ansi)) || historyRule(ansi) {
		return false
	}
	rows := AboveInputBox(recent)
	for i, r := range rows {
		rows[i] = strings.ReplaceAll(r, "\u00a0", " ")
	}
	// The last prompt row (a local command's, `❯ /rename x`, is not one of the conversation's prompts) has to be the
	// prompt before; the first prompt gone back to leaves no prompt and no words of the agent's.
	last := -1
	for i, r := range rows {
		r = strings.TrimRight(r, " ")
		switch {
		case agentRowRE.MatchString(r) && first:
			return false // the agent's words above a first prompt: its turn may be scrolled out, not gone
		case !strings.HasPrefix(r, "❯ ") || selectedRE.MatchString(r):
		case strings.HasPrefix(r, "❯ /") && !rowShows(rows, i, before) && !rowShows(rows, i, prompt):
		default:
			last = i
		}
	}
	if first {
		return last < 0
	}
	return last >= 0 && rowShows(rows, last, before)
}

// historyRule says the rule over the input box is the one ↑'s history walk labels.
func historyRule(screen string) bool {
	lines := strings.Split(Plain(screen), "\n")
	for i, rules := len(lines)-1, 0; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if !isRule(t) {
			continue
		}
		if rules++; rules == 2 {
			return historyRuleRE.MatchString(t)
		}
	}
	return false
}

// rowShows says the conversation's ❯ row at i (with the rows it wraps onto, indented) is the prompt, up to spaces.
func rowShows(rows []string, i int, prompt string) bool {
	want := squash(prompt)
	got := squash(strings.TrimPrefix(rows[i], "❯ "))
	for j := i + 1; j < len(rows) && got != want && strings.HasPrefix(want, got); j++ {
		r := strings.TrimRight(rows[j], " ")
		if !strings.HasPrefix(r, "  ") || promptRowRE.MatchString(r) {
			break
		}
		got += squash(r)
	}
	return got != "" && got == want
}
