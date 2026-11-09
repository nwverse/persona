package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
)

// SplitEnv set to 0 keeps on personad what would run a program of its own in its place: the hub's and the built-in
// manager's systemd units that hub setup and manager install --builtin write (which then keep it), the hub image's
// supervisor, and the node of the hub's computer (docs/server/server.md "Programs"). A Mac's agents run personad
// either way.
const SplitEnv = "PERSONA_SPLIT_PROGRAMS"

// SplitOff says SplitEnv is 0 in this process's environment.
func SplitOff() bool { return strings.TrimSpace(identity.Getenv(SplitEnv)) == "0" }

// ProgramBeside is the program name in bin's directory, with bin's extension (persona-hub.exe beside personad.exe).
func ProgramBeside(bin, name string) string {
	return filepath.Join(filepath.Dir(bin), name+filepath.Ext(bin))
}

// BuildOf is what the executable at path says to `version -v`: its version, channel and build number.
func BuildOf(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version", "-v")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// SplitBeside is the program name beside bin when it can run in bin's place: it is there and says the same to
// `version -v` as bin (version, BuildOf when nil). Else path is "", and note says why one there was passed over (""
// when there is none).
func SplitBeside(ctx context.Context, bin, name string, version func(context.Context, string) (string, error)) (path, note string) {
	if version == nil {
		version = BuildOf
	}
	p := ProgramBeside(bin, name)
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return "", ""
	}
	theirs, err := version(ctx, p)
	if err != nil {
		return "", fmt.Sprintf("%s does not run (%v)", p, err)
	}
	mine, err := version(ctx, bin)
	switch {
	case err != nil:
		return "", fmt.Sprintf("%s version -v failed (%v)", bin, err)
	case theirs == "" || theirs != mine:
		return "", fmt.Sprintf("%s is another build (%q, %s %q)", p, firstLine(theirs), filepath.Base(bin), firstLine(mine))
	}
	return p, ""
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
