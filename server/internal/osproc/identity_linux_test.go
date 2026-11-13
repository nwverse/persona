//go:build linux

package osproc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/procinfo"
	"golang.org/x/sys/unix"
)

func TestIdentifyZombie(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise Stat's shared parser with spaces and unmatched parentheses in comm.
	program := filepath.Join(t.TempDir(), "a ) ( child")
	if err := os.Symlink(sleep, program); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(program, "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	pid := child.Process.Pid
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	id, known := IdentifyKnown(pid)
	if !known || id.Start == "" || Identify(pid) != id || !id.StillIs(pid) {
		t.Fatalf("running child identity = %+v, known = %v", id, known)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// Observe exit without reaping: the child remains a zombie until Wait.
	var info unix.Siginfo
	err = unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
	for errors.Is(err, unix.EINTR) {
		err = unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if fields, err := procinfo.Stat(pid); err != nil || len(fields) == 0 || fields[0] != "Z" {
		t.Fatalf("not a zombie: %v, %v", fields, err)
	}
	checkGone := func(t *testing.T) {
		t.Helper()
		got, known := IdentifyKnown(pid)
		if got != (ProcIdentity{}) || !known || Identify(pid) != (ProcIdentity{}) {
			t.Fatalf("exited child identity = %+v, known = %v", got, known)
		}
		if id.StillIs(pid) || got.StillIs(pid) {
			t.Fatal("exited child still matches an identity")
		}
		if err := id.KillIfStill(pid); err != nil {
			t.Fatalf("killing an exited child: %v", err)
		}
	}
	t.Run("zombie", checkGone)
	_ = child.Wait()
	reaped = true
	t.Run("reaped", checkGone)
}

func TestIdentifyFromStat(t *testing.T) {
	live := strings.Fields("S " + strings.Repeat("0 ", 18) + "123")
	for _, tc := range []struct {
		name    string
		fields  []string
		err     error
		selfErr error
		start   string
		known   bool
	}{
		{name: "live", fields: live, start: "ticks:123", known: true},
		{name: "zombie", fields: []string{"Z"}, known: true},
		{name: "dead", fields: []string{"X"}, known: true},
		{name: "empty"},
		{name: "short", fields: []string{"S"}},
		{name: "unreadable", err: os.ErrPermission},
		{name: "malformed", err: errors.New("no name")},
		{name: "missing-pid", err: os.ErrNotExist, known: true},
		{name: "missing-procfs", err: os.ErrNotExist, selfErr: os.ErrNotExist},
		{name: "masked-procfs", err: os.ErrNotExist, selfErr: os.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, known := identifyFromStat(0, func(pid int) ([]string, error) {
				if pid == os.Getpid() {
					return live, tc.selfErr
				}
				return tc.fields, tc.err
			})
			if id.Start != tc.start || known != tc.known {
				t.Fatalf("identity = %+v, known = %v; want %q, %v", id, known, tc.start, tc.known)
			}
		})
	}
	for _, pid := range []int{-1, 0} {
		if id, known := IdentifyKnown(pid); id != (ProcIdentity{}) || !known {
			t.Fatalf("invalid pid %d: %+v, known = %v", pid, id, known)
		}
	}
}
