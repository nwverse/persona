package hub

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTrafficFastRequestsAndPrivacy(t *testing.T) {
	var output bytes.Buffer
	h := &Hub{logger: log.New(&output, "", 0), apiMux: http.NewServeMux()}
	h.apiMux.HandleFunc("GET /api/v1/sessions/{machine}/{session}/items", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("private response text")) })
	defer h.flushTraffic(time.Now(), true)
	for i := 0; i < 100; i++ {
		r := httptest.NewRequest("GET", "/api/v1/sessions/private-machine/private-session/items?token=secret-token", nil)
		r = r.WithContext(context.WithValue(r.Context(), requestClientKey{}, requestClient{Platform: "ios", Version: "private-version"}))
		h.observeClientRequest(h.apiMux, httptest.NewRecorder(), r, principal{ID: "private-credential", Name: "private-name", Role: roleFull})
	}
	h.flushTraffic(time.Now(), true)
	text := output.String()
	for _, secret := range []string{"private-machine", "private-session", "secret-token", "private-credential", "private-name", "private-version", "private response text"} {
		if strings.Contains(text, secret) {
			t.Fatalf("private data %q entered telemetry", secret)
		}
	}
	if !strings.Contains(text, "action=started status=0 count=100") || !strings.Contains(text, "action=completed status=200 count=100 body_bytes=2100 max_body_bytes=21") {
		t.Fatalf("fast success requests or response bytes missing: %s", text)
	}
	t.Log("fast HTTP requests: old slow/error log=0, new started/completed counters=100; uncompressed body bytes=2100")
}

func TestTrafficFlushBoundariesAndConcurrency(t *testing.T) {
	var output bytes.Buffer
	h := &Hub{logger: log.New(&output, "", 0)}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			for n := 0; n < 100; n++ {
				h.recordTraffic(trafficKey{minute: base, identity: "test", route: "GET /test", event: "http", action: "started"}, 0, 0)
			}
		})
	}
	workers.Wait()
	h.flushTraffic(base.Add(30*time.Second), false)
	if output.Len() != 0 {
		t.Fatal("current minute flushed before completion")
	}
	h.flushTraffic(base.Add(time.Minute), true)
	if !strings.Contains(output.String(), "count=800") {
		t.Fatal(output.String())
	}
	h.recordTraffic(trafficKey{minute: base}, 0, 0)
	if len(h.traffic.rows) != 0 || h.traffic.timer != nil {
		t.Fatal("stopped collector restarted")
	}
}

func TestTrafficBoundedCardinality(t *testing.T) {
	var output bytes.Buffer
	h := &Hub{logger: log.New(&output, "", 0)}
	defer h.flushTraffic(time.Now(), true)
	now := time.Now()
	for i := 0; i < trafficMaxBuckets+100; i++ {
		h.recordTraffic(trafficKey{minute: now, identity: fmt.Sprint(i)}, 0, 0)
	}
	if len(h.traffic.rows) != trafficMaxBuckets+1 {
		t.Fatalf("bucket count=%d", len(h.traffic.rows))
	}
	h.flushTraffic(time.Now(), true)
	if !strings.Contains(output.String(), "identity=overflow") || !strings.Contains(output.String(), "count=100") {
		t.Fatal("overflow lost counts")
	}
}

func TestTrafficStreamingWriter(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusSwitchingProtocols} {
		w := &trafficWriter{ResponseWriter: httptest.NewRecorder()}
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(status)
		if w.status != status {
			t.Fatalf("status=%d, want %d", w.status, status)
		}
		if err := w.FlushError(); err != nil {
			t.Fatal(err)
		}
		if w.Unwrap() == nil {
			t.Fatal("underlying writer inaccessible")
		}
	}
}
