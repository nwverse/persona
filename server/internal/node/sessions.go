package node

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// stateFile remembers which sessions Persona started and with which launcher. It lives in the hub's Postgres:
// loaded with `state.load` when the node starts and written through with `state.put` events. A session in Sessions
// is Persona's (takesReport, close and decorate's StartedByPersona go by that): what the node only observes of the
// user's own sessions never goes in (ultracode.go keeps that in memory, #5510).
type stateFile struct {
	mu       sync.Mutex
	Sessions map[string]sessionState `json:"sessions"`
	persist  func(id string, v sessionState)
	// loading: the hub's copy has yet to come (Serve opens it before loadState runs, which closes it). Until then a
	// session missing from Sessions may still be Persona's. Nil once loaded.
	loading chan struct{}
}

type sessionState struct {
	Pane             string    `json:"pane,omitempty"`
	Launcher         string    `json:"launcher"`
	Model            string    `json:"model,omitempty"`
	ModelFingerprint string    `json:"model_fingerprint,omitempty"`
	ModelEffort      string    `json:"model_effort,omitempty"`
	Runtime          string    `json:"runtime,omitempty"` // the assistant runtime it was asked to run on (assistant_sdk.go)
	Cwd              string    `json:"cwd"`
	IsMain           bool      `json:"is_main,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	// AppMode / AppEffort: the permission mode and effort the app set on an assistant conversation (modes.go). They
	// win over what its transcript recorded and outlive the node: an old "default" there no longer locks it in.
	AppMode   string `json:"app_mode,omitempty"`
	AppEffort string `json:"app_effort,omitempty"`
	// Tasks: the assistant conversation's background tasks and monitors running right now (node/assistant.go).
	Tasks []string `json:"tasks,omitempty"`
	// Compaction: the last compaction the user asked for (assistant_compact.go).
	Compaction *proto.Compaction `json:"compaction,omitempty"`
	// Guest: a guest conversation's tools (assistant_guest.go): every process it runs in has no built-in tools and
	// only these of Persona's. Nil is an ordinary conversation.
	Guest []string `json:"guest,omitempty"`
	// Ultracode: what was last seen of ultracode in the process Persona runs this session in (ultracode.go). Written
	// by setUltracode only, which never makes a record for a session that has none.
	Ultracode *ultracodeRecord `json:"ultracode_process,omitempty"`
}

func newState() *stateFile { return &stateFile{Sessions: map[string]sessionState{}} }

// legacyState reads the pre-Postgres ~/.persona/sessions.json, if any.
func legacyState(path string) map[string]sessionState {
	var f struct {
		Sessions map[string]sessionState `json:"sessions"`
	}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	return f.Sessions
}

// merge adds sessions loaded from the hub without overwriting ones touched since the node started.
func (s *stateFile) merge(m map[string]sessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, v := range m {
		if observationOnly(v) {
			continue // not Persona's, and what it says of the user's session is not kept either (observationOnly)
		}
		if _, ok := s.Sessions[id]; !ok {
			s.Sessions[id] = v
		}
	}
}

// observationOnly: a record holding nothing but an ultracode observation, as a node of #3782 wrote for a session the
// user started. Persona's own records always have a directory and a launcher (checkStart fills both in), and the hub
// gives back a zero start time only for a record stored without one next to an ultracode observation
// (db.PutNodeSession's started_at_unknown). Such a record is dropped when loaded, observation and all: nothing would
// ever write it again (the user's sessions get no record), so a resume notice dismissed now, or a value of a process
// long gone, would come back from it with every node restart. What the node sees of the session from then on stays in
// memory, as for any session the user started.
func observationOnly(v sessionState) bool {
	return v.Ultracode != nil && v.StartedAt.IsZero() && v.Launcher == "" && v.Cwd == ""
}

// beginLoad and endLoad bracket loading the hub's copy (loading).
func (s *stateFile) beginLoad() {
	s.mu.Lock()
	if s.loading == nil {
		s.loading = make(chan struct{})
	}
	s.mu.Unlock()
}

func (s *stateFile) endLoad() {
	s.mu.Lock()
	if s.loading != nil {
		close(s.loading)
		s.loading = nil
	}
	s.mu.Unlock()
}

func (s *stateFile) isLoading() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loading != nil
}

// loaded waits until the hub's copy is loaded (or failed to load: loadState gives up after 20 seconds).
func (s *stateFile) loaded(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	ch := s.loading
	s.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ultracode is the stored ultracode observation of a session Persona started.
func (s *stateFile) ultracode(id string) (ultracodeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Sessions[id]
	if !ok || v.Ultracode == nil {
		return ultracodeRecord{}, false
	}
	return *v.Ultracode, true
}

// setUltracode changes the ultracode observation of a session Persona started and nothing else of its record, under
// the lock, so a copy read before another writer's change is never written back over it. A session without a record
// is not Persona's, or not loaded yet: nothing is written for it (false).
func (s *stateFile) setUltracode(id string, rec ultracodeRecord) bool {
	s.mu.Lock()
	v, ok := s.Sessions[id]
	if !ok {
		s.mu.Unlock()
		return false
	}
	v.Ultracode = &rec
	s.Sessions[id] = v
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		persist(id, v)
	}
	return true
}

func (s *stateFile) get(id string) (sessionState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Sessions[id]
	return v, ok
}

// panes returns the remembered pane of every session (herdr pane ids are not reused while the server lives).
func (s *stateFile) panes() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for id, v := range s.Sessions {
		if v.Pane != "" {
			out[v.Pane] = id
		}
	}
	return out
}

func (s *stateFile) setPane(id, pane string) {
	s.mu.Lock()
	v, ok := s.Sessions[id]
	if !ok || v.Pane == pane {
		s.mu.Unlock()
		return
	}
	v.Pane = pane
	s.Sessions[id] = v
	s.mu.Unlock()
	s.put(id, v)
}

func (s *stateFile) put(id string, v sessionState) {
	s.mu.Lock()
	s.Sessions[id] = v
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		persist(id, v)
	}
}

// quickMeta is a cheap summary of a transcript read from its head and tail only.
type quickMeta struct {
	size     int64
	mtime    time.Time
	title    string
	first    string
	activity time.Time // last real exchange; zero when unknown
	queue    []string
	loop     *proto.LoopInfo
	goal     *proto.GoalInfo        // from backgroundTasks too: a goal is often set at the start of a long transcript
	plan     *proto.PlanInfo        // from the same scan (bgPlan): a TodoWrite is often far back in a long turn
	bg       []proto.BackgroundTask // from backgroundTasks: the tail alone misses tasks started earlier in a long turn
	bgWait   bool                   // bg and loop are not known yet (a big first read is running)
	wf       []proto.Workflow       // workflow runs, as the transcript tells them (bgWorkflows)
	wfScript map[string]string      // their script files by run id
	cwd      string
	model    string
	surface  string // the client it runs in (transcript.Meta.Surface)
	lastText string
	// asks is what lastText asks the user (transcript.AskOf), when no message came in after it (#5096).
	asks string
	// lastTextAt is when the agent last wrote text (botmux's sentinel included: it said something, it is no probe).
	lastTextAt time.Time
	// lastPromptAt is when the latest message to the agent came in.
	lastPromptAt time.Time
	// doing / doingAt: what the agent's latest tool call does, when no text came after it (transcript.Activity).
	doing       string
	doingAt     time.Time
	wake        bool // the latest turn was a background task waking the agent
	readAt      time.Time
	mode        string
	modeAt      time.Time
	effort      string
	effortAt    time.Time
	version     string
	versionAt   time.Time // when the line that said version was written: a resumed process has written none yet
	ultracode   *bool
	ultracodeAt time.Time
	sandbox     string // Codex: the turn's sandbox, which with mode (its approval policy) gives the app's mode back
	// contextTokens / contextWindow: the latest reply's input tokens and, for Codex, the window its rollout records
	// (transcript.Meta; Session.ContextTokens / ContextWindow, #2430). Carried over from the last read when the tail
	// holds no reply (one huge line can fill it).
	contextTokens, contextWindow int
	// turnClosed: the tail ends with a turn finished (transcript.Meta.TurnClosed): a Codex rollout's, a Claude
	// transcript's (turn_duration, an interrupt)
	turnClosed bool
	// claudeTurn: a Claude transcript whose tail has a turn started and not finished (transcript.Meta.TurnOpen)
	claudeTurn bool
	// contentAt: the newest line in the tail that has a time, when the transcript last said something. Not the file's
	// mtime: Claude Code rewrites its trailing metadata (last-prompt, ai-title, mode…, no timestamps) in place while
	// idle, and that is no sign of work (#5967).
	contentAt time.Time
	// turnOpen: a Codex rollout whose tail is inside a turn: the last turn it starts has not finished, or one turn
	// fills the whole tail (a finished turn's task_complete is within a few KB of the end; real turns write MBs)
	turnOpen bool
	// turnError / turnErrorKind: the latest turn failed on the model service, as a Codex rollout records it
	// (transcript.Meta.TurnError, #2185); Session.TurnError / TurnErrorKind.
	turnError, turnErrorKind string
	// rewound: the lines session.rewind, or a rewind seen in the terminal, went back over (File.RewoundLines), which
	// Claude writes nothing for; kept from read to read, the tail leaves them out (applyRewound).
	rewound []string
}

const offlineWindow = 7 * 24 * time.Hour

// hostTextSlack: the transcript's words this long after the host's record of the answer still belong to that turn.
const hostTextSlack = 10 * time.Minute

// quickTail: how much of a transcript's end readQuickMeta reads for its title and last words. One line can fill it:
// Claude Code writes its api-request-blob lines whole (300–500 KB each, 2026-11).
const quickTail = 384 * 1024

// metaMinInterval: how stale a growing transcript's title / last text / background tasks may get in the list.
const metaMinInterval = 3 * time.Second

func (n *Node) meta(path string, st os.FileInfo) quickMeta {
	n.mu.Lock()
	m, ok := n.metas[path]
	n.mu.Unlock()
	// A working agent's transcript grows all the time and every list rebuild (each herdr event) would re-read its
	// 384 KB tail and scan the new bytes: a dozen busy agents with large transcripts kept a node reading ~20 MB/s.
	// A growing file's meta is re-read at most every metaMinInterval; the list's rev comes from stat, so it stays exact.
	if ok && (m.size == st.Size() && m.mtime.Equal(st.ModTime()) || time.Since(m.readAt) < metaMinInterval) {
		if m.bgWait { // the background read may be done now
			m.bg, m.loop, m.goal, m.bgWait = n.backgroundTasks(path)
			m.wf, m.wfScript = n.bgWorkflows(path)
			m.ultracode, m.ultracodeAt = n.bgUltracode(path)
			m.plan = n.bgPlan(path)
			n.mu.Lock()
			n.metas[path] = m
			n.mu.Unlock()
		}
		if m.size != st.Size() || !m.mtime.Equal(st.ModTime()) {
			// Written since that read: a turn error it saw may be over (a "Continue" ran and ended well within the interval),
			// and a stale one would have it continued again. None for a few seconds is the safe side (#2185).
			m.turnError, m.turnErrorKind = "", ""
		}
		return m
	}
	m = readQuickMetaSince(path, st, m) // m: the last read's, zero when there is none
	m.readAt = time.Now()
	m.bg, m.loop, m.goal, m.bgWait = n.backgroundTasks(path)
	m.wf, m.wfScript = n.bgWorkflows(path)
	m.ultracode, m.ultracodeAt = n.bgUltracode(path)
	m.plan = n.bgPlan(path)
	n.mu.Lock()
	n.metas[path] = m
	n.mu.Unlock()
	return m
}

func readQuickMeta(path string, st os.FileInfo) quickMeta {
	return readQuickMetaSince(path, st, quickMeta{})
}

// readQuickMetaSince reads a transcript's quick meta; prev is its meta from the last read (zero when there is none),
// whose last text stands when the tail has none and the file only grew within the tail since.
func readQuickMetaSince(path string, st os.FileInfo, prev quickMeta) quickMeta {
	m := quickMeta{size: st.Size(), mtime: st.ModTime()}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	var tm transcript.Meta
	// The first prompt is usually in the first 32 KB. A Codex rollout puts its instructions first (the assistant's
	// AGENTS.md and snapshot: 74 KB in), so up to 256 KB is read while it has not turned up.
	head := make([]byte, 32*1024)
	nr, _ := f.Read(head)
	for {
		for _, line := range bytes.Split(head[:nr], []byte("\n")) {
			if bytes.Contains(line, []byte(`"cwd"`)) || bytes.Contains(line, []byte(`"type":"user"`)) ||
				bytes.Contains(line, []byte(`"type":"UserMessage"`)) { // Codex rollouts: session_meta, then the first prompt
				transcript.ParseLine(0, line, &tm)
				if tm.Cwd != "" && tm.FirstUser != "" {
					break
				}
			}
		}
		if tm.FirstUser != "" || nr < len(head) || len(head) >= 256*1024 {
			break
		}
		head = make([]byte, len(head)*2)
		nr, _ = f.ReadAt(head, 0)
	}
	first := tm.FirstUser
	off := max(st.Size()-quickTail, 0)
	// The tail's lines in the transcript's tree: a turn the user went back over (a rewind in the terminal, then a recap
	// beside it) is not where the agent's last words come from (transcript/branch.go).
	var tail transcript.Tail
	tail.Rewound(prev.rewound)
	m.rewound = prev.rewound
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err == nil || len(buf) > 0 {
		lines := bytes.Split(buf, []byte("\n"))
		if off > 0 && len(lines) > 0 {
			lines = lines[1:] // first line is partial
		}
		for _, line := range lines {
			if len(line) == 0 {
				continue
			}
			if ts := lineTime(line); ts.After(m.contentAt) {
				m.contentAt = ts
			}
			// Skip parsing big tool results; they still count as activity (a turn that ended on one would otherwise
			// look finished earlier, and a look at it in the app in between would count as having seen the end). Not a
			// compaction summary or a meta line: no prompt (Claude Code compacts on its own while idle, and its 17 KB
			// summary read as one left the session working for hours, #5967); the parser reads them as what they are.
			if bytes.Contains(line, []byte(`"type":"user"`)) && len(line) > 8<<10 && !notAPrompt(line) {
				if ts := lineTime(line); ts.After(tm.LastActivity) {
					tm.LastActivity = ts
				}
				if !bytes.Contains(line, []byte(`"tool_result"`)) {
					tm.LastTool = nil // a long prompt: a new turn
					if !tm.Codex {
						tm.TurnOpen, tm.TurnClosed = true, false
					}
					if ts := lineTime(line); !bytes.Contains(line, []byte(`"isMeta":true`)) && ts.After(tm.LastPromptAt) {
						tm.LastPromptAt = ts
					}
				}
				if !tm.Codex {
					tail.Skipped(line) // its place in the tree: a rewound turn with it in is still found, and hidden whole
				}
				continue
			}
			tail.Line(&tm, transcript.ParseLine(0, line, &tm))
		}
	}
	if rest, ok := tail.Revoked(); ok { // Claude went on from a turn the node took for rewound (transcript.File.RewoundAt)
		m.rewound = rest
	}
	rewound := tail.Settle(&tm)
	m.title, m.cwd, m.model, m.lastText, m.first, m.activity, m.queue = tm.Title, tm.Cwd, tm.Model, tm.LastText, first, tm.LastActivity, tm.Queue
	m.surface = tm.Surface
	if m.surface == "" {
		m.surface = prev.surface // Claude writes it on every line, a rollout on its first: the head read above has it
	}
	m.wake, m.turnClosed, m.lastTextAt, m.lastPromptAt = tm.LastTurnWake, tm.TurnClosed, tm.LastTextAt, tm.LastPromptAt
	if m.lastText == "" && off > 0 {
		// An agent that said a line and then ran tools for a while: their results and hook lines push its words out of
		// the tail (a 1.5 MB transcript whose last text was 900 KB back, 2026-11-04) and its task card said "No activity yet".
		if !rewound && prev.size > 0 && prev.size >= off && prev.size <= st.Size() {
			// Nothing written since the last read is out of the tail: no newer words. Not after a turn in the tail was
			// gone back over: the last read's words may be that turn's.
			m.lastText = prev.lastText
		} else {
			m.lastText = lastWordsBefore(f, off)
		}
	}
	if !m.lastPromptAt.After(m.lastTextAt) { // a message after the question answered it (#5096)
		m.asks = transcript.AskOf(m.lastText)
	}
	if t := tm.LastTool; t != nil {
		m.doing, m.doingAt = transcript.Activity(t.Name, t.Input), t.At
	}
	m.turnOpen = tm.Codex && (tm.TurnOpen || off > 0 && !tm.TurnClosed)
	m.claudeTurn = !tm.Codex && tm.TurnOpen
	m.turnError, m.turnErrorKind = tm.TurnError, tm.TurnErrorKind
	m.mode, m.modeAt, m.effort, m.effortAt, m.sandbox = tm.PermissionMode, tm.ModeAt, tm.Effort, tm.EffortAt, tm.CodexSandbox
	m.version, m.versionAt, m.ultracode, m.ultracodeAt = tm.Version, tm.VersionAt, tm.Ultracode, tm.UltracodeAt
	m.contextTokens, m.contextWindow = tm.ContextTokens, tm.ContextWindow
	if m.contextTokens == 0 && prev.size > 0 && prev.size <= st.Size() {
		m.contextTokens, m.contextWindow = prev.contextTokens, prev.contextWindow // no reply in the tail: the last read's stands
	}
	return m
}

// lastWordsReach: how far before the tail lastWordsBefore looks for an agent's last words.
const lastWordsReach = 8 << 20

// lastWordsBefore is the last assistant text in the transcript before end, read backwards a chunk at a time (each
// twice the last) up to lastWordsReach; "" when there is none in that reach.
func lastWordsBefore(f *os.File, end int64) string {
	for chunk, read := int64(quickTail), int64(0); end > 0 && read < lastWordsReach; chunk *= 2 {
		start := max(end-chunk, 0)
		buf := make([]byte, end-start)
		if n, err := f.ReadAt(buf, start); err != nil && n < len(buf) {
			return ""
		}
		read += end - start
		lines := bytes.Split(buf, []byte("\n"))
		next := start
		if start > 0 && len(lines) > 0 {
			next += int64(len(lines[0])) // a partial line: whole in the next chunk
			lines = lines[1:]
		}
		var tm transcript.Meta
		for _, line := range lines {
			if len(line) == 0 || bytes.Contains(line, []byte(`"type":"user"`)) && len(line) > 8<<10 {
				continue
			}
			transcript.ParseLine(0, line, &tm)
		}
		if tm.LastText != "" {
			return tm.LastText
		}
		end = next
	}
	return ""
}

// codexStaleWorking: how long herdr may say a Codex agent works while its rollout says the turn is over. Codex writes
// task_started the moment a turn begins; herdr reads "working" off the screen and can be a moment ahead of it.
const codexStaleWorking = 30 * time.Second

// codexTurnOver reports whether a Codex session herdr calls working has in fact finished: its rollout ends with the
// turn complete and nothing has been written for a while. herdr has said so for hours-old turns (2026-10-30: a turn
// finished two days earlier showed "working for 45 hours" for eight minutes, the rollout untouched).
func (n *Node) codexTurnOver(id string, now time.Time) bool {
	p := n.transcriptPath(id)
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	return n.meta(p, st).turnClosed && now.Sub(st.ModTime()) > codexStaleWorking
}

// lineTime reads a transcript line's own "timestamp" without decoding it (Claude Code writes it after the message, so
// the last one; quotes inside the message are escaped and do not match).
// notAPrompt: a user line that is not a message to the agent whatever its size: a compaction's summary or a meta line.
func notAPrompt(line []byte) bool {
	return bytes.Contains(line, []byte(`"isCompactSummary":true`)) || bytes.Contains(line, []byte(`"isMeta":true`))
}

func lineTime(line []byte) time.Time {
	key := []byte(`"timestamp":"`)
	i := bytes.LastIndex(line, key)
	if i < 0 {
		return time.Time{}
	}
	rest := line[i+len(key):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, string(rest[:j]))
	return t
}

// startingSession reports whether a session without a transcript is one that exists but has not written its first
// line yet (a headless conversation, or an agent that is live or that Persona started), rather than an unknown id.
func (n *Node) startingSession(id string) bool {
	if n.isAssistantSession(id) {
		return true
	}
	if _, live := n.agent(id); live {
		return true
	}
	if _, known := n.codexSource(id); known {
		return true
	}
	_, known := n.state.get(id)
	return known
}

// transcriptPath finds <config dir>/projects/*/<id>.jsonl. One session can have a file in two config directories
// (resumed under another profile: the new writes go to the new copy, the old one stays): the newest is the one.
// A path a hook reported is Claude's own word; one found here is looked for again after half a minute.
func (n *Node) transcriptPath(id string) string {
	p := n.findTranscriptPath(id)
	if !n.scopedTranscriptAllows(p) {
		return ""
	}
	return p
}

func (n *Node) findTranscriptPath(id string) string {
	// A runtime's session whose conversation is a Claude transcript (runtimetranscript.go): that one's file.
	if t := n.runtimes.transcriptOf(id); t != "" {
		id = t
	}
	if n.codexBlocked(id) {
		return ""
	}
	if src, ok := n.codexSource(id); ok && !n.ownedAssistantSource(id) {
		if src.path != "" {
			if _, err := os.Stat(src.path); err == nil {
				return src.path
			}
		}
		return rolloutPath(src.home, src.thread)
	}
	if strings.HasPrefix(id, "c0de-") {
		return n.oldCodexTranscript(id)
	}
	n.mu.Lock()
	p := n.paths[id]
	at, guessed := n.pathsAt[id]
	n.mu.Unlock()
	if p != "" && (!guessed || time.Since(at) < 30*time.Second) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if !validID(id) {
		return ""
	}
	matches := newestFirst(n.globProjects("*", id+".jsonl"))
	if len(matches) == 0 {
		if p := n.codexPath(id); p != "" {
			matches = []string{p}
		} else if p := n.traexPath(id); p != "" {
			matches = []string{p}
		} else {
			return ""
		}
	}
	n.setGuessedPath(id, matches[0])
	return matches[0]
}

func (n *Node) setGuessedPath(id, p string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.paths[id] = p
	if n.pathsAt == nil {
		n.pathsAt = map[string]time.Time{}
	}
	n.pathsAt[id] = time.Now()
}

// newestFirst orders the copies of a transcript by modification time, newest first.
func newestFirst(paths []string) []string {
	if len(paths) < 2 {
		return paths
	}
	mod := map[string]time.Time{}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil {
			mod[p] = st.ModTime()
		}
	}
	sort.SliceStable(paths, func(i, j int) bool { return mod[paths[i]].After(mod[paths[j]]) })
	return paths
}

func validID(id string) bool { return nodeclient.ValidSessionID(id) }

func (n *Node) file(id string) (*transcript.File, error) {
	n.mu.Lock()
	f := n.files[id]
	if f != nil {
		n.used(id)
	}
	n.mu.Unlock()
	if f != nil {
		if old := f.Where(); !fileExists(old) {
			// Moved since it was opened (a Codex thread archived or restored): it was read from a path that is gone.
			if p := n.transcriptPath(id); p != "" && p != old {
				f.Relocate(p)
			}
		}
		return f, nil
	}
	if _, _, sub := splitSubagent(id); sub {
		var err error
		if f, err = n.subagentFile(id); err != nil {
			return nil, err
		}
	} else if parent, agent, ok := strings.Cut(id, subagentSep); ok && n.runtimes.transcriptOf(parent) != "" {
		// A subagent of a runtime's session whose conversation is a Claude transcript: that transcript's subagent.
		var err error
		if f, err = n.subagentFile(n.runtimes.transcriptOf(parent) + subagentSep + agent); err != nil {
			return nil, err
		}
	} else {
		p := n.transcriptPath(id)
		if p == "" {
			return nil, fmt.Errorf("transcript for session %s not found", id)
		}
		f = transcript.NewFile(p)
	}
	n.mu.Lock()
	if existing := n.files[id]; existing != nil {
		f = existing
	} else {
		n.files[id] = f
	}
	n.used(id)
	n.evictFiles()
	n.mu.Unlock()
	return f, nil
}

// warmTranscripts parses the live sessions' transcripts once, one at a time, soon after the node starts. Right
// after a (re)deploy the phone pulls every busy session, and parsing a transcript of a few hundred MB on that first
// request took up to 30 s (the hub gave up with a 502).
func (n *Node) warmTranscripts(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second): // the first herdr snapshot is in
	}
	n.mu.Lock()
	ids := make([]string, 0, len(n.agents))
	for id := range n.agents {
		ids = append(ids, id)
	}
	n.mu.Unlock()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if f, err := n.file(id); err == nil {
			n.refresh(id, f)
		}
	}
}

// maxFiles caps the transcripts kept parsed in memory: every session ever opened used to stay, items and all.
const maxFiles = 16

// used marks a parsed transcript as just asked for. Callers hold n.mu.
func (n *Node) used(id string) {
	if n.fileUse == nil {
		n.fileUse = map[string]uint64{}
	}
	n.useTick++
	n.fileUse[id] = n.useTick
}

// evictFiles drops the least recently used parsed transcripts of offline sessions nobody watches, down to maxFiles.
// Live sessions stay (warmTranscripts parsed them so the phone never waits for a cold parse). Reopening one parses
// the file again; its history stays the same (same file, same first line). Callers hold n.mu.
func (n *Node) evictFiles() {
	for len(n.files) > maxFiles {
		oldest, at := "", uint64(0)
		for id := range n.files {
			if _, watched := n.watches[id]; watched {
				continue
			}
			if _, live := n.agents[id]; live {
				continue
			}
			if t := n.fileUse[id]; oldest == "" || t < at {
				oldest, at = id, t
			}
		}
		if oldest == "" {
			return // all watched
		}
		delete(n.files, oldest)
		delete(n.fileUse, oldest)
	}
}

// hostNotices applies what the host says beyond the state: a failure notice (FailureReason, the state stays the
// turn's), and an attention the agent is past (a message came in after it: the turn's state, no reason). A host only
// clears its notices when a message comes through it; one sent another way leaves them standing.
func (n *Node) hostNotices(id string, s *proto.Session, a host.Agent) {
	if a.Failure == "" && a.AttentionAt.IsZero() {
		return
	}
	var promptAt time.Time
	if p := n.transcriptPath(id); p != "" {
		if st, err := os.Stat(p); err == nil {
			promptAt = n.meta(p, st).lastPromptAt
		}
	}
	if a.Failure != "" && (a.FailureAt.IsZero() || !promptAt.After(a.FailureAt)) {
		s.FailureReason = a.Failure
	}
	if !a.AttentionAt.IsZero() && promptAt.After(a.AttentionAt) && s.Status == proto.StatusBlocked {
		s.Status = a.TurnStatus
	}
}

// sessionList returns live herdr sessions plus recent offline transcripts.
func (n *Node) sessionList(ctx context.Context, refreshAgents bool) []proto.Session {
	if refreshAgents {
		_ = n.refreshHosts(ctx)
	}
	n.mu.Lock()
	agents := make(map[string]host.Agent, len(n.agents))
	for k, v := range n.agents {
		agents[k] = v
	}
	onList := n.onAgentsList // replaced whole with n.agents (setAgentsAt), never changed in place
	n.mu.Unlock()

	var out []proto.Session
	seen := map[string]bool{}
	livePanes := map[string]bool{} // the resting Claudes' panes, whose screens are read for a dialog
	now := time.Now()
	for id, a := range agents {
		if !onList[id] { // a pane on Claude's agents list stays idle: the transcript is its parked session's (claudebg.go)
			a.AgentStatus = n.claudeStatus(id, a, now)
		}
		s := proto.Session{
			SessionID: id, Cwd: a.Cwd, Status: a.AgentStatus, PaneID: a.PaneID, TabID: a.TabID,
			WorkspaceID: a.WorkspaceID, AgentName: a.Name, Title: a.Title, Agent: a.Agent,
			ExternalURL: a.ExternalURL, Chat: a.Chat, Topic: a.Topic, Dispatched: a.Dispatched,
		}
		viaBotmux := host.ParsePane(a.PaneID).Kind == host.Botmux
		switch {
		case viaBotmux && (a.Agent == "codex" || a.Agent == traexAgent):
			// The thread's name, else botmux's title of the topic; messages go through botmux, neither route.
			if t := n.codexTitle(id); a.Agent == "codex" && t != "" {
				s.Title = t
			} else if t := n.traexTitle(id); a.Agent == traexAgent && t != "" {
				s.Title = t
			}
		case a.Agent == "codex":
			s.Title = n.codexTitle(id)                        // the terminal title is "dots | codex | Ready"
			s.CodexRoute, s.CodexRouteNote = n.codexRoute(id) // codex_daemon.go
		case a.Agent == traexAgent:
			s.Title = n.traexTitle(id) // the thread's name; the terminal title adds " | <directory>"
			s.CodexRoute, s.CodexRouteNote = codexRouteTerminal, "TRAEX 没有共享的 app-server"
		}
		n.placeIn(&s, a)
		s.Ports = n.portsOf(a.PaneID)
		if a.Agent == "codex" && s.Status == proto.StatusWorking && n.codexTurnOver(id, time.Now()) {
			s.Status = proto.StatusDone
		}
		n.hostNotices(id, &s, a)
		n.mu.Lock()
		if n.driving[id] && s.Status == proto.StatusBlocked {
			s.Status = proto.StatusIdle
		}
		n.mu.Unlock()
		if onList[id] { // its pane is on Claude's agents list: shown in its tab, picked from in its terminal (claudebg.go)
			s.Capabilities, s.ReadOnlyReason = &proto.SessionCapabilities{Read: true, Terminal: true}, claudeAgentsList
		}
		if s.Status == proto.StatusBlocked { // only while it still waits (not once the phone drives it)
			s.AttentionReason = a.Attention
		}
		if why := n.lostFirstPrompt(id, s.Status); why != "" { // its first message did not go in (firstprompt.go)
			s.Status, s.AttentionReason = proto.StatusBlocked, why
		}
		if restingClaude(a, s.Status) && !onList[id] && !n.isDriving(id) {
			livePanes[a.PaneID] = true
			if head := n.dialogOn(id, a.PaneID); head != "" { // a dialog holds its terminal (dialogstatus.go)
				s.Status, s.AttentionReason = proto.StatusBlocked, head
			}
		}
		if !onList[id] { // the list's prompt is no suggestion of its
			s.Suggestion = n.noteStatus(id, a.Agent, a.PaneID, s.Status) // suggestion.go
		}
		// textAt: when the transcript last had the agent's words; rollout: a Codex pane's rollout meta, for its effort.
		var textAt, promptAt time.Time
		var rollout quickMeta
		if p := n.transcriptPath(id); p != "" {
			if st, err := os.Stat(p); err == nil {
				m := n.meta(p, st)
				textAt = m.lastTextAt
				rollout = m
				if m.title != "" && a.Agent != "codex" && a.Agent != traexAgent {
					s.Title = m.title
				}
				if s.Title == "" || s.Title == "Claude Code" {
					s.Title = ""
					untitled(&s, m)
				}
				s.Model, s.LastText, s.UpdatedAt, s.Rev = m.model, snippet(m.lastText), lastUsed(m, st), st.Size()
				s.AgentSurface = m.surface
				s.Asks, s.AsksRead = m.asks, true // the list's last_text is only the reply's start (#5096)
				promptAt = m.lastPromptAt
				if a.Agent != "codex" && a.Agent != traexAgent {
					s.Model = n.shownModel(id, m.model) // the pick made in the app until a reply shows the real one (model_switch.go)
				}
				s.TurnError, s.TurnErrorKind = m.turnError, m.turnErrorKind
				s.Activity, s.ActivityAt = m.activityOf()
				setContext(&s, m)
				if a.Agent != "codex" && a.Agent != traexAgent {
					s.PermissionMode, s.Effort = n.currentModes(id, m)
				}
				s.WokeByTask = m.wake
				if s.Status == proto.StatusWorking || s.Status == proto.StatusBlocked {
					s.Queued = m.queue // an idle agent has taken everything; stale entries are from outside the tail
				}
				// Background shells keep running after the turn ends; background subagents are listed as subagents.
				n.setBackground(&s, m)
				s.BackgroundWaiting = n.backgroundWaits(&s) // bgwait.go (#6045)
				s.Loop, s.Goal, s.Plan = m.loop, m.goal, m.plan
				s.Subagents = n.subagents(id)
			}
		}
		// The host's record of the answer (botmux's reply in the topic), unless the transcript has words well after it:
		// the session went on outside the host (a `claude --resume` in a terminal). Text written in the same turn
		// right after the reply (the sentinel, a closing line) comes within minutes.
		if a.LastText != "" && (a.LastTextAt.IsZero() || !textAt.After(a.LastTextAt.Add(hostTextSlack))) {
			s.LastText = snippet(a.LastText)
			// A message to the agent after the host's answer answered its question (#5096); a host that gives no time
			// for its answer is taken as the latest word.
			if !a.LastTextAt.IsZero() && promptAt.After(a.LastTextAt) {
				s.Asks = ""
			} else {
				s.Asks = transcript.AskOf(a.LastText)
			}
			s.AsksRead = true
		}
		if a.Agent == "codex" {
			n.codexEffortRow(id, &s.Efforts, &s.Effort, rollout) // joined on its app-server: its effort (codex_effort.go)
		}
		if a.Agent == piAgent {
			n.mergePiRow(&s) // its conversation is the extension's (pi.go)
		}
		switch {
		case a.Agent == opencodeAgent:
			s.Title = opencodeTitle(a.Title)
			n.mergeOpencodeRow(&s) // its conversation is the plugin's (opencode.go)
		case otherAgent(a):
			s.ReadOnlyReason = otherNoReader(a.Agent)
		}
		if n.codexBlocked(id) {
			markCodexConflict(&s)
		}
		n.decorate(&s)
		out = append(out, s)
		seen[id] = true
	}

	n.forgetDialogs(livePanes)

	for _, s := range n.starts.listed(seen) { // starting.go
		n.decorate(&s)
		out = append(out, s)
		seen[s.SessionID] = true
	}
	for _, s := range n.runtimes.sessions() { // runtimes (runtimes.go): listed as they say
		if !seen[s.SessionID] {
			if n.runtimeAssistant(s.SessionID) { // the assistant keeps its mode (assistant_runtime.go)
				if m := n.assistantModeOf(s.SessionID); m != "" {
					s.PermissionMode = m
				}
			}
			// A conversation in a Claude transcript here (runtimetranscript.go): what it says, and its own row goes.
			if t := n.mergeRuntimeTranscript(&s); t != "" {
				seen[t] = true
			}
			out = append(out, s)
			seen[s.SessionID] = true
		}
	}
	for _, s := range n.assistantSessions() {
		if !seen[s.SessionID] {
			out = append(out, s)
			seen[s.SessionID] = true
		}
	}
	for _, s := range n.claudeBgSessions(n.liveClaudes(), seen) { // Claude's background sessions (claudebg.go)
		out = append(out, s)
		seen[s.SessionID] = true
	}
	for _, s := range n.codexSessions(seen) {
		out = append(out, s)
	}
	offlineRows := n.offline(seen)
	n.markInterrupted(offlineRows, seen) // the restart cut these off (#1917)
	for _, s := range offlineRows {
		n.decorate(&s)
		out = append(out, s)
	}
	// Terminal-owned Codex parents were already seen before history was added.
	resolveCodexHierarchy(out)
	var keptSessions []proto.Session
	for _, s := range out {
		if !n.sessionScopeAllows(s.Cwd) {
			continue
		}
		if s.Cwd != "" && n.cwdBlocked(s.Cwd) && !n.isAssistantSession(s.SessionID) && !n.keptSessions()[s.SessionID] {
			continue
		}
		s.WorkspaceCwd = n.sessionWorkspaceCwd(s.Cwd)
		s.CanFork = n.forkable(s) // "Branch to New Chat" (fork.go)
		keptSessions = append(keptSessions, s)
	}
	out = keptSessions
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

var offlineCache struct {
	sync.Mutex
	at   time.Time
	home string // whose scan it is: another node's (tests run many in one process) scans its own
	list []proto.Session
}

// offline lists recent transcripts without a live agent; the directory scan is cached for 20s.
func (n *Node) offline(live map[string]bool) []proto.Session {
	offlineCache.Lock()
	defer offlineCache.Unlock()
	if time.Since(offlineCache.at) > 20*time.Second || offlineCache.home != n.offlineCacheKey() {
		offlineCache.list = n.scanOffline()
		offlineCache.at, offlineCache.home = time.Now(), n.offlineCacheKey()
	}
	var out []proto.Session
	for _, s := range offlineCache.list {
		if !live[s.SessionID] {
			out = append(out, s)
		}
	}
	return out
}

// setKept replaces the sessions that are always listed; the next list has them.
func (n *Node) setKept(ids []string) {
	m := map[string]bool{}
	for _, id := range ids {
		if validID(id) {
			m[id] = true
		}
	}
	n.mu.Lock()
	n.kept = m
	n.mu.Unlock()
	offlineCache.Lock()
	offlineCache.at = time.Time{}
	offlineCache.Unlock()
}

func (n *Node) keptSessions() map[string]bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.kept
}

func (n *Node) scanOffline() []proto.Session {
	var out []proto.Session
	files := n.globProjects("*", "*.jsonl")
	type cand struct {
		path string
		st   os.FileInfo
	}
	var cands, assistant, kept []cand
	keep := n.keptSessions()
	n.mu.Lock()
	forked := maps.Clone(n.forked) // "Branch to New Chat"'s copies (fork.go)
	n.mu.Unlock()
	own := n.assistantProject()
	for _, p := range files {
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if !validID(id) {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if keep[id] { // the main conversation: listed however long it has been quiet and however busy the machine
			kept = append(kept, cand{p, st})
			continue
		}
		if time.Since(st.ModTime()) > offlineWindow || (st.Size() < 2048 && !forked[id]) {
			continue
		}
		if isAssistantProject(filepath.Base(filepath.Dir(p)), own) {
			assistant = append(assistant, cand{p, st})
			continue
		}
		// Another platform's runs (agentfilters.go) go before the newest are counted: hundreds a day would crowd out
		// the user's own.
		if !forked[id] && n.agentFiltered("claude", p) {
			continue
		}
		cands = append(cands, cand{p, st})
	}
	newest := func(list []cand, limit int) []cand {
		sort.Slice(list, func(i, j int) bool { return list[i].st.ModTime().After(list[j].st.ModTime()) })
		if len(list) > limit {
			list = list[:limit]
		}
		return list
	}
	// The assistant's conversations are counted on their own: a script running `claude -p` hundreds of times a day
	// filled the 80 newest with its sessions, and the "Chat" tab lost every conversation quieter than a couple of hours (#2105).
	cands = append(newest(cands, 80), newest(assistant, maxAssistantOffline)...)
	cands = append(cands, kept...)
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].st.ModTime().After(cands[j].st.ModTime()) })
	listed := map[string]bool{}
	for _, c := range cands {
		id := strings.TrimSuffix(filepath.Base(c.path), ".jsonl")
		if listed[id] {
			continue // an older copy in another config directory (newest first): it used to win, and list the session twice
		}
		listed[id] = true
		m := n.meta(c.path, c.st)
		if !n.sessionScopeAllows(m.cwd) {
			continue
		}
		if m.cwd != "" && n.cwdBlocked(m.cwd) {
			continue
		}
		// Sessions that never produced output (hook probes): small files read whole with no title and no words. A bigger
		// one only had its tail read, and a tail of tool calls under one huge line says nothing; the main conversation is
		// listed whatever its tail holds. Dropping it left the app and the web with no session for the main chat, and its composer
		// disabled (#1163: its last turn stopped mid tool call, under a 377 KB line).
		if m.lastText == "" && m.lastTextAt.IsZero() && m.title == "" && !keep[id] && !forked[id] && c.st.Size() <= quickTail {
			continue
		}
		n.setGuessedPath(id, c.path)
		s := proto.Session{
			SessionID: id, Title: m.title, Cwd: m.cwd, Status: proto.StatusOffline, Model: m.model,
			Agent: "claude", AgentSurface: m.surface,
			LastText: snippet(m.lastText), UpdatedAt: lastUsed(m, c.st), Rev: c.st.Size(),
		}
		s.Activity, s.ActivityAt = m.activityOf()
		setContext(&s, m)
		s.PermissionMode, s.Effort = n.currentModes(id, m)
		untitled(&s, m)
		out = append(out, s)
	}
	return out
}

// activityOf is the session's activity for the list: what its latest tool call does, and since when.
func (m quickMeta) activityOf() (string, *time.Time) {
	if m.doing == "" {
		return "", nil
	}
	if m.doingAt.IsZero() {
		return m.doing, nil
	}
	at := m.doingAt.UTC()
	return m.doing, &at
}

// lastUsed is when the session was last really used: its last exchange, or the file time for transcripts
// without any (only system lines).
func lastUsed(m quickMeta, st os.FileInfo) time.Time {
	if !m.activity.IsZero() {
		return m.activity.UTC()
	}
	return st.ModTime().UTC()
}

// untitled gives sessions without an AI title (headless ones) their first message as title: what the user wrote,
// not the context blocks it starts with (the main conversation's, a Feishu /agent request's), or the title of the
// block it is made of only ("continue the old main chat"). Stripped first, clipped after.
func untitled(s *proto.Session, m quickMeta) {
	if first := transcript.Title(m.first); s.Title == "" && first != "" {
		r := []rune(snippet(first))
		if len(r) > 40 {
			r = append(r[:40], '…')
		}
		s.Title = string(r)
	}
}

func (n *Node) decorate(s *proto.Session) {
	s.AlsoOpenIn = n.alsoOpenIn(s.SessionID) // live in another host too (ownership.go)
	if s.PaneID != "" && s.Host == "" {
		s.Host = host.ParsePane(s.PaneID).Kind // the host it runs in (#2576)
	}
	if st, ok := n.state.get(s.SessionID); ok {
		s.StartedByPersona = true
		s.Launcher = st.Launcher
		s.Compaction = st.Compaction
		if s.Cwd == "" {
			s.Cwd = st.Cwd
		}
	}
	if s.Launcher == "" {
		s.Launcher = n.inferLauncher(s.SessionID)
	}
	if s.Title == "" {
		s.Title = filepath.Base(s.Cwd)
	}
	n.ultracodeRow(s)
}

func snippet(s string) string {
	const most = 200
	s = wire.AssistantCardText(s)
	// One pass instead of strings.Fields, Join and []rune over the whole reply: runs of white space become one space,
	// leading and trailing space go, and the walk stops once there is more than most runes to show.
	var out strings.Builder
	out.Grow(min(len(s), most*utf8.UTFMax))
	runes, space := 0, false
	for i, r := range s {
		if unicode.IsSpace(r) {
			space = out.Len() > 0
			continue
		}
		if space {
			if runes == most {
				return truncatedSnippet(out.String())
			}
			out.WriteByte(' ')
			runes++
			space = false
		}
		if runes == most {
			return truncatedSnippet(out.String())
		}
		_, n := utf8.DecodeRuneInString(s[i:])
		out.WriteString(s[i : i+n])
		runes++
	}
	return out.String()
}

// truncatedSnippet ends a cut snippet with "…". Like the string([]rune) it replaces, a cut snippet has each
// invalid UTF-8 byte as U+FFFD; an uncut one keeps the bytes it had.
func truncatedSnippet(s string) string {
	if !utf8.ValidString(s) {
		s = string([]rune(s))
	}
	return s + "…"
}

// errEngineChanged: the engine was switched while a refresh ran; its result is the old engine's and is dropped.
var errEngineChanged = errors.New("the engine changed during the refresh")

// errNoHost: the node has no terminal host to look at.
var errNoHost = errors.New("no terminal host")

// layout remembers a host's space and slot names from its last snapshot.
type layout struct {
	spaces map[string]host.Space
	slots  map[string]host.Slot
	// panes is every pane of the snapshot, agent or not: the terminal browser lists shells from it (panes.go).
	panes []host.Agent
	// paneIndex is each pane's place in its slot (host.Layout.PaneIndex), nil when the host does not say.
	paneIndex map[string]int
	at        time.Time
}

// refreshHosts takes one snapshot of every host, or of only the hosts given: agents plus the space / slot names shown
// as space / slot. The hosts are asked concurrently, each bounded by its own client's timeouts, so a slow host never
// holds up the other host of a combined engine. It fails only when no host answered; a host that did not answer keeps
// the agents it gave last. A host that is not running (host.ErrNotRunning: herdr-core without its socket) answered: it
// has no agents.
func (n *Node) refreshHosts(ctx context.Context, only ...host.Host) error {
	if n.sessionScope != nil {
		return nil
	}
	n.mu.Lock()
	epoch := n.engEpoch // a refresh that outlives an engine switch must not bring the old engine's agents back
	n.mu.Unlock()
	all := n.hosts.All()
	if len(all) == 0 {
		return errNoHost
	}
	targets := only
	if len(targets) == 0 {
		targets = all
	}
	type outcome struct {
		kind   string
		agents []host.Agent
		full   bool
		err    error
	}
	outs := make([]outcome, len(targets))
	var wg sync.WaitGroup
	for i, h := range targets {
		wg.Add(1)
		go func(i int, h host.Host) {
			defer wg.Done()
			agents, full, err := n.lookAt(ctx, h, epoch)
			outs[i] = outcome{kind: h.Kind(), agents: agents, full: full, err: err}
		}(i, h)
	}
	wg.Wait()

	n.mu.Lock()
	if n.engEpoch != epoch {
		n.mu.Unlock()
		return errEngineChanged
	}
	if n.hostAgents == nil {
		n.hostAgents = map[string][]host.Agent{}
	}
	answered, snapped, firstErr := false, false, error(nil)
	heard := map[string]bool{} // the hosts that answered: the merge asks only their panes what they run (setAgentsAt)
	for _, o := range outs {
		if o.err != nil && errors.Is(o.err, host.ErrNotRunning) {
			o.err, o.agents = nil, nil // an answer: nothing runs there
		}
		if o.err != nil {
			if firstErr == nil {
				firstErr = o.err
			}
			o.agents = n.hostAgents[o.kind]
		} else {
			answered, snapped = true, snapped || o.full
			n.hostAgents[o.kind] = o.agents
			heard[o.kind] = true
		}
	}
	union := make([]host.Agent, 0)
	for _, agents := range n.hostAgents { // hosts not in this partial refresh keep what they gave last
		union = append(union, agents...)
	}
	n.mu.Unlock()

	if !answered {
		return firstErr
	}
	if !n.setAgentsAt(union, epoch, heard) {
		return errEngineChanged
	}
	n.backfillResume(union) // herdrresume.go
	if snapped {
		n.panesChanged()
	}
	return nil
}

// lookAt is one host's agents: from a snapshot (full), which also gives the space / slot names; a host that cannot
// snapshot (older herdr without `api snapshot`) gives its agent list instead.
func (n *Node) lookAt(ctx context.Context, h host.Host, epoch uint64) (agents []host.Agent, full bool, err error) {
	snap, err := h.Snapshot(ctx)
	if err != nil {
		agents, aerr := h.Agents(ctx)
		if aerr != nil {
			return nil, false, err
		}
		return agents, false, nil
	}
	l := layout{spaces: map[string]host.Space{}, slots: map[string]host.Slot{}, panes: snap.Panes, paneIndex: snap.PaneIndex, at: time.Now()}
	for _, w := range snap.Spaces {
		l.spaces[w.ID] = w
	}
	for _, t := range snap.Slots {
		l.slots[t.ID] = t
	}
	n.mu.Lock()
	if n.engEpoch != epoch {
		n.mu.Unlock()
		return nil, false, errEngineChanged
	}
	if n.layouts == nil {
		n.layouts = map[string]layout{}
	}
	n.layouts[h.Kind()] = l
	n.mu.Unlock()
	// Panes running an agent the host does not detect (a compatible CLI) become agents of our own.
	agents = append(snap.Agents, n.traexAgents(ctx, snap.Panes, snap.Agents)...)
	// Retain active Claude agents when Herdr temporarily drops agent detection during tool or subshell execution (#1641).
	return append(agents, n.claudeGraceAgents(snap.Panes, agents)...), true, nil
}

// claudeGraceAgents recovers live Claude agents whose terminal panes are still open in snap.Panes
// but were temporarily omitted from snap.Agents because the host's foreground process detection
// flapped to a child tool process or subshell (#1641).
func (n *Node) claudeGraceAgents(panes, agents []host.Agent) []host.Agent {
	if len(panes) == 0 {
		return nil
	}
	hasAgent := make(map[string]bool, len(agents))
	claimedSessions := make(map[string]bool, len(agents))
	for _, a := range agents {
		if a.PaneID != "" {
			hasAgent[a.PaneID] = true
		}
		if id := a.SessionID(); id != "" {
			claimedSessions[id] = true
		}
	}

	panesByID := make(map[string]host.Agent, len(panes))
	for _, p := range panes {
		if p.PaneID != "" {
			panesByID[p.PaneID] = p
		}
	}

	claudes := n.liveClaudes()
	liveSessions := make(map[string]bool, len(claudes))
	for _, c := range claudes {
		if c.SessionID != "" && c.Kind != "bg" {
			liveSessions[c.SessionID] = true
		}
	}
	if len(liveSessions) == 0 {
		return nil
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	var out []host.Agent
	for id, prev := range n.agents {
		if prev.Agent != "claude" || prev.PaneID == "" {
			continue
		}
		if hasAgent[prev.PaneID] || claimedSessions[id] {
			continue
		}
		if !liveSessions[id] {
			continue
		}
		p, ok := panesByID[prev.PaneID]
		if !ok {
			continue
		}
		retained := prev
		retained.WorkspaceID = p.WorkspaceID
		retained.TabID = p.TabID
		if p.Cwd != "" {
			retained.Cwd = p.Cwd
		}
		if p.Title != "" {
			retained.Title = p.Title
			retained.RawTitle = p.RawTitle
		}
		if p.Tokens != nil {
			retained.Tokens = p.Tokens
		}
		if retained.AgentSession == nil {
			retained.AgentSession = &host.AgentSession{Kind: "id", Value: id}
		}
		if retained.AgentStatus == "" {
			retained.AgentStatus = proto.StatusWorking
		}
		out = append(out, retained)
		hasAgent[prev.PaneID] = true
		claimedSessions[id] = true
	}
	return out
}

// placeIn fills the space / slot fields of a live session.
func (n *Node) placeIn(s *proto.Session, a host.Agent) {
	kind := n.hosts.Ref(a.PaneID).Kind
	n.mu.Lock()
	l := n.layouts[kind]
	w, wok := l.spaces[a.WorkspaceID]
	t, tok := l.slots[a.TabID]
	pi, piok := l.paneIndex[a.PaneID]
	n.mu.Unlock()
	s.SpaceKind, s.SpaceID, s.SlotID = kind, a.WorkspaceID, a.TabID
	if wok {
		s.SpaceName, s.SpaceIndex, s.ParentSpaceID = w.Label, w.Number, w.ParentID
	}
	if tok {
		s.SlotName, s.SlotIndex = t.Label, t.Number
	}
	s.PaneIndex = nil
	if piok {
		s.PaneIndex = &pi
	}
	if a.Tokens != nil && a.Tokens["persona_from"] != "" {
		s.SpawnFrom = a.Tokens["persona_from"]
	}
}

// unplaced: a claude in a terminal (not a background session, not parked) runs a session no pane is named after.
func unplaced(procs []claudeProc, agents []host.Agent) bool {
	named := map[string]bool{}
	for _, a := range agents {
		if id := a.SessionID(); id != "" {
			named[id] = true
		}
	}
	for _, c := range procs {
		if c.Kind == "interactive" && c.ParkedJobID == "" && validID(c.SessionID) && !named[c.SessionID] {
			return true
		}
	}
	return false
}

// setAgents maps live claude agents to session ids: the host's own report when it knows (herdr's claude integration),
// else what our hooks told us about the pane, else the newest transcript in the agent's project directory.
func (n *Node) setAgents(agents []host.Agent) {
	n.mu.Lock()
	epoch := n.engEpoch
	n.mu.Unlock()
	n.setAgentsAt(agents, epoch, nil)
}

// setAgentsAt is setAgents for a refresh of engine epoch: false (nothing set) when the engine changed since. heard is
// the hosts that answered the refresh (nil: every host): only their panes' processes are asked for. The merge covers
// every host's agents, and asking all of them tied each host's refresh to the others' I/O (#5816): Orca's poll every
// second asked herdr, and herdr's refresh ran Orca's process scan (three ps runs, about 0.2 s on a Mac). A pane of
// another host keeps the pid its own refresh found (sessionFromKnownProcess), and is looked at again only by that
// host's refresh. Ownership's rule 1 still asks every candidate's host (resolve).
func (n *Node) setAgentsAt(agents []host.Agent, epoch uint64, heard map[string]bool) bool {
	fromProcess := func(pane string) string {
		if heard == nil || heard[host.ParsePane(pane).Kind] {
			return n.sessionFromProcess(pane)
		}
		return n.sessionFromKnownProcess(pane)
	}
	m, onList := map[string]host.Agent{}, map[string]bool{}
	var unknown, codex, pi, opencode, botmux []host.Agent
	other := map[string]host.Agent{} // terminal-only agents by their row id (opencode.go)
	contested := contest{}           // sessions live in two hosts at once: an owner is picked below (ownership.go)
	procs := n.liveClaudes()
	parked := parkedClaudeSessions(procs)    // claudebg.go
	botmuxThreads := n.botmuxThreads(agents) // hostbotmux.go
	// What the claude in each pane says it runs, for the panes herdr names a session for: herdr takes the session from
	// a SessionStart hook that reported HERDR_PANE_ID, and a claude elsewhere that inherited the variable (botmux's
	// tmux server, first started in a herdr pane, 2026-11-04) reports into the pane too. The pane's own claude wins.
	// Asked only while a terminal claude runs a session herdr names for no pane (the pane's own one, then): one
	// process lookup per pane, kept a minute.
	own := map[string]string{}
	if unplaced(procs, agents) {
		for _, a := range agents {
			if a.Agent == "claude" && a.SessionID() != "" && a.PaneID != "" {
				if id := fromProcess(a.PaneID); id != "" {
					own[a.PaneID] = id
				}
			}
		}
	}
	n.mu.Lock()
	for _, a := range agents {
		if (a.Agent == "codex" || a.Agent == traexAgent) && host.ParsePane(a.PaneID).Kind == host.Botmux {
			botmux = append(botmux, a) // after the panes that run their thread themselves
			continue
		}
		if a.Agent == "codex" {
			codex = append(codex, a)
			continue
		}
		if a.Agent == piAgent { // its session: herdr's pi integration, or the extension in it (pi.go)
			pi = append(pi, a)
			continue
		}
		if a.Agent == opencodeAgent { // its session: herdr's opencode plugin, or the Persona plugin in the server (opencode.go)
			opencode = append(opencode, a)
			continue
		}
		if a.Agent == traexAgent { // found by its process, which names its session (the compatible CLI integration)
			if id := a.SessionID(); id != "" {
				m[id] = a
			}
			continue
		}
		if a.Agent != "claude" {
			// An agent nothing here reads is still an agent the user runs: listed with its terminal (opencode.go).
			if otherAgent(a) {
				if id := otherSession(a); id != "" {
					other[id] = a
				}
			}
			continue
		}
		if id := a.SessionID(); id != "" {
			if p := own[a.PaneID]; p != "" && p != id {
				id = p // reported by a claude outside the pane (above)
			}
			if j, ok := parked[id]; ok {
				// its claude sent the session to the background: the pane shows Claude's agents view, or the job
				if bg := j.shownIn(a); bg != "" {
					if !liveTaken(m, bg) {
						m[bg] = a
					}
				} else if !liveTaken(m, id) {
					m[id], onList[id] = onAgentsList(a), true
				}
				continue
			}
			if liveTaken(m, id) && a.AgentStatus == proto.StatusOffline {
				continue // the same session runs in another host's pane (botmux's parked row, herdr's resume)
			}
			if crossHost(m, id, a) {
				contested.add(id, m[id], a) // live in another host too: its owner is picked below
				continue
			}
			m[id] = a
			continue
		}
		unknown = append(unknown, a)
	}
	n.notePanes(unknown)
	n.mu.Unlock()
	for _, a := range pi {
		if id := n.piSession(a); id != "" {
			if _, taken := m[id]; !taken {
				m[id] = a
			}
		}
	}
	for _, a := range opencode {
		id := n.opencodeSession(a)
		if id == "" {
			id = otherSession(a) // herdr reported no session id (an older integration): the pane is the row, terminal only
		}
		if id != "" {
			if _, taken := m[id]; !taken {
				m[id] = a
			}
		}
	}
	for id, a := range other {
		if _, taken := m[id]; !taken {
			m[id] = a
		}
	}
	for _, a := range unknown {
		// Without herdr's claude integration the pane's claude process says which session it runs. A pane binding
		// (our start, a hook) comes after it: the bindings of sessions Persona started are kept in the hub's
		// database and come back when node restarts, long after another claude took the pane (2026-10-31: after a
		// redeploy two panes showed sessions they had run days earlier). A guess (title, then newest transcript in
		// the directory) is the last resort — six agents in one repo got each other's tab names that way.
		id := fromProcess(a.PaneID)
		if j, ok := parked[id]; ok { // the same, as the pane's own claude tells it
			if bg := j.shownIn(a); bg != "" {
				if !liveTaken(m, bg) {
					m[bg] = a
				}
			} else if !liveTaken(m, id) {
				m[id], onList[id] = onAgentsList(a), true
			}
			continue
		}
		if id != "" && crossHost(m, id, a) { // its own claude runs a session another host's pane lists
			contested.add(id, m[id], a)
			continue
		}
		if id == "" {
			n.mu.Lock()
			id = n.panes[a.PaneID]
			n.mu.Unlock()
		}
		if id == "" || liveTaken(m, id) {
			if n.freshPane(a.PaneID) {
				continue // its claude has not written sessions/<pid>.json yet; the newest transcript is an older session
			}
			id = n.guessSession(a, m)
		}
		if id != "" {
			m[id] = a
		}
	}
	n.resolve(m, contested) // the owner of every session live in two hosts (ownership.go)
	// Codex panes: the thread named in the title (what the pane shows now: a /resume in Codex changes it), else what
	// herdr's codex integration or our own start said; only then, after every pane that could be told has its
	// thread, the newest rollout in the directory nobody else has.
	var unnamed []host.Agent
	for _, a := range codex {
		home := n.codexPaneHome(a.PaneID)
		id := a.SessionID()
		if id != "" {
			id = mainThread(home, id) // a subagent's id stands for its conversation
		}
		if id == "" {
			n.mu.Lock()
			id = n.panes[a.PaneID]
			n.mu.Unlock()
		}
		if named := n.codexByTitle(a, id, m); named != "" {
			id = named
		}
		if id != "" {
			id = n.codexPublicID(home, id)
		}
		if _, taken := m[id]; id == "" || taken {
			unnamed = append(unnamed, a)
			continue
		}
		m[id] = a
	}
	// botmux's Codex and compatible variants sessions, by the thread their rollout names (botmuxThreads), before any guess could take
	// that rollout for another pane. A parked one gives way to a pane that runs the same thread.
	for _, a := range botmux {
		id := botmuxThreads[a.PaneID]
		if id == "" || (a.AgentStatus == proto.StatusOffline && liveTaken(m, id)) {
			continue
		}
		m[id] = a
	}
	for _, a := range unnamed {
		n.mu.Lock()
		starting := n.codexStarting[a.PaneID]
		n.mu.Unlock()
		if starting {
			continue // a start of ours: its thread is not on disk yet, and an older one in the directory is not it
		}
		if id := n.codexSession(a, m); id != "" {
			m[id] = a
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.engEpoch != epoch {
		return false
	}
	// A pane that now runs another session than it did: rare, and the first thing to look at when a session drops off
	// the live list while its agent runs (#5463: a tmux worker took its manager's pane).
	was := map[string]string{}
	for id, a := range n.agents {
		if a.PaneID != "" {
			was[a.PaneID] = id
		}
	}
	for id, a := range m {
		if old := was[a.PaneID]; a.PaneID != "" && old != "" && old != id {
			log.Printf("agents: pane %s runs session %s now, not %s", a.PaneID, id, old)
		}
	}
	n.agents = m
	n.onAgentsList = onList
	if n.lastPanes == nil {
		n.lastPanes = map[string]string{}
	}
	for id, a := range m {
		n.lastPanes[id] = a.PaneID
	}
	return true
}

// liveTaken: a running agent already has session id in m. An offline one (a parked botmux session) gives way to the
// pane that runs it.
func liveTaken(m map[string]host.Agent, id string) bool {
	a, ok := m[id]
	return ok && a.AgentStatus != proto.StatusOffline
}

// sessionFromProcess reads the session a pane's claude runs from ~/.claude/sessions/<pid>.json, which Claude Code
// keeps current (a /clear moves it to a new session id). The pane's pid is cached for a minute; not finding it only
// for a few seconds: right after node starts the claudes' config directories are not all known yet, and a minute of
// "no pid file" let a stale pane binding show another session after every redeploy (2026-10-31).
func (n *Node) sessionFromProcess(pane string) string {
	retry := panePIDRetry
	if n.freshPane(pane) {
		retry = 0 // its claude is about to write the file: look every refresh, so the pane shows within a second of it
	}
	n.mu.Lock()
	c, ok := n.panePIDs[pane]
	n.mu.Unlock()
	if !ok || time.Since(c.checked) > time.Minute || (c.pid == 0 && time.Since(c.checked) > retry) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		pids, err := n.paneProcesses(ctx, pane)
		cancel()
		if err != nil {
			return n.sessionOfPID(pane, c.pid) // the pid found before, as the other hosts' refreshes read it
		}
		c = panePID{checked: time.Now()}
		for _, pid := range pids {
			if _, err := os.Stat(n.claudeSessionFile(pid)); err == nil {
				c.pid = pid
				break
			}
		}
		n.mu.Lock()
		n.panePIDs[pane] = c
		n.mu.Unlock()
	}
	return n.sessionOfPID(pane, c.pid)
}

// sessionFromKnownProcess is sessionFromProcess without asking the pane's host: from the pid an earlier look found,
// however long ago ("" when none was found).
func (n *Node) sessionFromKnownProcess(pane string) string {
	n.mu.Lock()
	c := n.panePIDs[pane]
	n.mu.Unlock()
	return n.sessionOfPID(pane, c.pid)
}

// sessionOfPID is the session the claude with pid runs, from its sessions/<pid>.json; with the file gone the pane's pid
// is forgotten, and the next look at the pane finds it again.
func (n *Node) sessionOfPID(pane string, pid int) string {
	if pid == 0 {
		return ""
	}
	f := n.claudeSessionFile(pid)
	if _, err := os.Stat(f); err != nil {
		n.mu.Lock()
		if c, ok := n.panePIDs[pane]; ok && c.pid == pid { // not a newer pid a concurrent look just found
			delete(n.panePIDs, pane) // claude exited / restarted: look again next time
		}
		n.mu.Unlock()
		return ""
	}
	return readClaudeSessionID(f)
}

// readClaudeSessionID reads the session id out of a claude's sessions/<pid>.json ("" when missing or invalid).
func readClaudeSessionID(path string) string { return nodeclient.ReadClaudeSessionID(path) }

// panePIDRetry: how soon a pane whose claude had no sessions/<pid>.json is looked at again; a fresh pane's
// (freshPane) every refresh while its claude starts.
const panePIDRetry = 5 * time.Second

// freshPaneGrace: how long a claude pane that appeared after node started goes without a guessed session. A claude
// writes its sessions/<pid>.json a few seconds after it starts (four or five on Windows), and until its first prompt it
// has no transcript of its own: a guess then is the newest transcript in the directory, an older session, which the
// pane showed for those seconds (2026-11-04, #1980). Past the grace a claude that never writes the file (older Claude
// Code) is guessed as before.
const freshPaneGrace = 20 * time.Second

// notePanes remembers when each claude pane without a host session id was first seen, and forgets the panes gone. The
// panes of the first call are there since before node started: zero, never fresh. Callers hold n.mu.
func (n *Node) notePanes(unknown []host.Agent) {
	first := n.paneSeen == nil
	if first {
		n.paneSeen = map[string]time.Time{}
	}
	now, here := time.Now(), make(map[string]bool, len(unknown))
	for _, a := range unknown {
		here[a.PaneID] = true
		if _, ok := n.paneSeen[a.PaneID]; !ok && !first {
			n.paneSeen[a.PaneID] = now
		} else if !ok {
			n.paneSeen[a.PaneID] = time.Time{}
		}
	}
	for p := range n.paneSeen {
		if !here[p] {
			delete(n.paneSeen, p)
		}
	}
}

// freshPane: the pane's claude appeared after node started, less than freshPaneGrace ago.
func (n *Node) freshPane(pane string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	t, ok := n.paneSeen[pane]
	return ok && !t.IsZero() && time.Since(t) < freshPaneGrace
}

type panePID struct {
	pid     int
	checked time.Time
}

// bindPane records which session runs in a pane (from start() and from hook events).
func (n *Node) bindPane(pane, id string) {
	if pane == "" || !validID(id) {
		return
	}
	n.mu.Lock()
	n.panes[pane] = id
	n.mu.Unlock()
	n.state.setPane(id, pane)
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// maxAssistantOffline is how many of the assistant directory's quiet transcripts the list keeps, apart from the 80
// newest of every other directory.
const maxAssistantOffline = 100

// assistantProject is the projects directory name Claude Code keeps for this node's assistant directory.
func (n *Node) assistantProject() string {
	return nonAlnum.ReplaceAllString(identity.AssistantDir(n.home), "-")
}

// isAssistantProject: base is the name of the projects directory Claude Code keeps for the assistant's directory
// (own: this node's, assistantProject). The assistant may have run under ~/.persona or ~/.persona, through a symlinked
// home, so the name is matched by its end as well.
// The health space's directory (docs/assistant/health.md) sits next to it and counts as the assistant's too.
func isAssistantProject(base, own string) bool {
	if base == own || base == strings.TrimSuffix(own, "-assistant")+"-health" {
		return true
	}
	for _, suffix := range []string{"--persona-assistant", "--persona-assistant", "--persona-health", "--persona-health"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// guessSession picks a transcript for an agent herdr could not identify. Claude sets the terminal title to the
// session's AI title, so a title match wins; otherwise the newest unclaimed transcript in the project directory.
func (n *Node) guessSession(a host.Agent, taken map[string]host.Agent) string {
	if a.Cwd == "" {
		return ""
	}
	files := n.globProjects(nonAlnum.ReplaceAllString(a.Cwd, "-"), "*.jsonl")
	title := strings.TrimSpace(a.Title)
	best, bestT := "", time.Time{}
	for _, p := range files {
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if _, used := taken[id]; used || !validID(id) {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if title != "" && n.meta(p, st).title == title {
			return id
		}
		if st.ModTime().After(bestT) {
			best, bestT = id, st.ModTime()
		}
	}
	return best
}

// agent is the live agent of session id, with a Claude's status as claudeStatus says.
func (n *Node) agent(id string) (host.Agent, bool) {
	n.mu.Lock()
	a, ok := n.agents[id]
	n.mu.Unlock()
	if ok {
		a.AgentStatus = n.claudeStatus(id, a, time.Now())
	}
	return a, ok
}

// claudeFresh: how recently a Claude transcript with an open turn must have been written to count as at work.
const claudeFresh = 45 * time.Second

// claudeStatus is a Claude's status with its own signs of work over its host's (#2069: herdr 0.9.3 said done through a
// whole turn started from the phone, so the app showed it as finished while it searched code). When the host says
// done, idle or does not know:
//
//   - a session that runs Persona's hooks is judged by their turn counters first (judgeHookTurn): a prompt no Stop has
//     answered is working, a Stop with no prompt after it is not;
//   - otherwise, or while the hooks have said nothing yet, it is working while its terminal title shows Claude Code's
//     spinner, or its transcript has a turn started and not finished (no turn_duration, no interrupt) written to
//     within claudeFresh. A transcript whose turn has finished keeps the host's word: a title read just before Claude
//     redrew it says nothing then.
//
// Hooks whose claude process is gone (or whose pid now belongs to another program) are forgotten.
func (n *Node) claudeStatus(id string, a host.Agent, now time.Time) string {
	if a.Agent != "claude" || a.AgentStatus != proto.StatusDone && a.AgentStatus != proto.StatusIdle && a.AgentStatus != proto.StatusUnknown {
		return a.AgentStatus
	}
	var m quickMeta
	var mod time.Time
	hasFile := false
	if p := n.transcriptPath(id); p != "" {
		if st, err := os.Stat(p); err == nil {
			m, mod, hasFile = n.meta(p, st), st.ModTime(), true
		}
	}
	// When the transcript last said something: its newest timed line, not its mtime (#5967); the mtime when the tail has
	// no time at all.
	if !m.contentAt.IsZero() {
		mod = m.contentAt
	}
	fresh := hasFile && now.Sub(mod) < claudeFresh
	spinner := claudeSpinnerTitle(a.RawTitle)
	if t, ok := n.hooks.get(id); ok {
		if t.pid > 0 && !processAlive(t.pid, t.start) {
			n.hooks.forget(id) // a dead process' turn is over, and a reused pid is not it
		} else {
			switch judgeHookTurn(hookSignals{turn: t, spinner: spinner, waiting: claudeWaitingTitle(a.RawTitle), hasFile: hasFile,
				fileMod: mod, turnOpen: m.claudeTurn, turnClosed: m.turnClosed, now: now}) {
			case hookWorking:
				return proto.StatusWorking
			case hookResting:
				return a.AgentStatus
			}
		}
	}
	switch {
	case m.turnClosed:
		return a.AgentStatus
	case spinner, m.claudeTurn && fresh:
		return proto.StatusWorking
	}
	return a.AgentStatus
}

// restingClaude: a Claude Code in a herdr pane whose host says its turn is over (or does not know), so a dialog on
// its screen is what it waits on (dialogstatus.go). Only herdr: where it was seen (#2553); Orca reports its own
// permission state, and botmux sessions have no screen of theirs to read.
func restingClaude(a host.Agent, status string) bool {
	if a.Agent != "claude" || a.PaneID == "" || host.ParsePane(a.PaneID).Kind != host.Herdr {
		return false
	}
	return status == proto.StatusDone || status == proto.StatusIdle || status == proto.StatusUnknown
}

// isDriving: Persona is working the session's TUI itself (modes.go): a picker it opened is not the user's to answer.
func (n *Node) isDriving(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.driving[id]
}

// claudeSpinnerTitle: the terminal title starts with the spinner Claude Code shows in it while it works — a quarter
// circle (◐◓◑◒, 2.1.3xx) or, in older versions, a braille dot; ✳ when it waits.
func claudeSpinnerTitle(title string) bool {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(title))
	return r >= '◐' && r <= '◓' || r >= 0x2800 && r <= 0x28FF
}

// claudeWaitingTitle: the title starts with the mark Claude Code shows in it while it waits for input (✳).
func claudeWaitingTitle(title string) bool {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(title))
	return r == '✳'
}

// pollAgents refreshes the hosts' state every second (only the ones due) and pushes sessions.changed when anything
// visible changes. Each watcher host is snapshotted on its own stream's changes and at most every slowSnapshot while
// its stream is live, independently of the other hosts (the combined engine): a herdr-core event never snapshots
// herdr and vice versa.
func (n *Node) pollAgents(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	last := map[string]time.Time{} // host kind → its last snapshot
	var listed time.Time
	for {
		sigs := n.signalSnapshot()
		kicked, ok := nextChange(ctx, t.C, sigs, listed)
		if !ok {
			return
		}
		var due []host.Host
		for _, h := range n.hosts.All() {
			if n.hostDue(h.Kind(), last[h.Kind()], kicked[h.Kind()]) {
				due = append(due, h)
			}
		}
		// The transcripts (revs) are checked every tick regardless; a host that does not answer (not installed or not
		// running) does not stop the list: runtimes, the assistant and the transcripts do not need it. Its agents stay
		// as last seen (refreshHosts).
		if len(due) > 0 {
			_ = n.refreshHosts(ctx, due...)
			now := time.Now()
			for _, h := range due {
				last[h.Kind()] = now
			}
		}
		list := n.sessionList(ctx, false)
		listed = time.Now()
		if revs := n.publish(list); len(revs) > 0 {
			n.kickPorts() // a tool call ran: it may have started (or stopped) a server (ports.go)
		}
	}
}

// publish pushes a rebuilt list to the hub: the whole list when anything it shows changed, else only the revs that
// moved (a transcript grew: a few bytes instead of the list, so the app can pull those sessions). It returns the
// revs that moved. Runtimes (runtimes.go) publish through it too when their list changes. Nothing goes while the hub
// has yet to say whether botmux's sessions are listed (botmuxPending).
func (n *Node) publish(list []proto.Session) map[string]int64 {
	if n.botmuxPending() {
		return nil
	}
	n.mu.Lock()
	size := len(n.lastList)
	n.mu.Unlock()
	key := listKeySized(list, size)
	n.mu.Lock()
	changed := key != n.lastList
	n.lastList = key
	revs := n.revsChanged(list)
	n.mu.Unlock()
	if n.peer == nil {
		return revs
	}
	if changed {
		_ = n.peer.Notify("sessions.changed", map[string]any{"sessions": list})
	} else if len(revs) > 0 {
		_ = n.peer.Notify("sessions.revs", map[string]any{"revs": revs})
	}
	return revs
}

// revsChanged records the list's revs and returns those that differ from the last pushed. Callers hold n.mu.
func (n *Node) revsChanged(list []proto.Session) map[string]int64 {
	var out map[string]int64
	next := make(map[string]int64, len(list))
	for _, s := range list {
		next[s.SessionID] = s.Rev
		if s.Rev != 0 && n.lastRevs[s.SessionID] != s.Rev {
			if out == nil {
				out = map[string]int64{}
			}
			out[s.SessionID] = s.Rev
		}
	}
	n.lastRevs = next
	return out
}

// listKey ignores fields that churn without meaning (updated_at of live sessions).
func listKey(list []proto.Session) string { return listKeySized(list, 0) }

// listKeySized is listKey with the builder grown to size first (the last key's length: lists change little). It runs
// on every publish, so the common fields go straight into the builder; fmt only writes the rare composite ones.
func listKeySized(list []proto.Session, size int) string {
	var w keyWriter
	w.b.Grow(size)
	for i := range list {
		s := &list[i]
		w.str(s.SessionID, '|')
		w.str(s.Status, '|')
		w.str(s.Title, '|')
		w.str(s.PaneID, '|')
		w.str(s.SpaceName, '|')
		w.str(s.ParentSpaceID, '|')
		w.str(s.SlotName, '|')
		w.str(s.LastText, '|')
		w.int(s.UpdatedAt.Unix()/10, '|')
		w.quotes(s.Queued)
		w.b.WriteByte('|')
		w.str(s.Suggestion, '|')
		w.str(s.SpawnFrom, '\n')
		w.b.WriteString("hierarchy:")
		w.quote(s.ParentThreadID, '/')
		w.quote(s.ParentSessionID, '/')
		w.quote(s.RootSessionID, '/')
		w.str(strconv.FormatBool(s.IsSubagent), '/')
		w.quote(s.RepoRoot, '/')
		w.quote(s.SpaceID, '/')
		w.quote(s.SpaceKind, '\n')
		writeUltracodeKey(&w, s)
		if s.Activity != "" { // a new step shows at once, not with the next 10 s of updated_at
			w.b.WriteString("activity:")
			w.str(s.Activity, '/')
			w.int(unixOf(s.ActivityAt), '\n')
		}
		// A new reason or link alone is a change.
		w.b.WriteString("attention:")
		w.quote(s.AttentionReason, '/')
		w.quote(s.FailureReason, '/')
		w.str(s.ExternalURL, '\n')
		// A topic dispatched or a session's chat known: the todos follow it at once.
		if s.Chat != "" || len(s.Dispatched) > 0 {
			w.b.WriteString("botmux:")
			w.str(s.Chat, '/')
			w.str(s.Topic, '/')
			w.quotes(s.Dispatched)
			w.b.WriteByte('\n')
		}
		// The pane's place in its tab, present or not: a swap or a closed shell pane changes only this (and 0 is not none).
		w.b.WriteString("pane:")
		w.str(s.SlotID, '/')
		w.int(int64(s.SlotIndex), '/')
		if s.PaneIndex != nil {
			w.int(int64(*s.PaneIndex), '\n')
		} else {
			w.str("none", '\n')
		}
		if c := s.Compaction; c != nil {
			w.b.WriteString("compaction:")
			w.str(c.State, '/')
			w.int(c.StartedAt.Unix(), '\n')
		}
		// A Codex thread's effort, which may change on the computer without a turn (codex_effort.go).
		if len(s.Efforts) > 0 {
			fmt.Fprintf(&w.b, "effort:%s/%v\n", s.Effort, s.Efforts)
		}
		w.b.WriteString("source:")
		w.str(s.SourceKind, '/')
		w.str(s.SourceThreadID, '/')
		w.str(s.SourceInstanceID, '/')
		w.str(s.ReadOnlyReason, '/')
		if s.Capabilities != nil {
			fmt.Fprint(&w.b, s.Capabilities)
		} else {
			w.b.WriteString("<nil>")
		}
		w.b.WriteByte('/')
		w.str(strconv.FormatBool(s.Attached), '/')
		w.str(s.CodexRoute, '/')
		w.str(s.CodexRouteNote, '\n')
		for _, t := range s.Background {
			w.str(t.ID, ',')
		}
		w.str(strconv.FormatBool(s.BackgroundWaiting), '\n')
		// Workflow journals advance without changing the main transcript's rev, and so do its agents: each one's
		// latest tool call (its name, and its calls to tell one Read from the next) goes out as often as the
		// workflow cache reads them (workflowReadInterval). Tokens and the last text ride along.
		for _, run := range s.Workflows {
			w.b.WriteString("workflow:")
			w.str(run.TaskID, '/')
			w.str(run.Status, '/')
			w.quote(run.Phase, '/')
			w.int(int64(run.AgentsDone), '/')
			w.int(int64(run.AgentsStarted), '/')
			w.int(int64(run.AgentsRunning), '/')
			w.int(int64(run.AgentsFailed), '\n')
			for _, a := range run.Agents {
				if a.LatestTool != nil {
					w.b.WriteString(a.LatestTool.Name)
				}
				w.b.WriteByte('/')
				w.int(int64(a.Calls), ',')
			}
		}
		if s.Loop != nil {
			w.b.WriteString("loop")
			w.tmp = strconv.AppendInt(w.tmp[:0], s.Loop.NextAt.Unix(), 10)
			w.b.Write(w.tmp)
		}
		if g := s.Goal; g != nil {
			w.b.WriteString("goal:")
			w.str(g.Status, '/')
			w.quote(g.Objective, '/')
			w.quote(g.Reason, '\n')
		}
		if p := s.Plan; p != nil { // a step done or started is pushed when it happens
			w.b.WriteString("plan:")
			w.int(int64(p.Done), '/')
			w.int(int64(p.Total), '/')
			w.quote(p.Current, '\n')
		}
		// A subagent's new call shows at once (its card says what it does now, #5602); the list goes out at most once a
		// second, with the poll.
		for _, a := range s.Subagents {
			w.str(a.ID, ':')
			w.str(strconv.FormatBool(a.Running), ':')
			w.int(int64(a.Steps), ':')
			if a.LastTool != nil {
				w.str(a.LastTool.ID, ':')
			}
			w.int(a.UpdatedAt.Unix()/10, ',')
		}
		for _, p := range s.Ports {
			w.b.WriteString("port")
			w.int(int64(p.Port), '/')
			w.int(int64(p.PID), '/')
			w.str(p.Title, ',')
		}
	}
	return w.b.String()
}

// keyWriter writes listKey's fields, each followed by its separator; tmp is the scratch numbers and quotes go
// through.
type keyWriter struct {
	b   strings.Builder
	tmp []byte
}

func (w *keyWriter) str(s string, sep byte) {
	w.b.WriteString(s)
	w.b.WriteByte(sep)
}

func (w *keyWriter) int(v int64, sep byte) {
	w.tmp = strconv.AppendInt(w.tmp[:0], v, 10)
	w.b.Write(w.tmp)
	w.b.WriteByte(sep)
}

// quote writes s as %q would.
func (w *keyWriter) quote(s string, sep byte) {
	w.tmp = strconv.AppendQuote(w.tmp[:0], s)
	w.b.Write(w.tmp)
	w.b.WriteByte(sep)
}

// quotes writes a list of strings as %q would ([] when empty, the rare others through fmt).
func (w *keyWriter) quotes(list []string) {
	if len(list) == 0 {
		w.b.WriteString("[]")
		return
	}
	fmt.Fprintf(&w.b, "%q", list)
}

func unixOf(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}

var launcherCache struct {
	sync.Mutex
	at       time.Time
	list     []proto.Launcher
	fetch    sync.Mutex // one login shell at a time
	fetching bool       // a background refresh (cachedLaunchers) is running
	// codex: how each Codex launcher runs (codex_start.go), its words as the shell would run them.
	codex map[string][]string
	// settings: the --settings a claude alias brings itself, as its words give it (launcher_settings.go).
	settings map[string]string
	// scripts: where each wrapper script on PATH is (pathlaunchers.go).
	scripts map[string]string
}

// launcherScript lists the login shell's launchers in one go (sourcing ~/.zshrc takes a while): what the
// `_claude_launchers` helper prints when the rc file has one, a GS byte, then every alias as its name and its words
// as zsh itself splits and unquotes them (name US word US … RS), so quoting never has to be parsed here (bash prints
// `alias -p` after bashAliasMark instead, launchers_shell.go); then, each after a GS byte, where codex, traex and omp
// are on the PATH, the names of the shell's functions and aliases (one a line: they come before a file on PATH of the
// same name), and its PATH (for the wrapper scripts on it, pathlaunchers.go). All of it between launcherBegin and
// launcherEnd, so what the rc files print around it is never read as a section (launcherOutput).
const launcherScript = `printf '\001persona-launchers-begin\001'
__jl_path=$PATH
_claude_launchers 2>/dev/null || true
printf '\035'
if [ -n "$ZSH_VERSION" ]; then
  eval 'for __k __v in "${(@kv)aliases}"; do
    print -rn -- "$__k"$'\''\x1f'\''
    for __w in "${(@Q)${(z)__v}}"; do print -rn -- "$__w"$'\''\x1f'\''; done
    print -rn -- $'\''\x1e'\''
  done'
elif [ -n "$BASH_VERSION" ]; then
  printf '\002'
  alias -p
fi
printf '\035'
PATH="$PATH:$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin"
if [ -n "$ZSH_VERSION" ]; then whence -p codex 2>/dev/null; else command -v codex 2>/dev/null; fi
printf '\035'
if [ -n "$ZSH_VERSION" ]; then whence -p traex 2>/dev/null; else command -v traex 2>/dev/null; fi
printf '\035'
if [ -n "$ZSH_VERSION" ]; then whence -p omp 2>/dev/null; else command -v omp 2>/dev/null; fi
printf '\035'
if [ -n "$ZSH_VERSION" ]; then eval 'print -rl -- ${(k)functions} ${(k)aliases}'; elif [ -n "$BASH_VERSION" ]; then compgen -A function; compgen -a; fi
printf '\035'
printf '%s' "$__jl_path"
printf '\001persona-launchers-end\001'`

// launcherBegin and launcherEnd frame what launcherScript prints (launcherOutput).
const (
	launcherBegin = "\x01persona-launchers-begin\x01"
	launcherEnd   = "\x01persona-launchers-end\x01"
)

// launcherOutput is what launcherScript itself printed: rc files print what they like to stdout while the shell
// starts (Ubuntu's /etc/bash.bashrc a sudo hint, #6310) and an exit trap after the script, none of it the script's.
// Nothing without the begin marker; to the end of the output without the end marker (a shell cut off by the timeout).
func launcherOutput(out string) string {
	i := strings.LastIndex(out, launcherBegin)
	if i < 0 {
		return ""
	}
	out, _, _ = strings.Cut(out[i+len(launcherBegin):], launcherEnd)
	return out
}

// launchers lists the launchers of this machine's login shell: the `_claude_launchers` helper's (claude wrapper
// functions), then every alias that runs claude (`alias dmcc="ANTHROPIC_BASE_URL=… claude …"`), by name. A name
// the helper lists is not repeated; none at all: plain claude.
func (n *Node) launchers(ctx context.Context) ([]proto.Launcher, error) {
	launcherCache.fetch.Lock()
	defer launcherCache.fetch.Unlock()
	launcherCache.Lock()
	if time.Since(launcherCache.at) < time.Minute && launcherCache.list != nil {
		list := launcherCache.list
		launcherCache.Unlock()
		return list, nil
	}
	launcherCache.Unlock()
	// Not cut short when the caller gives up (the hub waits a few seconds per machine): a slow rc file still finishes
	// and the next ask within the minute gets the list from the cache.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	out, err := loginShell(ctx, launcherScript).Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("list launchers: %w", err)
	}
	list, codex, settings, scripts := mergeLaunchers(string(out))
	launcherCache.Lock()
	launcherCache.at, launcherCache.list, launcherCache.codex, launcherCache.settings = time.Now(), list, codex, settings
	launcherCache.scripts = scripts
	launcherCache.Unlock()
	return list, nil
}

// mergeLaunchers is launcherScript's output as the launcher list: the helper's, then the aliases it does not name,
// then the wrapper scripts on PATH no function or alias shadows (nothing at all: plain claude), then Codex's — plain
// codex when it is on the PATH, and the aliases that run it (how each runs: codex) — then plain traex when it is on the
// PATH (started without Claude's flags, start_agent.go). settings: the claude aliases' and scripts' own --settings, by
// name (launcher_settings.go); an alias is what the shell runs for its name even when the helper lists the name too.
// scripts: where each wrapper script is.
func mergeLaunchers(out string) (list []proto.Launcher, codex map[string][]string, settings, scripts map[string]string) {
	helper, rest, _ := strings.Cut(launcherOutput(out), "\x1d")
	aliases, rest, _ := strings.Cut(rest, "\x1d")
	if a, ok := strings.CutPrefix(aliases, bashAliasMark); ok {
		aliases = bashAliases(a)
	}
	codexPath, rest, _ := strings.Cut(rest, "\x1d")
	traexPath, rest, _ := strings.Cut(rest, "\x1d")
	ompPath, rest, _ := strings.Cut(rest, "\x1d")
	shellNames, pathVar, _ := strings.Cut(rest, "\x1d")
	list = parseLaunchers(helper)
	seen := map[string]bool{}
	for _, l := range list {
		seen[l.Name] = true
	}
	claudeAliases, settings := parseAliasLaunchers(aliases)
	for _, l := range claudeAliases {
		if !seen[l.Name] {
			list = append(list, l)
		}
	}
	taken := maps.Clone(seen)
	for _, name := range strings.Fields(shellNames) {
		taken[name] = true
	}
	for _, rec := range strings.Split(aliases, "\x1e") {
		if name, _, ok := strings.Cut(rec, "\x1f"); ok {
			taken[name] = true
		}
	}
	pathList, pathSettings, scripts := pathLaunchers(pathVar, taken)
	list = append(list, pathList...)
	maps.Copy(settings, pathSettings)
	if len(list) == 0 {
		list = []proto.Launcher{{Name: "claude", Model: "默认"}}
	}
	codex = map[string][]string{}
	ls, words := parseCodexAliases(aliases)
	if strings.TrimSpace(codexPath) != "" && words["codex"] == nil {
		list = append(list, proto.Launcher{Name: "codex", Model: "默认", Agent: "codex", Source: "zshrc"})
		codex["codex"] = []string{"codex"}
	}
	for _, l := range ls {
		if !seen[l.Name] {
			list = append(list, l)
			codex[l.Name] = words[l.Name]
		}
	}
	if strings.TrimSpace(traexPath) != "" && !seen[traexAgent] && codex[traexAgent] == nil {
		list = append(list, proto.Launcher{Name: traexAgent, Model: "默认", Agent: traexAgent, Source: "zshrc"})
	}
	if strings.TrimSpace(ompPath) != "" && !seen["omp"] && codex["omp"] == nil {
		list = append(list, proto.Launcher{Name: "omp", Model: "默认", Agent: "omp", Source: "zshrc"})
	}
	return list, codex, settings, scripts
}

// cachedLaunchers never waits for the login shell: it returns the last list (possibly stale, nil before the first
// one) and starts a refresh in the background when the list is missing or old.
func (n *Node) cachedLaunchers() []proto.Launcher {
	launcherCache.Lock()
	defer launcherCache.Unlock()
	if (launcherCache.list == nil || time.Since(launcherCache.at) >= time.Minute) && !launcherCache.fetching {
		launcherCache.fetching = true
		go func() {
			_, _ = n.launchers(context.Background())
			launcherCache.Lock()
			launcherCache.fetching = false
			launcherCache.Unlock()
		}()
	}
	return launcherCache.list
}

// inferLauncher names the launcher of a claude Persona did not start (typed in a terminal), from the
// ANTHROPIC_BASE_URL / ANTHROPIC_MODEL the scan saw in its environment; "" when unknown.
func (n *Node) inferLauncher(id string) string {
	env, ok := n.sessionEnv(id)
	if !ok {
		return ""
	}
	return matchLauncher(n.cachedLaunchers(), env.BaseURL, env.Model)
}

// matchLauncher picks the launcher that exports this base URL and model. Several launchers can share one endpoint
// (and a launcher whose model is chosen at run time shows a placeholder such as "(claude-choose)"), so: the one
// launcher with both equal; else the one launcher with that base URL; else nothing, never a guess between several.
func matchLauncher(list []proto.Launcher, baseURL, model string) string {
	if baseURL == "" {
		return "" // the default endpoint: nothing to tell launchers apart by
	}
	var byURL, byBoth []string
	for _, l := range list {
		if l.BaseURL != baseURL {
			continue
		}
		byURL = append(byURL, l.Name)
		if l.Model == model {
			byBoth = append(byBoth, l.Name)
		}
	}
	if len(byBoth) == 1 {
		return byBoth[0]
	}
	if len(byURL) == 1 {
		return byURL[0]
	}
	return ""
}

var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

var assignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// parseAliasLaunchers reads launcherScript's alias records and keeps the aliases that start claude: after the
// variables set in front of it (`A=1 B=2 claude …`), through env / command / exec / noglob, by path or not, with
// `export A=1;` before it in the same alias. Their gateway and model come from ANTHROPIC_BASE_URL and ANTHROPIC_MODEL
// (or --model). Codex's are parseCodexAliases'. Sorted by name: the shell keeps no order. settings: the ones that
// bring their own --settings, by name (launcher_settings.go).
func parseAliasLaunchers(out string) (list []proto.Launcher, settings map[string]string) {
	settings = map[string]string{}
	for _, rec := range strings.Split(out, "\x1e") {
		words := strings.Split(rec, "\x1f")
		if len(words) < 2 {
			continue
		}
		name := words[0]
		if !nameRe.MatchString(name) || name == "claude" {
			continue
		}
		if l, s, ok := aliasLauncher(words[1 : len(words)-1]); ok {
			l.Name = name
			list = append(list, l)
			if s != "" {
				settings[name] = s
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, settings
}

// parseCodexAliases keeps the aliases that run Codex: `export` statements and one codex command (through env /
// command / exec / noglob, with variables set in front). How each runs is its words for codex-start: the variables,
// then the command and its arguments. Its model is -m / --model or -c model=…; an alias that does more (`cd x && codex`)
// is left out.
func parseCodexAliases(out string) ([]proto.Launcher, map[string][]string) {
	var list []proto.Launcher
	words := map[string][]string{}
	for _, rec := range strings.Split(out, "\x1e") {
		w := strings.Split(rec, "\x1f")
		if len(w) < 2 || !nameRe.MatchString(w[0]) {
			continue
		}
		argv, model, ok := codexAliasArgv(w[1 : len(w)-1])
		if !ok {
			continue
		}
		if model == "" {
			model = "默认"
		}
		list = append(list, proto.Launcher{Name: w[0], Model: model, Agent: "codex", Source: "zshrc"})
		words[w[0]] = argv
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, words
}

// codexAliasArgv reads an alias's words as one codex command; ok false when it is anything else.
func codexAliasArgv(words []string) (argv []string, model string, ok bool) {
	var exported []string
	found := false
	for len(words) > 0 {
		end := len(words)
		for i, w := range words {
			if w == ";" || w == "&&" || w == "||" || w == "|" || w == "&" {
				end = i
				break
			}
		}
		cmd := words[:end]
		if end < len(words) {
			words = words[end+1:]
		} else {
			words = nil
		}
		if len(cmd) == 0 {
			continue
		}
		if cmd[0] == "export" {
			for _, w := range cmd[1:] {
				if assignRe.MatchString(w) {
					exported = append(exported, w)
				}
			}
			continue
		}
		if found {
			return nil, "", false // more than one command
		}
		env := map[string]string{}
		i := commandIndex(cmd, env)
		if i < 0 || filepath.Base(cmd[i]) != "codex" {
			return nil, "", false
		}
		found = true
		argv = append([]string(nil), exported...)
		for _, w := range cmd[:i] {
			if assignRe.MatchString(w) {
				argv = append(argv, w)
			}
		}
		argv = append(argv, cmd[i:]...)
		for j := i + 1; j < len(cmd); j++ {
			switch {
			case (cmd[j] == "-m" || cmd[j] == "--model") && j+1 < len(cmd):
				model = cmd[j+1]
			case strings.HasPrefix(cmd[j], "--model="):
				model = strings.TrimPrefix(cmd[j], "--model=")
			case (cmd[j] == "-c" || cmd[j] == "--config") && j+1 < len(cmd) && strings.HasPrefix(cmd[j+1], "model="):
				model = strings.Trim(strings.TrimPrefix(cmd[j+1], "model="), `"'`)
			}
		}
	}
	return argv, model, found
}

// commandIndex is where the command of one simple command starts, past the variables set for it (put in env) and
// the wrappers that run what follows (env, with its -u / -i, command, exec, noglob, nocorrect); -1: there is none.
func commandIndex(cmd []string, env map[string]string) int {
	for i := 0; i < len(cmd); i++ {
		w := cmd[i]
		switch {
		case assignRe.MatchString(w):
			k, v, _ := strings.Cut(w, "=")
			env[k] = v
		case w == "env" || w == "command" || w == "exec" || w == "noglob" || w == "nocorrect":
		case w == "-u" && i > 0 && i+1 < len(cmd): // env -u NAME
			delete(env, cmd[i+1])
			i++
		case strings.HasPrefix(w, "-") && i > 0 && (cmd[i-1] == "env" || cmd[i-1] == "-i"): // env -i
		default:
			return i
		}
	}
	return -1
}

// aliasLauncher finds the claude command in an alias's words and what it is started with; settings is its own
// --settings (the last one, as Claude Code takes it), "" for none: launcher_settings.go.
func aliasLauncher(words []string) (l proto.Launcher, settings string, ok bool) {
	exported := map[string]string{}
	for len(words) > 0 {
		end := len(words)
		for i, w := range words {
			if w == ";" || w == "&&" || w == "||" || w == "|" || w == "&" {
				end = i
				break
			}
		}
		cmd := words[:end]
		if end < len(words) {
			words = words[end+1:]
		} else {
			words = nil
		}
		if len(cmd) > 0 && cmd[0] == "export" {
			for _, w := range cmd[1:] {
				if k, v, ok := strings.Cut(w, "="); ok && assignRe.MatchString(w) {
					exported[k] = v
				}
			}
			continue
		}
		env := map[string]string{}
		for k, v := range exported {
			env[k] = v
		}
		i := commandIndex(cmd, env)
		if i < 0 {
			continue
		}
		if filepath.Base(cmd[i]) != "claude" {
			continue
		}
		l = proto.Launcher{BaseURL: env["ANTHROPIC_BASE_URL"], Model: env["ANTHROPIC_MODEL"], Source: "zshrc"}
		for j := i + 1; j < len(cmd); j++ {
			if cmd[j] == "--model" && j+1 < len(cmd) && l.Model == "" {
				l.Model = cmd[j+1]
			} else if m, ok := strings.CutPrefix(cmd[j], "--model="); ok && l.Model == "" {
				l.Model = m
			}
			if cmd[j] == "--settings" && j+1 < len(cmd) {
				settings = cmd[j+1]
			} else if s, ok := strings.CutPrefix(cmd[j], "--settings="); ok {
				settings = s
			}
		}
		return l, settings, true
	}
	return proto.Launcher{}, "", false
}

// parseLaunchers parses `_claude_launchers` output: name padded to 26, base URL padded to 40, then model.
func parseLaunchers(out string) []proto.Launcher {
	var list []proto.Launcher
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 || !nameRe.MatchString(f[0]) {
			continue
		}
		l := proto.Launcher{Name: f[0]}
		rest := ""
		if len(line) > 26 {
			rest = line[26:]
		}
		if len(rest) > 40 {
			l.BaseURL = strings.TrimSpace(rest[:40])
			l.Model = strings.TrimSpace(rest[40:])
		} else {
			l.BaseURL = strings.TrimSpace(rest)
		}
		if l.BaseURL == "-" {
			l.BaseURL = ""
		}
		list = append(list, l)
	}
	return list
}

func (n *Node) dirs(path string) (*proto.DirList, error) {
	if path == "" || path == "~" {
		path = n.home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(n.home, path[2:])
	}
	path = filepath.Clean(path)
	ents, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	res := &proto.DirList{Path: path, Parent: filepath.Dir(path), Entries: []proto.DirEntry{}, Recent: n.recentDirs()}
	if path == "/" {
		res.Parent = ""
	}
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(path, name)
		if !e.IsDir() {
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			if st, err := os.Stat(full); err != nil || !st.IsDir() {
				continue
			}
		}
		if strings.Contains(full, "Mobile Documents") { // never touch iCloud Drive: it hangs on locked Macs
			continue
		}
		_, gerr := os.Stat(filepath.Join(full, ".git"))
		res.Entries = append(res.Entries, proto.DirEntry{Name: name, Path: full, IsGit: gerr == nil})
	}
	sort.Slice(res.Entries, func(i, j int) bool {
		return strings.ToLower(res.Entries[i].Name) < strings.ToLower(res.Entries[j].Name)
	})
	return res, nil
}

// dirsByUse is every working directory a session on this node is known to have run in, with when it was last used
// (its newest transcript); a running agent's without a transcript yet has the zero time. Nothing is read from disk.
func (n *Node) dirsByUse() map[string]time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	last := map[string]time.Time{}
	for _, m := range n.metas {
		if m.cwd != "" && m.mtime.After(last[m.cwd]) {
			last[m.cwd] = m.mtime
		}
	}
	for _, a := range n.agents {
		if _, ok := last[a.Cwd]; a.Cwd != "" && !ok {
			last[a.Cwd] = time.Time{} // running but no transcript yet: listed, oldest
		}
	}
	return last
}

func (n *Node) recentDirs() []proto.RecentDir {
	last := n.dirsByUse()
	var out []proto.RecentDir
	for p, t := range last {
		if identity.IsAssistantDir(p) {
			continue
		}
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			continue
		}
		out = append(out, proto.RecentDir{Path: p, Name: filepath.Base(p), LastUsed: t.UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	if len(out) > 15 {
		out = out[:15]
	}
	return out
}

// bgScan follows one transcript for background tasks. The quick meta only reads the tail, which in a long turn full
// of screenshots (base64 lines of a megabyte) holds just the last few items; this reads the whole file once and then
// only what was appended, parsing just the lines that can start or finish a background task.
type bgScan struct {
	mu      sync.Mutex
	off     int64
	meta    transcript.Meta
	pending bool // the first read of a big transcript is running in the background
}

// bgSyncLimit: up to this much unread transcript is scanned for background tasks right away. More than that (the
// first look at a big transcript, every busy one after a node restart) is read in the background, one file at a
// time: building the session list waited on reading gigabytes, and a machine showed no sessions for minutes.
var bgSyncLimit int64 = 4 << 20

// bgSlot lets one big first read run at a time.
var bgSlot = make(chan struct{}, 1)

var bgMarkers = [][]byte{[]byte("ScheduleWakeup"), []byte("Next wakeup scheduled"), []byte("CronCreate"), []byte("CronDelete"),
	[]byte("run_in_background"), []byte("in background with ID"), []byte("moved to the background"), []byte("Async agent launched"),
	[]byte("task-notification"), []byte(`"name":"Agent"`), []byte(`"name":"Task"`), []byte(`"TaskStop"`), []byte(`"KillShell"`),
	[]byte(`"name":"Workflow"`), []byte("Workflow launched in background"), []byte(`"name":"Monitor"`), []byte("Monitor started")}

func (n *Node) backgroundTasks(path string) (tasks []proto.BackgroundTask, loop *proto.LoopInfo, goal *proto.GoalInfo, pending bool) {
	n.mu.Lock()
	sc := n.bgScans[path]
	if sc == nil {
		sc = &bgScan{}
		n.bgScans[path] = sc
	}
	n.mu.Unlock()
	sc.mu.Lock()
	defer sc.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, nil, false
	}
	if st.Size() < sc.off { // rewritten
		sc.off, sc.meta = 0, transcript.Meta{}
	}
	if sc.pending {
		return nil, nil, nil, true
	}
	if st.Size()-sc.off > bgSyncLimit {
		sc.pending = true
		go n.scanBackgroundLater(path, sc, st.Size())
		return nil, nil, nil, true
	}
	scanBackground(f, &sc.off, &sc.meta, st.Size())
	for _, t := range sc.meta.Background {
		// Older ones are surely gone (claude restarted); a Monitor past its expiry has stopped.
		if time.Since(t.StartedAt) < 24*time.Hour && (t.ExpiresAt == nil || time.Now().Before(*t.ExpiresAt)) {
			tasks = append(tasks, t)
		}
	}
	if l := sc.meta.Loop; l != nil {
		// A wakeup loop whose next tick is long overdue died with its claude; cron jobs live until deleted.
		if l.Kind == "cron" || l.NextAt.IsZero() || time.Since(l.NextAt) < 15*time.Minute {
			c := *l
			loop = &c
		}
	}
	if g := sc.meta.Goal; g != nil {
		c := *g
		goal = &c
	}
	return tasks, loop, goal, false
}

// bgWorkflows is the workflow runs the last backgroundTasks scan of path found, and their script files.
func (n *Node) bgWorkflows(path string) ([]proto.Workflow, map[string]string) {
	n.mu.Lock()
	sc := n.bgScans[path]
	n.mu.Unlock()
	if sc == nil {
		return nil, nil
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.pending || len(sc.meta.Workflows) == 0 {
		return nil, nil
	}
	return slices.Clone(sc.meta.Workflows), maps.Clone(sc.meta.WorkflowScripts)
}

// bgPlan is the plan the last backgroundTasks scan of path found (transcript.Meta.Plan), nil while a big first read runs.
func (n *Node) bgPlan(path string) *proto.PlanInfo {
	n.mu.Lock()
	sc := n.bgScans[path]
	n.mu.Unlock()
	if sc == nil {
		return nil
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.pending || sc.meta.Plan == nil {
		return nil
	}
	p := *sc.meta.Plan
	return &p
}

// scanBackground reads the marker lines between *off and size into m, streamed (the first look at a 300 MB
// transcript once held all of it in memory). A last line not complete yet is read next time.
func scanBackground(f *os.File, off *int64, m *transcript.Meta, size int64) {
	r := bufio.NewReaderSize(io.NewSectionReader(f, *off, size-*off), 256<<10)
	at, long := *off, false
	for {
		line, err := r.ReadSlice('\n')
		at += int64(len(line))
		if err == bufio.ErrBufferFull {
			long = true
			continue
		}
		if err != nil {
			return
		}
		// api-request blobs repeat everything; skip them
		// A plan tool's result line does not name the tool: it is the line with the call's id.
		if !long && len(line) <= 64<<10 && (containsAny(line, bgMarkers) || containsAny(line, transcript.GoalMarkers) || containsAny(line, transcript.UltracodeMarkers) ||
			containsAny(line, transcript.PlanMarkers) || containsAny(line, m.PlanPending())) {
			transcript.ParseLine(0, bytes.TrimSuffix(line, []byte("\n")), m)
		}
		long = false
		*off = at
	}
}

// scanBackgroundLater does a big first read off the list's path, then hands the result to the next caller.
func (n *Node) scanBackgroundLater(path string, sc *bgScan, size int64) {
	bgSlot <- struct{}{}
	defer func() { <-bgSlot }()
	var off int64
	var m transcript.Meta
	if f, err := os.Open(path); err == nil {
		scanBackground(f, &off, &m, size)
		f.Close()
	}
	sc.mu.Lock()
	sc.off, sc.meta, sc.pending = off, m, false
	sc.mu.Unlock()
}

func containsAny(b []byte, subs [][]byte) bool {
	for _, s := range subs {
		if bytes.Contains(b, s) {
			return true
		}
	}
	return false
}
