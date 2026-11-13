package proc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector/serve"
	"github.com/nwverse/persona/server/internal/rpc"
)

// child is one run of the program.
type child struct {
	cmd    *exec.Cmd
	peer   *rpc.Peer
	stdin  io.Closer
	served chan error
}

func (ch *child) err(start error) error {
	if start != nil || ch == nil {
		return start
	}
	return ch.peer.Err()
}

// stop closes the program's stdin (it exits when the hub goes away) and kills it if it does not.
func (ch *child) stop() {
	_ = ch.stdin.Close()
	ch.peer.Close(rpc.ErrClosed)
	done := make(chan struct{})
	go func() { _ = ch.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = ch.cmd.Process.Kill()
		<-done
	}
}

// spawn checks the program and starts it, sending init.
func (c *Conn) spawn(ctx context.Context) (*child, error) {
	if _, err := os.Stat(c.cfg.Path); err != nil {
		return nil, fmt.Errorf("没有找到 %s 的程序（%s）", c.cfg.Name, c.cfg.Path)
	}
	if c.cfg.Version != "" {
		vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		vcmd := exec.CommandContext(vctx, c.cfg.Path, "version", "-v")
		vcmd.Env = append(os.Environ(), c.cfg.Env...)
		out, err := vcmd.Output()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("%s 的程序运行不了：%v", c.cfg.Name, err)
		}
		if got := strings.TrimSpace(string(out)); got != strings.TrimSpace(c.cfg.Version) {
			return nil, fmt.Errorf("%s 的程序和 hub 不是同一个版本（%s / %s）", c.cfg.Name, firstLine(got), firstLine(c.cfg.Version))
		}
	}
	cmd := exec.Command(c.cfg.Path, c.cfg.Args...)
	cmd.Env = append(os.Environ(), c.cfg.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s 的程序起不来：%v", c.cfg.Name, err)
	}
	go c.logLines(stderr)
	peer := rpc.New("h", stdin)
	peer.OnRequest = c.fromProgram
	peer.OnEvent = c.onEvent
	ch := &child{cmd: cmd, peer: peer, stdin: stdin, served: make(chan error, 1)}
	go func() { ch.served <- peer.Serve(context.Background(), stdout) }()
	ictx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	settings := c.cfg.Settings
	if c.cfg.SettingsNow != nil {
		settings = c.cfg.SettingsNow()
	}
	var man serve.Manifest
	if err := peer.Call(ictx, serve.MInit, serve.InitParams{Protocol: serve.Protocol, Config: settings}, &man); err != nil {
		ch.stop()
		return nil, fmt.Errorf("%s 的程序没准备好：%v", c.cfg.Name, err)
	}
	if man.Protocol != serve.Protocol {
		ch.stop()
		return nil, fmt.Errorf("%s 的程序用的协议版本是 %d，hub 要 %d", c.cfg.Name, man.Protocol, serve.Protocol)
	}
	if man.ID != c.cfg.ID {
		ch.stop()
		return nil, fmt.Errorf("the program says it is %q, not %q", man.ID, c.cfg.ID)
	}
	if d := c.cfg.Manifest; len(d.Capabilities) > 0 && !sameManifest(d, man.Of()) {
		c.cfg.Logf("connector %s: the program declares %v, the hub registered it with %v (the hub's is used)", c.cfg.ID, man.Of(), d)
	}
	c.mu.Lock()
	c.man = man
	c.mu.Unlock()
	return ch, nil
}

// logLineMost is the longest line of a program's log kept whole; a longer one is cut (the rest of it dropped).
const logLineMost = 64 << 10

// logLines keeps what the program writes to stderr (its own log: it keeps secrets and messages out of it), until
// stderr ends. Nothing else reads it: stopping early (a line too long) would leave the program stuck on its next
// write once the pipe is full (#3959).
func (c *Conn) logLines(r io.Reader) {
	br := bufio.NewReaderSize(r, logLineMost)
	for {
		b, err := br.ReadSlice('\n')
		line := strings.TrimRight(string(b), "\r\n")
		if errors.Is(err, bufio.ErrBufferFull) {
			line += "…"
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
		}
		if len(b) > 0 {
			c.cfg.Logf("connector %s: %s", c.cfg.ID, line)
		}
		if err != nil {
			return
		}
	}
}

func firstLine(s string) string {
	line, _, _ := bytes.Cut([]byte(s), []byte("\n"))
	return string(line)
}
