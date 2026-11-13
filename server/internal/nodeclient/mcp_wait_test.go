package nodeclient

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Interim lines are consumed only as wait updates; the one real response is returned unchanged. A caller without a
// receiver remains compatible, while an unknown enum reaches a receiver as unknown and is never reflected onward.
func TestDialAtReadsMCPWaitUpdates(t *testing.T) {
	for _, withReceiver := range []bool{false, true} {
		t.Run(map[bool]string{false: "without receiver", true: "with receiver"}[withReceiver], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node.sock")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
					serverDone <- err
					return
				}
				_, err = conn.Write([]byte("{\"wait_kind\":\"approval\"}\n{\"wait_kind\":\"a future value\"}\n{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}\n"))
				serverDone <- err
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			received := make(chan proto.MCPWaitKind, 2)
			if withReceiver {
				ctx = WithMCPWait(ctx, func(kind proto.MCPWaitKind) { received <- kind })
			}
			line, err := DialAt(ctx, path, Request{Kind: "mcp.call", WaitUpdates: true})
			if err != nil {
				t.Fatal(err)
			}
			var result proto.ToolResult
			if err := json.Unmarshal(line, &result); err != nil || len(result.Content) != 1 || result.Content[0].Text != "done" {
				t.Fatalf("final response = %s (%v)", line, err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
			if !withReceiver {
				select {
				case kind := <-received:
					t.Fatalf("no receiver was installed, got %q", kind)
				default:
				}
				return
			}
			for _, want := range []proto.MCPWaitKind{proto.MCPWaitApproval, proto.MCPWaitUnknown} {
				select {
				case got := <-received:
					if got != want {
						t.Fatalf("kind = %q, want %q", got, want)
					}
				case <-ctx.Done():
					t.Fatal("wait update was not received")
				}
			}
		})
	}
}
