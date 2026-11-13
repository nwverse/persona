package kb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
)

// heldModel is a model whose connection is held (its quota used up) until open; then it answers like next.
type heldModel struct {
	open  bool
	calls int
	next  Model
}

func (m *heldModel) JSONMax(ctx context.Context, system, user string, maxTokens int, out any) error {
	m.calls++
	if !m.open {
		return &ai.PausedError{Pause: ai.Pause{Reason: ai.ReasonQuota, Until: time.Now().Add(30 * time.Minute)}}
	}
	return m.next.JSONMax(ctx, system, user, maxTokens, out)
}

// While the model's connection is held each kind of work asks once, logs nothing and keeps everything queued: the
// chat stays due, the projects dirty, the pairs unjudged, the items untagged. Once it is back all of it is done.
func TestHeldModelPutsTheWorkOff(t *testing.T) {
	ctx := context.Background()
	m := &heldModel{next: &fakeModel{}}
	e := testEngine(t, m)
	var logs []string
	e.Logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	st := e.Store
	t0 := time.Now().Add(-time.Hour).Truncate(time.Minute)
	e.Sources = []Source{&fakeSource{
		chats: []SourceChat{{ID: "f1", Name: "X 项目群", Kind: "group", UpdatedAt: t0.Add(time.Minute)}},
		msgs:  map[string][]SourceMessage{"f1": {msg(0, "张三", "周五灰度", t0), msg(1, "李四", "好的", t0.Add(time.Minute))}},
	}}
	if _, err := e.Archive(ctx); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"a", "b", "c"} {
		_ = st.PutProject(&Project{Slug: slug, Name: "项目 " + slug, Status: "active", Dirty: true, Chats: []string{"lark-f1"}})
	}
	_ = st.PutItem(&Item{Kind: KindEvent, ID: "e1", Title: "周会"})
	logs = nil

	c, _ := st.Chat("lark-f1")
	if err := e.Compress(ctx, c); !ai.IsPaused(err) {
		t.Fatalf("compress: %v", err)
	}
	if n := e.RefreshProjects(ctx, 3); n != 0 {
		t.Fatalf("refreshed %d", n)
	}
	if n := e.DedupeProjects(ctx, 3); n != 0 {
		t.Fatalf("merged %d", n)
	}
	if _, err := e.TagItems(ctx, 10); !ai.IsPaused(err) {
		t.Fatalf("tag items: %v", err)
	}
	if m.calls != 4 || len(logs) != 0 {
		t.Fatalf("%d call(s), logs %q; want one call for each kind of work and no log", m.calls, logs)
	}
	if c, _ := st.Chat("lark-f1"); c.Pending != 2 || !c.CompressedAt.IsZero() {
		t.Fatalf("chat = %+v", c)
	}
	for _, slug := range []string{"a", "b", "c"} {
		if p, _ := st.Project(slug); !p.Dirty {
			t.Fatalf("project %s is not dirty any more", slug)
		}
	}
	if len(st.dedupeVerdicts()) != 0 {
		t.Fatalf("verdicts = %v", st.dedupeVerdicts())
	}
	if it, _ := st.Item(KindEvent, "e1"); !it.TaggedAt.IsZero() {
		t.Fatalf("item = %+v", it)
	}

	m.open, m.calls = true, 0
	c, _ = st.Chat("lark-f1")
	if err := e.Compress(ctx, c); err != nil {
		t.Fatal(err)
	}
	// Compressing named a project and dirtied it too: the three plus that one.
	if n := e.RefreshProjects(ctx, 10); n < 3 {
		t.Fatalf("refreshed %d", n)
	}
	before := m.calls
	e.DedupeProjects(ctx, 3)
	if m.calls == before || len(st.dedupeVerdicts()) == 0 {
		t.Fatal("the pairs were not asked about again")
	}
	if _, err := e.TagItems(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.Chat("lark-f1"); c.Pending != 0 {
		t.Fatalf("chat = %+v", c)
	}
	for _, slug := range []string{"a", "b", "c"} {
		if p, _ := st.Project(slug); p.Dirty {
			t.Fatalf("project %s is still dirty", slug)
		}
	}
	if it, _ := st.Item(KindEvent, "e1"); it.TaggedAt.IsZero() {
		t.Fatalf("item = %+v", it)
	}
}
