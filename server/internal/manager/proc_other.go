//go:build !unix

package manager

import "os/exec"

// killGroup: only the program itself is killed here.
func killGroup(*exec.Cmd) {}
