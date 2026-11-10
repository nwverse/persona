//go:build codexlive

package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Codex panes on a shared app-server, answered over its protocol (docs/integrations/codex.md "Sessions on the daemon"): a real
// `codex app-server` on a socket of its own (never the daemon in use), a TUI attached to it in the isolated herdr, and
// a node that knows only that app-server (discovery off) and answers the cards the way the phone does. Env as
// TestLiveCodexCards; PERSONA_CODEX_DAEMON=1 runs it (scripts/test/test-codex.sh, for a Codex with app-server daemons), and
// PERSONA_CODEX_FALLBACK=<a codex without one> also checks that such a Codex beside it stays on the terminal route.
func TestLiveCodexDaemon(t *testing.T) {
	bin, work := os.Getenv("PERSONA_CODEX_BIN"), os.Getenv("PERSONA_CODEX_WORK")
	if bin == "" || work == "" || os.Getenv("PERSONA_CODEX_DAEMON") == "" {
		t.Skip("PERSONA_CODEX_BIN / PERSONA_CODEX_WORK / PERSONA_CODEX_DAEMON not set")
	}
	d := startLiveDaemon(t, bin, filepath.Join(work, "daemon"+os.Getenv("PERSONA_CODEX_COLS")))
	c := herdr.New()
	n, hub := daemonNode(t, c, d)
	lc := startLiveCodex(t, bin, work, liveOptions{name: "daemon", node: n,
		args: []string{"--remote", "unix://" + d.sock, "-C", "{dir}", "-s", "read-only"}})
	keepSynced(t, lc)
	dc := &daemonCases{lc: lc, hub: hub, d: d, bin: bin}
	if err := lc.until(30*time.Second, "the pane joined on the app-server", dc.joined); err != nil {
		route, note := n.codexRoute(lc.id)
		t.Fatalf("%v: route %s (%s)\n%s", err, route, note, lc.plain())
	}
	rounds, _ := strconv.Atoi(os.Getenv("PERSONA_CODEX_ROUNDS"))
	rounds = max(rounds, 1)
	only := map[string]bool{}
	for _, c := range strings.Split(os.Getenv("PERSONA_CODEX_CASES"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			only[c] = true
		}
	}
	cases := []struct {
		name string
		run  func(*daemonCases, int) error
	}{
		{"d-async", (*daemonCases).caseAsync},
		{"d-send", (*daemonCases).caseSend},
		{"d-draft", (*daemonCases).caseDraft},
		{"d-plan", (*daemonCases).casePlan},
		{"d-command", (*daemonCases).caseCommand},
		{"d-file", (*daemonCases).caseFile},
		{"d-permissions", (*daemonCases).casePermissions},
		{"d-deny-reason", (*daemonCases).caseDenyReason},
		{"d-computer-first", (*daemonCases).caseComputerFirst},
		{"d-phone-first", (*daemonCases).casePhoneFirst},
		{"d-both", (*daemonCases).caseBoth},
		{"d-restart", (*daemonCases).caseRestart},
		{"d-fallback", (*daemonCases).caseFallback},
	}
	failed := 0
	for r := 1; r <= rounds; r++ {
		for i, c := range cases {
			if len(only) > 0 && !only[c.name] {
				continue
			}
			start := time.Now()
			err := c.run(dc, r*100+i)
			if why := serviceError(lc.plain()); err != nil && err != errSkip && why != "" {
				t.Logf("LIVE %s round %d %s: SERVICE-RETRY: Codex's model service failed the turn (%s)", lc.version, r, c.name, why)
				lc.recover()
				_ = lc.until(30*time.Second, "joined again", dc.joined)
				start, err = time.Now(), c.run(dc, r*100+i+50)
			}
			switch {
			case err == errSkip:
				t.Logf("LIVE %s round %d %s: skipped", lc.version, r, c.name)
			case err != nil:
				failed++
				t.Errorf("LIVE %s round %d %s: FAIL after %s: %v\n%s", lc.version, r, c.name, time.Since(start).Round(time.Second), err, lc.plain())
				lc.recover()
				_ = lc.until(30*time.Second, "joined again", dc.joined)
			default:
				t.Logf("LIVE %s round %d %s: ok in %s", lc.version, r, c.name, time.Since(start).Round(time.Second))
			}
		}
	}
	if failed > 0 {
		t.Fatalf("LIVE %s: %d case(s) failed", lc.version, failed)
	}
}

// liveDaemon is a codex app-server listening on a Unix socket of its own, restartable.
type liveDaemon struct {
	t         *testing.T
	bin, sock string
	log       string
	cmd       *osexec.Cmd
}

func startLiveDaemon(t *testing.T, bin, dir string) *liveDaemon {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0o700)
	d := &liveDaemon{t: t, bin: bin, sock: filepath.Join(dir, "app.sock"), log: filepath.Join(dir, "daemon.log")}
	if err := d.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stop)
	return d
}

func (d *liveDaemon) start() error {
	args := []string{"app-server", "--listen", "unix://" + d.sock, "-c", "notify=[]", "-c", "features.request_permissions_tool=true"}
	a := strings.Fields(os.Getenv("PERSONA_CODEX_ARGS"))
	for i := 0; i < len(a); i++ {
		switch {
		case a[i] == "-m" && i+1 < len(a):
			args, i = append(args, "-c", "model="+a[i+1]), i+1
		case a[i] == "-c" && i+1 < len(a):
			args, i = append(args, "-c", a[i+1]), i+1
		}
	}
	f, err := os.OpenFile(d.log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	d.cmd = osexec.Command(d.bin, args...)
	d.cmd.Stdout, d.cmd.Stderr = f, f
	d.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // the npm wrapper and the server it starts go together
	if err := d.cmd.Start(); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		if p, err := codexSocketPath(d.sock); err == nil {
			if st, err := os.Stat(p); err == nil && st.Mode()&os.ModeSocket != 0 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("app-server did not listen on %s (log %s)", d.sock, d.log)
}

func (d *liveDaemon) stop() {
	if d.cmd == nil || d.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-d.cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = d.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-d.cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	d.cmd = nil
}

// liveHub stands in for the hub: it holds each card the node asks about until the test answers it, and notes the
// ones the node withdraws (serverRequest/resolved, the request gone).
type liveHub struct {
	mu        sync.Mutex
	asked     []proto.Interaction
	answers   map[string]chan proto.Resolution
	withdrawn map[string]bool
}

func newLiveHub(t *testing.T, n *Node) *liveHub {
	ctx, cancel := context.WithCancel(context.Background())
	n2hR, n2hW := io.Pipe()
	h2nR, h2nW := io.Pipe()
	n.peer = rpc.New("n", n2hW)
	peer := rpc.New("h", h2nW)
	h := &liveHub{answers: map[string]chan proto.Resolution{}, withdrawn: map[string]bool{}}
	peer.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != "interaction.create" {
			return map[string]any{}, nil
		}
		var p struct {
			Interaction proto.Interaction `json:"interaction"`
		}
		_ = json.Unmarshal(params, &p)
		ch := make(chan proto.Resolution, 1)
		h.mu.Lock()
		h.asked = append(h.asked, p.Interaction)
		h.answers[p.Interaction.ID] = ch
		h.mu.Unlock()
		select {
		case r := <-ch:
			return r, nil
		case <-time.After(30 * time.Minute):
			return proto.Resolution{Dismiss: true}, nil
		}
	}
	peer.OnEvent = func(event string, params json.RawMessage) {
		if event != "interaction.dismiss" && event != "interaction.cancel" {
			return
		}
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(params, &p)
		h.mu.Lock()
		h.withdrawn[p.ID] = true
		h.mu.Unlock()
	}
	go func() { _ = peer.Serve(ctx, n2hR) }()
	go func() { _ = n.peer.Serve(ctx, h2nR) }()
	t.Cleanup(func() { cancel(); _ = n2hW.Close(); _ = h2nW.Close() })
	return h
}

// next waits for a card of kind asked after the first `from` ones.
func (h *liveHub) next(kind string, from int, d time.Duration) (proto.Interaction, error) {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		h.mu.Lock()
		for _, ix := range h.asked[min(from, len(h.asked)):] {
			if ix.Kind == kind {
				h.mu.Unlock()
				return ix, nil
			}
		}
		h.mu.Unlock()
	}
	return proto.Interaction{}, fmt.Errorf("no %s card within %s", kind, d)
}

func (h *liveHub) count() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.asked) }

// answer gives the phone's answer to a card still waiting; false when nobody waits on it any more.
func (h *liveHub) answer(id string, r proto.Resolution) bool {
	h.mu.Lock()
	ch := h.answers[id]
	delete(h.answers, id)
	h.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- r
	return true
}

func (h *liveHub) isWithdrawn(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.withdrawn[id]
}

// daemonNode is a node that knows only the test app-server, reconnects to it the way runCodex does, and joins and
// lists cards on its own (watchCodexCards).
func daemonNode(t *testing.T, c *herdr.Client, d *liveDaemon) (*Node, *liveHub) {
	n := liveCodexNode(c)
	n.logger = log.New(os.Stderr, "node: ", log.Lmicroseconds)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	n.dir = t.TempDir()
	n.codices = newCodexRegistry()
	n.codices.scan = func() []codexProcess { return nil } // never the processes (and daemons) in use
	off := false
	home := canonicalCodexHome(filepath.Join(n.home, ".codex"), n.home)
	spec := proto.CodexServer{ID: "live-daemon", Home: home, Endpoint: "unix://" + d.sock}
	n.configureCodex(proto.CodexConfig{Discovery: &off, Servers: []proto.CodexServer{spec}})
	hub := newLiveHub(t, n)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			n.ensureCodexRemote(ctx, spec)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	go n.watchCodexCards(ctx)
	return n, hub
}

// keepSynced keeps the node's view of the pane current between the test's own looks.
func keepSynced(t *testing.T, lc *liveCodex) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for ctx.Err() == nil {
			if lc.id != "" {
				lc.sync()
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
}

type daemonCases struct {
	lc       *liveCodex
	hub      *liveHub
	d        *liveDaemon
	bin      string
	fallback *liveCodex // a Codex beside it on the terminal route (caseFallback)
}

func (dc *daemonCases) joined() bool {
	route, _ := dc.lc.n.codexRoute(dc.lc.id)
	return route == codexRouteAppServer
}

// approval asks for a command approval and waits for its card.
func (dc *daemonCases) approval(n int, host string) (proto.Interaction, error) {
	from := dc.hub.count()
	if err := dc.lc.type_(approve(host, n)); err != nil {
		return proto.Interaction{}, err
	}
	ix, err := dc.hub.next(proto.IxPermission, from, 90*time.Second)
	if err != nil {
		return ix, err
	}
	if ix.Agent != "codex" || ix.ToolName != "Bash" || !strings.Contains(ix.Summary, "curl -sI https://"+host) {
		return ix, fmt.Errorf("card %+v", ix)
	}
	return ix, nil
}

// overlayGone: the TUI no longer shows an approval or a question overlay (it got serverRequest/resolved).
func (dc *daemonCases) overlayGone(d time.Duration) error {
	return dc.lc.until(d, "the TUI's dialog to close", func() bool {
		s, _ := dc.lc.n.codexScreen(bg, dc.lc.pane)
		return s.Approval == nil && s.Question == nil
	})
}

func (dc *daemonCases) caseAsync(n int) error {
	lc := dc.lc
	if err := lc.type_(ask("Which fruit should the demo use?", "Apple", "Pear", "Plum")); err != nil {
		return err
	}
	c, err := lc.card(proto.IxQuestion, true, 90*time.Second)
	if err != nil {
		return err
	}
	if err := lc.idle(60 * time.Second); err != nil {
		return err
	}
	off := lc.size()
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, answer(c, "Plum")); err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	if err := lc.rolloutAfter(off, 20*time.Second, "send_user_message_question_reply", "Plum"); err != nil {
		return err
	}
	return lc.idle(90 * time.Second)
}

// A message from the phone is a turn on the app-server; the TUI shows it like any other.
func (dc *daemonCases) caseSend(n int) error {
	lc := dc.lc
	off := lc.size()
	word := fmt.Sprintf("pong%d", n)
	if err := lc.n.send(bg, lc.id, "Reply with the single word: "+word, nil, sendOpts{}); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if err := lc.rolloutAfter(off, 20*time.Second, "UserMessage", word); err != nil {
		return err
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	return lc.until(10*time.Second, "the reply on the TUI", func() bool { return strings.Count(lc.plain(), word) >= 2 })
}

// A draft in the TUI's box stays exactly as it was through a message and an answer from the phone.
func (dc *daemonCases) caseDraft(n int) error {
	lc := dc.lc
	draft := fmt.Sprintf("half typed note %d", n)
	if err := lc.c.PaneSendText(bg, lc.pane, draft); err != nil {
		return err
	}
	defer func() { _ = lc.keys("ctrl+c") }()
	time.Sleep(500 * time.Millisecond)
	off := lc.size()
	word := fmt.Sprintf("draftpong%d", n)
	if err := lc.n.send(bg, lc.id, "Reply with the single word: "+word, nil, sendOpts{}); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if err := lc.rolloutAfter(off, 20*time.Second, "UserMessage", word); err != nil {
		return err
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	if s, _ := lc.n.codexScreen(bg, lc.pane); s.Draft != draft {
		return fmt.Errorf("the draft became %q", s.Draft)
	}
	if err := lc.rolloutAfter(off, time.Second, "UserMessage", draft); err == nil {
		return fmt.Errorf("the draft was sent")
	}
	return nil
}

// Plan mode's request_user_input arrives as the app-server's request: answered on the phone, the TUI's overlay closes.
func (dc *daemonCases) casePlan(n int) error {
	lc := dc.lc
	if err := lc.type_("/plan"); err != nil {
		return err
	}
	defer lc.leavePlan()
	if err := lc.until(10*time.Second, "Plan mode", lc.inPlan); err != nil {
		return err
	}
	from := dc.hub.count()
	prompt := "Before planning, use request_user_input to ask me exactly two questions at once: (1) header 'Color', question 'Which color should the banner be?', options Red (warm) and Blue (calm); (2) header 'Size', question 'How tall should it be?', options Small (one line) and Large (two lines). Ask nothing else."
	if err := lc.type_(prompt); err != nil {
		return err
	}
	ix, err := dc.hub.next(proto.IxQuestion, from, 90*time.Second)
	if err != nil {
		return err
	}
	if ix.Agent != "codex" || len(ix.Questions) != 2 || ix.Questions[1].Options[1].Description == "" {
		return fmt.Errorf("card %+v", ix)
	}
	if len(lc.cards()) != 0 {
		return fmt.Errorf("the terminal route made a card too: %+v", lc.cards())
	}
	off := lc.size()
	words := fmt.Sprintf("Medium, about %d pixels", 40+n%10)
	if !dc.hub.answer(ix.ID, proto.Resolution{Answers: map[string]string{ix.Questions[0].Question: "Blue", ix.Questions[1].Question: words}}) {
		return fmt.Errorf("the card was not waiting")
	}
	if err := lc.rolloutAfter(off, 20*time.Second, "function_call_output", "Blue", words); err != nil {
		return err
	}
	return dc.overlayGone(10 * time.Second)
}

func (dc *daemonCases) caseCommand(n int) error {
	lc := dc.lc
	off := lc.size()
	ix, err := dc.approval(n, "example.com")
	if err != nil {
		return err
	}
	if len(lc.cards()) != 0 {
		return fmt.Errorf("the terminal route made a card too: %+v", lc.cards())
	}
	yes := true
	if !dc.hub.answer(ix.ID, proto.Resolution{Allow: &yes}) {
		return fmt.Errorf("the card was not waiting")
	}
	if err := dc.overlayGone(10 * time.Second); err != nil {
		return err
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	return lc.rolloutAfter(off, 5*time.Second, "HTTP/")
}

// A file change (the sandbox is read-only) comes as an Edit card naming the file; allowed, the file is written.
func (dc *daemonCases) caseFile(n int) error {
	lc := dc.lc
	name := fmt.Sprintf("note-%d.txt", n)
	from := dc.hub.count()
	if err := lc.type_(fmt.Sprintf("Use apply_patch (not a shell command) to create the file %s containing the single line: hello %d. Do nothing else.", name, n)); err != nil {
		return err
	}
	ix, err := dc.hub.next(proto.IxPermission, from, 90*time.Second)
	if err != nil {
		return err
	}
	if ix.ToolName != "Edit" || !strings.Contains(ix.Summary, name) || ix.AlwaysLabel == "" {
		return fmt.Errorf("card %+v", ix)
	}
	yes := true
	dc.hub.answer(ix.ID, proto.Resolution{Allow: &yes})
	if err := lc.until(60*time.Second, name+" written", func() bool {
		b, err := os.ReadFile(filepath.Join(lc.dir, name))
		return err == nil && strings.Contains(string(b), fmt.Sprintf("hello %d", n))
	}); err != nil {
		return err
	}
	return lc.idle(90 * time.Second)
}

// Codex asking for more permissions (request_permissions): a permission card; allowed for this turn.
func (dc *daemonCases) casePermissions(n int) error {
	lc := dc.lc
	from := dc.hub.count()
	if err := lc.type_("Call the request_permissions tool to ask for network access for this turn (reason: check a website). Once it is granted, reply with the single word: granted. Do not run any command."); err != nil {
		return err
	}
	ix, err := dc.hub.next(proto.IxPermission, from, 90*time.Second)
	if err != nil {
		return err
	}
	if ix.ToolName != "权限" {
		return fmt.Errorf("card %q %q", ix.ToolName, ix.Summary)
	}
	off := lc.size()
	yes := true
	dc.hub.answer(ix.ID, proto.Resolution{Allow: &yes})
	if err := dc.overlayGone(10 * time.Second); err != nil {
		return err
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	return lc.rolloutAfter(off, 5*time.Second, "granted")
}

// Refused with words on the phone: declined, and the words go into the turn.
func (dc *daemonCases) caseDenyReason(n int) error {
	lc := dc.lc
	off := lc.size()
	ix, err := dc.approval(n, "example.org")
	if err != nil {
		return err
	}
	no := false
	reason := fmt.Sprintf("skip the network check %d", n)
	dc.hub.answer(ix.ID, proto.Resolution{Allow: &no, Message: reason})
	if err := dc.overlayGone(10 * time.Second); err != nil {
		return err
	}
	if err := lc.rolloutAfter(off, 30*time.Second, "UserMessage", reason); err != nil {
		return err
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	if err := lc.rolloutAfter(off, time.Second, "HTTP/"); err == nil {
		return fmt.Errorf("the refused command ran")
	}
	return nil
}

// Answered at the computer first: the phone's card is withdrawn, and an answer to it afterwards goes nowhere.
func (dc *daemonCases) caseComputerFirst(n int) error {
	lc := dc.lc
	off := lc.size()
	ix, err := dc.approval(n, "example.net")
	if err != nil {
		return err
	}
	if err := lc.until(10*time.Second, "the TUI's approval", func() bool {
		s, _ := lc.n.codexScreen(bg, lc.pane)
		return s.Approval != nil
	}); err != nil {
		return err
	}
	_ = lc.keys("1")
	if err := lc.until(10*time.Second, "the card withdrawn", func() bool { return dc.hub.isWithdrawn(ix.ID) }); err != nil {
		return err
	}
	no := false
	dc.hub.answer(ix.ID, proto.Resolution{Allow: &no}) // too late: nobody acts on it
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	return dc.ranOnce(off, "example.net")
}

func (dc *daemonCases) casePhoneFirst(n int) error {
	lc := dc.lc
	off := lc.size()
	ix, err := dc.approval(n, "example.com")
	if err != nil {
		return err
	}
	if err := lc.until(10*time.Second, "the TUI's approval", func() bool {
		s, _ := lc.n.codexScreen(bg, lc.pane)
		return s.Approval != nil
	}); err != nil {
		return err
	}
	yes := true
	dc.hub.answer(ix.ID, proto.Resolution{Allow: &yes})
	if err := dc.overlayGone(10 * time.Second); err != nil {
		return err
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	return dc.ranOnce(off, "example.com")
}

// Both at once: one decision is taken, the command runs once, the TUI goes on.
func (dc *daemonCases) caseBoth(n int) error {
	lc := dc.lc
	off := lc.size()
	ix, err := dc.approval(n, "example.org")
	if err != nil {
		return err
	}
	if err := lc.until(10*time.Second, "the TUI's approval", func() bool {
		s, _ := lc.n.codexScreen(bg, lc.pane)
		return s.Approval != nil
	}); err != nil {
		return err
	}
	yes := true
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); dc.hub.answer(ix.ID, proto.Resolution{Allow: &yes}) }()
	go func() { defer wg.Done(); _ = lc.keys("1") }()
	wg.Wait()
	if err := dc.overlayGone(10 * time.Second); err != nil {
		return err
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	return dc.ranOnce(off, "example.org")
}

// ranOnce: the approved command's output (a tool call's output line with the HTTP status) is in the rollout after
// off exactly once.
func (dc *daemonCases) ranOnce(off int64, host string) error {
	b, err := os.ReadFile(dc.lc.n.codexPath(dc.lc.id))
	if err != nil || int64(len(b)) < off {
		return fmt.Errorf("rollout: %v", err)
	}
	runs := 0
	for _, l := range strings.Split(string(b[off:]), "\n") {
		if strings.Contains(l, `"type":"response_item"`) && strings.Contains(l, `_output"`) && strings.Contains(l, "HTTP/") {
			runs++
		}
	}
	if runs != 1 {
		return fmt.Errorf("the command ran %d times", runs)
	}
	return nil
}

// The app-server restarts: the pane falls back to the terminal route while it is gone, the TUI reconnects to the new
// one, Persona joins again, and an approval goes through the protocol again.
func (dc *daemonCases) caseRestart(n int) error {
	lc := dc.lc
	dc.d.stop()
	if err := lc.until(20*time.Second, "the terminal route", func() bool { return !dc.joined() }); err != nil {
		return err
	}
	if err := dc.d.start(); err != nil {
		return err
	}
	if err := lc.until(60*time.Second, "joined on the new app-server", dc.joined); err != nil {
		return err
	}
	return dc.caseCommand(n)
}

// A Codex beside it that runs its own thread (0.153, or any in-process one) stays on the terminal route, and its
// approval is a terminal card.
func (dc *daemonCases) caseFallback(n int) error {
	other := os.Getenv("PERSONA_CODEX_FALLBACK")
	if other == "" {
		return errSkip
	}
	if dc.fallback == nil {
		dc.fallback = startLiveCodex(dc.lc.t, other, filepath.Dir(dc.lc.dir), liveOptions{name: "fallback", node: dc.lc.n})
	}
	f := dc.fallback
	f.sync()
	if route, note := f.n.codexRoute(f.id); route != codexRouteTerminal || note == "" {
		return fmt.Errorf("route %q %q", route, note)
	}
	off := f.size()
	c, err := f.approval(n, "example.com")
	if err != nil {
		return err
	}
	return f.allowed(c, off)
}
