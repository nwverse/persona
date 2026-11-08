//go:build !linux && !darwin && !windows

package osproc

// identify gives no exact start time on other platforms: the caller's portable
// checks (a second-resolution start time, ownership) stay the whole identity.
func identify(int) ProcIdentity { return ProcIdentity{} }

// KillIfStill still refuses without an identity; with one it verifies and kills
// by pid, the way the callers without an exact identity always did.
func (id ProcIdentity) KillIfStill(pid int) error {
	if pid < 1 || id.Start == "" {
		return nil
	}
	return id.killUnpinned(pid)
}
