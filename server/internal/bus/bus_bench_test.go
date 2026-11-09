package bus

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/coder/websocket"
)

// benchSizes are a computer's session list as the bus carries it: 50 and 200 sessions.
var benchSizes = []int{39 << 10, 260 << 10}

func benchLine(size int) []byte {
	return append(bytes.Repeat([]byte("s"), size-1), '\n')
}

// BenchmarkConnRead reads messages of a session list's size through Conn (the slot reading each manager line).
func BenchmarkConnRead(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(fmt.Sprintf("%dK", size>>10), func(b *testing.B) {
			ctx := context.Background()
			cws, sws := wsPair(b, false)
			c := NewConn(ctx, cws)
			msg := benchLine(size)[:size-1]
			go func() {
				for range b.N {
					if sws.Write(ctx, websocket.MessageText, msg) != nil {
						return
					}
				}
			}()
			buf := make([]byte, 64<<10)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for lines := 0; lines < b.N; {
				n, err := c.Read(buf)
				if err != nil {
					b.Fatal(err)
				}
				lines += bytes.Count(buf[:n], []byte{'\n'})
			}
		})
	}
}

// BenchmarkConnWrite writes lines of a session list's size through Conn (the manager telling a list).
func BenchmarkConnWrite(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(fmt.Sprintf("%dK", size>>10), func(b *testing.B) {
			ctx := context.Background()
			cws, sws := wsPair(b, false)
			c := NewConn(ctx, cws)
			go func() {
				for {
					_, r, err := sws.Reader(ctx)
					if err != nil {
						return
					}
					_, _ = io.Copy(io.Discard, r)
				}
			}()
			line := benchLine(size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := c.Write(line); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
