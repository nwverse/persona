package voice

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/proto"
)

// Holding a call (#9290, docs/assistant/voice.md 4.9 "Suspension", suspend.go holdFrame): the app's hold frame pauses
// the upstream at once and nothing wakes it until the hold is off.

func hold(on bool) map[string]any { return map[string]any{"type": "hold", "on": on} }

func asList(v any) []any { l, _ := v.([]any); return l }

// never reads the app's frames for d: none of them may match.
func (a *app) never(d time.Duration, what string, match func(map[string]any) bool) {
	a.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case f, ok := <-a.frames:
			if !ok {
				a.t.Fatal("the plugin closed the conversation")
			}
			if match(f) {
				a.t.Fatalf("%s: %v", what, f)
			}
		case <-deadline:
			return
		}
	}
}

// A hold pauses an idle conversation's upstream at once, long before pauseAfter; a hold said again changes nothing;
// its off wakes the upstream (waking, then live, a woken session), and an off said again changes nothing. The log
// has the hold, the pause for it, the wake and the count on the call's last line.
func TestHoldPausesAtOnce(t *testing.T) {
	setVar(t, &pauseAfter, time.Hour)
	hub := agentHub(t)
	up := newSessionsUp(t, false, nil)
	pl := testPlugin(t, liveProvider(up.fakeUpstream), hub.server(t))
	logs := captureLog(t, pl)
	a := dialApp(t, pl, boundStart("a1", nil))
	if ready := a.until(ofType("ready")); !slices.Contains(asList(ready["features"]), any("hold")) {
		t.Fatalf("ready: %v", ready)
	}
	held := time.Now()
	a.send(hold(true))
	a.until(linkIn(linkPaused))
	if took := time.Since(held); took > 2*time.Second {
		t.Fatalf("paused %v after the hold", took)
	}
	up.find(t, 0, "session.close", ofType("session.close"))
	logs.wait("call 1: hold 1: on")
	logs.wait("call 1: paused the upstream (1): session 1 closed, the call is on hold")

	a.send(hold(true))
	a.never(500*time.Millisecond, "a hold said again", func(f map[string]any) bool { return f["type"] == "link" })
	a.send(hold(false))
	a.until(linkIn(linkWaking))
	a.until(linkIn(linkLive))
	logs.wait("call 1: upstream: session 2 started in", "(woken)")
	logs.wait("call 1: hold 1: off after")
	logs.wait("call 1: waking the upstream: the call is off hold")
	a.send(hold(false))
	a.never(500*time.Millisecond, "an off said again", func(f map[string]any) bool { return f["type"] == "link" })
	if n := up.count(); n != 2 {
		t.Fatalf("%d sessions", n)
	}
	for _, line := range logs.lines() {
		if strings.Contains(line, "hold 2") {
			t.Fatalf("a hold said again counted: %s", line)
		}
	}
	a.send(hold(true)) // a second hold
	a.until(linkIn(linkPaused))
	logs.wait("call 1: hold 2: on")
	_ = a.c.Close(websocket.StatusNormalClosure, "bye")
	if end := logs.wait("call 1: conversation closed"); !strings.Contains(end, "pauses 2, nudges 0, holds 2") {
		t.Errorf("the call's last line: %s", end)
	}
}

// While the call is on hold nothing wakes its upstream: the agent's result, a card, a line the user types and the
// user's voice. What is to be said and the typed line are kept and go to the session the hold's off wakes; the
// user's voice is dropped (they spoke to someone else): the new session never gets it.
func TestHoldKeepsWhatComes(t *testing.T) {
	card := proto.Interaction{ID: "q1", Kind: proto.IxQuestion, MachineID: "m", SessionID: "a1",
		Questions: []proto.Question{{Question: "数据库用哪个？", Options: []proto.QuestionOption{{Label: "Postgres"}, {Label: "SQLite"}}}}}
	for _, tt := range []struct {
		name  string
		while func(t *testing.T, a *app, hub *fakeHub)
		after func(t *testing.T, a *app, up *sessionsUp)
	}{
		{"a result", func(t *testing.T, a *app, hub *fakeHub) {
			hub.add("a1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "集成测试全部通过。"})
			hub.end("a1")
		}, func(t *testing.T, a *app, up *sessionsUp) {
			if got := content(up.find(t, 1, "the result, said", said)); !strings.Contains(got, "集成测试全部通过") {
				t.Fatalf("said: %s", got)
			}
		}},
		{"a card", func(t *testing.T, a *app, hub *fakeHub) {
			hub.raise(card)
		}, func(t *testing.T, a *app, up *sessionsUp) {
			a.until(func(f map[string]any) bool { return f["type"] == "ask" && f["state"] == askAsking })
			up.find(t, 1, "the question", func(f map[string]any) bool { return strings.Contains(content(f), "数据库用哪个") })
		}},
		{"a typed line", func(t *testing.T, a *app, hub *fakeHub) {
			a.send(map[string]any{"type": "text", "text": "数字是几"})
			a.until(finalLine("user", "数字是几")) // its caption, at once
		}, func(t *testing.T, a *app, up *sessionsUp) {
			up.find(t, 1, "the typed line", func(f map[string]any) bool {
				return f["type"] == "session.commentary.append" && f["delegation_id"] == nil && content(f) == "数字是几"
			})
		}},
		{"the user's voice", func(t *testing.T, a *app, hub *fakeHub) {
			a.speak(time.Second, true)
		}, func(t *testing.T, a *app, up *sessionsUp) {
			time.Sleep(300 * time.Millisecond)
			if amps, _ := up.audio(1); slices.Contains(amps, 3000) {
				t.Fatal("the voice of the hold went to the new session")
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setVar(t, &lookEvery, 100*time.Millisecond)
			setVar(t, &pauseAfter, time.Hour)
			hub := workingAgent(t)
			up := newSessionsUp(t, false, nil)
			pl := testPlugin(t, liveProvider(up.fakeUpstream), hub.server(t))
			a := dialApp(t, pl, boundStart("a1", nil))
			a.until(ofType("ready"))
			hub.subscribed("a1")
			time.Sleep(300 * time.Millisecond) // the call sees a1 working
			a.send(hold(true))
			a.until(linkIn(linkPaused))
			tt.while(t, a, hub)
			a.never(time.Second, "woken on hold", linkIn(linkWaking))
			if n := up.count(); n != 1 {
				t.Fatalf("%d sessions on hold", n)
			}
			a.send(hold(false))
			a.until(linkIn(linkLive))
			tt.after(t, a, up)
			if old, _ := up.audio(0); slices.Contains(old, 3000) {
				t.Fatal("the voice of the hold went to the paused session")
			}
		})
	}
}

// A hold pauses even while a card is asked by voice, which holds a quiet pause off: back from the hold, the new
// session asks it again, once.
func TestHoldWhileACardIsAsked(t *testing.T) {
	setVar(t, &lookEvery, 100*time.Millisecond)
	hub := workingAgent(t)
	hub.raise(proto.Interaction{ID: "q1", Kind: proto.IxQuestion, MachineID: "m", SessionID: "a1",
		Questions: []proto.Question{{Question: "数据库用哪个？", Options: []proto.QuestionOption{{Label: "Postgres"}, {Label: "SQLite"}}}}})
	up := newSessionsUp(t, false, nil)
	pl := testPlugin(t, liveProvider(up.fakeUpstream), hub.server(t))
	a := dialApp(t, pl, boundStart("a1", nil))
	a.until(ofType("ready"))
	a.until(func(f map[string]any) bool { return f["type"] == "ask" && f["state"] == askAsking })
	a.send(hold(true))
	a.until(linkIn(linkPaused))
	a.send(hold(false))
	a.until(linkIn(linkLive))
	asked := func() int {
		return len(slices.DeleteFunc(up.frames(1), func(f map[string]any) bool { return !strings.Contains(content(f), "数据库用哪个") }))
	}
	waitFor(t, "the question in the new session", func() bool { return asked() > 0 })
	time.Sleep(time.Second) // the tracker's looks after the session came up
	if n := asked(); n != 1 {
		t.Fatalf("asked %d times in the new session", n)
	}
}

// A hold that comes as the upstream wakes (the user took the call off hold and put it on hold again at once) pauses
// the woken session once it is up, long before pauseAfter; its off wakes it again.
func TestHoldWhileWaking(t *testing.T) {
	setVar(t, &pauseAfter, time.Hour)
	hub := agentHub(t)
	up := newSessionsUp(t, false, nil)
	pl := testPlugin(t, liveProvider(up.fakeUpstream), hub.server(t))
	a := dialApp(t, pl, boundStart("a1", nil))
	a.until(ofType("ready"))
	a.send(hold(true))
	a.until(linkIn(linkPaused))
	a.send(hold(false))
	a.send(hold(true))
	a.until(linkIn(linkWaking))
	a.until(linkIn(linkLive))
	a.until(linkIn(linkPaused))
	if n := up.count(); n != 2 {
		t.Fatalf("%d sessions", n)
	}
	up.find(t, 1, "session.close", ofType("session.close"))
	a.send(hold(false))
	a.until(linkIn(linkLive))
	if n := up.count(); n != 3 {
		t.Fatalf("%d sessions", n)
	}
}

// A hold that comes as the upstream reconnects after a drop pauses it once the new session is up, and what came
// meanwhile (the agent's result) is not said to that session: it goes to the one the hold's off wakes.
func TestHoldWhileReconnecting(t *testing.T) {
	setVar(t, &lookEvery, 100*time.Millisecond)
	setVar(t, &pauseAfter, time.Hour)
	hub := workingAgent(t)
	up := newSessionsUp(t, false, func(n int) bool { return n == 1 }) // the first reconnect fails: the next is 2 s later
	pl := testPlugin(t, liveProvider(up.fakeUpstream), hub.server(t))
	a := dialApp(t, pl, boundStart("a1", nil))
	a.until(ofType("ready"))
	hub.subscribed("a1")
	time.Sleep(300 * time.Millisecond) // the call sees a1 working
	up.drop(0)
	a.until(linkIn(linkReconnecting))
	a.send(hold(true))
	hub.add("a1", proto.Item{Role: "assistant", Kind: proto.KindText, Text: "集成测试全部通过。"})
	hub.end("a1")
	a.until(linkIn(linkLive))
	a.until(linkIn(linkPaused))
	if slices.ContainsFunc(up.frames(2), said) {
		t.Fatal("said to the session that came up on hold")
	}
	a.send(hold(false))
	a.until(linkIn(linkLive))
	if got := content(up.find(t, 3, "the result, said", said)); !strings.Contains(got, "集成测试全部通过") {
		t.Fatalf("said: %s", got)
	}
}

// A hold is not for a call that takes none: on protocol 1 and on a meeting call it changes nothing, and the log says
// so once. A hold the app sends before it has its ready is read once the call is up (the extension reads no frame
// before its ready), and pauses it then.
func TestHoldIgnored(t *testing.T) {
	t.Run("protocol 1", func(t *testing.T) {
		setVar(t, &pauseAfter, 100*time.Millisecond)
		hub := newFakeHub(t)
		hub.status("main1", "working")
		up := newSessionsUp(t, false, nil)
		pl := testPlugin(t, liveProvider(up.fakeUpstream), hub.server(t))
		logs := captureLog(t, pl)
		a := dialApp(t, pl, map[string]any{"type": "start"})
		a.until(ofType("ready"))
		a.send(hold(true))
		a.send(hold(false))
		a.send(hold(true))
		a.send(map[string]any{"type": "text", "text": "数字是几"})
		up.find(t, 0, "the typed line", func(f map[string]any) bool { return content(f) == "数字是几" })
		logs.wait("call 1: hold frames on a protocol 1 call: ignored")
		time.Sleep(300 * time.Millisecond)
		if n := strings.Count(logs.String(), "hold frames"); n != 1 {
			t.Fatalf("logged %d times", n)
		}
		if up.count() != 1 || strings.Contains(logs.String(), "paused the upstream") {
			t.Fatal("paused")
		}
		a.protocolOne()
	})

	t.Run("meeting call", func(t *testing.T) {
		setVar(t, &pauseAfter, 100*time.Millisecond)
		hub := roomHub(t)
		up := newSessionsUp(t, true, nil)
		pl := testPlugin(t, liveProvider(up.fakeUpstream), hub.server(t))
		logs := captureLog(t, pl)
		a := dialApp(t, pl, meetingStart("rec1", "room1", nil))
		a.until(ofType("ready"))
		a.send(hold(true))
		logs.wait("call 1: hold frames on a meeting call: ignored")
		a.never(500*time.Millisecond, "a meeting call on hold", func(f map[string]any) bool { return f["type"] == "link" })
		a.send(map[string]any{"type": "text", "text": "What did we decide on the vendor?"})
		hub.waitSent(1) // its lines go to the room as before
		if up.count() != 1 {
			t.Fatalf("%d sessions", up.count())
		}
	})

	t.Run("before ready", func(t *testing.T) {
		setVar(t, &pauseAfter, time.Hour)
		hub := agentHub(t)
		up := newSessionsUp(t, false, nil)
		pl := testPlugin(t, liveProvider(up.fakeUpstream), hub.server(t))
		a := dialApp(t, pl, nil)
		a.send(boundStart("a1", nil))
		a.send(hold(true))
		a.until(ofType("ready"))
		a.until(linkIn(linkPaused))
		up.find(t, 0, "its start", isStart)
	})
}
