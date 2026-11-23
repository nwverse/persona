package node

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
)

// An agy pane's conversation is herdr's to report (reportedAgyConversation, agypane.go), and the node never guesses
// one from the directory or the newest conversation. When herdr reports nothing for a pane Persona started itself (no
// hook, a hook that did not run, a herdr that could not be asked), the node still knows what it did there: the
// directory, when it typed agy's command, and the first message it typed. agy writes enough to find that conversation
// exactly (#13463). It is the one, and only, conversation that
//   - an agy terminal opened after the start: its presence/<id>.lock is no older than the start;
//   - began with that message, no earlier than the start: the first USER_INPUT the user typed, from
//     transcript_full.jsonl (the compact transcript cuts long messages), compared without whitespace (shownText);
//   - agy places in the start's directory: cache/last_conversations.json names it for the directory, or its metadata's
//     WorkspaceURIs has it;
//   - no herdr report or other pane's proof claims.
// Anything else (none yet, two, a conversation another pane has) proves nothing. A proof stands only while herdr
// reports nothing for the pane and its agent row is the one the start made; it is dropped once that row leaves the
// pane (pruneAgyProofsLocked), as an agy started there again is another conversation.

// agyProof is the conversation proved for the agy row row of a pane.
type agyProof struct{ row, conversation string }

// agyProofWait bounds how long a start looks for its conversation, agyProofPoll how often; agyProofLines how many
// lines of a transcript the first message is looked for in. Tests shorten the waits.
var agyProofWait, agyProofPoll = 2 * time.Minute, time.Second

const agyProofLines = 64

// proveAgyPane looks, in the background, for the conversation of the agy start that typed its command into pane at
// began, in directory cwd, with first message prompt; row is the pane's agent row. It stops once herdr reports a
// conversation for the pane, the agent leaves it, or agyProofWait is over.
func (n *Node) proveAgyPane(pane, row, cwd, prompt string, began time.Time) {
	want := shownText(prompt)
	if want == "" || row == "" || cwd == "" || host.ParsePane(pane).Kind != host.Herdr {
		return
	}
	parent := n.firstPromptParent
	if parent == nil {
		parent = context.Background()
	}
	n.proving.Add(1) // not firstWait: the look outlasts the first message's check
	go func() {
		defer n.proving.Done()
		ctx, cancel := context.WithTimeout(parent, agyProofWait)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(agyProofPoll):
			}
			n.mu.Lock()
			a, ok := n.agents[row]
			n.mu.Unlock()
			if !ok || a.Agent != agyAgent || a.PaneID != pane || reportedAgyConversation(a) != "" {
				return // the agent left, or herdr's hook names its conversation
			}
			found := agyProofCandidates(n.agyDataRoot(), cwd, want, began)
			if len(found) != 1 {
				continue // none yet, or more than one: never a guess
			}
			if n.recordAgyProof(pane, row, found[0]) {
				log.Printf("agy pane %s: its conversation %s proved from its first message (herdr reported none)", pane, found[0])
				n.notifySessionsSoon()
			}
			return
		}
	}()
}

// recordAgyProof keeps conversation as row's in pane, unless the row left the pane, herdr reports a conversation for
// it, or another pane claims the conversation.
func (n *Node) recordAgyProof(pane, row, conversation string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if a, ok := n.agents[row]; !ok || a.Agent != agyAgent || a.PaneID != pane || reportedAgyConversation(a) != "" {
		return false
	}
	if n.agyClaimedLocked(pane, conversation) {
		return false
	}
	if n.agyProofs == nil {
		n.agyProofs = map[string]agyProof{}
	}
	n.agyProofs[pane] = agyProof{row: row, conversation: conversation}
	return true
}

// agyClaimedLocked: a herdr report or a proof for another pane names conversation.
func (n *Node) agyClaimedLocked(pane, conversation string) bool {
	for _, raw := range n.hostAgents[host.Herdr] {
		if raw.PaneID != pane && reportedAgyConversation(raw) == conversation {
			return true
		}
	}
	for other, p := range n.agyProofs {
		if other != pane && p.conversation == conversation {
			return true
		}
	}
	return false
}

// agyProvenLocked is the conversation proved for row id in pane while herdr reports none for the pane; "" for another
// row, a pane herdr reports a conversation for (its report decides, agyConversationLocked), or a conversation another
// pane claims.
func (n *Node) agyProvenLocked(id, pane string) string {
	p, ok := n.agyProofs[pane]
	if !ok || p.row != id {
		return ""
	}
	a := n.agents[id]
	if a.Agent != agyAgent || a.PaneID != pane || id != otherSession(a) || host.ParsePane(pane).Kind != host.Herdr {
		return ""
	}
	for _, raw := range n.hostAgents[host.Herdr] {
		if raw.PaneID == pane && reportedAgyConversation(raw) != "" {
			return ""
		}
	}
	if n.agyClaimedLocked(pane, p.conversation) {
		return ""
	}
	return p.conversation
}

// pruneAgyProofsLocked forgets the proof of a pane that no longer runs the agy row it was proved for, once herdr's
// agents were heard (heard nil: every host's).
func (n *Node) pruneAgyProofsLocked(heard map[string]bool) {
	if len(n.agyProofs) == 0 || heard != nil && !heard[host.Herdr] {
		return
	}
	for pane, p := range n.agyProofs {
		if a, ok := n.agents[p.row]; !ok || a.Agent != agyAgent || a.PaneID != pane {
			delete(n.agyProofs, pane)
		}
	}
}

// agyProofCandidates are the conversations under agy's data directory dir that the start in cwd which typed its
// command at began, with first message want (shownText), can be (the package comment's rules but the claims).
func agyProofCandidates(dir, cwd, want string, began time.Time) []string {
	floor := began.Truncate(time.Second) // agy writes created_at to the second
	entries, err := os.ReadDir(filepath.Join(dir, "presence"))
	if err != nil {
		return nil
	}
	var where *agyWorkspaces // read once, and only for a conversation that began with the message
	var out []string
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".lock")
		if !ok || !uuidRe.MatchString(id) || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(floor) {
			continue
		}
		text, at, ok := agyFirstInput(dir, id)
		if !ok || at.Before(floor) || shownText(agyUserText(text)) != want {
			continue
		}
		if where == nil {
			where = readAgyWorkspaces(dir)
		}
		if where.places(id, cwd) {
			out = append(out, id)
		}
	}
	return out
}

// agyFirstInput is the text and time of conversation id's first message the user typed (its first USER_INPUT step
// whose source is USER_EXPLICIT or none): from transcript_full.jsonl, else transcript.jsonl when it kept the whole text.
// false while there is none yet, or only a cut one.
func agyFirstInput(dir, id string) (string, time.Time, bool) {
	root, err := agyBrain(dir, id)
	if err != nil {
		return "", time.Time{}, false
	}
	defer root.Close()
	for _, rel := range []string{agyFullTranscriptPath, agyTranscriptPath} {
		f, err := root.Open(rel)
		if err != nil {
			continue
		}
		text, at, found, cut := firstAgyUserInput(f)
		f.Close()
		if found && !cut {
			return text, at, true
		}
	}
	return "", time.Time{}, false
}

// firstAgyUserInput reads the first USER_INPUT the user typed from a transcript's first agyProofLines complete lines;
// cut says the record kept only part of its text.
func firstAgyUserInput(r io.Reader) (text string, at time.Time, found, cut bool) {
	br := bufio.NewReaderSize(r, 64<<10)
	for range agyProofLines {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return "", time.Time{}, false, false // a line still being written: the next look reads it
		}
		var step struct {
			Type            string   `json:"type"`
			Source          string   `json:"source"`
			CreatedAt       string   `json:"created_at"`
			Content         string   `json:"content"`
			TruncatedFields []string `json:"truncated_fields"`
		}
		if json.Unmarshal(bytes.TrimSpace(line), &step) != nil || step.Type != "USER_INPUT" {
			continue
		}
		if step.Source != "" && step.Source != "USER_EXPLICIT" {
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, step.CreatedAt)
		if err != nil {
			return "", time.Time{}, false, false
		}
		return step.Content, created, true, slices.Contains(step.TruncatedFields, "content")
	}
	return "", time.Time{}, false, false
}

// agyWorkspaces is where agy says its conversations are: cache/last_conversations.json (a directory -> the last
// conversation there) and the WorkspaceURIs in cache/conversation_metadata.json.
type agyWorkspaces struct {
	last map[string]string
	uris map[string][]string
}

func readAgyWorkspaces(dir string) *agyWorkspaces {
	w := &agyWorkspaces{}
	if b, err := os.ReadFile(filepath.Join(dir, "cache", "last_conversations.json")); err == nil {
		_ = json.Unmarshal(b, &w.last)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "cache", "conversation_metadata.json")); err == nil {
		var meta struct {
			Conversations map[string]struct {
				Summary *struct {
					WorkspaceURIs []string `json:"WorkspaceURIs"`
				} `json:"summary"`
			} `json:"conversations"`
		}
		if json.Unmarshal(b, &meta) == nil {
			w.uris = make(map[string][]string, len(meta.Conversations))
			for id, c := range meta.Conversations {
				if c.Summary != nil {
					w.uris[id] = c.Summary.WorkspaceURIs
				}
			}
		}
	}
	return w
}

// places: agy says conversation id is in directory cwd (as written, or with its symlinks resolved).
func (w *agyWorkspaces) places(id, cwd string) bool {
	dirs := map[string]bool{filepath.Clean(cwd): true}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		dirs[filepath.Clean(real)] = true
	}
	for d, c := range w.last {
		if c == id && dirs[filepath.Clean(d)] {
			return true
		}
	}
	for _, raw := range w.uris[id] {
		if u, err := url.Parse(raw); err == nil && u.Scheme == "file" && u.Path != "" && dirs[filepath.Clean(filepath.FromSlash(u.Path))] {
			return true
		}
	}
	return false
}
