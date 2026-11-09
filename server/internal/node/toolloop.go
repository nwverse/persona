package node

import (
	"bytes"
	"os"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// toolLoopScan keeps the incremental detector and its file position together.
type toolLoopScan struct {
	detector transcript.LoopDetector
	offset   int64
	size     int64
	mtime    time.Time
}

// toolLoopOf is called only for running sessions. Reads are incremental and
// bounded; a gap too large to reconstruct safely starts a fresh tail scan.
func (n *Node) toolLoopOf(id string) *proto.ToolLoop {
	path := n.transcriptPath(id)
	if path == "" {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	n.mu.Lock()
	m := n.metas[path]
	n.mu.Unlock()
	if m.toolLoop.size != st.Size() || !m.toolLoop.mtime.Equal(st.ModTime()) {
		updated := readToolLoop(path, st, m)
		n.mu.Lock()
		current := n.metas[path]
		if current.toolLoop.size == m.toolLoop.size && current.toolLoop.mtime.Equal(m.toolLoop.mtime) {
			current.toolLoop = updated.toolLoop
			n.metas[path] = current
		}
		m = current
		n.mu.Unlock()
	}
	return m.toolLoop.detector.Candidate()
}

func readToolLoop(path string, st os.FileInfo, m quickMeta) quickMeta {
	off := m.toolLoop.offset
	d := m.toolLoop.detector.Clone()
	rewritten := st.Size() < m.toolLoop.size || st.Size() == m.toolLoop.size && !st.ModTime().Equal(m.toolLoop.mtime)
	if rewritten || st.Size()-off > quickTail {
		d = transcript.LoopDetector{}
		off = max(st.Size()-quickTail, 0)
	}
	f, err := os.Open(path)
	if err != nil {
		m.toolLoop.detector = transcript.LoopDetector{}
		return m
	}
	defer f.Close()
	buf := make([]byte, st.Size()-off)
	n, _ := f.ReadAt(buf, off)
	buf = buf[:n]
	if off > 0 && (m.toolLoop.offset != off || rewritten) {
		// A tail scan may begin inside a line.
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			m.toolLoop = toolLoopScan{detector: d, offset: st.Size(), size: st.Size(), mtime: st.ModTime()}
			return m
		}
		off += int64(i + 1)
		buf = buf[i+1:]
	}
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		d.Line(buf[:i])
		off += int64(i + 1)
		buf = buf[i+1:]
	}
	m.toolLoop = toolLoopScan{detector: d, offset: off, size: st.Size(), mtime: st.ModTime()}
	return m
}
