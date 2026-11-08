package node

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
)

// Claude Code's status line while it works (docs/protocol/protocol.md "Live status"): a spinner, the verb with an ellipsis and, in
// parentheses, the parts it has right now separated by "·":
//
//	✻ Frosting… (16m 15s · ↓ 22.8k tokens · thinking with xhigh effort)
//
// Only the screen has it (the transcript does not), so the node reads it off the pane while a client watches.

var (
	// statusLineRE: a spinner at column 0, the verb, "…" (or "..."), and the rest. Claude's own output starts with
	// "⏺", tool results and tips are indented, and what the user wrote starts with "❯" or ">": none of those match.
	statusLineRE = lazyre.New(`^(\S) +([^\s….(][^….(]*?)(?:…|\.\.\.)\s*(.*)$`)
	durationRE   = lazyre.New(`^(?:(\d+)h)?\s*(?:(\d+)m)?\s*(?:(\d+)s)?$`)
	tokensRE     = lazyre.New(`^([↓↑])\s*([\d.,]+)\s*([kKmM]?)\s*tokens?$`)
)

// notSpinner are the column-0 marks Claude draws that are not a spinner: its replies, the prompt, quotes, tool output.
const notSpinner = "⏺❯>⎿│|─━╭╰"

// parseClaudeStatus finds the status line above Claude Code's input box and reads it. Rows are taken from the box's
// top rule upwards: blank and indented rows (the tip, the todo list, a right-aligned "/goal" pill) are skipped, and
// the first row that starts at column 0 is the status line or there is none — so a status line quoted further up in
// the conversation is never taken for the live one.
func parseClaudeStatus(text string) (proto.SessionLive, bool) {
	lines := screen.AboveInputBox(text)
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimRight(lines[i], " \t\u00a0")
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		return parseStatusLine(l)
	}
	return proto.SessionLive{}, false
}

func parseStatusLine(line string) (proto.SessionLive, bool) {
	m := statusLineRE.FindStringSubmatch(line)
	if m == nil {
		return proto.SessionLive{}, false
	}
	spin, _ := utf8.DecodeRuneInString(m[1])
	if unicode.IsLetter(spin) || unicode.IsDigit(spin) || strings.ContainsRune(notSpinner, spin) {
		return proto.SessionLive{}, false
	}
	live := proto.SessionLive{Verb: strings.TrimSpace(m[2])}
	rest := strings.TrimSpace(m[3])
	if rest == "" {
		return live, true
	}
	inside, ok := strings.CutPrefix(rest, "(")
	closing := strings.IndexByte(inside, ')')
	if !ok || closing < 0 {
		return proto.SessionLive{}, false // text after the verb that is not its parenthesis: not a status line
	}
	for _, part := range strings.Split(inside[:closing], "·") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "thinking") {
			live.Thinking = true
		} else if d, ok := strings.CutPrefix(part, "thought for "); ok {
			live.ThoughtS, _ = parseDuration(d)
		} else if t := tokensRE.FindStringSubmatch(part); t != nil {
			if t[1] == "↓" { // ↑ is the input sent so far, not what it wrote
				live.TokensDown = parseCount(t[2], t[3])
			}
		} else if s, ok := parseDuration(part); ok {
			live.ElapsedS = s
		} // anything else ("esc to interrupt", hints) is not ours
	}
	return live, true
}

// parseDuration reads "2s", "1m 5s", "1h 2m 3s" (and "2m", "1h") as seconds.
func parseDuration(s string) (int, bool) {
	m := durationRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil || m[1]+m[2]+m[3] == "" {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	sec, _ := strconv.Atoi(m[3])
	return h*3600 + mi*60 + sec, true
}

// parseCount reads "22.8" "k" as 22800 ("1,234" as 1234).
func parseCount(num, unit string) int {
	f, err := strconv.ParseFloat(strings.ReplaceAll(num, ",", ""), 64)
	if err != nil {
		return 0
	}
	switch unit {
	case "k", "K":
		f *= 1e3
	case "m", "M":
		f *= 1e6
	}
	return int(math.Round(f))
}

// liveEvery is how often a watched, working Claude's status line is read; liveReadTimeout bounds one read.
var (
	liveEvery       = time.Second
	liveReadTimeout = 3 * time.Second
)

// liveLines is how much of the visible screen is read: the status line sits right above the input box at the bottom.
const liveLines = 60

// watchLive pushes a watched Claude's status line as session.live until ctx ends: about once a second while the agent
// works, only when it changed (At aside), and live: null once the turn stops, the line goes or the watch ends (when
// something was sent). Its own goroutine, so a slow screen read never holds up the transcript (watchLoop).
func (n *Node) watchLive(ctx context.Context, id string) {
	t := time.NewTicker(liveEvery)
	defer t.Stop()
	var last *proto.SessionLive
	push := func(l *proto.SessionLive) {
		last = l
		if n.peer != nil {
			_ = n.peer.Notify("session.live", map[string]any{"session_id": id, "live": l})
		}
	}
	defer func() {
		if last != nil {
			push(nil)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		l, ok := n.readLive(ctx, id)
		if !ok || sameLive(l, last) {
			continue
		}
		push(l)
	}
}

// liveHosts are the hosts whose screens readLive reads: each reads a pane's text (herdr-core and Orca as plain text,
// which parseClaudeStatus takes as it is), so a Claude in the Herdr app or Orca shows its line too (#3904). botmux has
// no screen of its own.
var liveHosts = map[string]bool{host.Herdr: true, host.HerdrCore: true, host.Orca: true}

// readLive is the session's status line now: nil when it is not a working Claude on a screen it can read or shows no line; ok is
// false when the screen could not be read (keep what was sent; a failing read is not logged, it would be every second).
func (n *Node) readLive(ctx context.Context, id string) (*proto.SessionLive, bool) {
	a, found := n.agent(id)
	if !found || a.Agent != "claude" || a.AgentStatus != proto.StatusWorking || a.PaneID == "" ||
		!liveHosts[n.hosts.Ref(a.PaneID).Kind] || n.isAssistantSession(id) {
		return nil, true
	}
	rctx, cancel := context.WithTimeout(ctx, liveReadTimeout)
	defer cancel()
	text, err := n.readPane(rctx, a.PaneID, "visible", liveLines)
	if err != nil {
		return nil, false
	}
	l, ok := parseClaudeStatus(text)
	if !ok {
		return nil, true
	}
	l.At = time.Now().UTC().Format(time.RFC3339)
	return &l, true
}

// sameLive compares two status lines without when they were read.
func sameLive(a, b *proto.SessionLive) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := *a, *b
	x.At, y.At = "", ""
	return x == y
}
