package node

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

// The background supervisor's rounds (#7315, docs/assistant/unified-entry.md "Each round starts clean"). The manager
// marks a supervision message to the background supervisor as a round (assistant.send supervise_round). The node then
// stops the conversation's idle process and resumes it at the end of its first turn, `claude -p --resume <id>
// --resume-session-at <uuid>`, so the model reads the start brief and this round, not every round before it. The
// conversation id, its file and its row stay the same: Claude Code appends the round to the same file as a branch from
// that line, which the transcript reader keeps in view (transcript/branch.go), and a later plain --resume (the owner
// writing there) continues the latest round. The round's process gets only the tools supervision uses: Persona's own
// MCP server, told PERSONA_TOOL_SET=supervise (the manager narrows the list it gives), and the built-ins
// roundBuiltinTools.
//
// --resume-session-at is a print-mode flag Claude Code does not document. It is used only where it was measured: Claude
// Code on the CLI (not Codex, the Agent SDK runtime, another runtime or a guest), roundMinClaude or later, an idle
// process with no background tasks, and an anchor found on the transcript's latest chain (roundAnchor). Anything else
// sends as before and says why in the answer (context "kept"); a round whose process does not start is sent once more
// as before. A round's process has started when it prints its init (readAssistant): one that refuses the anchor prints
// only a result with no turn and exits 1.

// The answer's context: the round started from the first turn (fresh), or went in as before (kept), and why.
const (
	roundFresh          = "fresh"
	roundKept           = "kept"
	roundWhyBusy        = "busy"         // the process answers or runs background tasks: the message joins it
	roundWhyUnsupported = "unsupported"  // not Claude Code on the CLI, or one older than roundMinClaude
	roundWhyNoAnchor    = "no_anchor"    // no first turn on the latest chain (a compaction cut it off, or none ended)
	roundWhyStartFailed = "start_failed" // the round's process did not start: resumed as before
)

// roundMinClaude is the first Claude Code --resume-session-at was measured on (2.1.322); older ones keep the context.
var roundMinClaude = [3]int{2, 1, 322}

// roundBuiltinTools are a round's built-in tools: what supervision uses (reading evidence, gh in Bash, a skill), none of
// the others the assistant gets (assistantBuiltinTools), and ToolSearch, which --tools filters too: with nothing
// deferred Claude Code lists no ToolSearch (measured on 2.1.322, where leaving it out added a DeferredToolPlaceholder
// tool instead), and a Persona tool the manager defers can still be loaded (#5839).
const roundBuiltinTools = "Bash,Read,Write,Skill,ToolSearch"

// roundToolSet names the round's tool set to the manager (PERSONA_TOOL_SET, mcp.tools tool_set).
const roundToolSet = "supervise"

// spawnRound is how a process starts for a round: resumed at anchor, with tool set toolSet. The zero value is an
// ordinary start.
type spawnRound struct{ anchor, toolSet string }

// roundResult is what became of a round asked for by one assistant.send, for its answer.
type roundResult struct {
	mu      sync.Mutex
	context string // roundFresh or roundKept
	why     string
	bytes   int64 // the transcript's size when the round was decided (the manager replaces an outgrown one)
	decided bool  // false while nothing was decided (the send did not run)
}

type roundKey struct{}

// withSuperviseRound marks ctx as an assistant.send of a round, with what becomes of it.
func withSuperviseRound(ctx context.Context) (context.Context, *roundResult) {
	r := &roundResult{}
	return context.WithValue(ctx, roundKey{}, r), r
}

// superviseRoundFrom is the round ctx's send was asked for, nil for any other send.
func superviseRoundFrom(ctx context.Context) *roundResult {
	r, _ := ctx.Value(roundKey{}).(*roundResult)
	return r
}

func (r *roundResult) set(context, why string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.context, r.why, r.decided = context, why, true
	r.mu.Unlock()
}

func (r *roundResult) sized(n int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.bytes = n
	r.mu.Unlock()
}

// answer puts the round's outcome on the send's answer: context, why when it was kept, the transcript's size. Nothing
// when no round was asked for or the send did not run (the same once_id asked again).
func (r *roundResult) answer(out map[string]any) map[string]any {
	if r == nil {
		return out
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.decided {
		return out
	}
	out["context"] = r.context
	if r.why != "" {
		out["why"] = r.why
	}
	if r.bytes > 0 {
		out["transcript_bytes"] = r.bytes
	}
	return out
}

// superviseRound decides how the round r for conversation id goes in: from its first turn (a spawnRound with the anchor,
// after which the caller stops p, if any, and starts anew) or as before (the zero spawnRound, r says why). p is the
// conversation's process (nil for none), agent the launcher's kind, guest whether it is a guest conversation and
// execution what it runs on.
func (n *Node) superviseRound(ctx context.Context, r *roundResult, id string, p *assistantProc, agent string, guest bool, execution modelExecution) spawnRound {
	kept := func(why string) spawnRound {
		r.set(roundKept, why)
		n.logf("assistant %s: supervision round goes in as before (%s)", short(id), why)
		return spawnRound{}
	}
	if id == "" || agent != "claude" || guest || execution.Runtime != "" {
		return kept(roundWhyUnsupported)
	}
	version := ""
	if p != nil && p.alive() {
		p.mu.Lock()
		busy := p.busy || len(p.tasks) > 0 || p.compacting || p.manual
		other := p.agent != "claude" || p.runtime != ""
		version = p.claudeVersion
		p.mu.Unlock()
		if other {
			return kept(roundWhyUnsupported)
		}
		if busy {
			return kept(roundWhyBusy)
		}
	}
	path := n.transcriptPath(id)
	if path == "" {
		return kept(roundWhyNoAnchor)
	}
	tr, err := roundAnchor(path)
	if err != nil {
		n.logf("assistant %s: read its transcript for the round: %v", short(id), err)
		return kept(roundWhyNoAnchor)
	}
	r.sized(tr.size)
	if version = cmp.Or(version, tr.version); version == "" {
		version = n.claudeV.get(ctx)
	}
	if !versionAtLeast(version, roundMinClaude) {
		return kept(roundWhyUnsupported)
	}
	if tr.anchor == "" {
		return kept(roundWhyNoAnchor)
	}
	r.set(roundFresh, "")
	return spawnRound{anchor: tr.anchor, toolSet: roundToolSet}
}

// roundTranscript is what roundAnchor read in a transcript: the anchor ("" for none), the Claude Code version that wrote
// its last line, and its size.
type roundTranscript struct {
	anchor, version string
	size            int64
}

// roundLine is what roundAnchor reads of a line.
type roundLine struct {
	Type      string  `json:"type"`
	UUID      string  `json:"uuid"`
	Parent    *string `json:"parentUuid"`
	Sidechain bool    `json:"isSidechain"`
	Meta      bool    `json:"isMeta"`
	Summary   bool    `json:"isCompactSummary"`
	Version   string  `json:"version"`
	Message   *struct {
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

// prompt says the line is a message someone wrote to the conversation: a user line that is no tool result, meta line
// or compaction summary.
func (l roundLine) prompt() bool {
	if l.Type != "user" || l.Sidechain || l.Meta || l.Summary || l.Message == nil {
		return false
	}
	c := bytes.TrimSpace(l.Message.Content)
	if len(c) > 0 && c[0] == '"' {
		return true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(c, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return false
		}
	}
	return len(blocks) > 0
}

// roundAnchor finds where a round resumes in the transcript at path: the last assistant line that ended a turn
// (stop_reason end_turn) before the second message, so the round reads the start message and its answer, not the start
// message merged into its own prompt. It counts only when it lies on the chain Claude Code resumes, walked back from
// the last user or assistant line: a compaction starts a new chain (its first turn is no longer an ancestor, and
// Claude Code refuses the anchor), and so does a file whose chain is broken. Sidechain lines (subagents) are not the
// conversation's.
func roundAnchor(path string) (roundTranscript, error) {
	f, err := os.Open(path)
	if err != nil {
		return roundTranscript{}, err
	}
	defer f.Close()
	var out roundTranscript
	if st, err := f.Stat(); err == nil {
		out.size = st.Size()
	}
	up := map[string]string{} // line → its parent ("" for a chain's start)
	messages := 0
	candidate, leaf := "", ""
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var l roundLine
			if json.Unmarshal(line, &l) == nil {
				if l.Version != "" {
					out.version = l.Version
				}
				if l.UUID != "" && !l.Sidechain {
					if _, seen := up[l.UUID]; !seen {
						parent := ""
						if l.Parent != nil {
							parent = *l.Parent
						}
						up[l.UUID] = parent
					}
					if l.prompt() {
						messages++
					}
					if l.Type == "user" || l.Type == "assistant" {
						leaf = l.UUID
					}
					if messages == 1 && l.Type == "assistant" && l.Message != nil && l.Message.StopReason == "end_turn" {
						candidate = l.UUID
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, err
		}
	}
	if candidate == "" || leaf == "" {
		return out, nil
	}
	seen := map[string]bool{}
	for u := leaf; u != "" && !seen[u]; u = up[u] {
		if u == candidate {
			out.anchor = candidate
			break
		}
		seen[u] = true
	}
	return out, nil
}
