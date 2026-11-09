package link

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// SystemName is what this computer's system calls it, as a person named it: the Mac's Computer Name ("Alex's
// MacBook Pro"), else the host name without a .local suffix (Windows' computer name, Linux's host name); "" when
// there is none. Pairing hands it to the hub (Pair), which names a computer that has no name of its own yet after it
// (docs/operations/link.md "The computer's name"); nothing here changes it.
func SystemName() string {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", "ComputerName").Output()
		cancel()
		if name := clipText(string(out), MaxMachineName); err == nil && name != "" {
			return name
		}
	}
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return clipText(strings.TrimSuffix(host, ".local"), MaxMachineName)
}
