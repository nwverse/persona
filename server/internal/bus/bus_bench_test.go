package bus

import (
	"bufio"
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
			sws.SetReadLimit(MaxMessage)
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

// BenchmarkEcho sends a line of a session list's size across the bus and back, compressed (permessage-deflate with
// context takeover, as a manager elsewhere has it) or not (the hub's own manager, Client.Uncompressed).
func BenchmarkEcho(b *testing.B) {
	for _, compress := range []bool{true, false} {
		for _, size := range benchSizes {
			b.Run(fmt.Sprintf("compressed=%v/%dK", compress, size>>10), func(b *testing.B) {
				ctx := context.Background()
				cws, sws := wsPair(b, compress)
				c, s := NewConn(ctx, cws), NewConn(ctx, sws)
				go func() { // the other end writes back each line it reads, whole, as an rpc peer writes
					r := bufio.NewReaderSize(s, 2*size)
					for {
						l, err := r.ReadSlice('\n')
						if err != nil {
							return
						}
						if _, err := s.Write(l); err != nil {
							return
						}
					}
				}()
				line := append(sessionsLike(size), '\n')
				buf := make([]byte, 64<<10)
				b.SetBytes(int64(len(line)))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if _, err := c.Write(line); err != nil {
						b.Fatal(err)
					}
					for got := 0; got < len(line); {
						n, err := c.Read(buf)
						if err != nil {
							b.Fatal(err)
						}
						got += n
					}
				}
			})
		}
	}
}

// sessionsLike is JSON of about size bytes shaped like a session list (so it compresses as one does).
func sessionsLike(size int) []byte {
	b := []byte(`{"sessions":[`)
	for i := 0; len(b) < size; i++ {
		b = fmt.Appendf(b, `{"session_id":"%08x-load","title":"Task %d","cwd":"/work/project-%d","status":"done","rev":%d},`,
			uint32(i)*2654435761, i, i%12, i*7)
	}
	return append(b[:len(b)-1], "]}"...)
}
