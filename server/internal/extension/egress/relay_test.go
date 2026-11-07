package egress

import (
	"bufio"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// RelayUnix (#5413): every connection on the extension's socket is joined to the target, dialed then; the target
// closing ends the extension's end too; Unregister takes no more connections.
func TestProxyRelayUnix(t *testing.T) {
	dir := shortTempDir(t)
	target, sock := filepath.Join(dir, "node.sock"), filepath.Join(dir, "ext.sock")
	p := &Proxy{}
	if err := p.RelayUnix("notes", sock, target); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("its socket: %v %v", fi, err)
	}
	// No target yet: the connection is taken and closed.
	if c, err := net.Dial("unix", sock); err == nil {
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := c.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("without a target: %v", err)
		}
		c.Close()
	}
	ln, err := net.Listen("unix", target)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { // echo one line, then hang up
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err == nil {
					_, _ = io.WriteString(c, "node: "+line)
				}
			}()
		}
	}()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, "hello\n"); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(c)
	if line, err := r.ReadString('\n'); err != nil || line != "node: hello\n" {
		t.Fatalf("through the relay: %q %v", line, err)
	}
	if _, err := r.ReadByte(); err != io.EOF {
		t.Fatalf("the node hung up, the extension's end stays: %v", err)
	}
	p.Unregister("notes")
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		t.Fatal("a connection after Unregister")
	}
}

// An extension's runtime holds at most runtimeConnsPerListener relayed connections open: the ones over that are closed
// without reaching the target, a connection that ends frees its place, and Unregister closes the rest.
func TestProxyRelayUnixBoundsConnections(t *testing.T) {
	dir := shortTempDir(t)
	target, sock := filepath.Join(dir, "node.sock"), filepath.Join(dir, "ext.sock")
	ln, err := net.Listen("unix", target)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var reached atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			reached.Add(1)
			go func() { // echo until the relay hangs up
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	p := &Proxy{}
	defer p.Close()
	if err := p.RelayUnix("notes", sock, target); err != nil {
		t.Fatal(err)
	}
	dial := func() net.Conn {
		t.Helper()
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	// relayed says whether c reaches the target (an echo) or was closed by the relay. A closed one fails however the
	// platform says so (EOF, ECONNRESET, EPIPE, ENOTCONN on macOS…); only no answer at all is neither.
	relayed := func(c net.Conn) bool {
		t.Helper()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		b := make([]byte, 1)
		_, err := c.Write([]byte{'x'})
		if err == nil {
			_, err = io.ReadFull(c, b)
		}
		if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
			t.Fatalf("neither relayed nor closed: %v", err)
		}
		return err == nil && b[0] == 'x'
	}
	const over = 4
	var open []net.Conn
	closed := 0
	for range runtimeConnsPerListener + over {
		c := dial()
		if relayed(c) {
			open = append(open, c)
		} else {
			closed++
		}
	}
	if len(open) != runtimeConnsPerListener || closed != over || reached.Load() != runtimeConnsPerListener {
		t.Fatalf("relayed %d, closed %d, reached the target %d; want %d, %d, %d",
			len(open), closed, reached.Load(), runtimeConnsPerListener, over, runtimeConnsPerListener)
	}
	// One ends: the next connection gets its place (once the relay has seen it end).
	_ = open[0].Close()
	open = open[1:]
	deadline := time.Now().Add(5 * time.Second)
	for {
		c := dial()
		if relayed(c) {
			open = append(open, c)
			break
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatal("a connection that ended kept its place")
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Unregister("notes")
	for _, c := range open {
		if relayed(c) {
			t.Fatal("a relayed connection outlived Unregister")
		}
	}
}
