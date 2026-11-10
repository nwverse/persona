package node

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// promptLands makes the transcript of the session claude was started with (--session-id) get the user's message
// after the try-th agent.prompt (1 when omitted), as Claude writes it when it takes a message; earlier ones are dropped.
func promptLands(t *testing.T, f *fakeHerdr, home, cwd string, try ...int) {
	t.Helper()
	at := 1
	if len(try) > 0 {
		at = try[0]
	}
	var mu sync.Mutex
	prompts, id := 0, ""
	f.after(func(c herdrCall) {
		mu.Lock()
		defer mu.Unlock()
		switch c.Method {
		case "pane.send_input":
			if m := sessionIDArg.FindStringSubmatch(str(c.Params["text"])); m != nil {
				id = m[1]
			}
		case "agent.prompt":
			if prompts++; prompts == at {
				writeUserLine(t, home, cwd, id, str(c.Params["text"]))
			}
		}
	})
}

var sessionIDArg = regexp.MustCompile(`--session-id (\S+)`)

// startedID is the session id the start typed (--session-id).
func startedID(f *fakeHerdr) string {
	for _, c := range f.calls() {
		if m := sessionIDArg.FindStringSubmatch(str(c.Params["text"])); c.Method == "pane.send_input" && m != nil {
			return m[1]
		}
	}
	return ""
}

func writeUserLine(t *testing.T, home, cwd, id, text string) {
	writeTranscript(t, home, cwd, id, time.Now(),
		`{"type":"user","timestamp":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","cwd":"`+cwd+`","message":{"role":"user","content":"`+text+`"}}`)
}

// updatedScreen is a fresh Claude that updated itself on start and shows the auto mode notice above its empty box
// (2026-11-05: the first message typed then was dropped, #2423).
func updatedScreen(t *testing.T) (plain, ansi string) {
	empty := screenFixture(t, "claude-2.1.312-empty-162.txt")
	notice := "\n  ✓ Updated to latest. Got 3 features, 14 bugfixes\n\n" +
		"  Auto mode lets Claude handle permission prompts automatically\n"
	lines := strings.SplitN(empty, "\n", 4)
	return strings.Join(lines[:3], "\n") + notice + lines[3], screenFixture(t, "claude-2.1.312-empty-162.ansi")
}

// startPane makes herdr open pane w1:p2 for a start into workspace w1, with a claude that is idle there.
func startPane(t *testing.T, f *fakeHerdr, plain, ansi string) {
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	startHerdr(f, "w1:p2", proto.StatusIdle)
	f.setScreens("w1:p2", plain, ansi)
}

// The first message typed while Claude finishes drawing its update notice is dropped (the transcript never gets it,
// the box stays empty): once the box is ready again it is typed a second time, and that one goes in.
func TestStartTypesADroppedFirstMessageAgain(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	plain, ansi := updatedScreen(t)
	startPane(t, f, plain, ansi)
	promptLands(t, f, home, cwd, 2)

	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the build"}); err != nil {
		t.Fatal(err)
	}
	n.firstWait.Wait()
	if _, ok := n.lostFirst.get(startedID(f)); ok {
		t.Error("marked as lost")
	}
	prompt := call("agent.prompt", map[string]any{"target": "w1:p2", "text": "fix the build"})
	if got := f.trace("agent.prompt", "pane.send_keys"); len(got) != 2 || got[0] != prompt || got[1] != prompt {
		t.Errorf("typed %v", got)
	}
	// Typed again only after the box was seen ready (twice) and empty.
	trace := squeeze(f.trace())
	i := strings.Index(strings.Join(trace, "\n"), prompt)
	rest := strings.Join(trace, "\n")[i+len(prompt):]
	if !strings.Contains(rest, agentGet("w1:p2")+"\n"+readVisible("w1:p2")) || !strings.Contains(rest, readANSI("w1:p2")+"\n"+prompt) {
		t.Errorf("not checked before typing again:\n%s", rest)
	}
}

// A first message that stays in the box (typed, not submitted) gets one Enter, and is not typed a second time.
func TestStartEntersAFirstMessageLeftInTheBox(t *testing.T) {
	withSubmitGrace(t, 100*time.Millisecond)
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	startPane(t, f, screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	typedTxt, typedANSI := screenFixture(t, "claude-2.1.312-typed-162.txt"), screenFixture(t, "claude-2.1.312-typed-162.ansi")
	f.after(func(c herdrCall) {
		switch {
		case c.Method == "agent.prompt": // the box keeps showing it ("fix the flaky test")
			f.setScreens("w1:p2", typedTxt, typedANSI)
		case isKeys("enter")(c):
			writeUserLine(t, home, cwd, startedID(f), "fix the flaky test")
		}
	})
	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the flaky test"}); err != nil {
		t.Fatal(err)
	}
	n.firstWait.Wait()
	assertTrace(t, f.trace("agent.prompt", "pane.send_keys"), []string{
		call("agent.prompt", map[string]any{"target": "w1:p2", "text": "fix the flaky test"}),
		sendKeys("w1:p2", "enter"),
	})
	if _, ok := n.lostFirst.get(startedID(f)); ok {
		t.Error("marked as lost")
	}
}

// A first message with a control character goes in inert, and the box showing it so is that message left there: it gets
// its Enter.
func TestStartEntersAnInertFirstMessageLeftInTheBox(t *testing.T) {
	withSubmitGrace(t, 100*time.Millisecond)
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	startPane(t, f, screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	inert := func(s string) string {
		return strings.Replace(s, "fix the flaky test", "<U+0007>fix the flaky test", 1)
	}
	typedTxt, typedANSI := inert(screenFixture(t, "claude-2.1.312-typed-162.txt")), inert(screenFixture(t, "claude-2.1.312-typed-162.ansi"))
	f.after(func(c herdrCall) {
		switch {
		case c.Method == "agent.prompt":
			f.setScreens("w1:p2", typedTxt, typedANSI)
		case isKeys("enter")(c):
			writeUserLine(t, home, cwd, startedID(f), "<U+0007>fix the flaky test")
		}
	})
	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "\afix the flaky test"}); err != nil {
		t.Fatal(err)
	}
	n.firstWait.Wait()
	assertTrace(t, f.trace("agent.prompt", "pane.send_keys"), []string{
		call("agent.prompt", map[string]any{"target": "w1:p2", "text": "<U+0007>fix the flaky test"}),
		sendKeys("w1:p2", "enter"),
	})
}

// A first message that never reaches the transcript is typed firstPromptTries times; then the session is listed as
// needing the user, saying why, until a message from the phone goes in.
func TestStartMarksANeverDeliveredFirstMessage(t *testing.T) {
	withSubmitGrace(t, 50*time.Millisecond)
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	plain, ansi := updatedScreen(t)
	startPane(t, f, plain, ansi)

	res, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the build"})
	if err != nil {
		t.Fatal(err)
	}
	// Answered at once, as before; the check goes on in the background.
	id := decodeSessions(t, []any{res["session"]})[0].SessionID
	n.firstWait.Wait()
	if got := f.trace("agent.prompt"); len(got) != firstPromptTries {
		t.Errorf("typed %d times: %v", len(got), got)
	}
	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(id, proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
	if l := listSessions(t, n); len(l) != 1 || l[0].Status != proto.StatusBlocked || !strings.Contains(l[0].AttentionReason, "送了 3 次都没进去") {
		t.Fatalf("listed %+v", l)
	}

	// Sent again from the phone: it goes in, and the session is what herdr says again.
	if _, err := request(t, n, "session.send", map[string]any{"session_id": id, "text": "fix the build"}); err != nil {
		t.Fatal(err)
	}
	if l := listSessions(t, n); len(l) != 1 || l[0].Status != proto.StatusIdle || l[0].AttentionReason != "" {
		t.Errorf("listed after the send %+v", l)
	}
}

// The transcript getting a message typed at the computer clears it too.
func TestALostFirstMessageClearsOnceTheTranscriptHasOne(t *testing.T) {
	n, _, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	n.lostFirst.set(startID, lostDialog, time.Now())
	if why := n.lostFirstPrompt(startID, proto.StatusIdle); why != lostDialog {
		t.Fatalf("why %q", why)
	}
	writeUserLine(t, home, cwd, startID, "typed at the computer")
	if why := n.lostFirstPrompt(startID, proto.StatusIdle); why != "" {
		t.Errorf("still lost: %q", why)
	}
	n.lostFirst.set(startID, lostDialog, time.Now().Add(time.Minute))
	if why := n.lostFirstPrompt(startID, proto.StatusWorking); why != "" {
		t.Errorf("a working agent still lost: %q", why)
	}
}

// A claude that turns to working without the transcript showing the message yet took it: not typed twice.
func TestStartDoesNotRetypeForAWorkingClaude(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	startPane(t, f, screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	f.after(func(c herdrCall) {
		if c.Method == "agent.prompt" {
			startHerdr(f, "w1:p2", proto.StatusWorking)
		}
	})
	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the build"}); err != nil {
		t.Fatal(err)
	}
	n.firstWait.Wait()
	if got := f.trace("agent.prompt"); len(got) != 1 {
		t.Errorf("typed %v", got)
	}
	if _, ok := n.lostFirst.get(startedID(f)); ok {
		t.Error("marked as lost")
	}
}
