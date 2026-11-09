package assistant

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// A card's push spares the phone (LinkCovered) only when a summary the manager's Persona Link desktop took rang for it:
// the card that stands for its agent, notifying, among the listed items. These tests have the manager look at the
// fleet by hand (as liveLoop does), so what each summary holds is the test's to say, and check every card's push.

// coverFlow is a manager with n blocked agents on "mac" (s01…), one Persona Link desktop that takes every summary the
// manager hands over (the first as its baseline), and an assistant that decides on questions when told (decided).
type coverFlow struct {
	s       *Service
	f       *fakeHub
	d       *fleetDevs
	decided chan DelegateResult
}

func newCoverFlow(t *testing.T, n int, delegate bool) *coverFlow {
	t.Helper()
	f, d := newFakeHub(), newFleetDevs()
	d.add("mac", "Mac")
	d.linkDesktop = true
	st := newFleetMem()
	s := New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), Devices: d})
	f.hello.Agents.Delegate.Enabled = &delegate
	var list []proto.Session
	for i := 1; i <= n; i++ {
		// Waiting since two hours ago: a card that comes now is newer than the agents without one (screen items).
		list = append(list, proto.Session{SessionID: fmt.Sprintf("s%02d", i), Title: fmt.Sprintf("Task %d", i), Cwd: "/code/app",
			Status: proto.StatusBlocked, UpdatedAt: time.Now().Add(-2 * time.Hour).UTC()})
	}
	d.setSessions("mac", list)
	f.setSessions("mac", list...)
	cf := &coverFlow{s: s, f: f, d: d, decided: make(chan DelegateResult)}
	s.delegateHook = func(ctx context.Context, _ DelegateParams) (DelegateResult, error) {
		select {
		case r := <-cf.decided:
			return r, nil
		case <-ctx.Done():
			return DelegateResult{}, ctx.Err()
		}
	}
	return cf
}

// look is one of liveLoop's looks: the summary of the fleet as it is now, handed to the devices.
func (cf *coverFlow) look() {
	agents, ixs, told := cf.s.fleetInputs()
	cf.d.SetAttention(linkAttention(cf.s.lang(context.Background()), agents, ixs, told))
}

// raise has a hook raise ix (it waits for an answer until the test ends) and waits until the card is pending.
func (cf *coverFlow) raise(t *testing.T, ix proto.Interaction) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _, _ = cf.s.waitInteraction(ctx, ix) }()
	waitUntil(t, func() bool { _, ok := cf.s.pendingIx(ix.ID); return ok })
}

// told waits until n cards are the user's (tell rang them).
func (cf *coverFlow) told(t *testing.T, n int) {
	t.Helper()
	waitUntil(t, func() bool { _, _, told := cf.s.fleetInputs(); return len(told) >= n })
}

func permissionCard(id, session string, at time.Time) proto.Interaction {
	return proto.Interaction{ID: id, MachineID: "mac", SessionID: session, Kind: proto.IxPermission, ToolName: "Bash",
		Summary: "npm test", CreatedAt: at.UTC(), ExpiresAt: at.Add(24 * time.Hour).UTC()}
}

// pushes waits for n card pushes and returns them by card; every one that spared the phone was rung for by a summary
// the desktop took after its baseline, as its agent's card, notifying.
func (cf *coverFlow) pushes(t *testing.T, n int) map[string]Notification {
	t.Helper()
	out := map[string]Notification{}
	for deadline := time.Now().Add(10 * time.Second); len(out) < n; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d card pushes, want %d", len(out), n)
		}
		for _, p := range cf.f.pushed() {
			if p.Kind == assistantwire.NotifyInput && p.Interaction != nil {
				out[p.Interaction.ID] = p
			}
		}
	}
	for id, p := range out {
		if p.LinkCovered && !rangIn(cf.d.handedAttention(), id) {
			t.Fatalf("card %s spared the phone, but no summary the desktop took rang for it", id)
		}
	}
	return out
}

// 21 agents' cards come within one look: the summary lists the newest 20, the oldest in More. The 20 are left to
// Persona Link; the oldest, which no summary rang for, still goes to the phone.
func TestLinkCoverWhenCardsComeTogether(t *testing.T) {
	cf := newCoverFlow(t, 21, false)
	cf.look() // the desktop's baseline: nothing yet
	base := time.Now()
	for i := 1; i <= 21; i++ { // each told before the next comes: p01 stands among the first 20 when it is told
		cf.raise(t, permissionCard(fmt.Sprintf("p%02d", i), fmt.Sprintf("s%02d", i), base.Add(time.Duration(i)*time.Second)))
		cf.told(t, i)
	}
	cf.look() // one summary for all 21
	start := time.Now()
	got := cf.pushes(t, 21)
	if time.Since(start) < linkRangWait/2 {
		t.Fatal("p01's push did not wait for Persona Link: it was not among the first 20 when told, which tests nothing")
	}
	if got["p01"].LinkCovered {
		t.Fatal("p01 is in More: Persona Link rang nothing for it, yet the phone was spared")
	}
	for i := 2; i <= 21; i++ {
		if id := fmt.Sprintf("p%02d", i); !got[id].LinkCovered {
			t.Fatalf("%s: rung for by Persona Link, yet pushed to the phone too", id)
		}
	}
}

// The oldest agent's question waits on the assistant while 20 newer agents' cards are rung for; handed to the user
// then, it is past the cap (its time is when it came): it goes to the phone.
func TestLinkCoverQuestionHandedPastTheCap(t *testing.T) {
	cf := newCoverFlow(t, 21, true)
	cf.look()
	base := time.Now().Add(-time.Hour)
	q := proto.Interaction{ID: "q01", MachineID: "mac", SessionID: "s01", Kind: proto.IxQuestion,
		Questions: []proto.Question{{Question: "Which branch?", Options: []proto.QuestionOption{{Label: "main"}, {Label: "feat/x"}}}},
		CreatedAt: base.UTC(), ExpiresAt: base.Add(24 * time.Hour).UTC()}
	cf.raise(t, q)
	waitUntil(t, func() bool { ix, ok := cf.s.pendingIx("q01"); return ok && ix.Delegating })
	for i := 2; i <= 21; i++ {
		cf.raise(t, permissionCard(fmt.Sprintf("p%02d", i), fmt.Sprintf("s%02d", i), base.Add(time.Duration(i)*time.Second)))
	}
	cf.told(t, 20)
	cf.look()
	cf.pushes(t, 20)
	cf.decided <- DelegateResult{} // the assistant leaves it to the user: tell rings it
	cf.told(t, 21)
	cf.look()
	if got := cf.pushes(t, 21); got["q01"].LinkCovered {
		t.Fatal("a question past the cap spared the phone")
	}
}

// One agent with two cards: the summary stands for it by the older one, so only that one is left to Persona Link; the
// second goes to the phone.
func TestLinkCoverSecondCardOfAnAgent(t *testing.T) {
	cf := newCoverFlow(t, 1, false)
	cf.look()
	base := time.Now().Add(-time.Minute)
	cf.raise(t, permissionCard("a1", "s01", base))
	cf.told(t, 1)
	cf.look()
	start := time.Now()
	cf.raise(t, permissionCard("b1", "s01", base.Add(time.Second)))
	cf.told(t, 2)
	cf.look()
	got := cf.pushes(t, 2)
	if took := time.Since(start); took > linkRangWait/2 {
		t.Fatalf("the second card waited %v for Persona Link, which never rings for it", took)
	}
	if !got["a1"].LinkCovered {
		t.Fatal("the card that stands for the agent was pushed to the phone too")
	}
	if got["b1"].LinkCovered {
		t.Fatal("the agent's second card, which no summary rings for, spared the phone")
	}
}

// Without a Persona Link desktop every card goes to the phone at once.
func TestLinkCoverWithoutADesktop(t *testing.T) {
	cf := newCoverFlow(t, 1, false)
	cf.d.mu.Lock()
	cf.d.linkDesktop = false
	cf.d.mu.Unlock()
	cf.look()
	start := time.Now()
	cf.raise(t, permissionCard("a1", "s01", time.Now()))
	if got := cf.pushes(t, 1); got["a1"].LinkCovered || time.Since(start) > linkRangWait {
		t.Fatal("a card spared the phone, or waited, with no desktop")
	}
}
