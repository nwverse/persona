package rpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"slices"
)

// frameReader reads a framed connection's lines (lanes.go) for Serve, as a lineReader reads plain ones: Scan, Bytes,
// Owned (a line put together from several frames is in a buffer of its own), Err (nil at io.EOF on a frame's edge).
// Acknowledgements are handed to the writer as they come (acked); before it waits for more input, the reader asks the
// writer to acknowledge the frames with credit it has read (ack, creditLane), so a reader held up by its own side stops
// the other side's interactive and bulk frames once its window is full.
type frameReader struct {
	r          io.Reader
	max        int
	buf        []byte
	start, end int
	err        error
	seq        uint64               // the last data frame read
	creditRead bool                 // a frame with credit was read since the last ack asked for
	partial    map[uint32]*partLine // lines in progress, by lane<<16 | slot
	held       int                  // their bytes
	reads      int                  // reads made so far
	middleLine uint64               // the first frame of the line whose middle counted last (holding)
	token      []byte
	owned      bool
	ack        func(seq uint64)       // ask for an acknowledgement of every frame read through seq
	acked      func(seq uint64) error // the other side read every frame this side sent through seq
	// middle, when set, is told of each read that went on with a line begun before it, did not finish it and has
	// already grown past pingFrameMiddleBytes, as lineReader.middle is (PingActivity).
	middle func()
}

// maxPartialLines bounds the lines a reader puts together at once: a writer has at most laneSlots per lane.
const maxPartialLines = int(laneCount) * laneSlots

// frameReadBuf holds two whole frames: a read rarely stops at a frame's edge.
const frameReadBuf = 2 * frameMax

func newFrameReader(r io.Reader, maximum int) *frameReader {
	return &frameReader{r: r, max: maximum, buf: make([]byte, frameReadBuf), partial: map[uint32]*partLine{}}
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
	if creditLane(h.lane) {
		s.creditRead = true
	}
	key := frameKey(h)
	part, going := s.partial[key]
	// A line as long as the limit allows, and the lines in progress beside it at most as much again together: with one
	// long line at a time on each lane (pickLocked), lines that size seldom overlap.
	if going && part.n+h.size > s.max || !going && h.size > s.max || !h.fin && s.held+h.size > 2*s.max {
		return nil, false, bufio.ErrTooLong
	}
	if !going {
		if h.fin {
			return payload, true, nil // in the read buffer: valid until the next Scan, as a short line's is
		}
		if len(s.partial) >= maxPartialLines {
			return nil, false, fmt.Errorf("%w: more than %d lines in progress", errFrame, maxPartialLines)
		}
		part = &partLine{first: h.seq, read: s.reads, reply: replyEnvelope(payload)}
		s.partial[key] = part
	}
	if h.fin {
		delete(s.partial, key)
		s.held -= part.n
		s.owned = true
		return part.join(payload), true, nil
	}
	part.add(payload)
	s.held += h.size
	return nil, false, nil
}

func frameKey(h frameHead) uint32 { return uint32(h.lane)<<16 | uint32(h.slot) }

// partLine is a line being put together from its frames, in pieces taken from grownBufs and joined once in a buffer of
// the line's size, which is the line's own (as lineReader puts a long line together): a line grown by append would be
// copied each time it outgrew its buffer, and keep up to as much again of room for as long as its params are kept.
type partLine struct {
	pieces [][]byte
	n      int
	first  uint64 // its first frame's sequence number: which line it is, a slot carrying one line after another
	read   int    // the read its first frame came in
	// reply: its first frame opens an answer (replyEnvelope), which aheadActivity takes for the whole line rather than
	// put the line together to decode it (a deadline, the one answer that does not count, never takes two frames).
	reply bool
}

// replyEnvelope: first, the start of a line, opens an answer to a call: an id and then a result or an error, in the
// order message's fields put them.
func replyEnvelope(first []byte) bool {
	dec := jsontext.NewDecoder(bytes.NewReader(first))
	if t, err := dec.ReadToken(); err != nil || t.Kind() != '{' {
		return false
	}
	if t, err := dec.ReadToken(); err != nil || t.String() != "id" {
		return false
	}
	if _, err := dec.ReadValue(); err != nil {
		return false
	}
	t, err := dec.ReadToken()
	return err == nil && (t.String() == "result" || t.String() == "error")
}

// partPiece is the size of a partLine's pieces: grownBufs' smallest.
const partPiece = lineStart << 2

func (p *partLine) add(b []byte) {
	for len(b) > 0 {
		k := len(p.pieces) - 1
		if k < 0 || len(p.pieces[k]) == cap(p.pieces[k]) {
			p.pieces = append(p.pieces, takeGrown(partPiece)[:0])
			k++
		}
		c := min(len(b), cap(p.pieces[k])-len(p.pieces[k]))
		p.pieces[k] = append(p.pieces[k], b[:c]...)
		p.n += c
		b = b[c:]
	}
}

// join is the whole line, tail its last piece; the pieces go back to grownBufs.
func (p *partLine) join(tail []byte) []byte {
	line := make([]byte, 0, p.n+len(tail))
	for _, piece := range p.pieces {
		line = append(line, piece...)
		giveGrown(piece[:cap(piece)])
	}
	p.pieces = nil
	return append(line, tail...)
}

// fill reads more input, after asking for an acknowledgement of the frames with credit read.
func (s *frameReader) fill() {
	carried := s.end > s.start // a frame begun in an earlier read: this one goes on with it
	if s.creditRead && s.ack != nil {
		s.ack(s.seq)
		s.creditRead = false
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
			s.reads++
			s.noteMiddle(carried)
			return
		}
		loop++
		if loop > 100 {
			s.setErr(io.ErrNoProgress)
			return
		}
	}
}

// aheadLine is what is known of a line from the frames read and not yet taken (frameReader.ahead).
type aheadLine struct {
	key   uint32
	start uint64 // its first frame's sequence number
	size  int    // its bytes so far, those before these frames included
	first bool   // its first frame is among these
	lead  bool   // the first of these frames is its
	ended bool   // its last frame is among these, whole
	reply bool   // its first frame opens an answer (replyEnvelope)
}

// ahead walks the frames read and not yet taken (s.buf[s.start:s.end]), the last perhaps in part, calling f, when set,
// for each data frame with the part of its payload there and its line as known so far (false from f stops the walk);
// it returns those lines. A piece of a line is never taken for a line of its own: the line of a piece that comes after
// its first one is the one begun before.
func (s *frameReader) ahead(f func(h frameHead, payload []byte, l *aheadLine) bool) []aheadLine {
	var lines []aheadLine
	b := s.buf[s.start:s.end]
	for lead := true; len(b) >= frameHeader; lead = false {
		h, err := parseHead(b)
		if err != nil {
			break
		}
		have := min(len(b)-frameHeader, h.size)
		if h.kind == frameData {
			key := frameKey(h)
			i := slices.IndexFunc(lines, func(l aheadLine) bool { return l.key == key && !l.ended })
			if i < 0 {
				l := aheadLine{key: key, start: h.seq, first: true, lead: lead, reply: replyEnvelope(b[frameHeader : frameHeader+have])}
				// A slot whose line ended among these frames carries a new one; otherwise it goes on with the line in
				// progress there.
				if part, ok := s.partial[key]; ok && !slices.ContainsFunc(lines, func(l aheadLine) bool { return l.key == key }) {
					l.start, l.first, l.size, l.reply = part.first, false, part.n, part.reply
				}
				lines = append(lines, l)
				i = len(lines) - 1
			}
			l := &lines[i]
			l.size += have
			l.ended = have == h.size && h.fin
			if f != nil && !f(h, b[frameHeader:frameHeader+have], l) {
				break
			}
		}
		if have < h.size {
			break
		}
		b = b[frameHeader+h.size:]
	}
	return lines
}

// aheadActivity is bufferedActivity for frames: among the frames already read after the current line, a whole line
// that is a reply but not a deadline (or that does not decode), or the first piece of a line whose envelope names a
// result and no method or event; for a line in several frames that is all its first frame says (a deadline is never
// that long), and its later pieces say nothing of their own.
func (s *frameReader) aheadActivity() bool {
	for _, p := range s.partial {
		if p.reply && p.read == s.reads {
			return true // an answer begun in this read, taken in already: with lines its start would be ahead
		}
	}
	found := false
	s.ahead(func(h frameHead, payload []byte, l *aheadLine) bool {
		switch {
		case !l.first || l.size != len(payload): // a later piece: the line counts once whole, by its first frame
			found = l.ended && l.reply
		case l.ended: // a whole line in one frame
			var m message
			found = json.Unmarshal(payload, &m) != nil || replyMessage(m) && !deadlineMessage(m)
		default: // the first piece of a longer line
			found = bufferedActivity(payload)
		}
		return !found
	})
	return found
}

// noteMiddle tells middle about the read just made (carried: it went on with a frame begun before it) when it went on
// with a line begun before it, did not finish it, and that line has grown past pingFrameMiddleBytes: lineReader's
// rule, read for read, whatever frames the line came in.
func (s *frameReader) noteMiddle(carried bool) {
	if s.middle == nil {
		return
	}
	for _, l := range s.ahead(nil) {
		if (!l.first || l.lead && carried) && !l.ended && l.size > pingFrameMiddleBytes {
			s.middleLine = l.start
			s.middle()
			return
		}
	}
}

// holding: the line whose middle counted last is still arriving, so that activity stands (Serve). That line only: one
// held back (by credit the other side never gets) must not keep the middles of lines that finished alive.
func (s *frameReader) holding() bool {
	if s.middleLine == 0 {
		return false
	}
	for _, p := range s.partial {
		if p.first == s.middleLine {
			return true
		}
	}
	if s.end-s.start >= frameHeader { // its first frame still coming in
		h, err := parseHead(s.buf[s.start:])
		return err == nil && h.kind == frameData && h.seq == s.middleLine
	}
	return false
}
