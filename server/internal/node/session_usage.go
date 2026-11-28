package node

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/tokenuse"
)

// usage.sessions (#12936): what sessions of this computer used, from their records here, for the hub's usage per to-do
// (assistant/todo_usage.go). A Claude Code session is its transcript and every file under <session>/subagents (its
// subagents, and its workflow runs' agents in workflows/<run>/); a Codex or traex thread is its rollout. Records are
// read with the tokenuse parsers, so a Claude request counts once by its message.id, at the most its lines said; a
// request copied into more than one file of the call (a fork's history, a subagent's copy) counts once, in the first
// file read (each session's own transcript first). Each file's requests are kept by path, so asking again reads only
// what the file gained (sessionUsageCache).

// sessionUsageMaxIDs bounds one call's sessions (a to-do links a handful).
const sessionUsageMaxIDs = 200

// sessionUsageRequest is one request of a file: its key ("" when it has none), when it started, model and tokens.
type sessionUsageRequest struct {
	key   string
	day   string
	model string
	agent string
	t     tokenuse.Tokens
}

// sessionUsageFile is what a file gave so far: read up to off with its parser, its requests in order.
type sessionUsageFile struct {
	off    int64
	parser tokenuse.Parser
	reqs   []sessionUsageRequest
	byKey  map[string]int
	cur    int // the request a More record adds to; -1: a repeat, not counted
}

// sessionUsageCache keeps each file's requests by path (the zero value is ready).
type sessionUsageCache struct {
	mu    sync.Mutex
	files map[string]*sessionUsageFile
}

// sessionUsageCacheMax bounds the cached files; past it the cache starts again (a file is then read whole again).
const sessionUsageCacheMax = 512

// sessionUsageSource is a file of a session to read: whose, which subagent ("" the session's own) and its meta.
type sessionUsageSource struct {
	path, session, subagent, kind, title string
	parser                               func() tokenuse.Parser
}

func init() { nodeRPC(proto.MethodSessionUsage, (*Node).sessionUsage) }

func (n *Node) sessionUsage(_ context.Context, p proto.SessionUsageParams) (proto.SessionUsage, error) {
	out := proto.SessionUsage{Sessions: []proto.SessionUsageFound{}, Rows: []proto.SessionUsageRow{}}
	seen := map[string]bool{}
	type rowKey struct{ session, subagent, agent, model, day string }
	rows := map[rowKey]*proto.SessionUsageRow{}
	var order []rowKey
	counted := map[string]bool{}
	for _, id := range p.SessionIDs {
		if id == "" || seen[id] || len(seen) >= sessionUsageMaxIDs {
			continue
		}
		seen[id] = true
		srcs := n.sessionUsageSources(id)
		found := proto.SessionUsageFound{SessionID: id, Found: len(srcs) > 0}
		for _, src := range srcs {
			reqs := n.sessionUsageCache.read(src.path, src.parser)
			for _, r := range reqs {
				if r.key != "" {
					if counted[r.key] {
						continue
					}
					counted[r.key] = true
				}
				if found.Agent == "" && src.subagent == "" {
					found.Agent = r.agent
				}
				k := rowKey{id, src.subagent, r.agent, r.model, r.day}
				row := rows[k]
				if row == nil {
					row = &proto.SessionUsageRow{SessionID: id, Subagent: src.subagent, SubagentType: src.kind,
						SubagentTitle: src.title, Agent: r.agent, Model: r.model, Day: r.day}
					rows[k] = row
					order = append(order, k)
				}
				row.Input += r.t.Input
				row.CacheWrite += r.t.CacheWrite
				row.CacheRead += r.t.CacheRead
				row.Output += r.t.Output
				row.Requests++
			}
		}
		out.Sessions = append(out.Sessions, found)
	}
	for _, k := range order {
		out.Rows = append(out.Rows, *rows[k])
	}
	return out, nil
}

// sessionUsageSources are the files of session id on this computer: its transcript, then (a Claude Code one) its
// subagents' and workflow agents' in name order. None when the computer has no record of it.
func (n *Node) sessionUsageSources(id string) []sessionUsageSource {
	if !validID(id) {
		return nil
	}
	path := n.transcriptPath(id)
	if path == "" {
		return nil
	}
	if strings.HasPrefix(filepath.Base(path), "rollout-") {
		agent := tokenuse.Codex
		if home := n.traexHome(); home != "" && strings.HasPrefix(path, filepath.Clean(home)+string(filepath.Separator)) {
			agent = tokenuse.Traex
		}
		return []sessionUsageSource{{path: path, session: id, parser: func() tokenuse.Parser { return tokenuse.CodexParser(agent) }}}
	}
	if !strings.HasSuffix(path, ".jsonl") {
		return nil
	}
	claude := func() tokenuse.Parser { return tokenuse.ClaudeParser() }
	out := []sessionUsageSource{{path: path, session: id, parser: claude}}
	dir := subagentsDirOf(path)
	if dir == "" || filepath.Base(filepath.Dir(path)) == "subagents" {
		return out
	}
	add := func(file, sub string) {
		meta := subagentUsageMeta(strings.TrimSuffix(file, ".jsonl") + ".meta.json")
		out = append(out, sessionUsageSource{path: file, session: id, subagent: sub, kind: meta.AgentType, title: meta.Description, parser: claude})
	}
	agents, _ := filepath.Glob(filepath.Join(dir, "agent-*.jsonl"))
	sort.Strings(agents)
	for _, f := range agents {
		add(f, strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "agent-"), ".jsonl"))
	}
	runs, _ := filepath.Glob(filepath.Join(dir, "workflows", "*", "agent-*.jsonl"))
	sort.Strings(runs)
	for _, f := range runs {
		run := filepath.Base(filepath.Dir(f))
		add(f, run+"/"+strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "agent-"), ".jsonl"))
	}
	return out
}

// subagentUsageMeta is a subagent's meta (Claude Code's agent-<id>.meta.json), its description cut short; empty
// when there is none.
func subagentUsageMeta(path string) subagentMeta {
	m, _ := subagentMetaCached(path)
	if r := []rune(m.Description); len(r) > 200 {
		m.Description = string(r[:200])
	}
	return m
}

// read returns the requests of the file at path, reading only what it gained since the last read (all of it again
// when it shrank: rewritten). A file that cannot be opened gives none.
func (c *sessionUsageCache) read(path string, parser func() tokenuse.Parser) []sessionUsageRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := os.Stat(path)
	if err != nil {
		delete(c.files, path)
		return nil
	}
	if c.files == nil || len(c.files) >= sessionUsageCacheMax && c.files[path] == nil {
		c.files = map[string]*sessionUsageFile{}
	}
	f := c.files[path]
	if f == nil || st.Size() < f.off {
		f = &sessionUsageFile{parser: parser(), byKey: map[string]int{}, cur: -1}
		c.files[path] = f
	}
	if st.Size() > f.off {
		f.readFrom(path, st.Size())
	}
	return f.reqs
}

// readFrom reads the file's whole lines from f.off up to size.
func (f *sessionUsageFile) readFrom(path string, size int64) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	if _, err := fh.Seek(f.off, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(io.LimitReader(fh, size-f.off), 256<<10)
	var long bool // inside a line longer than usageMaxLine: skipped
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			if !long {
				buf = append(buf, chunk...)
				if len(buf) > usageMaxLine {
					long, buf = true, nil
				}
			}
			f.off += int64(len(chunk))
			continue
		}
		if err != nil { // the last, unfinished line: read again next time
			return
		}
		f.off += int64(len(chunk))
		line := chunk
		if len(buf) > 0 {
			line = append(buf, chunk...)
		}
		if !long {
			f.line(line)
		}
		long, buf = false, nil
	}
}

// line counts one line: a new request, more of the one before it, or a repeat of one counted.
func (f *sessionUsageFile) line(line []byte) {
	rec, ok := f.parser.Line(line)
	if !ok {
		return
	}
	if rec.More {
		if f.cur >= 0 {
			f.reqs[f.cur].t = f.reqs[f.cur].t.Plus(rec.Tokens)
		}
		return
	}
	if rec.Key != "" {
		if _, dup := f.byKey[rec.Key]; dup {
			f.cur = -1
			return
		}
	}
	model := rec.Model
	if model == "" {
		model = "unknown"
	}
	day := ""
	if !rec.Time.IsZero() {
		day = rec.Time.UTC().Format("2006-01-02")
	}
	f.reqs = append(f.reqs, sessionUsageRequest{key: rec.Key, day: day, model: model, agent: rec.Agent, t: rec.Tokens})
	f.cur = len(f.reqs) - 1
	if rec.Key != "" {
		f.byKey[rec.Key] = f.cur
	}
}
