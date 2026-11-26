package assistant

import (
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A node's sessions.changed is handled on the read loop of that node's connection (rpc.Peer.Serve, devices'
// OnEvent, NodeEvent, setSessions), and only that loop reads the node's answers. The main conversation's attention
// scan read the conversation from the same node inside setSessions, so after every finished main-chat turn it waited
// for an answer nobody could read: 15 s until its deadline, with every other reply and event of the node held behind
// it (pings, the apps' reads, item pushes, the turn's end) (#16214). Here the node's answer to that read is held, as
// that loop would hold it: the list must still be taken, and once the node answers the verdict lands and the row is
// published again with it.
func TestMainConversationAttentionLeavesTheNodeReadLoop(t *testing.T) {
	ft := newFleetTaskTest(t)
	setSettings(t, ft.s, func(v *Settings) { v.MachineID = "server" }) // the main conversation runs on the hub machine's node
	at := time.Date(2026, 11, 26, 6, 16, 4, 0, time.UTC)
	ft.f.setItems(attentionTestRenumber(attentionTestItems(at, at.Add(time.Minute))[3:])...) // one completed silent round
	release := make(chan struct{})
	var releaseOnce sync.Once
	free := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(free)
	held := make(chan string, 64)
	ft.f.hookReads(receiptHooks{beforeCall: func(p DeviceCall) {
		select {
		case held <- p.Method:
		default:
		}
		<-release
	}})

	taken := make(chan struct{})
	go func() {
		ft.sessions("server", proto.Session{SessionID: "conv1", Assistant: true, Status: proto.StatusDone, Rev: 11})
		close(taken)
	}()
	select {
	case <-taken:
	case <-time.After(10 * time.Second):
		var waiting []string
		for len(held) > 0 {
			waiting = append(waiting, <-held)
		}
		t.Fatalf("the node's sessions.changed waited for the node itself (held calls: %v): its handler runs on that "+
			"node's read loop, which alone reads the answer (#16214)", waiting)
	}

	select {
	case method := <-held:
		if method != DeviceSessionItems {
			t.Fatalf("the first call to the node was %s, want the scan's %s", method, DeviceSessionItems)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the attention scan never read the main conversation")
	}
	if row := listedRow(ft.lastList("server"), "conv1"); row.State != proto.StateUnread {
		t.Fatalf("before the scan answered, the main conversation was published as %q, want unread", row.State)
	}

	free()
	deadline := time.Now().Add(10 * time.Second)
	for {
		entry, ok := ft.s.conversationAttentionCached("server", "conv1", 11)
		row := listedRow(ft.lastList("server"), "conv1")
		if ok && entry.Silent && row.State == proto.StateIdle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the node answered: verdict %+v (cached %v), published state %q; want the silent round "+
				"classified and the row published again as idle", entry, ok, row.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// listedRow is session's row in list (zero when it is not there).
func listedRow(list []proto.Session, session string) proto.Session {
	for _, row := range list {
		if row.SessionID == session {
			return row
		}
	}
	return proto.Session{}
}
