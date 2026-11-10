package node

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// runtimePushBudget is what a push of 300 sessions may allocate (TestRuntimeSessionsPushAllocatesOneList).
const runtimePushBudget = 683_000 // 663,316 measured (678,510 with -race)

// runtimePush is a runtime.sessions push of rows sessions as the runtime SDK sends them (the fleet soak's machines: a
// title, a directory, a status, the last 200 characters said, a model), and a host with that runtime connected and no
// hub: what the push costs on its own.
func runtimePush(tb testing.TB, rows int) (*RuntimeHost, *runtimeConn, json.RawMessage) {
	tb.Helper()
	rh := NewRuntimeHost("test", "")
	rh.logger = log.New(io.Discard, "", 0)
	h := rh.reg.add(nil)
	h.hello = runtimeHello{Runtime: "load", Name: "Load"}
	h.hello.Caps.Send = true
	h.ready = true
	text := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 5)[:200]
	list := make([]map[string]any, rows)
	for i := range list {
		list[i] = map[string]any{"session_id": listID(i), "title": fmt.Sprintf("Task %d", i), "cwd": fmt.Sprintf("/work/project-%d", i%12),
			"status": "done", "updated_at": listT0.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano), "rev": 40 + i,
			"space_id": fmt.Sprintf("/work/project-%d", i%12), "space_name": fmt.Sprintf("project-%d", i%12), "last_text": text, "model": "load-model"}
	}
	params, err := json.Marshal(map[string]any{"sessions": list})
	if err != nil {
		tb.Fatal(err)
	}
	return rh, h, params
}

// BenchmarkRuntimeSessionsPush is one runtime.sessions push of 50 and of 300 sessions, its list replacing the last.
func BenchmarkRuntimeSessionsPush(b *testing.B) {
	for _, rows := range []int{50, 300} {
		b.Run(fmt.Sprint(rows), func(b *testing.B) {
			rh, h, params := runtimePush(b, rows)
			rh.event(h, "runtime.sessions", params)
			if got := len(rh.reg.appendSessions(nil)); got != rows {
				b.Fatalf("kept %d sessions, want %d", got, rows)
			}
			b.ReportAllocs()
			for b.Loop() {
				rh.event(h, "runtime.sessions", params)
			}
		})
	}
}

// A push of 300 sessions is decoded into one slice the size of the last list and kept in it (#2826 follow-up): it was
// the rows grown from nil while decoding, then copied by normalizeRuntimeSessions, 2,238,500 bytes. The bound is the
// cost measured with the change, plus 3%.
func TestRuntimeSessionsPushAllocatesOneList(t *testing.T) {
	rh, h, params := runtimePush(t, 300)
	rh.event(h, "runtime.sessions", params)
	if per := bytesPerRun(20, func() { rh.event(h, "runtime.sessions", params) }); per > runtimePushBudget {
		t.Errorf("a push of 300 sessions allocates %.0f bytes, want at most %d", per, runtimePushBudget)
	}
}

// The list a push replaces is left as it was: the next push is decoded into an array of its own, so rows read before
// it (appendSessions, row) are never written under their reader.
func TestRuntimeSessionsPushKeepsTheLastList(t *testing.T) {
	rh, h, params := runtimePush(t, 3)
	rh.event(h, "runtime.sessions", params)
	last := h.sessions
	want := slices.Clone(last)
	rh.event(h, "runtime.sessions", json.RawMessage(`{"sessions":[{"session_id":"other-1","title":"x"},{"session_id":"bad id"}]}`))
	if !reflect.DeepEqual(last, want) {
		t.Errorf("the list replaced was written over: %+v", last[0])
	}
	if len(h.sessions) != 1 || h.sessions[0].SessionID != "other-1" || cap(h.sessions) > 2*len(h.sessions)+8 {
		t.Errorf("kept %d sessions (capacity %d), want other-1 alone", len(h.sessions), cap(h.sessions))
	}
}
