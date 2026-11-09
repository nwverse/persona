package hub

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Node streams through a member (docs/server/mesh.md "Routes through a member"): the center's manager reaches a computer a member has in
// hub.json by asking the center, which asks that member over the links for a stream to its node (the center's side:
// relayThroughHub, devices_relay.go). The request is an HTTP upgrade (Upgrade: persona-node) on the peer server, passed
// hop by hop like any other; the member's manager opens the node the way its hub.json says (its local node, ssh,
// tailcat, the computer's Persona Link or connector), the member answers 101 and from then on copies bytes both ways.
// Only the member's center may ask: a member drives none of its computers itself.

const nodeUpgrade = compat.NodeUpgrade

// nodeOpenWait bounds how long a member's manager may take to open a node for the center (each of its ways in turn).
const nodeOpenWait = time.Minute

// openThroughHub opens a stream to the node of machine mid in hub's hub.json, through that hub.
func (h *Hub) openThroughHub(ctx context.Context, hub, mid string) (*nodePipe, error) {
	l := h.nextLink(hub)
	if l == nil || l.transport == nil {
		return nil, i18n.Err("hub-peer_node.unreachable", i18n.Args{"name": h.hubNameOf(hub)})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://peer/api/v1/peer/node", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", nodeUpgrade)
	compat.SetHeader(req.Header, hdrPeerNode, hub+"/"+mid)
	compat.SetHeader(req.Header, hdrPeerFrom, h.hubID())
	compat.SetHeader(req.Header, hdrPeerTTL, itoa(meshTTL))
	client := &http.Client{Transport: l.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		msg := strings.TrimSpace(string(b))
		// The member's reason, not the summary it localized it into (apierr.Reason).
		if why := apierr.Reason(b); why != "" {
			msg = why
		}
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("「%s」：%s", h.hubNameOf(hub), clip(oneLine(msg), 300))
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, errors.New("the stream through the member is not writable")
	}
	var once sync.Once
	return &nodePipe{r: rwc, w: rwc, close: func() { once.Do(func() { _ = rwc.Close() }) }}, nil
}

// serveNodeOpen is a member opening a stream to one of its computers for its center: the manager opens it the way
// hub.json says (devices.accept, kind node) and the hub copies bytes between the center's stream and the relay.
func (h *Hub) serveNodeOpen(w http.ResponseWriter, r *http.Request, from, mid string) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), nodeUpgrade) && !strings.EqualFold(r.Header.Get("Upgrade"), "persona-node") {
		writeErr(w, http.StatusBadRequest, errors.New("a node stream is an upgrade"))
		return
	}
	if !h.isMember() || from == "" || from != h.centerID() {
		writeErr(w, http.StatusForbidden, i18n.Err("hub-peer_node.center_only"))
		return
	}
	if h.knownEntry(mid) == nil {
		writeErr(w, http.StatusNotFound, i18n.Err("hub-peer_node.no_machine", i18n.Args{"id": strconv.Quote(mid)}))
		return
	}
	if !h.awaitManager(r.Context(), linkManagerWait) { // the hub just started: the manager is on its way
		writeErr(w, http.StatusBadGateway, errAssistantOffline)
		return
	}
	taken, err := h.handToManager(context.Background(), devicewire.Accept{Kind: devicewire.AcceptNode, MachineID: mid})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	defer close(taken.done)
	// The manager opens the node before the center is answered: a text message says it did, a close says why it
	// could not (the center shows that on the machine and its path).
	rctx, rcancel := context.WithTimeout(r.Context(), nodeOpenWait)
	typ, _, err := taken.conn.Read(rctx)
	rcancel()
	if err != nil || typ != websocket.MessageText {
		why := "the manager did not open the node"
		var ce websocket.CloseError
		if errors.As(err, &ce) && ce.Reason != "" {
			why = ce.Reason
		} else if err != nil {
			why += ": " + err.Error()
		}
		_ = taken.conn.Close(websocket.StatusNormalClosure, "")
		writeErr(w, http.StatusBadGateway, errors.New(why))
		return
	}
	octx, ocancel := context.WithCancel(context.Background())
	defer ocancel()
	nc := websocket.NetConn(octx, taken.conn, websocket.MessageBinary)
	pipe := &nodePipe{r: nc, w: nc, close: func() { _ = nc.Close() }}
	hj, ok := w.(http.Hijacker)
	if !ok {
		pipe.close()
		writeErr(w, http.StatusInternalServerError, errors.New("cannot take the connection over"))
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		pipe.close()
		return
	}
	if _, err := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+nodeUpgrade+"\r\n\r\n"); err != nil {
		pipe.close()
		conn.Close()
		return
	}
	h.logf("[%s] stream to the node opened for the center", mid)
	joinNode(conn, buf, pipe)
	h.logf("[%s] stream to the node for the center closed", mid)
}

// nodePipe is a byte stream to a node: the reading and the writing end, and how to end it.
type nodePipe struct {
	r     io.Reader
	w     io.Writer
	close func()
}

// joinNode copies between the center's stream and the node until either ends.
func joinNode(conn net.Conn, buf *bufio.ReadWriter, pipe *nodePipe) {
	done := make(chan struct{}, 2)
	go func() {
		var src io.Reader = conn
		if buf != nil && buf.Reader.Buffered() > 0 {
			src = io.MultiReader(io.LimitReader(buf.Reader, int64(buf.Reader.Buffered())), conn)
		}
		_, _ = io.Copy(pipe.w, src)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, pipe.r)
		done <- struct{}{}
	}()
	<-done
	pipe.close()
	conn.Close()
	<-done
}
