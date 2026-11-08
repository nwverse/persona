package node

import (
	"errors"
	"io/fs"
	"os"
	"sync"
	"time"
)

// metaPruneEvery: how often scanOffline drops what the node keeps for transcripts that are gone (pruneGoneMetas).
const metaPruneEvery = time.Minute

// goneSize marks the light quick meta pruneGoneMetas leaves for a transcript that is gone. No file has that size, so a
// file made again at the path is read again in full.
const goneSize = -1

var metaPruneAt struct {
	sync.Mutex
	at  time.Time
	key string // whose prune it is: another node's (tests run many in one process) prunes its own, as offlineCache
}

// maybePruneGoneMetas runs pruneGoneMetas at most every metaPruneEvery. files are the transcripts scanOffline globbed:
// they exist, so only the paths it did not glob (live sessions' and others read on their own) are stat'ed.
func (n *Node) maybePruneGoneMetas(files []string) {
	key := n.offlineCacheKey()
	metaPruneAt.Lock()
	due := key != metaPruneAt.key || time.Since(metaPruneAt.at) >= metaPruneEvery
	if due {
		metaPruneAt.at, metaPruneAt.key = time.Now(), key
	}
	metaPruneAt.Unlock()
	if !due {
		return
	}
	present := make(map[string]bool, len(files))
	for _, p := range files {
		present[p] = true
	}
	n.pruneGoneMetas(present)
}

// metaStamp tells whether a quick meta was written again: every read sets readAt, a rewind clears it with mtime.
type metaStamp struct {
	size          int64
	mtime, readAt time.Time
}

func stampOf(m quickMeta) metaStamp { return metaStamp{m.size, m.mtime, m.readAt} }

func (s metaStamp) same(o metaStamp) bool {
	return s.size == o.size && s.mtime.Equal(o.mtime) && s.readAt.Equal(o.readAt)
}

// pruneGoneMetas drops what n.metas and n.bgScans hold for transcripts that no longer exist: a long-running node kept a
// full quick meta and background-task scan (title, last words, background tasks, workflows: several KB, more for long
// turns) for every transcript it had ever read. present are paths known
// to exist, which are not stat'ed. A gone path keeps a light meta with just its cwd and mtime: dirsByUse lists the
// directories of every transcript the node has seen (recent dirs, workDirs), and that list stays as it was.
func (n *Node) pruneGoneMetas(present map[string]bool) {
	check := n.pruneCandidates(present)
	var gone []string
	for p := range check { // off n.mu: a slow disk must not stall every session list
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			gone = append(gone, p)
		}
	}
	n.dropGoneMetas(check, gone)
}

// pruneCandidates are the full metas not in present, with how each looked.
func (n *Node) pruneCandidates(present map[string]bool) map[string]metaStamp {
	n.mu.Lock()
	defer n.mu.Unlock()
	check := map[string]metaStamp{}
	for p, m := range n.metas {
		if !present[p] && m.size != goneSize {
			check[p] = stampOf(m)
		}
	}
	return check
}

// dropGoneMetas makes gone paths' metas light and drops their scans, leaving any read again since check was taken.
func (n *Node) dropGoneMetas(check map[string]metaStamp, gone []string) {
	if len(gone) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, p := range gone {
		m, ok := n.metas[p]
		if !ok || !stampOf(m).same(check[p]) {
			continue // read again since the stat: the file may be back
		}
		n.metas[p] = quickMeta{size: goneSize, cwd: m.cwd, mtime: m.mtime}
		delete(n.bgScans, p)
	}
}

// forgetBgScan drops path's background-task scan when its file is gone, if it is still sc (a caller may have made a new
// one since). A file made again at the path is then scanned from the start, not from the old one's offset. The caller
// may hold sc.mu: n.mu is never held while taking a bgScan's lock.
func (n *Node) forgetBgScan(path string, sc *bgScan) {
	n.mu.Lock()
	if n.bgScans[path] == sc {
		delete(n.bgScans, path)
	}
	n.mu.Unlock()
}
