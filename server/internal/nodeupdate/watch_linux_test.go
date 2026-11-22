//go:build linux

package nodeupdate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/procinfo"
	"golang.org/x/sys/unix"
)

func TestAliveIsRejectsZombieWithoutPSFallback(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	start, err := processStart(pid)
	if err != nil || start == "" {
		t.Fatalf("running child's start time: %q, %v", start, err)
	}
	precise := osproc.Identify(pid).Start
	if precise == "" {
		t.Fatal("running child has no exact identity")
	}

	// ps also lists zombies. Keep returning the recorded start, and record
	// every invocation so a false result alone cannot hide a fallback call.
	log := filepath.Join(t.TempDir(), "ps-calls")
	t.Setenv("NODE_UPDATE_FAKE_PS_LOG", log)
	t.Setenv("NODE_UPDATE_FAKE_PS_START", start)
	onlyPS(t, "#!/bin/sh\nprintf 'called\\n' >>\"$NODE_UPDATE_FAKE_PS_LOG\"\nprintf '%s\\n' \"$NODE_UPDATE_FAKE_PS_START\"\n")
	assertNoPS := func(t *testing.T) {
		t.Helper()
		if _, err := os.Stat(log); !os.IsNotExist(err) {
			t.Fatalf("unexpected ps fallback: %v", err)
		}
	}

	t.Run("live-exact", func(t *testing.T) {
		if !aliveIs(pid, "different portable start", precise) {
			t.Fatal("matching exact identity did not recognize the running child")
		}
		if aliveIs(pid, start, precise+"0") {
			t.Fatal("mismatched exact identity recognized the running child")
		}
		assertNoPS(t)
	})
	t.Run("live-legacy", func(t *testing.T) {
		if !aliveIs(pid, start, "") {
			t.Fatal("legacy start did not recognize the running child")
		}
		if aliveIs(pid, "different portable start", "") {
			t.Fatal("mismatched legacy start recognized the running child")
		}
		if _, err := os.Stat(log); err != nil {
			t.Fatalf("legacy start did not use ps: %v", err)
		}
	})
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// Wait for exit without reaping: the state is now deterministically Z.
	var info unix.Siginfo
	err = unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
	for errors.Is(err, unix.EINTR) {
		err = unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	if fields, err := procinfo.Stat(pid); err != nil || len(fields) == 0 || fields[0] != "Z" {
		t.Fatalf("child is not a zombie: %v, %v", fields, err)
	}
	for _, tc := range []struct {
		name    string
		precise string
	}{
		{"exact", precise},
		{"legacy", ""},
	} {
		t.Run("zombie-"+tc.name, func(t *testing.T) {
			if aliveIs(pid, start, tc.precise) {
				t.Error("zombie still counts as alive")
			}
			if err := killRecorded(pid, start, tc.precise); err != nil {
				t.Errorf("killing a zombie should be a no-op: %v", err)
			}
			assertNoPS(t)
		})
	}
	_ = cmd.Wait()
	reaped = true
	for _, precise := range []string{precise, ""} {
		if aliveIs(pid, start, precise) {
			t.Error("reaped child still counts as alive")
		}
	}
	assertNoPS(t)
}
