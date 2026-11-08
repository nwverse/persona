package hub

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This fixture also compiles on the pre-attribution implementation to record
// actual before/after counts without exercising any production device.
func TestTrafficUAAggregation(t *testing.T) {
	var output bytes.Buffer
	h := &Hub{logger: log.New(&output, "", 0), apiMux: http.NewServeMux(), push: &Pusher{devices: map[string]Device{"test": {Kind: "iphone", CredentialID: "test", APNSToken: "fixture-only"}}}}
	h.apiMux.HandleFunc("GET /api/v1/todos", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) })
	for _, sample := range []struct {
		ua string
		n  int
	}{
		{"Sample/1 CFNetwork/1490 Darwin/25 private-ua-token", 80},
		{"SampleWidget/1 CFNetwork/1490 Darwin/25 private-ua-token", 30},
		{"SampleLiveActivity/1 CFNetwork/1490 Darwin/25 private-ua-token", 20},
	} {
		for i := 0; i < sample.n; i++ {
			r := httptest.NewRequest("GET", "/api/v1/todos?token=private-query", nil)
			r.Header.Set("User-Agent", sample.ua)
			h.observeClientRequest(h.apiMux, httptest.NewRecorder(), r, principal{ID: "test", Role: roleFull})
		}
	}
	h.flushTraffic(time.Now(), true)
	counts := map[string]int{}
	for _, line := range strings.Split(output.String(), "\n") {
		if !strings.Contains(line, "action=started") {
			continue
		}
		platform, source, n := "unknown", "unknown", 0
		for _, field := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(field, "platform="); ok {
				platform = v
			}
			if v, ok := strings.CutPrefix(field, "source="); ok {
				source = v
			}
			if v, ok := strings.CutPrefix(field, "count="); ok {
				n, _ = strconv.Atoi(v)
			}
		}
		counts[platform+"/"+source] += n
	}
	t.Logf("inferred request counts: %v", counts)
	if strings.Contains(output.String(), "private-ua-token") || strings.Contains(output.String(), "private-query") {
		t.Fatal("raw private metadata logged")
	}
	if counts["ios/app"] != 80 || counts["ios/widget"] != 30 || counts["ios/liveactivity"] != 20 {
		t.Fatalf("surfaces not attributed: %v", counts)
	}
}
