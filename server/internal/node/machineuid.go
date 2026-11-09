package node

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// The computer's own id (docs/server/mesh.md "Machine identity"): two hubs that both reach this computer tell it is the same one by
// it, as the ids hub.json gives machines are each hub's own names. Made on the node's first run and kept in
// ~/.persona/machine-uid (0600). A virtual machine cloned with its home directory carries the same file: `personad node
// reset-uid` makes a new one there.

const machineUIDFile = "machine-uid"

var uidPattern = lazyre.New(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// MachineUIDPath is where the id lives, under dir (~/.persona).
func MachineUIDPath(dir string) string { return filepath.Join(dir, machineUIDFile) }

// LoadMachineUID reads the id, making it the first time. A file that does not hold one (edited by hand, cut short) is
// replaced: an id nobody can match is worse than a new one.
func LoadMachineUID(dir string) (string, error) {
	b, err := os.ReadFile(MachineUIDPath(dir))
	if err == nil {
		if uid := strings.TrimSpace(string(b)); uidPattern.MatchString(uid) {
			return uid, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return ResetMachineUID(dir)
}

// ResetMachineUID writes a new id and returns it.
func ResetMachineUID(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	uid := newUUID()
	path := MachineUIDPath(dir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(uid+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return uid, nil
}
