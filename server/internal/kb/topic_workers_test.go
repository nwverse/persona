package kb

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// overlapModel holds each call until topicWorkers are in at once (or a second passes), counting the most in at once.
type overlapModel struct {
	in, most atomic.Int32
	calls    atomic.Int32
	full     chan struct{}
	once     sync.Once
	panicOn  string // a call whose prompt has it panics
}

func (m *overlapModel) JSONMax(_ context.Context, _, user string, _ int, out any) error {
	m.calls.Add(1)
	if m.panicOn != "" && strings.Contains(user, m.panicOn) {
		panic("example: a bad answer")
	}
	n := m.in.Add(1)
	defer m.in.Add(-1)
	for cur := m.most.Load(); n > cur && !m.most.CompareAndSwap(cur, n); cur = m.most.Load() {
	}
	if n >= topicWorkers {
		m.once.Do(func() { close(m.full) })
	}
	select {
	case <-m.full:
	case <-time.After(time.Second):
	}
	return json.Unmarshal([]byte(`{"points":[]}`), out)
}

// The sources of one distill run go to the model topicWorkers at a time, never more (#4247).
func TestDistillTopicsRunsSourcesInParallel(t *testing.T) {
	m := &overlapModel{full: make(chan struct{})}
	e := testEngine(t, m)
	for _, slug := range []string{"a", "b", "c"} {
		quietProject(t, e, slug, "示例：定了周五灰度")
	}
	if n := e.DistillTopics(context.Background(), 3); n != 3 || m.calls.Load() != 3 {
		t.Fatalf("distilled %d with %d call(s), want 3 and 3", n, m.calls.Load())
	}
	if most := m.most.Load(); most != topicWorkers {
		t.Fatalf("%d call(s) at once at most, want %d", most, topicWorkers)
	}
	for _, slug := range []string{"a", "b", "c"} {
		if mark := e.Store.topicMark(KindProject, slug); mark.Lines != 1 {
			t.Errorf("%s: mark %+v", slug, mark)
		}
	}
}

// A panic in one source's call is logged, starts no more, and puts that source off like a failure (the same panic
// would otherwise come first every run); the process (and the loop's goroutine) goes on.
func TestDistillTopicsRecoversAPanic(t *testing.T) {
	m := &overlapModel{full: make(chan struct{}), panicOn: "项目 a"}
	e := testEngine(t, m)
	var mu sync.Mutex
	var logs []string
	e.Logf = func(f string, a ...any) { mu.Lock(); logs = append(logs, f); mu.Unlock() }
	quietProject(t, e, "a", "示例：定了周五灰度")
	e.DistillTopics(context.Background(), 1)
	if e.DistillTopics(context.Background(), 1); m.calls.Load() != 1 {
		t.Fatalf("%d call(s), want 1: the panicking source was not put off", m.calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 2 || !strings.HasPrefix(logs[0], "kb: panic:") || !strings.Contains(logs[1], "failed %d time(s)") {
		t.Fatalf("logs = %q", logs)
	}
}
