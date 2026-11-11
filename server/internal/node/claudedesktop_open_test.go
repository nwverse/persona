package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const desktopLocal = "eeeeeeee-0000-4000-8000-000000000001" // the app's own id of sidA's session: local_<this>

// recordDesktopLinks replaces the opener: each link it is asked to open is recorded and sent on the channel returned.
func recordDesktopLinks(t *testing.T, fail error) <-chan string {
	t.Helper()
	links := make(chan string, 4)
	old := openDesktopLink
	openDesktopLink = func(_ context.Context, link string) error {
		links <- link
		return fail
	}
	t.Cleanup(func() { openDesktopLink = old })
	return links
}

// sendAsync sends text to id on another goroutine; the result comes on the channel.
func sendAsync(n *Node, id, text string) <-chan error { return sendWithin(context.Background(), n, id, text) }

func sendWithin(ctx context.Context, n *Node, id, text string) <-chan error {
	done := make(chan error, 1)
	b, _ := json.Marshal(map[string]any{"session_id": id, "text": text, "client_msg_id": newUUID()})
	go func() {
		_, err := n.handle(ctx, "session.send", b)
		done <- err
	}()
	return done
}

func herdrAsked(f *fakeHerdr) string {
	for _, c := range f.trace() {
		if strings.Contains(c, "workspace") || strings.Contains(c, "tab.create") || strings.Contains(c, "pane.") {
			return c
		}
	}
	return ""
}

// A session of the desktop app's sidebar that nothing runs is opened in the app, in the background, at its own link;
// the message goes to the inbox of the claude the app starts for it. Nothing is resumed or typed in a terminal.
func TestADesktopSessionNothingRunsIsOpenedInTheApp(t *testing.T) {
	n, f, home := herdrNode(t)
	noPaneEnv(t)
	shortPeerWait(t, 3*time.Second)
	first, _ := useDesktopDirs(t)
	p := bgTranscript(t, home, home, sidA, "plan")
	writeDesktopEntry(t, first, desktopLocal, sidA, false, nil, "")
	inbox := newFakeInbox(t, p)
	links := recordDesktopLinks(t, nil)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reset()
	done := sendAsync(n, sidA, "go on with the plan")
	select {
	case link := <-links:
		if link != "claude://claude.ai/epitaxy/local_"+desktopLocal {
			t.Fatalf("opened %q", link)
		}
	case err := <-done:
		t.Fatalf("sent without opening the app: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the app was not asked to open the session")
	}
	time.Sleep(300 * time.Millisecond) // the app starts the session's claude
	writeOutsideClaude(t, home, sidA, "claude-desktop-3p", "idle", inbox, nil)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("send: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the send did not end")
	}
	users := inbox.users()
	if len(users) != 1 {
		t.Fatalf("the inbox read %d messages", len(users))
	}
	if content, _ := users[0]["message"].(map[string]any)["content"].(string); !strings.Contains(content, "go on with the plan") {
		t.Errorf("message: %v", users[0])
	}
	if c := herdrAsked(f); c != "" {
		t.Fatalf("herdr was asked to start or type something: %s", c)
	}
	// Running now, it is sent to as any session the app runs: the app is not asked again.
	if err := <-sendAsync(n, sidA, "and then?"); err != nil {
		t.Fatalf("second send: %v", err)
	}
	if len(links) != 0 || len(inbox.users()) != 2 {
		t.Fatalf("second send: %d links, %d messages", len(links), len(inbox.users()))
	}
}

// The app does not start it in time, or cannot be asked: refused, saying so, with nothing sent anywhere.
func TestADesktopSessionTheAppDoesNotOpenIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail error
		want string
	}{{"no claude in time", nil, "30 秒内"}, {"the opener fails", errors.New("no handler"), "没能让"}} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home := herdrNode(t)
			noPaneEnv(t)
			first, _ := useDesktopDirs(t)
			bgTranscript(t, home, home, sidA, "plan")
			writeDesktopEntry(t, first, desktopLocal, sidA, false, nil, "")
			links := recordDesktopLinks(t, tc.fail)
			old := desktopOpenWait
			desktopOpenWait = 300 * time.Millisecond
			t.Cleanup(func() { desktopOpenWait = old })
			f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
			f.reset()
			err := <-sendAsync(n, sidA, "still there?")
			if refusalCode(err) != desktopOpenFailed || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("send: %v", err)
			}
			if len(links) != 1 {
				t.Fatalf("%d links opened", len(links))
			}
			if c := herdrAsked(f); c != "" {
				t.Fatalf("herdr was asked to start or type something: %s", c)
			}
		})
	}
}

// Only the sidebar's sessions, by the app's own id, and only messages go to the app: an archived session, an entry whose
// sessionId is not the app's form, and a slash command (the inbox runs none) go the old way.
func TestOnlyTheSidebarsSessionsAndMessagesGoToTheApp(t *testing.T) {
	for _, tc := range []struct {
		name, extra, text string
		archived          bool
	}{
		{"archived", "", "go on", true},
		{"not the app's id", `,"sessionId":"../../etc"`, "go on", false},
		{"a command", "", "/compact", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home := herdrNode(t)
			noPaneEnv(t)
			first, _ := useDesktopDirs(t)
			bgTranscript(t, home, home, sidA, "plan")
			writeDesktopEntry(t, first, desktopLocal, sidA, tc.archived, nil, tc.extra)
			links := recordDesktopLinks(t, nil)
			f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
			// The old way (a claude --resume in herdr) is not this test's business: it is cut short.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			select {
			case <-sendWithin(ctx, n, sidA, tc.text):
			case <-time.After(20 * time.Second):
				t.Fatal("the send did not end")
			}
			if len(links) != 0 {
				t.Fatalf("the app was asked to open it: %s", <-links)
			}
		})
	}
}

// The index keeps an unarchived session's local id, only in the app's own form (it goes into a link).
func TestTheIndexKeepsTheAppsOwnID(t *testing.T) {
	first, _ := useDesktopDirs(t)
	home := t.TempDir()
	writeDesktopEntry(t, first, desktopLocal, sidA, false, nil, "")
	writeDesktopEntry(t, first, "ffffffff-0000-4000-8000-000000000002", sidB, false, nil, `,"sessionId":"local_x y"`)
	x := readClaudeDesktopIndex(home)
	if got := x.about[sidA].local; got != "local_"+desktopLocal {
		t.Errorf("local id %q", got)
	}
	if got := x.about[sidB].local; got != "" {
		t.Errorf("a malformed id kept: %q", got)
	}
}
