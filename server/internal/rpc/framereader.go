package rpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// frameReader reads a framed connection's lines (lanes.go) for Serve, as a lineReader reads plain ones: Scan, Bytes,
// Owned (a line put together from several frames is in a buffer of its own), Err (nil at io.EOF on a frame's edge).
// Acknowledgements are handed to the writer as they come (acked); before it waits for more input, the reader asks the
// writer to acknowledge the bulk frames it has read (ack), so a reader held up by its own side stops the other side's
// bulk once its window is full.
type frameReader struct {
	r          io.Reader
	max        int
	buf        []byte
	start, end int
	err        error
	seq        uint64            // the last data frame read
	bulkRead   bool              // a bulk frame was read since the last ack asked for
	partial    map[uint32][]byte // lines in progress, by lane<<16 | slot
	token      []byte
	owned      bool
	ack        func(seq uint64)       // ask for an acknowledgement of every frame read through seq
	acked      func(seq uint64) error // the other side read every frame this side sent through seq
	middle     func()                 // the middle of a line already past pingFrameMiddleBytes came in (PingActivity)
	now        func() time.Time
}

// maxPartialLines bounds the lines a reader puts together at once: a writer has at most laneSlots per lane.
const maxPartialLines = int(laneCount) * laneSlots

// frameReadBuf holds two whole frames: a read rarely stops at a frame's edge.
const frameReadBuf = 2 * frameMax

func newFrameReader(r io.Reader, maximum int) *frameReader {
	return &frameReader{r: r, max: maximum, buf: make([]byte, frameReadBuf), partial: map[uint32][]byte{}, now: time.Now}
}

func (s *frameReader) Bytes() []byte { return s.token }
func (s *frameReader) Owned() bool   { return s.owned }

func (s *frameReader) Err() error {
	if s.err == io.EOF {
		return nil
	}
	return s.err
}

func (s *frameReader) setErr(err error) {
	if s.err == nil || s.err == io.EOF {
		s.err = err
	}
}

// Scan finds the next whole line.
func (s *frameReader) Scan() bool {
	s.token, s.owned = nil, false
	for {
		if s.end-s.start >= frameHeader {
			h, err := parseHead(s.buf[s.start:])
			if err != nil {
				s.setErr(err)
				return false
			}
			if s.end-s.start >= frameHeader+h.size {
				payload := s.buf[s.start+frameHeader : s.start+frameHeader+h.size]
				s.start += frameHeader + h.size
				line, ok, err := s.frame(h, payload)
				if err != nil {
					s.setErr(err)
					return false
				}
				if ok {
					s.token = line
					return true
				}
				continue
			}
		}
		if s.err != nil {
			if s.err == io.EOF && s.end > s.start {
				s.err = fmt.Errorf("%w: the connection ended inside a frame", io.ErrUnexpectedEOF)
			}
			s.token = nil
			return false
		}
		s.fill()
	}
}

// frame takes one frame; a line it finishes is returned, owned when it was put together.
func (s *frameReader) frame(h frameHead, payload []byte) ([]byte, bool, error) {
	if h.kind == frameAck {
		if s.acked == nil {
			return nil, false, nil
		}
		return nil, false, s.acked(h.seq)
	}
	if h.seq != s.seq+1 {
		return nil, false, fmt.Errorf("%w: frame %d after %d", errFrame, h.seq, s.seq)
	}
	s.seq = h.seq
	if h.lane == LaneBulk {
		s.bulkRead = true
	}
	key := uint32(h.lane)<<16 | uint32(h.slot)
	part, going := s.partial[key]
	if !going {
		if h.fin {
			return payload, true, nil // in the read buffer: valid until the next Scan, as a short line's is
		}
		if len(s.partial) >= maxPartialLines {
			return nil, false, fmt.Errorf("%w: more than %d lines in progress", errFrame, maxPartialLines)
		}
		if h.size > s.max {
			return nil, false, bufio.ErrTooLong
		}
		s.partial[key] = append(make([]byte, 0, 2*h.size), payload...)
		return nil, false, nil
	}
	if len(part)+h.size > s.max {
		return nil, false, bufio.ErrTooLong
	}
	part = append(part, payload...)
	if h.fin {
		delete(s.partial, key)
		s.owned = true
		if cap(part)-len(part) > len(part)/4 && len(part) > linePiece {
			// The line is kept as long as its params are (unmarshalOwned): not with up to as much again of room the
			// doubling left behind it.
			part = bytes.Clone(part)
		}
		return part, true, nil
	}
	s.partial[key] = part
	if s.middle != nil && len(part) > pingFrameMiddleBytes {
		s.middle()
	}
	return nil, false, nil
}

// fill reads more input, after asking for an acknowledgement of the bulk frames read.
func (s *frameReader) fill() {
	if s.bulkRead && s.ack != nil {
		s.ack(s.seq)
		s.bulkRead = false
	}
	if s.start > 0 && (s.start == s.end || len(s.buf)-s.end < frameMax) {
		copy(s.buf, s.buf[s.start:s.end])
		s.end -= s.start
		s.start = 0
	}
	for loop := 0; ; {
		n, err := s.r.Read(s.buf[s.end:])
		if n < 0 || len(s.buf)-s.end < n {
			s.setErr(bufio.ErrBadReadCount)
			return
		}
		s.end += n
		if err != nil {
			s.setErr(err)
			return
		}
		if n > 0 {
			return
		}
		loop++
		if loop > 100 {
			s.setErr(io.ErrNoProgress)
			return
		}
	}
}

// aheadActivity is bufferedActivity for frames: among the frames already read after the current line, a whole line
// that is a reply but not a deadline (or that does not decode), or the first piece of a line whose envelope names a
// result and no method or event.
func (s *frameReader) aheadActivity() bool {
	b := s.buf[s.start:s.end]
	for len(b) >= frameHeader {
		h, err := parseHead(b)
		if err != nil {
			return false
		}
		have := min(len(b)-frameHeader, h.size)
		payload := b[frameHeader : frameHeader+have]
		if h.kind == frameData {
			part, going := s.partial[uint32(h.lane)<<16|uint32(h.slot)]
			switch {
			case have == h.size && h.fin:
				line := payload
				if going {
					line = append(bytes.Clone(part), payload...)
				}
				var m message
				if err := json.Unmarshal(line, &m); err != nil || replyMessage(m) && !deadlineMessage(m) {
					return true
				}
			case !going:
				if bufferedActivity(payload) {
					return true
				}
			}
		}
		if have < h.size {
			break
		}
		b = b[frameHeader+h.size:]
	}
	return false
}
