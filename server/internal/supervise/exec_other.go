//go:build !unix

package supervise

import (
	"os"
	"os/exec"
)

// execHub runs `bin args…` and waits for it: there is no exec in place here.
func execHub(bin string, args, env []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// ownGroup and killGroup: no process groups here.
func ownGroup(*exec.Cmd)  {}
func killGroup(*exec.Cmd) {}
