package node

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A Claude Code screen with a message queued, as 2.1.319 draws it (#6088; the words made up).
const queuedScreen = `⏺ Bash(go test ./...)
  ⎿  Running…

✻ Waddling… (12s · ↓ 120 tokens)

❯ Also run the linter at the end.   ctrl+x ctrl+s to send now
────────────────────────────────────────
  Press up to edit queued messages · esc to interrupt`

// The same as 2.1.311 draws it in a herdr pane (#2467, read off a real one): the hint names ctrl+enter, on the line
// under the message.
const queuedScreenCtrlEnter = `  Running build.sh in the foreground · 10s
  ⎿  $ ./build.sh (10s)
     (ctrl+b to run in background)

❯ Also say the word pineapple at the end.
  ctrl+enter to send now

· Tinkering… (13s · ↓ 286 tokens)

─────────────────────────────────────────────────────────────────────────────────
❯ Press up to edit queued messages
─────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt`

// A Claude Code whose send-now names keys Persona does not know (made up).
const queuedScreenOtherKeys = `❯ Also run the linter at the end.
  ctrl+x ctrl+q to send now

✻ Waddling… (12s · ↓ 120 tokens)
────────────────────────────────────────
❯ Press up to edit queued messages`

// A message queued with no send-now hint beside it at all.
const queuedScreenNoHint = `❯ Also run the linter at the end.

✻ Waddling… (12s · ↓ 120 tokens)
────────────────────────────────────────
❯ Press up to edit queued messages`

const idleScreen = `⏺ All tests pass.

❯
────────────────────────────────────────
  ? for shortcuts`

// queueHost shows its queued screen (queuedScreen when none is given) until a send-now key is pressed (unless stuck),
// then the screen with nothing queued.
type queueHost struct {
	bareHost
	mu     sync.Mutex
	queued string
	sent   bool
	stuck  bool
	first  string
}

func (h *queueHost) Read(context.Context, host.PaneRef, host.ReadOpts) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.first != "" {
		return h.first, nil
	}
	if h.sent && !h.stuck {
		return idleScreen, nil
	}
	if h.queued != "" {
		return h.queued, nil
	}
	return queuedScreen, nil
}

func (h *queueHost) SendKeys(ctx context.Context, p host.PaneRef, keys ...string) error {
	h.mu.Lock()
	if slices.Contains(keys, "ctrl+s") || slices.Contains(keys, "ctrl+enter") {
		h.sent = true
	}
	h.mu.Unlock()
	return h.bareHost.SendKeys(ctx, p, keys...)
}

func codeOf(err error) string {
	var re *rpc.Error
	if errors.As(err, &re) {
		return re.Code
	}
	return ""
}

// Send now presses Claude Code's own send-now only while its screen offers it, the keys its hint names, and is done
// when the queue is gone.
func TestSendQueuedNow(t *testing.T) {
	defer func(d time.Duration) { sendNowWait = d }(sendNowWait)
	sendNowWait = 600 * time.Millisecond
	node := func(h *queueHost, agent string) *Node {
		return &Node{hosts: host.NewRegistry(h), agents: map[string]host.Agent{"s1": {Agent: agent, PaneID: "orca:t1:l1"}}, paths: map[string]string{}, metas: map[string]quickMeta{}, bgScans: map[string]*bgScan{}}
	}

	for screen, want := range map[string]string{queuedScreen: "ctrl+x ctrl+s", queuedScreenCtrlEnter: "ctrl+enter"} {
		h := &queueHost{bareHost: bareHost{kind: host.Orca}, queued: screen}
		if err := node(h, "claude").sendQueuedNow(context.Background(), "s1"); err != nil {
			t.Fatalf("send now with %q: %v", want, err)
		}
		if got := strings.Join(h.keys, " "); got != want {
			t.Fatalf("keys %q, want the hint's %q", got, want)
		}
	}

	// nothing queued on the screen (the queue was taken a moment ago): no key at all
	h := &queueHost{bareHost: bareHost{kind: host.Orca}, first: idleScreen}
	if err := node(h, "claude").sendQueuedNow(context.Background(), "s1"); codeOf(err) != "nothing_queued" || len(h.keys) != 0 {
		t.Fatalf("nothing queued: %v, keys %q", err, h.keys)
	}

	// not a Claude: refused before looking
	h = &queueHost{bareHost: bareHost{kind: host.Orca}}
	if err := node(h, "codex").sendQueuedNow(context.Background(), "s1"); codeOf(err) != "send_now_unsupported" || len(h.keys) != 0 {
		t.Fatalf("codex: %v, keys %q", err, h.keys)
	}

	// the key went in and the queue stays on the screen: in doubt, delivery unknown
	h = &queueHost{bareHost: bareHost{kind: host.Orca}, stuck: true}
	err := node(h, "claude").sendQueuedNow(context.Background(), "s1")
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != "send_now_in_doubt" || !strings.Contains(string(re.Data), `"delivered":"unknown"`) {
		t.Fatalf("stuck: %v", err)
	}
}

// A queue whose send-now hint Persona does not recognize is refused as that, with nothing pressed and no word of
// updating Claude Code: a hint naming other keys is quoted, and a queue the transcript has with no hint on the screen
// is refused the same way.
func TestSendQueuedNowUnrecognized(t *testing.T) {
	node := func(h *queueHost) *Node {
		return &Node{hosts: host.NewRegistry(h), agents: map[string]host.Agent{"s1": {Agent: "claude", PaneID: "orca:t1:l1"}}, paths: map[string]string{}, metas: map[string]quickMeta{}, bgScans: map[string]*bgScan{}}
	}

	h := &queueHost{bareHost: bareHost{kind: host.Orca}, first: queuedScreenOtherKeys}
	err := node(h).sendQueuedNow(context.Background(), "s1")
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != "send_now_unrecognized" || len(h.keys) != 0 {
		t.Fatalf("other keys: %v, keys %q", err, h.keys)
	}
	if !strings.Contains(re.Message, `"ctrl+x ctrl+q to send now"`) || strings.Contains(re.Message, "update") {
		t.Fatalf("message %q should quote the hint and not ask for an update", re.Message)
	}
	if !strings.Contains(string(re.Data), `"delivered":"none"`) {
		t.Fatalf("data %s", re.Data)
	}

	h = &queueHost{bareHost: bareHost{kind: host.Orca}, first: queuedScreenNoHint}
	n := node(h)
	p := filepath.Join(t.TempDir(), "s1.jsonl")
	writeLines(t, p, []string{`{"type":"queue-operation","operation":"enqueue","content":"Also run the linter at the end."}`})
	n.paths["s1"] = p
	err = n.sendQueuedNow(context.Background(), "s1")
	if !errors.As(err, &re) || re.Code != "send_now_unrecognized" || len(h.keys) != 0 || strings.Contains(re.Message, "update") {
		t.Fatalf("no hint, queued in the transcript: %v, keys %q", err, h.keys)
	}

	// the same screen with the transcript's queue empty: nothing queued, not unrecognized
	writeLines(t, p, []string{`{"type":"queue-operation","operation":"enqueue","content":"x"}`, `{"type":"queue-operation","operation":"remove","content":"x"}`})
	h = &queueHost{bareHost: bareHost{kind: host.Orca}, first: queuedScreenNoHint}
	n = node(h)
	n.paths["s1"] = p
	if err := n.sendQueuedNow(context.Background(), "s1"); codeOf(err) != "nothing_queued" || len(h.keys) != 0 {
		t.Fatalf("no hint, nothing in the transcript: %v, keys %q", err, h.keys)
	}
}

func TestSendNowOffer(t *testing.T) {
	for screen, want := range map[string]string{
		queuedScreen:          "ctrl+x ctrl+s",
		queuedScreenCtrlEnter: "ctrl+enter",
		"❯ x\n   ctrl+x   ctrl+s  to send   now": "ctrl+x ctrl+s", // spacing as the terminal lays it out
		"❯ x\n  ctrl+enter\n  to send now":       "ctrl+enter",
	} {
		if _, keys := sendNowOffer(screen); strings.Join(keys, " ") != want {
			t.Errorf("keys %q, want %q in\n%s", keys, want, screen)
		}
	}
	if hint, keys := sendNowOffer(queuedScreenOtherKeys); hint != "ctrl+x ctrl+q to send now" || keys != nil {
		t.Errorf("other keys: %q %q", hint, keys)
	}
	for _, screen := range []string{idleScreen, queuedScreenNoHint, "⏺ I am going to send now."} {
		if hint, keys := sendNowOffer(screen); hint != "" || keys != nil {
			t.Errorf("no hint in %q: %q %q", screen, hint, keys)
		}
	}
	if !offersSendNow(queuedScreen) || !offersSendNow(queuedScreenCtrlEnter) || offersSendNow(idleScreen) || offersSendNow(queuedScreenOtherKeys) {
		t.Fatal("the hint is what tells a queued message Persona can send")
	}
}
