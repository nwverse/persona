package node

import (
	"bytes"
	"os"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

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
	if m.toolLoopSize != st.Size() || !m.toolLoopMtime.Equal(st.ModTime()) {
		updated := readToolLoop(path, st, m)
		n.mu.Lock()
		current := n.metas[path]
		if current.toolLoopSize == m.toolLoopSize && current.toolLoopMtime.Equal(m.toolLoopMtime) {
			current.toolLoop, current.toolLoopOffset = updated.toolLoop, updated.toolLoopOffset
			current.toolLoopSize, current.toolLoopMtime = updated.toolLoopSize, updated.toolLoopMtime
			n.metas[path] = current
		}
		m = current
		n.mu.Unlock()
	}
	return m.toolLoop.Candidate()
}

func readToolLoop(path string, st os.FileInfo, m quickMeta) quickMeta {
	off := m.toolLoopOffset
	d := m.toolLoop.Clone()
	if st.Size() < m.toolLoopSize || st.Size() == m.toolLoopSize && !st.ModTime().Equal(m.toolLoopMtime) || st.Size()-off > quickTail {
		d = transcript.LoopDetector{}
		off = max(st.Size()-quickTail, 0)
	}
	f, err := os.Open(path)
	if err != nil {
		m.toolLoop = transcript.LoopDetector{}
		return m
	}
	defer f.Close()
	buf := make([]byte, st.Size()-off)
	n, _ := f.ReadAt(buf, off)
	buf = buf[:n]
	if off > 0 && (m.toolLoopOffset != off || st.Size() < m.toolLoopSize || st.Size() == m.toolLoopSize && !st.ModTime().Equal(m.toolLoopMtime)) {
		// A tail scan may begin inside a line.
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			m.toolLoop = d
			m.toolLoopOffset = st.Size()
			m.toolLoopSize = st.Size()
			m.toolLoopMtime = st.ModTime()
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
	m.toolLoop, m.toolLoopOffset, m.toolLoopSize, m.toolLoopMtime = d, off, st.Size(), st.ModTime()
	return m
}
