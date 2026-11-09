package node

// A Chrome DevTools Protocol connection over --remote-debugging-pipe (docs/web/preview.md "The browser"): messages are
// JSON, each ended by a NUL byte, commands written to the browser's fd 3 and answers and events read from its fd 4.
// There is no debugging port, so no other program on the computer can drive the preview's browser.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// cdpMessageMax: an answer bigger than this (a screenshot of an enormous page) ends the connection.
const cdpMessageMax = 64 << 20

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("cdp %d: %s", e.Code, e.Message) }

type cdpReply struct {
	result json.RawMessage
	err    error
}

// cdpConn is one connection to a browser. Events go to onEvent, called on the read loop: it must not block or call.
type cdpConn struct {
	w       io.WriteCloser
	wmu     sync.Mutex
	next    atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpReply
	done    chan struct{}
	err     error // why it ended, once done is closed

	onEvent func(session, method string, params json.RawMessage)
}

func newCDPConn(r io.Reader, w io.WriteCloser, onEvent func(session, method string, params json.RawMessage)) *cdpConn {
	c := &cdpConn{w: w, pending: map[int64]chan cdpReply{}, done: make(chan struct{}), onEvent: onEvent}
	go c.read(r)
	return c
}

func (c *cdpConn) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	var err error
	for {
		var line []byte
		line, err = readCDPMessage(br)
		if err != nil {
			break
		}
		var m struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
			Result    json.RawMessage `json:"result"`
			Error     *cdpError       `json:"error"`
			SessionID string          `json:"sessionId"`
		}
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.Method != "" {
			if c.onEvent != nil {
				c.onEvent(m.SessionID, m.Method, m.Params)
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ch != nil {
			rep := cdpReply{result: m.Result}
			if m.Error != nil {
				rep.err = m.Error
			}
			ch <- rep
		}
	}
	c.mu.Lock()
	if err == io.EOF {
		err = errors.New("the browser closed")
	}
	c.err = err
	for id, ch := range c.pending {
		ch <- cdpReply{err: err}
		delete(c.pending, id)
	}
	close(c.done)
	c.mu.Unlock()
}

// readCDPMessage reads up to the next NUL.
func readCDPMessage(br *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		part, err := br.ReadSlice(0)
		out = append(out, part...)
		if len(out) > cdpMessageMax {
			return nil, errors.New("cdp: message too big")
		}
		if err == nil {
			return out[:len(out)-1], nil
		}
		if err != bufio.ErrBufferFull {
			return nil, err
		}
	}
}

// call sends a command (to a page's session, or to the browser when session is "") and waits for its answer, which
// it decodes into out when out is not nil.
func (c *cdpConn) call(ctx context.Context, session, method string, params any, out any) error {
	id := c.next.Add(1)
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if session != "" {
		msg["sessionId"] = session
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return c.err
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()
	c.wmu.Lock()
	_, err = c.w.Write(append(b, 0))
	c.wmu.Unlock()
	if err != nil {
		c.forget(id)
		return err
	}
	select {
	case rep := <-ch:
		if rep.err != nil {
			return rep.err
		}
		if out != nil && len(rep.result) > 0 {
			return json.Unmarshal(rep.result, out)
		}
		return nil
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

// sendNoReply sends a command without waiting for its answer (which the read loop drops): input and
// acknowledgements, sent in order from one goroutine.
func (c *cdpConn) sendNoReply(session, method string, params any) error {
	msg := map[string]any{"id": c.next.Add(1), "method": method}
	if params != nil {
		msg["params"] = params
	}
	if session != "" {
		msg["sessionId"] = session
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	select {
	case <-c.done:
		return c.err
	default:
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(append(b, 0))
	return err
}

func (c *cdpConn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *cdpConn) close() { _ = c.w.Close() }
