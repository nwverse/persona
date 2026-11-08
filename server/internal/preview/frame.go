// Package preview holds what the hub and the node share about a dev server preview (docs/web/preview.md): the frames a
// preview stream carries. The stream itself is a streams.Mux byte stream like a forwarded port's; on it, each
// WebSocket message between the web and the node is one frame, so the hub only re-frames and reads nothing.
package preview

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame kinds: a JSON message (a WebSocket text message) or bytes (a binary one: a picture of the page).
const (
	Text   byte = 't'
	Binary byte = 'b'
)

// MaxFrame is the largest frame either side writes or reads: a full-size PNG screenshot of a large viewport fits.
const MaxFrame = 16 << 20

// ErrTooBig is a frame over MaxFrame.
var ErrTooBig = errors.New("preview: frame too big")

// WriteFrame writes one frame: its length (4 bytes, big-endian, kind included), its kind and its bytes.
func WriteFrame(w io.Writer, kind byte, p []byte) error {
	if len(p)+1 > MaxFrame {
		return ErrTooBig
	}
	buf := make([]byte, 5+len(p))
	binary.BigEndian.PutUint32(buf, uint32(len(p)+1))
	buf[4] = kind
	copy(buf[5:], p)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads the next frame. io.EOF only between frames; a stream cut inside one is io.ErrUnexpectedEOF.
func ReadFrame(r *bufio.Reader) (kind byte, p []byte, err error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 {
		return 0, nil, errors.New("preview: empty frame")
	}
	if n > MaxFrame {
		return 0, nil, ErrTooBig
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	if buf[0] != Text && buf[0] != Binary {
		return 0, nil, fmt.Errorf("preview: unknown frame kind %q", buf[0])
	}
	return buf[0], buf[1:], nil
}
