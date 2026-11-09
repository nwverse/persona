package hub

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestTrafficRealWebSocketUpgrade(t *testing.T) {
	var output bytes.Buffer
	h := &Hub{logger: log.New(&output, "", 0)}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_ = conn.Write(r.Context(), websocket.MessageText, []byte("test"))
		_, _, _ = conn.Read(r.Context())
	})
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.observeClientRequest(inner, w, r, principal{ID: "test-credential", Role: roleFull})
		close(done)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := conn.Read(ctx)
	if err != nil || string(body) != "test" {
		t.Fatalf("body=%q error=%v", body, err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	h.flushTraffic(time.Now(), true)
	if !strings.Contains(output.String(), "event=websocket action=completed status=101 count=1") {
		t.Fatal(output.String())
	}
}

func TestTrafficSSEFlushPreserved(t *testing.T) {
	var output bytes.Buffer
	h := &Hub{logger: log.New(&output, "", 0)}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = w.Write([]byte("data: test\n\n"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
	})
	writer := httptest.NewRecorder()
	h.observeClientRequest(inner, writer, httptest.NewRequest("GET", "/api/v1/test-stream", nil), principal{ID: "test-credential", Role: roleAnswer})
	h.flushTraffic(time.Now(), true)
	if !writer.Flushed || !strings.Contains(output.String(), "event=sse action=completed status=200 count=1") {
		t.Fatal(output.String())
	}
}
