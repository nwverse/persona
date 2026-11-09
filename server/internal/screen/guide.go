package screen

import (
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// A guide is one of Claude Code's own setup dialogs that comes up by itself, not from anything the agent or the user
// asked: 2.1.318's "Teach auto mode about your environment?" opened right as a message was submitted, took the typed
// text and the Enter, and the message was lost (#5504). Esc declines a guide with nothing chosen (Enter on it would
// start what it offers, such as a scan of the shell history). Only dialogs that are safe to decline with Esc belong
// here; permissions, logins, trust, quota and paid options, and an agent's own questions are the user's to answer.
var guides = []struct {
	name  string
	title *lazyre.Regexp
}{
	{"teach-auto-mode", lazyre.New(`^Teach auto mode about your environment\?$`)},
}

// guideFooterRE is a guide's key hint: Esc cancels it (`←/→ to change · Enter to continue · Esc to cancel`).
var guideFooterRE = lazyre.New(`(?i)\besc to cancel\b`)

// GuideKeys decline a guide.
var GuideKeys = []string{"esc"}

// Guide is the name of the guide at the bottom of a plain screen, "" when none: the dialog's first line (DialogHead)
// is a guide's title and its last line offers Esc to cancel. A conversation that quotes the title has the input box
// under it, not the dialog.
func Guide(plain string) string {
	head := DialogHead(plain)
	if head == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(plain, "\r", ""), "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			last = t
			break
		}
	}
	if !guideFooterRE.MatchString(last) {
		return ""
	}
	for _, g := range guides {
		if g.title.MatchString(head) {
			return g.name
		}
	}
	return ""
}
