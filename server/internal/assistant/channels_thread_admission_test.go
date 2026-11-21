package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/proto"
)

// Hold the first start outside the fake's lock, as a slow node would.
type threadAdmissionHub struct {
	*fakeHub
	starts    atomic.Int32
	entered   chan struct{}
	release   chan struct{}
	second    chan struct{}
	once      sync.Once
	failFirst bool
}

func (h *threadAdmissionHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method == DeviceAssistantSend {
		switch h.starts.Add(1) {
		case 1:
			close(h.entered)
			select {
			case <-h.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if h.failFirst {
				return nil, errors.New("synthetic start failure")
			}
		case 2:
			h.once.Do(func() { close(h.second) })
		}
	}
	raw, err := h.fakeHub.DeviceCall(ctx, p)
	if p.Method == DeviceAssistantSend && err == nil {
		var r struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(raw, &r)
		h.fakeHub.mu.Lock()
		h.fakeHub.sessions[p.Device] = append(h.fakeHub.sessions[p.Device], proto.Session{SessionID: r.SessionID, MachineID: p.Device, Assistant: true})
		h.fakeHub.mu.Unlock()
	}
	return raw, err
}

func TestChannelReceiveConcurrentFirstThreadMessages(t *testing.T) {
	s, f, _ := threadsChannelService(t, true)
	h := &threadAdmissionHub{fakeHub: f, entered: make(chan struct{}), release: make(chan struct{}), second: make(chan struct{})}
	s.hub = h
	var release sync.Once
	defer release.Do(func() { close(h.release) })
	receive := func(id string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"id": id, "text": "synthetic request", "thread": "chat_example:root_example"})
		if err := s.ChannelReceive(context.Background(), ChannelReceive{Channel: "examplechat", Message: raw}); err != nil {
			t.Fatal(err)
		}
	}
	receive("first")
	select {
	case <-h.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never started")
	}
	receive("second")
	// A second start while the first is unresolved proves the routing race. On a
	// serialized implementation the observation deadline expires instead.
	select {
	case <-h.second:
	case <-time.After(200 * time.Millisecond):
	}
	release.Do(func() { close(h.release) })
	waitUntil(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sends)+len(f.sentOnce) >= 2 })
	if got := h.starts.Load(); got != 1 {
		t.Fatalf("same thread started %d conversations, want one start and one continuation", got)
	}
}

func receiveAdmissionMessage(t *testing.T, s *Service, id, text, thread string) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"id": id, "text": text, "thread": thread, "forward": "synthetic preceding context", "quote": "synthetic quoted message"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChannelReceive(context.Background(), ChannelReceive{Channel: "examplechat", Message: raw}); err != nil {
		t.Fatal(err)
	}
}

func waitAdmissionDrained(t *testing.T, s *Service) {
	t.Helper()
	waitUntil(t, func() bool { s.chans.mu.Lock(); defer s.chans.mu.Unlock(); return len(s.chans.receiving) == 0 })
}

func TestChannelReceiveThreadNewFollowsInFlightStart(t *testing.T) {
	s, f, _ := threadsChannelService(t, true)
	h := &threadAdmissionHub{fakeHub: f, entered: make(chan struct{}), release: make(chan struct{}), second: make(chan struct{})}
	s.hub = h
	var release sync.Once
	defer release.Do(func() { close(h.release) })
	receiveAdmissionMessage(t, s, "first", "first request", "topic")
	select {
	case <-h.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never started")
	}
	receiveAdmissionMessage(t, s, "new", "/new", "topic")
	receiveAdmissionMessage(t, s, "later", "later request", "topic")
	release.Do(func() { close(h.release) })
	waitAdmissionDrained(t, s)
	bound := boundThread(s, "topic")
	f.mu.Lock()
	defer f.mu.Unlock()
	if h.starts.Load() != 2 || len(f.sends) != 2 || len(f.sentOnce) != 0 {
		t.Fatalf("/new did not start the following request afresh: starts=%d sends=%d continuations=%d", h.starts.Load(), len(f.sends), len(f.sentOnce))
	}
	if bound != "s2" {
		t.Fatalf("binding after /new: %q, want second conversation", bound)
	}
}

func TestChannelReceiveOtherThreadDoesNotWait(t *testing.T) {
	s, f, _ := threadsChannelService(t, true)
	h := &threadAdmissionHub{fakeHub: f, entered: make(chan struct{}), release: make(chan struct{}), second: make(chan struct{})}
	s.hub = h
	var release sync.Once
	defer release.Do(func() { close(h.release) })
	receiveAdmissionMessage(t, s, "first", "first request", "topic-a")
	select {
	case <-h.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never started")
	}
	receiveAdmissionMessage(t, s, "other", "independent request", "topic-b")
	select {
	case <-h.second:
	case <-time.After(5 * time.Second):
		t.Fatal("another topic waited for the blocked topic")
	}
	waitUntil(t, func() bool { return boundThread(s, "topic-b") != "" })
	receiveAdmissionMessage(t, s, "plain", "unthreaded request", "")
	waitUntil(t, func() bool { return lastSendOnceSession(f) == "m1" })
	release.Do(func() { close(h.release) })
	waitAdmissionDrained(t, s)
	if boundThread(s, "topic-a") == boundThread(s, "topic-b") {
		t.Fatal("independent topics share a conversation")
	}
}

func TestChannelReceiveThreadStartFailureReleasesNext(t *testing.T) {
	s, f, _ := threadsChannelService(t, true)
	h := &threadAdmissionHub{fakeHub: f, entered: make(chan struct{}), release: make(chan struct{}), second: make(chan struct{}), failFirst: true}
	s.hub = h
	var release sync.Once
	defer release.Do(func() { close(h.release) })
	receiveAdmissionMessage(t, s, "failed", "failed request", "topic")
	select {
	case <-h.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never started")
	}
	receiveAdmissionMessage(t, s, "retry", "next request", "topic")
	release.Do(func() { close(h.release) })
	waitAdmissionDrained(t, s)
	if h.starts.Load() != 2 || f.sent() != 1 || boundThread(s, "topic") == "" {
		t.Fatalf("failed start held the next message: starts=%d successful=%d binding=%q", h.starts.Load(), f.sent(), boundThread(s, "topic"))
	}
}

func TestChannelReceiveThreadRoutingCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name    string
		threads bool
		thread  string
	}{
		{"no capability", false, "topic"}, {"no key", true, ""}, {"blank key", true, "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, _ := threadsChannelService(t, tc.threads)
			receiveAdmissionMessage(t, s, "legacy", "original request", tc.thread)
			waitUntil(t, func() bool { return lastSendOnceSession(f) == "m1" })
			waitAdmissionDrained(t, s)
			if f.sent() != 0 || boundThread(s, "topic") != "" {
				t.Fatal("legacy input opened a thread conversation")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.sentOnce) != 1 {
				t.Fatalf("legacy input delivered %d times", len(f.sentOnce))
			}
			text, _ := f.sentOnce[0]["text"].(string)
			if !strings.Contains(text, "original request") || !strings.Contains(text, "synthetic preceding context") || !strings.Contains(text, "synthetic quoted message") {
				t.Fatalf("context missing from delivery: %q", text)
			}
		})
	}
}

func TestChannelReceiveUnbindDiscardsQueuedThreadMessage(t *testing.T) {
	s, f, _ := threadsChannelService(t, true)
	h := &threadAdmissionHub{fakeHub: f, entered: make(chan struct{}), release: make(chan struct{}), second: make(chan struct{})}
	s.hub = h
	var release sync.Once
	defer release.Do(func() { close(h.release) })
	receiveAdmissionMessage(t, s, "first", "in flight", "topic")
	select {
	case <-h.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never started")
	}
	receiveAdmissionMessage(t, s, "old", "queued before unbind", "topic")
	if err := s.ChannelUnbound(context.Background(), ChannelUnbound{Channel: "examplechat"}); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(h.release) })
	waitAdmissionDrained(t, s)
	f.mu.Lock()
	defer f.mu.Unlock()
	if h.starts.Load() != 1 || len(f.sentOnce) != 0 {
		t.Fatalf("queued old input delivered after unbind: starts=%d continuations=%d", h.starts.Load(), len(f.sentOnce))
	}
}

func TestChannelReceiveSameThreadKeyInOtherChannelDoesNotWait(t *testing.T) {
	s, f, _ := threadsChannelService(t, true)
	f.answerApp(AppChannels, ChannelsList, func(json.RawMessage) (any, error) {
		var views []channel.View
		for _, id := range []string{"examplechat", "otherchat"} {
			views = append(views, channel.View{ID: id, Name: "Example Chat", Kind: channel.KindIM, Source: channel.SourcePlugin, Installed: true, State: channel.StateConnected, Capabilities: channel.Capabilities{Threads: true, Asks: channel.AsksNone}})
		}
		return map[string]any{"channels": views}, nil
	})
	s.forgetChannelList()
	h := &threadAdmissionHub{fakeHub: f, entered: make(chan struct{}), release: make(chan struct{}), second: make(chan struct{})}
	s.hub = h
	var release sync.Once
	defer release.Do(func() { close(h.release) })
	receiveAdmissionMessage(t, s, "first", "first channel request", "same-key")
	select {
	case <-h.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never started")
	}
	raw, _ := json.Marshal(map[string]string{"id": "other", "text": "another channel request", "thread": "same-key"})
	if err := s.ChannelReceive(context.Background(), ChannelReceive{Channel: "otherchat", Message: raw}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.second:
	case <-time.After(5 * time.Second):
		t.Fatal("another channel waited for the blocked channel")
	}
	waitUntil(t, func() bool { return s.channelThreadOf(context.Background(), "otherchat", "same-key") != nil })
	release.Do(func() { close(h.release) })
	waitAdmissionDrained(t, s)
	other := s.channelThreadOf(context.Background(), "otherchat", "same-key")
	if other == nil || other.SessionID == boundThread(s, "same-key") {
		t.Fatal("channels with equal thread keys share a conversation")
	}
}
