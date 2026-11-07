package egress

import (
	"bufio"
	"io"
	"net"
	"os"
	"path/filepath"
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
