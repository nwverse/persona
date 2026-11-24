package assistant

import (
	"fmt"
	"testing"
	"time"
)

// A reply to a card of days ago still goes to its agent after many more cards were bound since; a card older than
// the keep is let go, and a full table drops its oldest.
func TestFeishuOldCardStillRoutesItsReply(t *testing.T) {
	s := newTestService(t, newFakeHub().withMachine())
	now := time.Unix(10_000_000, 0)
	s.setLarkNow(func() time.Time { return now })
	agent := botTarget{Kind: targetAgent, MachineID: "server", SessionID: "a1"}
	s.larkModule().RememberThread("om_first", agent, "Claude")
	for i := 0; i < 150; i++ {
		now = now.Add(time.Hour)
		s.larkModule().RememberThread(fmt.Sprintf("om_%d", i), botTarget{Kind: targetPersona, MachineID: "server", SessionID: "m1"}, "主对话")
	}
	if got := s.larkModule().Route("om_first", "om_first"); got != agent {
		t.Fatalf("a card of six days ago routes to %+v", got)
	}
	now = now.Add(botThreadsKeepFor)
	s.larkModule().RememberThread("om_new", agent, "Claude")
	if got := s.larkModule().Route("om_first", "om_first"); got.Kind != targetMain {
		t.Fatalf("a card past the keep still routes to %+v", got)
	}

	threads := map[string]*botThread{}
	for i := 0; i < botThreadsKeep+5; i++ {
		threads[fmt.Sprint(i)] = &botThread{At: now.Add(time.Duration(i) * time.Second)}
	}
	pruneBotThreads(threads, now.Add(time.Hour))
	if len(threads) != botThreadsKeep || threads["0"] != nil || threads["4"] != nil || threads["5"] == nil {
		t.Fatalf("kept %d, oldest %v", len(threads), threads["5"] != nil)
	}
}

// A stored thread without a time (from a state written before or by hand) is kept: it is not 30 days old.
func TestFeishuThreadWithoutATimeIsKept(t *testing.T) {
	threads := map[string]*botThread{"om_old": {Target: botTarget{Kind: targetMain}}}
	pruneBotThreads(threads, time.Unix(10_000_000, 0))
	if threads["om_old"] == nil {
		t.Fatal("a thread without a time was dropped")
	}
}
