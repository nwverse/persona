package tmux

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeTmux writes a tmux stand-in running body as a shell script and returns its path and the file it logs calls to.
func fakeTmux(t *testing.T, body string) (bin, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tmux is a shell script")
	}
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nCALLS=" + calls + "\nDIR=" + dir + "\necho \"$@\" >> \"$CALLS\"\n" + body
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func readCalls(t *testing.T, calls string) []string {
	t.Helper()
	b, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// bufferOf is the -b argument of a logged call.
func bufferOf(call string) string {
	f := strings.Fields(call)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "-b" {
			return f[i+1]
		}
	}
	return ""
}

// A paste that fails (the pane closed after the load) must not leave the typed text in the user's tmux server.
func TestTmuxSendTextDeletesTheBufferWhenThePasteFails(t *testing.T) {
	bin, calls := fakeTmux(t, `case "$1" in
load-buffer) cat > /dev/null ;;
paste-buffer) echo "can't find pane: %9" >&2; exit 1 ;;
esac
`)
	err := Client{Bin: bin}.SendText(context.Background(), "%9", "my secret answer")
	if err == nil || !strings.Contains(err.Error(), "paste-buffer") {
		t.Fatalf("SendText = %v, want the paste error", err)
	}
	log := readCalls(t, calls)
	if len(log) != 3 || !strings.HasPrefix(log[0], "load-buffer") || !strings.HasPrefix(log[1], "paste-buffer") ||
		!strings.HasPrefix(log[2], "delete-buffer") {
		t.Fatalf("calls = %q, want load-buffer, paste-buffer, delete-buffer", log)
	}
	if name := bufferOf(log[0]); name == "" || bufferOf(log[2]) != name {
		t.Fatalf("delete-buffer %q does not name the loaded buffer %q", bufferOf(log[2]), name)
	}
}

// A paste the context ends still gets the buffer deleted: the cleanup has a context of its own.
func TestTmuxSendTextDeletesTheBufferAfterTheContextEnds(t *testing.T) {
	bin, calls := fakeTmux(t, `case "$1" in
load-buffer) cat > /dev/null ;;
paste-buffer) exec sleep 30 ;;
esac
`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := (Client{Bin: bin}).SendText(ctx, "%1", "hello"); err == nil {
		t.Fatal("SendText with a paste outliving its context = nil error")
	}
	log := readCalls(t, calls)
	if len(log) != 3 || !strings.HasPrefix(log[2], "delete-buffer") || bufferOf(log[2]) != bufferOf(log[0]) {
		t.Fatalf("calls = %q, want the loaded buffer deleted after the context ended", log)
	}
}

// Two Persona processes on one tmux server must not share buffer names: one's load would overwrite the other's text.
func TestTmuxSendTextBufferNamesAreUniqueAndCarryThePid(t *testing.T) {
	bin, calls := fakeTmux(t, `[ "$1" = load-buffer ] && cat > /dev/null
exit 0
`)
	c := Client{Bin: bin}
	for range 2 {
		if err := c.SendText(context.Background(), "%1", "hello"); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, call := range readCalls(t, calls) {
		if strings.HasPrefix(call, "load-buffer") {
			names = append(names, bufferOf(call))
		}
	}
	if len(names) != 2 || names[0] == names[1] {
		t.Fatalf("buffer names = %q, want two distinct ones", names)
	}
	pid := "-" + strconv.Itoa(os.Getpid()) + "-"
	for _, n := range names {
		if !strings.HasPrefix(n, "persona-") || !strings.Contains(n, pid) {
			t.Fatalf("buffer name %q does not carry the pid %d", n, os.Getpid())
		}
	}
}

// Empty text types nothing: no tmux runs at all.
func TestTmuxSendTextEmptyIsANoOp(t *testing.T) {
	bin, calls := fakeTmux(t, "exit 1\n")
	if err := (Client{Bin: bin}).SendText(context.Background(), "%1", ""); err != nil {
		t.Fatalf("SendText(\"\") = %v", err)
	}
	if log := readCalls(t, calls); len(log) != 0 {
		t.Fatalf("calls = %q, want none", log)
	}
}

// killHolder ends the background child a fake left holding the output pipe.
func killHolder(t *testing.T, pidFile string) {
	t.Cleanup(func() {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
		}
	})
}

// A tmux wrapper whose child keeps stdout open must not hold Capture past its context.
func TestTmuxCaptureIsBoundedWhenAChildHoldsStdout(t *testing.T) {
	bin, _ := fakeTmux(t, `sleep 30 &
echo $! > "$DIR/holder"
wait
`)
	killHolder(t, filepath.Join(filepath.Dir(bin), "holder"))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Client{Bin: bin}.Capture(ctx, "%1", 0, false)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Capture took %v, want it bounded by the context plus the wait delay", elapsed)
	}
	if err == nil {
		t.Fatal("Capture after the context ended = nil error")
	}
}

// A tmux that exits 0 while a child it left behind still holds stdout succeeded: its output is the answer.
func TestTmuxCaptureSucceedsWhenAChildOutlivesTheExit(t *testing.T) {
	bin, _ := fakeTmux(t, `echo 'the screen'
sleep 30 &
echo $! > "$DIR/holder"
`)
	killHolder(t, filepath.Join(filepath.Dir(bin), "holder"))
	start := time.Now()
	out, err := Client{Bin: bin}.Capture(context.Background(), "%1", 0, false)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Capture took %v, want it bounded by the wait delay", elapsed)
	}
	if err != nil || out != "the screen\n" {
		t.Fatalf("Capture = %q, %v, want the screen and no error", out, err)
	}
}

// A node started inside a pane of another server (tmux -L work) carries that server in its own TMUX, and tmux without
// -S runs against the server TMUX names. A pane on the default server must still be reached on the default server, so
// the node's TMUX is not passed on; a pane on a named server is reached through -S.
func TestTmuxDefaultServerIgnoresTheNodesOwnTMUX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tmux is a shell script")
	}
	t.Setenv("TMUX", "/tmp/other/work,1,0")
	t.Setenv("TMUX_PANE", "%7")
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "tmux")
	script := `#!/bin/sh
echo "TMUX=$TMUX TMUX_PANE=$TMUX_PANE args=$*" >> ` + calls + `
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := (Client{Bin: bin}).Capture(ctx, "%11", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := (Client{Bin: bin, Socket: "/s"}).Capture(ctx, "%11", 0, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("calls = %q, want 2", b)
	}
	if want := "TMUX= TMUX_PANE= args=capture-pane -p -t %11"; lines[0] != want {
		t.Errorf("default server call = %q, want %q", lines[0], want)
	}
	if !strings.Contains(lines[1], "args=-S /s capture-pane -p -t %11") {
		t.Errorf("named server call = %q, want -S /s", lines[1])
	}
}
