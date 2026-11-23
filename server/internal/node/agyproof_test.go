package node

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// agyProofConv is one conversation's files as an agy terminal leaves them (agyproof.go): its presence lock and its first
// message at at, the message in transcript_full.jsonl (cut in transcript.jsonl), and, as asked, its directory in
// last_conversations.json and in its metadata's WorkspaceURIs.
type agyProofConv struct {
	id, cwd, text string
	at, lock      time.Time // lock zero: at
	last, uri     bool
	cutOnly       bool // no transcript_full.jsonl: only the compact transcript's cut copy
}

func writeAgyProofConv(t *testing.T, root string, c agyProofConv) {
	t.Helper()
	content := "<USER_REQUEST>\n" + c.text + "\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nlocal time\n</ADDITIONAL_METADATA>"
	created := c.at.UTC().Format(time.RFC3339)
	reply := fmt.Sprintf(`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":%q,"content":"On it."}`+"\n", created)
	compact := fmt.Sprintf(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":%q,"content":%q,"truncated_fields":["content"]}`+"\n", created, content[:24])
	agyTestFile(t, root, c.id, compact+reply)
	if !c.cutOnly {
		full := fmt.Sprintf(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":%q,"content":%q}`+"\n", created, content)
		agyTestWrite(t, filepath.Join(root, "brain", c.id, agyFullTranscriptPath), full+reply)
	}
	lock := filepath.Join(root, "presence", c.id+".lock")
	writeFile(t, lock, "")
	at := c.lock
	if at.IsZero() {
		at = c.at
	}
	if err := os.Chtimes(lock, at, at); err != nil {
		t.Fatal(err)
	}
	if c.last {
		path := filepath.Join(root, "cache", "last_conversations.json")
		last := map[string]string{}
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &last)
		}
		last[c.cwd] = c.id
		b, _ := json.Marshal(last)
		writeFile(t, path, string(b))
	}
	if c.uri {
		path := filepath.Join(root, "cache", "conversation_metadata.json")
		meta := map[string]map[string]any{"conversations": {}}
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &meta)
		}
		meta["conversations"][c.id] = map[string]any{"summary": map[string]any{"ID": c.id,
			"WorkspaceURIs": []string{(&url.URL{Scheme: "file", Path: c.cwd}).String()}}, "is_internal": false}
		b, _ := json.Marshal(meta)
		writeFile(t, path, string(b))
	}
}

const agyProofPrompt = "Fix the flaky test.\n  Report what you changed."

func TestAgyProofCandidates(t *testing.T) {
	began := time.Now().Add(-time.Minute)
	after := began.Add(5 * time.Second)
	want := shownText(agyProofPrompt)
	for _, tc := range []struct {
		name  string
		convs func(cwd, other string) []agyProofConv
		found []string
	}{
		{name: "the one conversation", found: []string{agyTestID}, convs: func(cwd, _ string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: cwd, text: agyProofPrompt, at: after, last: true}}
		}},
		{name: "placed by its metadata", found: []string{agyTestID}, convs: func(cwd, _ string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: cwd, text: agyProofPrompt, at: after, uri: true}}
		}},
		{name: "wrapped differently", found: []string{agyTestID}, convs: func(cwd, _ string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: cwd, text: "Fix the flaky\ntest. Report what\n\tyou changed.", at: after, last: true}}
		}},
		{name: "two that match", found: []string{agyTestID, agyOtherTestID}, convs: func(cwd, _ string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: cwd, text: agyProofPrompt, at: after, last: true},
				{id: agyOtherTestID, cwd: cwd, text: agyProofPrompt, at: after, uri: true}}
		}},
		{name: "begun before the start", convs: func(cwd, _ string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: cwd, text: agyProofPrompt, at: began.Add(-10 * time.Second), lock: after, last: true}}
		}},
		{name: "a lock older than the start", convs: func(cwd, _ string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: cwd, text: agyProofPrompt, at: after, lock: began.Add(-time.Hour), last: true}}
		}},
		{name: "another directory", convs: func(_, other string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: other, text: agyProofPrompt, at: after, last: true, uri: true}}
		}},
		{name: "another first message", convs: func(cwd, _ string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: cwd, text: "Something else.", at: after, last: true}}
		}},
		{name: "no word on its directory", convs: func(cwd, _ string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: cwd, text: agyProofPrompt, at: after}}
		}},
		{name: "only a cut message", convs: func(cwd, _ string) []agyProofConv {
			return []agyProofConv{{id: agyTestID, cwd: cwd, text: agyProofPrompt, at: after, last: true, cutOnly: true}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cwd, other := t.TempDir(), t.TempDir(), t.TempDir()
			for _, c := range tc.convs(cwd, other) {
				writeAgyProofConv(t, root, c)
			}
			got := agyProofCandidates(root, cwd, want, began)
			slices.Sort(got)
			if !slices.Equal(got, tc.found) {
				t.Fatalf("candidates %v, want %v", got, tc.found)
			}
		})
	}
	if got := agyProofCandidates(t.TempDir(), t.TempDir(), want, began); len(got) != 0 {
		t.Fatalf("no agy data: %v", got)
	}
}

func shortAgyProofWaits(t *testing.T, wait time.Duration) {
	t.Helper()
	oldWait, oldPoll := agyProofWait, agyProofPoll
	agyProofWait, agyProofPoll = wait, 20*time.Millisecond
	t.Cleanup(func() { agyProofWait, agyProofPoll = oldWait, oldPoll })
}

// A pane Persona started, which herdr's hook reports nothing for, reads its proved conversation, and the companion's row
// of the same conversation is hidden; herdr's report wins, and the proof goes with the agent.
func TestAgyPaneProofReadsConversation(t *testing.T) {
	n, f, home := herdrNode(t)
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	shortAgyProofWaits(t, 5*time.Second)
	acpRow := "agy-" + agyTestID
	n.runtimes = newRuntimeRegistry()
	runtime := n.runtimes.add(nil)
	runtime.ready, runtime.hello.Runtime = true, agyAgent
	n.runtimes.setSessions(runtime, []proto.Session{{SessionID: acpRow, Agent: agyAgent, Cwd: home, Status: proto.StatusIdle, Model: "gemini-test"}})
	seedAgents(t, n, f, agyTestAgent(agyTestPane, home, ""))
	began := time.Now()
	writeAgyProofConv(t, n.agyDataRoot(), agyProofConv{id: agyTestID, cwd: home, text: agyProofPrompt, at: time.Now(), last: true})

	n.proveAgyPane(agyTestPane, agyTestRow, home, agyProofPrompt, began)
	waitFor(t, "the proved conversation", func() bool { return n.agyConversation(agyTestRow, agyTestPane) == agyTestID })
	rows := listSessions(t, n)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.SessionID)
	}
	if !slices.Contains(ids, agyTestRow) || slices.Contains(ids, acpRow) {
		t.Fatalf("rows %v: want the pane and not the companion's row of its conversation", ids)
	}
	items, _ := agyTestItems(t, n, agyTestRow, nil)
	if len(items) == 0 || items[0].Kind == proto.KindNotice || items[0].Role != "user" {
		t.Fatalf("pane items %+v: want the conversation", items)
	}

	seedAgents(t, n, f, agyTestAgent(agyTestPane, home, agyOtherTestID))
	if got := n.agyConversation(agyTestRow, agyTestPane); got != agyOtherTestID {
		t.Fatalf("with herdr's report: %q, want %q", got, agyOtherTestID)
	}
	seedAgents(t, n, f)
	n.mu.Lock()
	left := len(n.agyProofs)
	n.mu.Unlock()
	if left != 0 {
		t.Fatal("the proof stayed after its agent left the pane")
	}
	seedAgents(t, n, f, agyTestAgent(agyTestPane, home, ""))
	if got := n.agyConversation(agyTestRow, agyTestPane); got != "" {
		t.Fatalf("a new agy in the pane got the old proof: %q", got)
	}
}

// Two conversations that match, or one another pane's herdr report has, prove nothing.
func TestAgyPaneProofNeverGuesses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		write  func(t *testing.T, root, cwd string, at time.Time)
		agents func(cwd string) []any
	}{
		{name: "two that match", write: func(t *testing.T, root, cwd string, at time.Time) {
			writeAgyProofConv(t, root, agyProofConv{id: agyTestID, cwd: cwd, text: agyProofPrompt, at: at, last: true})
			writeAgyProofConv(t, root, agyProofConv{id: agyOtherTestID, cwd: cwd, text: agyProofPrompt, at: at, uri: true})
		}, agents: func(cwd string) []any { return []any{agyTestAgent(agyTestPane, cwd, "")} }},
		{name: "another pane has it", write: func(t *testing.T, root, cwd string, at time.Time) {
			writeAgyProofConv(t, root, agyProofConv{id: agyTestID, cwd: cwd, text: agyProofPrompt, at: at, last: true})
		}, agents: func(cwd string) []any {
			return []any{agyTestAgent(agyTestPane, cwd, ""), agyTestAgent("w1:p2", cwd, agyTestID)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home := herdrNode(t)
			t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
			shortAgyProofWaits(t, 300*time.Millisecond)
			seedAgents(t, n, f, tc.agents(home)...)
			began := time.Now()
			tc.write(t, n.agyDataRoot(), home, time.Now())
			n.proveAgyPane(agyTestPane, agyTestRow, home, agyProofPrompt, began)
			n.proving.Wait() // the look ends at its deadline
			if got := n.agyConversation(agyTestRow, agyTestPane); got != "" {
				t.Fatalf("proved %q", got)
			}
		})
	}
}
