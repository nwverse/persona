package node

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func listedRuntime(t *testing.T, n *Node, id string) (*fakeRuntime, *runtimeConn) {
	t.Helper()
	rt := dialRuntime(t, n, dshHello())
	if err := rt.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": id}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "runtime listed", func() bool { return n.runtimes.ownerOf(id) != nil })
	return rt, n.runtimes.ownerOf(id)
}

func TestRuntimeSessionPublishesCoalesce(t *testing.T) {
	n, hub := runtimeNode(t)
	_, h := listedRuntime(t, n, "coalesce")
	waitFor(t, "initial list", func() bool { return len(hub.lastList()) > 0 })
	waitFor(t, "initial build finished", func() bool {
		n.listPush.mu.Lock()
		defer n.listPush.mu.Unlock()
		return !n.listPush.building
	})
	n.listPush.mu.Lock()
	before := n.listPush.pushed
	n.listPush.mu.Unlock()
	offlineCache.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 20 {
			raw := json.RawMessage(fmt.Sprintf(`{"sessions":[{"session_id":"coalesce","title":"title-%d"}]}`, i))
			n.runtimeHost.event(h, "runtime.sessions", raw)
		}
	}()
	returned := false
	select {
	case <-done:
		returned = true
	case <-time.After(time.Second):
	}
	offlineCache.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("session events did not finish")
	}
	if !returned {
		t.Fatal("runtime.sessions blocked on session list construction")
	}
	waitFor(t, "latest list", func() bool {
		for _, s := range hub.lastList() {
			if s.SessionID == "coalesce" && s.Title == "title-19" {
				return true
			}
		}
		return false
	})
	waitFor(t, "coalesced builds finished", func() bool {
		n.listPush.mu.Lock()
		defer n.listPush.mu.Unlock()
		return !n.listPush.building
	})
	n.listPush.mu.Lock()
	builds := n.listPush.pushed - before
	n.listPush.mu.Unlock()
	if builds != 2 {
		t.Fatalf("burst built %d lists, want 2", builds)
	}
}

func TestRuntimeRewindResetsRewatchCursor(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          int
	}{
		{"history change", `"from_seq":20,"to_seq":5,"history":"new"`, 5},
		{"full replacement", `"from_seq":0,"to_seq":5,"history":"old"`, 5},
		{"empty replacement", `"from_seq":0,"to_seq":0,"history":"old"`, 0},
		{"omitted start", `"to_seq":5,"history":"old"`, 20},
		{"older batch", `"from_seq":5,"to_seq":6,"history":"old"`, 20},
		{"empty history", `"from_seq":5,"to_seq":6,"history":""`, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, hub := runtimeNode(t)
			rt, _ := listedRuntime(t, n, "rewind")
			if _, err := nodeCall(t, n, "session.watch", map[string]any{"session_id": "rewind", "since_seq": 20}); err != nil {
				t.Fatal(err)
			}
			if err := rt.peer.Notify("session.items", json.RawMessage(`{"session_id":"rewind","from_seq":0,"to_seq":20,"history":"old","items":[]}`)); err != nil {
				t.Fatal(err)
			}
			if err := rt.peer.Notify("session.items", json.RawMessage(`{"session_id":"rewind","items":[],`+tc.payload+`}`)); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "replacement forwarded", func() bool {
				hub.mu.Lock()
				defer hub.mu.Unlock()
				return len(hub.items) == 2
			})
			if got := n.runtimes.lastPushed("rewind"); got != tc.want {
				t.Fatalf("cursor after rewrite = %d, want %d", got, tc.want)
			}
			_ = rt.conn.Close()
			waitFor(t, "runtime gone", func() bool {
				return n.runtimes.ownerOf("rewind") == nil
			})
			next, _ := listedRuntime(t, n, "rewind")
			waitFor(t, "runtime rewatched", func() bool { return len(next.got("session.watch")) == 1 })
			var p struct {
				SinceSeq int `json:"since_seq"`
			}
			if err := json.Unmarshal(next.got("session.watch")[0], &p); err != nil {
				t.Fatal(err)
			}
			if p.SinceSeq != tc.want {
				t.Fatalf("rewatch cursor = %d, want %d", p.SinceSeq, tc.want)
			}
		})
	}
}

func TestRuntimeLatestFoldKeepsCurrentNarration(t *testing.T) {
	n, _ := runtimeNode(t)
	rt, _ := listedRuntime(t, n, "fold")
	items := []proto.Item{
		{ID: "u", Seq: 1, Role: "user", Kind: proto.KindText, Text: "Build it"},
		{ID: "t1", Seq: 2, Role: "assistant", Kind: proto.KindThinking, Text: "First"},
		{ID: "t2", Seq: 3, Role: "assistant", Kind: proto.KindThinking, Text: "Second"},
		{ID: "t3", Seq: 4, Role: "assistant", Kind: proto.KindThinking, Text: "Third"},
		{ID: "n", Seq: 5, Role: "assistant", Kind: proto.KindText, Narration: true, Text: "Still working"},
	}
	rt.mu.Lock()
	rt.answer = func(method string, params json.RawMessage) (any, error) {
		return map[string]any{"items": items, "history": "h", "to_seq": 5}, nil
	}
	rt.mu.Unlock()
	for _, before := range []int{0, 10} {
		t.Run(fmt.Sprintf("before_%d", before), func(t *testing.T) {
			raw, err := nodeCall(t, n, "session.items", map[string]any{"session_id": "fold", "fold_work": true, "before_seq": before})
			if err != nil {
				t.Fatal(err)
			}
			var page struct {
				Items   []proto.Item `json:"items"`
				History string       `json:"history"`
				ToSeq   int          `json:"to_seq"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				t.Fatal(err)
			}
			narration, folds := 0, 0
			for _, it := range page.Items {
				if it.Narration {
					narration++
				}
				if it.Kind == proto.KindFold {
					folds++
				}
			}
			want := 0
			if before == 0 {
				want = 1
			}
			if narration != want || folds != 1 || page.History != "h" || page.ToSeq != 5 {
				t.Fatalf("folded page = %s; want narration=%d, one fold and preserved span", raw, want)
			}
		})
	}
}

func TestRuntimeDeltaWaitsForPendingDelta(t *testing.T) {
	n, hub := runtimeNode(t)
	_, h := listedRuntime(t, n, "delta")
	n.deltaMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.runtimeEvent(h, "session.delta", json.RawMessage(`{"session_id":"delta","text":"current"}`))
	}()
	early := false
	select {
	case <-done:
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	n.deltaMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("delta did not finish")
	}
	if early {
		t.Fatal("runtime delta bypassed a pending delta")
	}
	waitFor(t, "delta forwarded", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.deltas) == 1
	})
}

func TestRuntimeWatchHistoryLifecycle(t *testing.T) {
	for _, action := range []string{"unwatch", "reset", "unwatched"} {
		t.Run(action, func(t *testing.T) {
			n, _ := runtimeNode(t)
			_, h := listedRuntime(t, n, "lifecycle")
			push := func(seq int) {
				n.runtimeEvent(h, "session.items", json.RawMessage(fmt.Sprintf(`{"session_id":"lifecycle","history":"h","to_seq":%d,"items":[]}`, seq)))
			}
			if action != "unwatched" {
				n.runtimes.watch(h, "lifecycle", 20, nil)
			}
			push(20)
			switch action {
			case "unwatch":
				n.runtimes.unwatch("lifecycle")
			case "reset":
				n.runtimes.resetWatches()
			}
			n.runtimes.watch(h, "lifecycle", 20, nil)
			push(5)
			if got := n.runtimes.lastPushed("lifecycle"); got != 5 {
				t.Fatalf("new watch retained old history after %s: cursor %d, want 5", action, got)
			}
		})
	}
}
