//go:build unix

package devices

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

const (
	fakeSSHProxyParentEnv = "PERSONA_FAKE_SSH_PROXY_PARENT"
	fakeSSHProxyChildEnv  = "PERSONA_FAKE_SSH_PROXY_CHILD"
)

// TestFakeSSHProxyParent is not a test: as a subprocess it stands in for ssh, starts a ProxyCommand-like child, and
// stays connected without answering the node handshake. The child deliberately does not inherit the parent's pipes.
func TestFakeSSHProxyParent(t *testing.T) {
	pidFile := os.Getenv(fakeSSHProxyParentEnv)
	if pidFile == "" {
		t.Skip("helper process")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFakeSSHProxyChild$")
	cmd.Env = append(os.Environ(), fakeSSHProxyChildEnv+"=1")
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		panic(err)
	}
	select {}
}

// TestFakeSSHProxyChild is not a test: it is the ProxyCommand child left behind when only its ssh parent is killed.
func TestFakeSSHProxyChild(t *testing.T) {
	if os.Getenv(fakeSSHProxyChildEnv) != "1" {
		t.Skip("helper process")
	}
	select {}
}

// Abandoning an ssh handshake must take its ProxyCommand down too. Killing only ssh leaves that child adopted by the
// service manager; for a relay-backed ProxyCommand it keeps competing with the next connection for relay replies.
func TestAbandonedSSHHandshakeKillsProxyCommand(t *testing.T) {
	m := newMachine(newTestDevices(t), MachineConfig{ID: "box", Kind: "ssh"})
	pidFile := filepath.Join(t.TempDir(), "proxy.pid")
	m.nodeCmd = func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFakeSSHProxyParent$")
		cmd.Env = append(os.Environ(), fakeSSHProxyParentEnv+"="+pidFile)
		return cmd
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.connect(ctx) }()

	proxyPID := waitForProxyPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(proxyPID, syscall.SIGKILL) })
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceling the handshake returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceling the handshake did not close ssh")
	}

	for deadline := time.Now().Add(2 * time.Second); ; {
		err := syscall.Kill(proxyPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ProxyCommand process %d outlived the abandoned ssh connection", proxyPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForProxyPID(t *testing.T, name string) int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; {
		data, err := os.ReadFile(name)
		if err == nil {
			pid, convErr := strconv.Atoi(string(data))
			if convErr != nil || pid <= 0 {
				t.Fatalf("invalid ProxyCommand pid %q: %v", data, convErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal(fmt.Errorf("fake ssh did not start its ProxyCommand: %w", err))
		}
		time.Sleep(20 * time.Millisecond)
	}
}
