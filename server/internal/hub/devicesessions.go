package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/nwverse/persona/server/internal/db"
)

const deviceSessionRetention = 14 * 24 * time.Hour

var errDeviceSessionPongTimeout = fmt.Errorf("pong timeout: %w", context.DeadlineExceeded)

func (h *Hub) serveDeviceSessions(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	if raw := r.URL.Query().Get("since"); raw != "" {
		var err error
		since, err = time.Parse(time.RFC3339, raw)
		if err != nil || since.After(now) {
			writeErr(w, http.StatusBadRequest, errors.New("since must be an RFC3339 timestamp no later than now"))
			return
		}
	}
	if since.Before(now.Add(-deviceSessionRetention)) {
		since = now.Add(-deviceSessionRetention)
	}
	if h.store == nil {
		writeJSON(w, db.DeviceSessionHistory{Sessions: []db.DeviceSession{}})
		return
	}
	history, err := h.store.DeviceSessions(r.Context(), r.PathValue("id"), since, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, history)
}

// startDeviceSessions runs before serving requests and finishes before the database closes.
func (h *Hub) startDeviceSessions(ctx context.Context) (func(), error) {
	if h.store == nil {
		return func() {}, nil
	}
	if err := h.store.StopDeviceSessions(ctx, time.Now()); err != nil {
		return nil, err
	}
	prune := func() {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := h.store.PruneLockTests(pctx, time.Now().Add(-deviceSessionRetention)); err != nil {
			h.logf("prune lock tests: %v", err)
		}
		if err := h.store.PruneDeviceSessions(pctx, time.Now().Add(-deviceSessionRetention)); err != nil {
			h.logf("prune device sessions: %v", err)
		}
	}
	prune()
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				prune()
			}
		}
	}()
	return func() {
		cancel()
		<-done
		parent := context.Background()
		if h.shutdown != nil {
			parent = h.shutdown.start()
		}
		sctx, stop := context.WithTimeout(parent, shutdownStepTimeout)
		defer stop()
		if err := h.store.StopDeviceSessions(sctx, time.Now()); err != nil {
			h.logf("stop device sessions: %v", err)
		}
	}, nil
}

type deviceSessionConnection struct {
	hub      *Hub
	client   *client
	ctx      context.Context
	id       int64
	mu       sync.Mutex
	writeErr error
}

func (h *Hub) deviceSessionConnected(r *http.Request, c *client) *deviceSessionConnection {
	p := principalOf(r.Context())
	if h.store == nil || p.ID == "" || p.PairHash != "" {
		return nil
	}
	source := ""
	if ip := remoteIP(r); ip.IsValid() {
		bits := 64
		if ip.Is4() {
			bits = 24
		}
		source = netip.PrefixFrom(ip, bits).Masked().String()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	id, err := h.store.InsertDeviceSession(ctx, db.DeviceSession{DeviceID: p.ID,
		ConnectedAt: time.Now(), Source: source, HeartbeatInterval: defaultHeartbeat().Interval})
	if err != nil {
		h.logf("insert device session: %v", err)
		return nil
	}
	return &deviceSessionConnection{hub: h, client: c, ctx: r.Context(), id: id}
}

func (s *deviceSessionConnection) writerStopped(err error) {
	if s != nil {
		s.mu.Lock()
		s.writeErr = err
		s.mu.Unlock()
	}
}

func (s *deviceSessionConnection) heartbeat(hb heartbeatConfig) {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if err := s.hub.store.UpdateDeviceSessionHeartbeat(ctx, s.id, hb.Interval); err != nil {
		s.hub.logf("update device session heartbeat: %v", err)
	}
}

func (s *deviceSessionConnection) disconnected(readErr error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	err := s.writeErr
	s.mu.Unlock()
	if err == nil || errors.Is(err, context.Canceled) {
		err = readErr
	}
	reason := "error"
	var closeErr websocket.CloseError
	switch {
	case s.ctx.Err() != nil || s.client.stopping.Load():
		reason = "hub_stopped"
	case errors.Is(err, errDeviceSessionPongTimeout):
		reason = "pong_timeout"
	case errors.As(err, &closeErr):
		if closeErr.Reason == "replaced" {
			reason = "replaced"
		} else if closeErr.Code == websocket.StatusNormalClosure || closeErr.Code == websocket.StatusGoingAway {
			reason = "closed"
		}
	}
	s.client.recordSocketTraffic("websocket_close", reason, 0, 0)
	s.client.mu.Lock()
	interval := s.client.meta.heartbeat.Interval
	s.client.mu.Unlock()
	if interval == 0 {
		interval = defaultHeartbeat().Interval
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.hub.store.FinishDeviceSession(ctx, s.id, time.Now(), reason, interval); err != nil {
		s.hub.logf("finish device session: %v", err)
	}
}
