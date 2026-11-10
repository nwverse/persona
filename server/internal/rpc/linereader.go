package rpc

import (
	"bufio"
	"bytes"
	"io"
	"sync"
)

// lineStart is the buffer a lineReader starts with and goes back to, the one Serve's bufio.Scanner started with.
const lineStart = 64 << 10

// lineReader reads Serve's lines exactly as a bufio.Scanner with ScanLines did: the same limit, the same lines (the
// newline and one carriage return before it dropped, an unterminated last line given, empty lines kept) and the same
// errors (bufio.ErrTooLong, bufio.ErrBadReadCount, io.ErrNoProgress, the reader's own; nil at io.EOF). Only its memory
// differs: a Scanner kept the buffer its longest line grew for the connection's life (256 KiB per computer sending its
// session list, 32 MiB after one 21 MB request body), while a lineReader goes back to its first buffer once a long
// line is through, and hands the grown one to grownBufs for the next long line on any connection.
// FuzzLineReaderIsAScanner compares the two.
type lineReader struct {
	r          io.Reader
	max        int
	first      []byte // the first buffer, kept
	buf        []byte // first, or a grown one while a long line needs it
	start, end int    // the unread part of buf
	err        error
	token      []byte
}

func newLineReader(r io.Reader, maximum int) *lineReader {
	return newLineReaderSize(r, min(lineStart, maximum), maximum)
}

func newLineReaderSize(r io.Reader, size, maximum int) *lineReader {
	b := make([]byte, size)
	return &lineReader{r: r, max: maximum, first: b, buf: b}
}

// Bytes is the line Scan found, valid until the next Scan.
func (s *lineReader) Bytes() []byte { return s.token }

// Err is what ended the input, nil for io.EOF.
func (s *lineReader) Err() error {
	if s.err == io.EOF {
		return nil
	}
	return s.err
}

func (s *lineReader) setErr(err error) {
	if s.err == nil || s.err == io.EOF {
		s.err = err
	}
}

// Scan finds the next line, step for step as bufio.Scanner.Scan with ScanLines does.
func (s *lineReader) Scan() bool {
	// The last line goes now, as a Scanner's does when the next split finds none: kept while Scan waits for input, it
	// would keep the buffer a long line grew (Bytes is valid only until the next Scan).
	s.token = nil
	for {
		if s.end > s.start || s.err != nil {
			data := s.buf[s.start:s.end]
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				s.token = dropCR(data[:i])
				s.start += i + 1
				return true
			}
			if s.err != nil && len(data) > 0 {
				s.token = dropCR(data)
				s.start = s.end
				return true
			}
		}
		if s.err != nil {
			s.token, s.start, s.end = nil, 0, 0
			s.shrink()
			return false
		}
		// A long line is through once what is left fits the first buffer again.
		if len(s.buf) > len(s.first) && s.end-s.start < len(s.first) {
			s.shrink()
		}
		if s.start > 0 && (s.end == len(s.buf) || s.start > len(s.buf)/2) {
			copy(s.buf, s.buf[s.start:s.end])
			s.end -= s.start
			s.start = 0
		}
		if s.end == len(s.buf) {
			const maxInt = int(^uint(0) >> 1)
			if len(s.buf) >= s.max || len(s.buf) > maxInt/2 {
				s.token = nil
				s.setErr(bufio.ErrTooLong)
				return false
			}
			grown := takeGrown(min(len(s.buf)*2, s.max))
			copy(grown, s.buf[s.start:s.end])
			if len(s.buf) > len(s.first) {
				giveGrown(s.buf)
			}
			s.buf = grown
			s.end -= s.start
			s.start = 0
		}
		for loop := 0; ; {
			n, err := s.r.Read(s.buf[s.end:len(s.buf)])
			if n < 0 || len(s.buf)-s.end < n {
				s.setErr(bufio.ErrBadReadCount)
				break
			}
			s.end += n
			if err != nil {
				s.setErr(err)
				break
			}
			if n > 0 {
				break
			}
			loop++
			if loop > 100 { // bufio's maxConsecutiveEmptyReads
				s.setErr(io.ErrNoProgress)
				break
			}
		}
	}
}

// shrink moves the unread part back into the first buffer and lets the grown one go.
func (s *lineReader) shrink() {
	if len(s.buf) == len(s.first) {
		return
	}
	n := copy(s.first, s.buf[s.start:s.end])
	giveGrown(s.buf)
	s.buf, s.start, s.end = s.first, 0, n
}

func dropCR(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\r' {
		return b[:len(b)-1]
	}
	return b
}

// grownBufs keeps the buffers long lines grew to, 128 KiB to 1 MiB (a pool per size), for the next long line on any
// connection: lines that long come often (a computer's session list), and only a few are being read at once. A larger
// buffer is left to the GC, as a pool's are at every other GC.
var grownBufs [4]sync.Pool

func grownPool(size int) *sync.Pool {
	for i := range grownBufs {
		if size == lineStart<<(i+1) {
			return &grownBufs[i]
		}
	}
	return nil
}

func takeGrown(size int) []byte {
	if p := grownPool(size); p != nil {
		if b, ok := p.Get().(*[]byte); ok {
			return *b
		}
	}
	return make([]byte, size)
}

func giveGrown(b []byte) {
	if p := grownPool(len(b)); p != nil {
		p.Put(&b)
	}
}
