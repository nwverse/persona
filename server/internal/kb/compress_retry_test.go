package kb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
)

// failingModel answers every call with err, or with the fake digest once err is nil.
type failingModel struct {
	err   error
	calls int
}

func (m *failingModel) JSONMax(ctx context.Context, system, user string, n int, out any) error {
	m.calls++
	if m.err != nil {
		return m.err
	}
	return (&fakeModel{}).JSONMax(ctx, system, user, n, out)
}

func dueChat(t *testing.T, e *Engine) *Chat {
	t.Helper()
	at := time.Now().Add(-time.Hour)
	c := &Chat{ID: "lark-chat", Source: "lark", SourceID: "chat", Kind: "dm", Name: "张三", Created: at.Add(-time.Hour), LastAt: at}
	if err := e.Store.PutChat(c); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.AppendMessages(c, []Line{{At: at, Sender: "张三", Text: "示例：周五见"}}); err != nil {
		t.Fatal(err)
	}
	c.Pending = 1
	if err := e.Store.PutChat(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func isDue(e *Engine, id string, now time.Time) bool {
	for _, c := range e.DueForCompression(now) {
		if c.ID == id {
			return true
		}
	}
	return false
}

// The model declines the same messages every time: the batch is settled without a summary instead of being sent again
// every round (it was, every two minutes for three days).
func TestCompressSettlesARefusedBatch(t *testing.T) {
	m := &failingModel{err: ai.ErrRefused}
	e := testEngine(t, m)
	c := dueChat(t, e)
	err := e.Compress(context.Background(), c)
	if !errors.Is(err, ai.ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
	got, _ := e.Store.Chat(c.ID)
	if got.Pending != 0 || !got.CoveredUntil.Equal(c.LastAt.Add(time.Minute)) || got.CompressFailures != 0 {
		t.Errorf("chat = pending %d, covered until %v, failures %d; want 0, %v, 0", got.Pending, got.CoveredUntil, got.CompressFailures, c.LastAt.Add(time.Minute))
	}
	if !got.CompressedAt.IsZero() || e.Store.Summary(KindChat, c.ID) != "" {
		t.Errorf("a refused batch wrote a summary (compressed at %v)", got.CompressedAt)
	}
	if isDue(e, c.ID, time.Now()) {
		t.Error("the refused chat is still due")
	}
}

// Any other failure puts the chat off, longer with each failure in a row; a success clears that.
func TestCompressBacksOffAfterFailures(t *testing.T) {
	m := &failingModel{err: errors.New("ai: no JSON object in reply")}
	e := testEngine(t, m)
	c := dueChat(t, e)
	for i, want := range []time.Duration{compressRetry, 2 * compressRetry, 4 * compressRetry} {
		start := time.Now()
		if err := e.Compress(context.Background(), c); err == nil {
			t.Fatalf("failure %d: no error", i+1)
		}
		got, _ := e.Store.Chat(c.ID)
		if got.CompressFailures != i+1 || got.Pending != 1 {
			t.Fatalf("failure %d: failures %d, pending %d", i+1, got.CompressFailures, got.Pending)
		}
		if wait := got.CompressRetryAt.Sub(start); wait < want || wait > want+time.Minute {
			t.Errorf("failure %d: next try in %v, want %v", i+1, wait, want)
		}
		if isDue(e, c.ID, start) || !isDue(e, c.ID, got.CompressRetryAt) {
			t.Errorf("failure %d: due now or not due at %v", i+1, got.CompressRetryAt)
		}
	}

	m.err = nil
	if err := e.Compress(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Store.Chat(c.ID)
	if got.CompressFailures != 0 || !got.CompressRetryAt.IsZero() || got.Pending != 0 {
		t.Errorf("after success: failures %d, retry at %v, pending %d", got.CompressFailures, got.CompressRetryAt, got.Pending)
	}
}

func TestCompressRetryIsCapped(t *testing.T) {
	m := &failingModel{err: errors.New("boom")}
	e := testEngine(t, m)
	c := dueChat(t, e)
	c.CompressFailures = 40 // a shift this far would overflow
	if err := e.Store.PutChat(c); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = e.Compress(context.Background(), c)
	got, _ := e.Store.Chat(c.ID)
	if wait := got.CompressRetryAt.Sub(start); wait < compressRetryMax || wait > compressRetryMax+time.Minute {
		t.Errorf("next try in %v, want %v", wait, compressRetryMax)
	}
}
