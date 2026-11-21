package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestFeishuTopicBlockedNativeAllowsLaterProgressAndAnswer(t *testing.T) {
	for _, method := range []string{FeishuCotCreate, FeishuCotAppend} {
		t.Run(method, func(t *testing.T) {
			s, _, _ := cotHub(t, false)
			r, prog := topicCotRun(s)
			r.Started = time.Now().Add(-time.Minute)
			prog.from = 1
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			s.hub.(*fakeHub).answerAppBlocking(AppFeishu, method, func(json.RawMessage) (any, error) {
				select {
				case <-entered:
				default:
					close(entered)
				}
				<-release
				return map[string]any{"cot_id": "cot1", "message_id": "om_cot"}, nil
			})
			first := make(chan struct{})
			go func() { s.channelProgressTick(r.ID); close(first) }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("native request did not start")
			}
			s.chans.mu.Lock()
			prog.updated = time.Now().Add(-time.Minute)
			prog.dirty = true
			prog.steps = append(prog.steps, "second progress")
			s.chans.mu.Unlock()
			s.channelProgressTick(r.ID)
			if got := len(s.hub.(*fakeHub).appParams(AppFeishu, FeishuUpdateCard)); got == 0 {
				t.Error("later progress card update was blocked by native I/O")
			}
			answered := make(chan struct{})
			go func() { s.finishChannelRun(r, &channel.Result{Answer: "Synthetic answer"}); close(answered) }()
			select {
			case <-answered:
			case <-time.After(time.Second):
				unblock()
				<-first
				<-answered
				t.Fatal("answer waited for native I/O")
			}
			s.chans.mu.Lock()
			w := prog.native
			s.chans.mu.Unlock()
			unblock()
			<-first
			if w != nil {
				select {
				case <-w.done:
				case <-time.After(2 * time.Second):
					t.Fatal("native worker did not end")
				}
			}
		})
	}
}

// nativeCancelHub models a transport that respects the request context, unlike
// the late-response fake above. Only its first selected request blocks.
type nativeCancelHub struct {
	Hub
	method   string
	entered  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func (h *nativeCancelHub) AppCall(ctx context.Context, p AppCall) (json.RawMessage, error) {
	blocked := false
	if p.AppID == AppFeishu && p.Method == h.method {
		h.once.Do(func() { blocked = true; close(h.entered) })
	}
	if blocked {
		<-ctx.Done()
		close(h.canceled)
		return nil, ctx.Err()
	}
	return h.Hub.AppCall(ctx, p)
}

func TestFeishuTopicFinishCancelsNativeProgress(t *testing.T) {
	for _, method := range []string{FeishuCotCreate, FeishuCotAppend} {
		t.Run(method, func(t *testing.T) {
			s, posts, _ := cotHub(t, false)
			r, p := topicCotRun(s)
			h := &nativeCancelHub{Hub: s.hub, method: method, entered: make(chan struct{}), canceled: make(chan struct{})}
			s.hub = h
			feishuSink{s}.progress(context.Background(), r, time.Second, nil, "")
			select {
			case <-h.entered:
			case <-time.After(time.Second):
				t.Fatal("native request did not start")
			}
			s.finishChannelRun(r, &channel.Result{Answer: "Synthetic answer"})
			select {
			case <-h.canceled:
			case <-time.After(time.Second):
				t.Fatal("finish did not cancel native progress")
			}
			select {
			case <-p.native.done:
			case <-time.After(2 * time.Second):
				t.Fatal("native worker did not finish")
			}
			answers := 0
			for _, post := range *posts {
				if card, _ := post["card"].(string); strings.Contains(card, "Synthetic answer") {
					answers++
				}
			}
			if answers != 1 {
				t.Fatalf("answer deliveries = %d", answers)
			}
			f := h.Hub.(*fakeHub)
			if method == FeishuCotCreate && len(f.appParams(AppFeishu, FeishuCotCreate)) != 0 {
				t.Fatal("ambiguous canceled creation was retried")
			}
			if method == FeishuCotAppend {
				// The canceled batch is not replayed; the known message still receives its end.
				if got := len(f.appParams(AppFeishu, FeishuCotAppend)); got != 1 {
					t.Fatalf("appends after cancellation = %d, want only terminal batch", got)
				}
			}
		})
	}
}

func TestFeishuTopicNativeWorkerCoalescesOrderedSnapshots(t *testing.T) {
	s, _, calls := cotHub(t, false)
	r, p := topicCotRun(s)
	f := s.hub.(*fakeHub)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	original := f.apps[AppFeishu+" "+FeishuCotAppend]
	var first sync.Once
	f.answerAppBlocking(AppFeishu, FeishuCotAppend, func(raw json.RawMessage) (any, error) {
		block := false
		first.Do(func() { block = true; close(entered) })
		if block {
			<-release
		}
		return original(raw)
	})
	feishuSink{s}.progress(context.Background(), r, time.Second, nil, "")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native append did not start")
	}
	for i := 0; i < 60; i++ {
		s.chans.mu.Lock()
		p.items = append(p.items, proto.Item{ID: fmt.Sprintf("think-%d", i), Kind: proto.KindThinking, Role: "assistant", Text: fmt.Sprintf("ordered-%d", i)})
		s.chans.mu.Unlock()
		feishuSink{s}.progress(context.Background(), r, time.Duration(i+2)*time.Second, nil, "")
	}
	s.chans.mu.Lock()
	w := p.native
	s.chans.mu.Unlock()
	w.mu.Lock()
	if !w.queued || len(w.pending) != len(p.items) || len(w.wake) > 1 {
		t.Errorf("pending snapshot was not coalesced: items=%d wake=%d", len(w.pending), len(w.wake))
	}
	w.mu.Unlock()
	unblock()
	waitTopicCot(t, s, r)
	finishTopicCot(t, s, r, &channel.Result{Answer: "Synthetic answer"})
	var all string
	for _, params := range f.appParams(AppFeishu, FeishuCotAppend) {
		raw, _ := json.Marshal(params["events"])
		all += string(raw)
	}
	last := -1
	for i := 0; i < 60; i++ {
		needle := fmt.Sprintf(`ordered-%d\"`, i)
		pos := strings.Index(all, needle)
		if pos <= last || strings.Count(all, needle) != 1 {
			t.Fatalf("native event %d missing, repeated or reordered", i)
		}
		last = pos
	}
	if got := strings.Join(*calls, " "); strings.Count(got, "RUN_STARTED") != 1 || strings.Count(got, "RUN_FINISHED") != 1 {
		t.Fatalf("native lifecycle = %s", got)
	}
	if n := len(f.appParams(AppFeishu, FeishuCotAppend)); n > 7 {
		t.Fatalf("burst produced %d appends; pending ticks were not bounded", n)
	}
}

func TestFeishuTopicLateNativeResponseCannotMutateHeldNextTurn(t *testing.T) {
	s, _, _ := cotHub(t, false)
	r, p := topicCotRun(s)
	f := s.hub.(*fakeHub)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var attempt int
	f.answerAppBlocking(AppFeishu, FeishuCotCreate, func(json.RawMessage) (any, error) {
		attempt++
		if attempt == 1 {
			close(entered)
			<-release
			return map[string]any{"cot_id": "old-cot", "message_id": "old-message"}, nil
		}
		return map[string]any{"cot_id": "next-cot", "message_id": "next-message"}, nil
	})
	feishuSink{s}.progress(context.Background(), r, time.Second, nil, "")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old native create did not start")
	}
	s.chans.mu.Lock()
	old := p.native
	s.chans.mu.Unlock()
	s.finishChannelRun(r, &channel.Result{Answer: "Synthetic partial answer", Held: true})
	s.chans.mu.Lock()
	if p.native != nil {
		t.Error("held turn retained old native worker")
	}
	p.items = []proto.Item{{ID: "next", Kind: proto.KindThinking, Role: "assistant", Text: "Synthetic next turn"}}
	s.chans.mu.Unlock()
	// A late result from the previous turn must be fenced even while the next run
	// reuses the same Run and chanLive pointers.
	unblock()
	select {
	case <-old.done:
	case <-time.After(2 * time.Second):
		t.Fatal("old native finish did not end")
	}
	for _, key := range []string{"cot", "cot_msg", "cot_para", "cot_done", "cot_off"} {
		if value := s.runExt(r, key); value != "" {
			t.Fatalf("late old-turn write: %s=%q", key, value)
		}
	}
	topicCotProgress(t, s, r, time.Second, nil, "")
	if got := s.runExt(r, "cot"); got != "next-cot" {
		t.Fatalf("next turn native ID = %q", got)
	}
	finishTopicCot(t, s, r, &channel.Result{Answer: "Synthetic final answer"})
	var ids []string
	for _, params := range f.appParams(AppFeishu, FeishuCotAppend) {
		ids = append(ids, params["cot_id"].(string))
	}
	if len(ids) < 2 || ids[0] != "old-cot" || ids[len(ids)-1] != "next-cot" {
		t.Fatalf("native turn identities = %v", ids)
	}
}

func TestFeishuTopicQuickAnswerDoesNotWaitForNativeCreation(t *testing.T) {
	s, posts, _ := cotHub(t, false)
	r, p := topicCotRun(s)
	f := s.hub.(*fakeHub)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	f.answerAppBlocking(AppFeishu, FeishuCotCreate, func(json.RawMessage) (any, error) {
		close(entered)
		<-release
		return map[string]any{"cot_id": "cot1", "message_id": "om_cot"}, nil
	})
	answerDone := make(chan struct{})
	go func() { s.finishChannelRun(r, &channel.Result{Answer: "Synthetic quick answer"}); close(answerDone) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("quick turn did not start native creation")
	}
	select {
	case <-answerDone:
	case <-time.After(time.Second):
		unblock()
		<-answerDone
		t.Fatal("quick answer waited for native creation")
	}
	if len(*posts) != 1 || !strings.Contains((*posts)[0]["card"].(string), "Synthetic quick answer") {
		t.Fatalf("quick answer = %v", *posts)
	}
	unblock()
	select {
	case <-p.native.done:
	case <-time.After(2 * time.Second):
		t.Fatal("quick turn native worker did not end")
	}
	if got := len(f.appParams(AppFeishu, FeishuCotCreate)); got != 1 {
		t.Fatalf("native creates = %d", got)
	}
}

func TestFeishuTopicRemovalCancelsAndFencesLateCreation(t *testing.T) {
	s, _, _ := cotHub(t, false)
	r, initial := topicCotRun(s)
	s.chans.mu.Lock()
	delete(s.chans.live, r.ID)
	s.chans.mu.Unlock()
	watched := make(chan struct{})
	go func() { s.watchChannelRun(r); close(watched) }()
	var p *chanLive
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, p = s.channelRunByID(r.ID)
		if p != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if p == nil {
		t.Fatal("watcher did not start")
	}
	s.chans.mu.Lock()
	p.items = initial.items
	s.chans.mu.Unlock()
	f := s.hub.(*fakeHub)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	f.answerAppBlocking(AppFeishu, FeishuCotCreate, func(json.RawMessage) (any, error) {
		close(entered)
		<-release
		return map[string]any{"cot_id": "late-cot", "message_id": "late-message"}, nil
	})
	feishuSink{s}.progress(context.Background(), r, time.Second, nil, "")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native create did not start")
	}
	s.chans.mu.Lock()
	w := p.native
	s.chans.st.Runs = nil
	s.chans.mu.Unlock()
	// Wait until removal has canceled this generation before releasing its reply.
	deadline = time.Now().Add(2 * time.Second)
	aborted := false
	for time.Now().Before(deadline) {
		w.mu.Lock()
		aborted = w.aborted
		w.mu.Unlock()
		if aborted {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !aborted {
		t.Error("removed run did not cancel native work")
	}
	select {
	case <-watched:
	case <-time.After(time.Second):
		t.Error("removed run watcher did not stop")
	}
	unblock()
	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("removed native worker did not end")
	}
	if s.runExt(r, "cot") != "" {
		t.Fatal("late create mutated a removed run")
	}
	if got := len(f.appParams(AppFeishu, FeishuCotAppend)); got != 0 {
		t.Fatalf("removed run appended %d batches", got)
	}
	if got := len(f.appParams(AppFeishu, FeishuCotComplete)); got != 1 {
		t.Fatalf("late known message was completed %d times", got)
	}
}
