package node

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/transcript"
)

// A program running Claude Code's command line once (`claude -p`: entrypoint "sdk-cli") writes a transcript like any
// session's; a script doing it all day filled the offline list with them and pushed the user's own sessions out of its 80
// (#3828). The offline list leaves such a run out, as a running one is left to its program (claudebg.go) and as Codex's
// program runs are (codex_hierarchy.go programRun), unless Persona has a hand in it: the assistant's, a health space's or
// a guest's conversations (every one started under Persona's state directory), a session the node keeps state for, a
// fork, a kept one. What decides is the last entrypoint in the transcript's last bytes; none there (a huge last line)
// lists it, as before. The Agent SDKs' runs ("sdk-ts", "sdk-py") are listed: interactive hosts run the user's sessions
// through them (Paseo).

// oneShotTail is how much of a transcript's end is read for its last entrypoint: Claude Code writes one on every line.
const oneShotTail = 8 << 10

type entrypointMark struct {
	size  int64
	mtime time.Time
	ep    string
}

var lastEntrypoints = struct {
	sync.Mutex
	byPath map[string]entrypointMark
}{byPath: map[string]entrypointMark{}}

// lastEntrypoint is the entrypoint of the last line of transcript p that has one within its last oneShotTail bytes ("" when
// none does); read again only when the file changed.
func lastEntrypoint(p string, st os.FileInfo) string {
	lastEntrypoints.Lock()
	m, ok := lastEntrypoints.byPath[p]
	lastEntrypoints.Unlock()
	if ok && m.size == st.Size() && m.mtime.Equal(st.ModTime()) {
		return m.ep
	}
	ep := readLastEntrypoint(p, st.Size())
	lastEntrypoints.Lock()
	if len(lastEntrypoints.byPath) > 20000 { // gone transcripts' marks: start over rather than keep them forever
		clear(lastEntrypoints.byPath)
	}
	lastEntrypoints.byPath[p] = entrypointMark{st.Size(), st.ModTime(), ep}
	lastEntrypoints.Unlock()
	return ep
}

func readLastEntrypoint(p string, size int64) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	off := max(size-oneShotTail, 0)
	buf := make([]byte, size-off)
	n, _ := f.ReadAt(buf, off)
	buf = buf[:n]
	key := []byte(`"entrypoint":"`)
	i := bytes.LastIndex(buf, key)
	if i < 0 {
		return ""
	}
	rest := buf[i+len(key):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return string(rest[:j])
}

// personaProjectDirs are the names Claude Code gives the projects of sessions started under Persona's state directory
// (the assistant's, a health space's, a guest's, any manager's): its path with every character but letters and digits
// as "-", then "-" and the subdirectory.
func (n *Node) personaProjectDirs() []string {
	var out []string
	for _, dir := range []string{identity.DataDir(n.home), filepath.Join(n.home, ".persona"), filepath.Join(n.home, compat.StateDir)} {
		out = append(out, nonAlnum.ReplaceAllString(dir, "-")+"-")
	}
	return out
}

// oneShotRun: the transcript at p (project directory base) is a program's one-shot run of Claude Code that the offline
// list leaves out (see above).
func (n *Node) oneShotRun(id, base, p string, st os.FileInfo, prefixes []string) bool {
	if !transcript.ClaudeOneShot(lastEntrypoint(p, st)) {
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(base, prefix) {
			return false // started under Persona's state directory: its assistant, a health space, a guest
		}
	}
	if n.state != nil {
		if _, ok := n.state.get(id); ok {
			return false // the node started or drove it
		}
	}
	return true
}
