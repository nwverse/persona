package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/hubedge"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/update"
)

// The new build did not confirm the update and goes back, but the edge cannot start the old one (#10826, review
// finding H-CE1, model counterexample HI-CE6): its standby fails, so the slot's switch to it returns an error and this
// build keeps serving. The update must still settle: not stay "confirming" (which turns every later update request
// into a silent no-op), say why, tell the owner, and not report a rollback while the build it names is the one running.
func TestUpdateRollbackThatCannotStartSettles(t *testing.T) {
	p := newPublisher(t, "v2-202611021700", 202611021700)
	h, m, target, _ := updatingHub(t, p, "release")
	h.build = update.Make("v2-202611021700", "release", "202611021700", p.srv.URL+"/dl")
	h.version = h.build.Version
	m.online = false // the machine that was online before the restart does not come back
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho new\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target+".prev", []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var told []string
	h.upd.notify = func(title, body string) { mu.Lock(); told = append(told, title+": "+body); mu.Unlock() }
	h.upd.exec = func(string) error { return errors.New("the new hub did not start: database: connection refused") }
	pending := update.Pending{FromVersion: "v1-202611021500", FromBuild: 202611021500, ToVersion: "v2-202611021700",
		ToBuild: 202611021700, Target: target, Prev: target + ".prev", Online: []string{"box"}}
	if err := update.Save(update.PendingPath(h.upd.dir), pending); err != nil {
		t.Fatal(err)
	}
	if !bootUpdater(t, h.upd) {
		t.Fatal("the new build has no update to confirm")
	}
	h.upd.confirm(context.Background(), pending) // confirmIn is 300 ms here: the rollback runs, its switch fails

	v := h.upd.view()
	if v.State == "confirming" {
		t.Fatalf("the update stays confirming after a rollback that could not start (error %q)", v.Error)
	}
	if v.State != "failed" || !strings.Contains(v.Error, "database: connection refused") {
		t.Fatalf("state %q error %q, want failed with the reason the old build did not start", v.State, v.Error)
	}
	h.upd.mu.Lock()
	active := h.upd.updateActiveLocked()
	h.upd.mu.Unlock()
	if active {
		t.Fatal("the update still counts as under way: every later update request would be ignored")
	}
	mu.Lock()
	n := len(told)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("the owner was told %d times, want once: %q", n, told)
	}
	if b, _ := os.ReadFile(target); string(b) != "#!/bin/sh\necho old\n" {
		t.Fatal("the files are not back on the old build (the next restart would run the bad one again)")
	}
	// The hourly check: the marker names the build that is still running, so it is not a rollback yet.
	h.upd.recordRolledBack(context.Background())
	if v := h.upd.view(); v.State == "rolled_back" {
		t.Fatalf("reported %q while %s is still the build serving", v.Error, h.version)
	}
	var rb update.RolledBack
	if ok, _ := update.Load(update.RolledBackPath(h.upd.dir), &rb); !ok || rb.ToBuild != 202611021700 {
		t.Fatal("the rollback marker is gone: the old build would not record the rollback when it runs")
	}
}

// The edge asks the hub right before it releases it (#10826, model counterexample HI-CE1): a conversation that started
// after the idle wait keeps the hub, the update waits for idle again with its files in place, and hands over once all
// is idle. The start the dropped standby counted is not a failed start.
func TestUpdateWaitsAgainWhenBusyAtRelease(t *testing.T) {
	p := newPublisher(t, "v2-202611021700", 202611021700)
	h, m, target, _ := updatingHub(t, p, "release")
	var mu sync.Mutex
	calls := 0
	h.upd.handover = func(ctx context.Context, path string, build int64) error {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		var pend update.Pending
		if ok, _ := update.Load(update.PendingPath(h.upd.dir), &pend); ok {
			pend.Starts++ // the standby's boot
			update.Save(update.PendingPath(h.upd.dir), pend)
		}
		if n == 1 {
			setSessions(m, proto.Session{SessionID: "s1", Title: "a new turn", Status: proto.StatusWorking})
			if ok, why := h.upd.idleForRelease(); ok || why != "1 busy" {
				t.Errorf("idleForRelease = %v %q with a conversation working", ok, why)
			}
			return fmt.Errorf("%w: 1 busy", hubedge.ErrBusy)
		}
		if ok, why := h.upd.idleForRelease(); !ok {
			t.Errorf("idleForRelease = %v %q with all idle", ok, why)
		}
		return nil
	}
	ctx := context.Background()
	if _, err := h.upd.check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.upd.begin(ctx, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "waiting for idle again", func() bool { v := h.upd.view(); return v.State == "waiting_idle" && len(v.Busy) == 1 })
	if b, _ := os.ReadFile(target); !bytes.Equal(b, p.script) {
		t.Fatal("the new binary is not in place while the update waits again")
	}
	var pend update.Pending
	if ok, _ := update.Load(update.PendingPath(h.upd.dir), &pend); !ok || pend.Starts != 0 {
		t.Fatalf("pending %v %+v: want it kept, with the dropped standby's start not counted", ok, pend)
	}
	setSessions(m, proto.Session{SessionID: "s1", Status: proto.StatusIdle})
	waitFor(t, "the second handover", func() bool { mu.Lock(); defer mu.Unlock(); return calls == 2 })
	waitFor(t, "the update handed over", func() bool { return h.upd.view().State == "restarting" })
}

// The wait after a busy answer keeps the update's limits: a manual update gives up after manualIdle, and the files
// go back as they were.
func TestUpdateBusyAtReleaseGivesUpLikeTheWait(t *testing.T) {
	p := newPublisher(t, "v2-202611021700", 202611021700)
	h, m, target, _ := updatingHub(t, p, "release")
	h.upd.manualIdle = 200 * time.Millisecond
	h.upd.handover = func(context.Context, string, int64) error {
		setSessions(m, proto.Session{SessionID: "s1", Status: proto.StatusWorking})
		return fmt.Errorf("%w: 1 busy", hubedge.ErrBusy)
	}
	ctx := context.Background()
	if _, err := h.upd.check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.upd.begin(ctx, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the update giving up", func() bool { return h.upd.view().State == "failed" })
	if b, _ := os.ReadFile(target); string(b) != "#!/bin/sh\necho old\n" {
		t.Fatal("the old binary is not back")
	}
	if _, err := os.Stat(update.PendingPath(h.upd.dir)); !os.IsNotExist(err) {
		t.Fatal("the pending marker stayed")
	}
	if v := h.upd.view(); !strings.Contains(v.Error, su(h.userLang(), "err_busy")) {
		t.Fatalf("error %q, want the busy reason", v.Error)
	}
}

// "Update now" is the person's consent to interrupt: the edge's last question is answered idle.
func TestIdleForReleaseAfterUpdateNow(t *testing.T) {
	p := newPublisher(t, "v2-202611021700", 202611021700)
	h, m, _, _ := updatingHub(t, p, "release")
	setSessions(m, proto.Session{SessionID: "s1", Status: proto.StatusWorking})
	if ok, _ := h.upd.idleForRelease(); ok {
		t.Fatal("idle with a conversation working")
	}
	h.upd.mu.Lock()
	h.upd.now = true
	h.upd.mu.Unlock()
	if ok, why := h.upd.idleForRelease(); !ok {
		t.Fatalf("not idle after Update now: %q", why)
	}
}
