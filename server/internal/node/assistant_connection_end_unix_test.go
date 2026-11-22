//go:build !windows

package node

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// connectionEndProcs starts, as a node without a resident keeper runs them, a conversation that ignores SIGTERM, one
// that ends on it, and a warm spare.
func connectionEndProcs(t *testing.T, n *Node) (stubborn, polite, spare *assistantProc) {
	t.Helper()
	k := newAssistantKeeper() // keptShell's bookkeeping only: n has no keeper
	stubborn = keptShell(t, k, "11111111-2222-3333-4444-000000000031", `trap '' TERM; echo ready; exec sleep 60`)
	polite = keptShell(t, k, "11111111-2222-3333-4444-000000000032", `echo ready; exec cat`)
	spare = keptShell(t, k, "11111111-2222-3333-4444-000000000033", `echo ready; exec cat`)
	n.mu.Lock()
	n.procs[stubborn.id], n.procs[polite.id] = stubborn, polite
	n.spare = spare
	n.mu.Unlock()
	return stubborn, polite, spare
}

// allExited fails unless every process in procs has exited.
func allExited(t *testing.T, what string, procs ...*assistantProc) {
	t.Helper()
	for _, p := range procs {
		select {
		case <-p.exited:
		default:
			t.Errorf("%s: %s still running", what, p.id)
		}
	}
}

// Without a resident keeper the end of a connection is the end of its conversations: reapAssistants stops them and
// the warm spare and returns only once they have exited, the one ignoring SIGTERM killed (#4259). Left to stop()'s
// own SIGKILL goroutine, it would go on after the node exits, beside the process the next node starts for it.
func TestConnectionEndStopsConversationsWithoutResidentKeeper(t *testing.T) {
	shortenKeeperStop(t, 200*time.Millisecond, 10*time.Second)
	n := assistantNode(t)
	stubborn, polite, spare := connectionEndProcs(t, n)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.reapAssistants(ctx)

	allExited(t, "reapAssistants returned", stubborn, polite, spare)
	if sig := exitSignal(t, stubborn); sig != syscall.SIGKILL {
		t.Errorf("the conversation ignoring SIGTERM ended by %v, want SIGKILL", sig)
	}
	if sig := exitSignal(t, polite); sig == syscall.SIGKILL {
		t.Error("the conversation that ends on SIGTERM was killed")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.procs) != 0 || n.spare != nil {
		t.Fatalf("still listed: %d conversations, spare %v", len(n.procs), n.spare != nil)
	}
}

// Serve of a node without a resident keeper (`node --stdio`, which exits as soon as Serve returns) returns only once
// its conversations have exited: the hub going away (EOF) used to leave them to a goroutine the exit cut short.
func TestServeWithoutResidentKeeperReturnsAfterItsConversations(t *testing.T) {
	shortenKeeperStop(t, 200*time.Millisecond, 10*time.Second)
	home := shortHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CODEX_CONFIG_DIR", "")
	t.Setenv("HERDR_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PATH", os.Getenv("PATH")) // New puts ~/.local/bin and the like in front
	n, err := New("test")
	if err != nil {
		t.Fatal(err)
	}
	n.cdirs.scan = func() map[int]procEnv { return nil }
	n.codices.scan = func() []codexProcess { return nil }
	n.codices.checked = func() ([]codexProcess, bool) { return nil, true }
	stubborn, polite, spare := connectionEndProcs(t, n)

	done := make(chan error, 1)
	go func() { done <- n.Serve(context.Background(), strings.NewReader(""), io.Discard) }() // the hub went away at once
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Serve did not return after EOF")
	}
	allExited(t, "Serve returned", stubborn, polite, spare)
}
