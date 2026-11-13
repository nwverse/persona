//go:build !linux

package osproc

// These platforms do not distinguish an unreadable identity from a missing
// process. Preserve their portable fallback when no exact identity is available.
func identifyKnown(pid int) (ProcIdentity, bool) {
	id := identify(pid)
	return id, id.Start != ""
}
