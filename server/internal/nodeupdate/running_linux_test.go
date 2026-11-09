package nodeupdate

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// holdExecutable waits for the fixture to acknowledge execution and keeps it
// alive until cleanup closes stdin and reaps the child.
func holdExecutable(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, path, "fixture-hold")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		err := cmd.Wait()
		cancel()
		if err != nil {
			t.Errorf("reap fixture: %v", err)
		}
	})
	ready := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(stdout, ready); err != nil || string(ready) != "ready\n" {
		t.Fatalf("fixture readiness %q: %v", ready, err)
	}
}

func assertExecutableBusy(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("direct write"), 0755); !errors.Is(err, syscall.ETXTBSY) {
		t.Fatalf("direct write to running executable: got %v, want ETXTBSY", err)
	}
}

func TestRunningExecutableReplacement(t *testing.T) {
	t.Run("commit and rollback", func(t *testing.T) {
		m, data, _ := testManager(t)
		oldHash := hash(t, m.exe)
		holdExecutable(t, m.exe)
		assertExecutableBusy(t, m.exe)
		id := receive(t, m, data, spec(data, "next"))
		if _, err := m.Commit(context.Background(), id, nil); err != nil {
			t.Fatal(err)
		}
		if hash(t, m.exe) != spec(data, "next").SHA256 {
			t.Fatal("commit did not install the new executable")
		}
		holdExecutable(t, m.exe)
		assertExecutableBusy(t, m.exe)
		p, err := loadPending(markerPath(m.exe))
		if err != nil {
			t.Fatal(err)
		}
		p.Deadline = time.Now().Add(-time.Second)
		if err := savePending(p); err != nil {
			t.Fatal(err)
		}
		fresh := &Manager{exe: m.exe, version: "next"}
		if err := fresh.recover(); err != nil {
			t.Fatal(err)
		}
		if hash(t, m.exe) != oldHash || !fresh.RestartRequired() {
			t.Fatal("rollback did not restore the old executable and require restart")
		}
	})

	for _, method := range []string{"recover", "watch"} {
		t.Run("external replacement/"+method, func(t *testing.T) {
			m, data, _ := testManager(t)
			id := receive(t, m, data, spec(data, "next"))
			if _, err := m.Commit(context.Background(), id, nil); err != nil {
				t.Fatal(err)
			}
			p, err := loadPending(markerPath(m.exe))
			if err != nil {
				t.Fatal(err)
			}
			p.Deadline = time.Now().Add(-time.Second)
			if err := savePending(p); err != nil {
				t.Fatal(err)
			}
			holdExecutable(t, m.exe)
			assertExecutableBusy(t, m.exe)
			deployed := []byte("independent deployment")
			replaceExternally(t, m.exe, deployed)
			fresh := &Manager{exe: m.exe, version: "next"}
			if method == "recover" {
				if err := fresh.recover(); err != nil {
					t.Fatal(err)
				}
			} else if err := Watch(context.Background(), markerPath(m.exe), id, nil); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(m.exe)
			if err != nil || string(got) != string(deployed) || fresh.RestartRequired() {
				t.Fatalf("external deployment was not preserved: %q, %v", got, err)
			}
			if _, err := os.Stat(markerPath(m.exe)); !os.IsNotExist(err) {
				t.Fatalf("external deployment left pending marker: %v", err)
			}
			if _, err := os.Stat(p.Backup); !os.IsNotExist(err) {
				t.Fatalf("external deployment left backup: %v", err)
			}
		})
	}
}
