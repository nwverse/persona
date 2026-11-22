package node

// Keeping a Mac awake while it is operated or watched (docs/extensions/computer-use.md "Locking, preventing sleep and per-App approval"): one
// caffeinate -dimsu at a time, held for awakeAfter past the last operation and for as long as its live screen is
// open, then let go. macOS locks when the display dims from idleness; Cua Driver holds that off itself for its own
// calls (0.37+), but not while someone only watches the screen. No system setting is changed: the assertion is
// caffeinate's and goes with it, and -w ties it to this node.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var caffeinatePath = "/usr/bin/caffeinate"

var pmsetPath = "/usr/bin/pmset"

// lockScreen puts the display to sleep now (pmset displaysleepnow): with macOS asking for the password once the
// display sleeps (the default), the Mac locks. The operations' hold goes first; a screen still watched keeps its own
// (the Mac is locked behind it all the same).
func lockScreen(ctx context.Context) error {
	if installOS != "darwin" {
		return errors.New("只有 Mac 能锁上")
	}
	awake.mu.Lock()
	awake.opsUntil = time.Time{}
	awake.reconcile()
	awake.mu.Unlock()
	if out, err := exec.CommandContext(ctx, pmsetPath, "displaysleepnow").CombinedOutput(); err != nil {
		return fmt.Errorf("没锁上：%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// awakeAfter is how long the last operation keeps the Mac awake, and how far ahead the screen being watched does.
var awakeAfter = 10 * time.Minute

type awakeHold struct {
	mu       sync.Mutex
	proc     *exec.Cmd
	ends     time.Time // when the running caffeinate lets go by itself
	opsUntil time.Time
	watching int
	renew    *time.Timer
}

var awake awakeHold

// operated: an operation on this Mac now.
func (a *awakeHold) operated() {
	if installOS != "darwin" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.opsUntil = time.Now().Add(awakeAfter)
	a.reconcile()
}

// watch: its live screen opened; done, when it closes.
func (a *awakeHold) watch() (done func()) {
	if installOS != "darwin" {
		return func() {}
	}
	a.mu.Lock()
	a.watching++
	a.reconcile()
	a.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			a.watching--
			a.reconcile()
			a.mu.Unlock()
		})
	}
}

// reconcile holds what is wanted now (a.mu held): while watched, awakeAfter ahead, renewed shortly before it ends
// (a tenth of it, at most a minute); else until the last operation's awakeAfter; nothing once that has passed.
func (a *awakeHold) reconcile() {
	if a.renew != nil {
		a.renew.Stop()
		a.renew = nil
	}
	now := time.Now()
	want := a.opsUntil
	if a.watching > 0 {
		want = now.Add(awakeAfter)
	}
	if !want.After(now) {
		a.stop()
		return
	}
	margin := min(awakeAfter/10, time.Minute)
	if a.proc == nil || a.ends.Before(want.Add(-margin)) || a.ends.After(want.Add(margin)) {
		a.start(want.Sub(now))
	}
	if a.watching > 0 && a.proc != nil {
		a.renew = time.AfterFunc(time.Until(a.ends.Add(-margin)), func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.reconcile()
		})
	}
}

// start a caffeinate for d, then let the one before go (no gap between them).
func (a *awakeHold) start(d time.Duration) {
	secs := int((d + time.Second - 1) / time.Second)
	cmd := exec.Command(caffeinatePath, "-dimsu", "-t", strconv.Itoa(secs), "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return
	}
	old := a.proc
	a.proc, a.ends = cmd, time.Now().Add(time.Duration(secs)*time.Second)
	go func() {
		_ = cmd.Wait()
		a.mu.Lock()
		if a.proc == cmd {
			a.proc = nil
		}
		a.mu.Unlock()
	}()
	if old != nil && old.Process != nil {
		_ = old.Process.Kill()
	}
}

func (a *awakeHold) stop() {
	if a.proc != nil && a.proc.Process != nil {
		_ = a.proc.Process.Kill()
	}
	a.proc = nil
}

// held is how long the caffeinate there is now has left; 0 when there is none (tests).
func (a *awakeHold) held() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc == nil {
		return 0
	}
	return time.Until(a.ends)
}

// watchedConn is the live screen's connection (port.dial of 5900 with keep_awake): closing it ends the watch.
type watchedConn struct {
	net.Conn
	done func()
}

func (c *watchedConn) Close() error {
	c.done()
	return c.Conn.Close()
}

// noLockFor: how long "Don't auto-lock while the extension is on" holds without the hub saying so again (it does every few minutes,
// computer.nolock), so the Mac goes back to locking by itself that long after the hub is gone.
var noLockFor = 30 * time.Minute

// lockHold is caffeinate -dims for noLockFor (no -u: a display already off stays off), renewed by each computer.nolock
// with on, let go with off.
type lockHold struct {
	mu   sync.Mutex
	proc *exec.Cmd
}

var noLock lockHold

func (l *lockHold) set(on bool) {
	if installOS != "darwin" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	old := l.proc
	l.proc = nil
	if on {
		cmd := exec.Command(caffeinatePath, "-dims", "-t", strconv.Itoa(int(noLockFor/time.Second)), "-w", strconv.Itoa(os.Getpid()))
		if cmd.Start() == nil {
			l.proc = cmd
			go func() {
				_ = cmd.Wait()
				l.mu.Lock()
				if l.proc == cmd {
					l.proc = nil
				}
				l.mu.Unlock()
			}()
		}
	}
	if old != nil && old.Process != nil {
		_ = old.Process.Kill()
	}
}

func (l *lockHold) held() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.proc != nil
}
