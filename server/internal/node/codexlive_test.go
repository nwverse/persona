//go:build codexlive

package node

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Codex questions and approvals answered the way a card's answer is (codexAnswer), against a real Codex TUI in an
// isolated herdr — every state of docs/integrations/codex.md "Questions and approvals" a computer can be in. scripts/test/test-codex.sh sets it up:
//
//	HERDR_SOCKET_PATH    the isolated herdr (never the one in use)
//	PERSONA_CODEX_BIN     the codex under test
//	PERSONA_CODEX_ARGS    extra arguments, space-separated (a model the login takes: -m gpt-6-astra)
//	PERSONA_CODEX_WORK    a scratch directory: the Codex runs in a new git repo there
//	PERSONA_CODEX_COLS    start Codex in a window this many columns wide (a small window)
//	PERSONA_CODEX_ROUNDS  how many times every case runs (default 1)
//	PERSONA_CODEX_CASES   only these cases (comma-separated names)
func TestLiveCodexCards(t *testing.T) {
	bin, work := os.Getenv("PERSONA_CODEX_BIN"), os.Getenv("PERSONA_CODEX_WORK")
	if bin == "" || work == "" {
		t.Skip("PERSONA_CODEX_BIN / PERSONA_CODEX_WORK not set")
	}
	lc := startLiveCodex(t, bin, work, liveOptions{})
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
		run  func(*liveCodex, int) error
	}{
		{"async", (*liveCodex).caseAsync},
		{"async-draft", (*liveCodex).caseAsyncDraft},
		{"async-pane", (*liveCodex).caseAsyncPane},
		{"async-at-computer", (*liveCodex).caseAsyncAtComputer},
		{"approval", (*liveCodex).caseApproval},
		{"approval-scrolled", (*liveCodex).caseApprovalScrolled},
		{"approval-other-tab", (*liveCodex).caseApprovalOtherTab},
		{"approval-pager-deny", (*liveCodex).caseApprovalPagerDeny},
		{"approval-at-computer", (*liveCodex).caseApprovalAtComputer},
		{"question-then-approval", (*liveCodex).caseQuestionThenApproval},
		{"plan-questions", (*liveCodex).casePlanQuestions},
	}
	failed := 0
	for r := 1; r <= rounds; r++ {
		for i, c := range cases {
			if len(only) > 0 && !only[c.name] {
				continue
			}
			start := time.Now()
			err := c.run(lc, r*100+i)
			if why := serviceError(lc.plain()); err != nil && err != errSkip && why != "" {
				// Codex's model service failed the turn (not Persona): logged, and the case runs once more.
				t.Logf("LIVE %s round %d %s: SERVICE-RETRY: Codex's model service failed the turn (%s)", lc.version, r, c.name, why)
				lc.recover()
				start, err = time.Now(), c.run(lc, r*100+i+50)
			}
			if err == errSkip {
				t.Logf("LIVE %s round %d %s: skipped", lc.version, r, c.name)
				continue
			}
			if err != nil {
				failed++
				t.Errorf("LIVE %s round %d %s: FAIL after %s: %v\n%s", lc.version, r, c.name, time.Since(start).Round(time.Second), err, lc.plain())
				lc.recover()
			} else {
				t.Logf("LIVE %s round %d %s: ok in %s", lc.version, r, c.name, time.Since(start).Round(time.Second))
			}
		}
	}
	if failed > 0 {
		t.Fatalf("LIVE %s: %d case(s) failed", lc.version, failed)
	}
}

type liveCodex struct {
	t       *testing.T
	n       *Node
	c       *herdr.Client
	ws      string
	pane    string
	dir     string
	id      string
	version string
}

var bg = context.Background()

// errSkip: the case does not apply to this version.
var errSkip = fmt.Errorf("skipped")

// liveOptions vary how a live Codex starts.
type liveOptions struct {
	name string   // its scratch directory (default codex-<version>)
	args []string // more arguments ({dir} becomes the directory): --remote unix://… -C {dir}, -s read-only
	node *Node    // the node answering it (default: a fresh one that knows no app-server)
}

// liveCodexNode is a node on the isolated herdr that knows no app-server.
func liveCodexNode(c *herdr.Client) *Node {
	home, _ := os.UserHomeDir()
	return &Node{home: home, hosts: herdrHosts(c), agents: map[string]host.Agent{}, paths: map[string]string{}, panes: map[string]string{},
		files: map[string]*transcript.File{}, procs: map[string]*assistantProc{}, watches: map[string]*watch{},
		state: &stateFile{Sessions: map[string]sessionState{}}}
}

func startLiveCodex(t *testing.T, bin, work string, o liveOptions) *liveCodex {
	lc := &liveCodex{t: t, c: herdr.New(), n: o.node}
	if lc.n == nil {
		lc.n = liveCodexNode(lc.c)
	}
	out, err := liveRun(bin, "--version")
	if err != nil {
		t.Fatalf("%s --version: %v", bin, err)
	}
	lc.version = strings.TrimSpace(strings.TrimPrefix(out, "codex-cli"))
	if o.name == "" {
		o.name = "codex-" + strings.ReplaceAll(lc.version, ".", "_")
	}
	lc.dir = filepath.Join(work, o.name+os.Getenv("PERSONA_CODEX_COLS"))
	if err := os.MkdirAll(lc.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lc.dir, _ = filepath.EvalSymlinks(lc.dir)
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=lab", "-c", "user.email=lab@example.com", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if _, err := liveRun("git", append([]string{"-C", lc.dir}, args...)...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	created, err := lc.c.WorkspaceCreate(bg, lc.dir, "codex "+lc.version)
	if err != nil {
		t.Fatal(err)
	}
	lc.ws, lc.pane = created.Workspace.ID, created.RootPane.ID
	cmd := []string{shellQuote(bin), "-c", "'notify=[]'", "-c", "check_for_update_on_startup=false"}
	for _, a := range append(strings.Fields(os.Getenv("PERSONA_CODEX_ARGS")), o.args...) {
		cmd = append(cmd, shellQuote(strings.ReplaceAll(a, "{dir}", lc.dir)))
	}
	line := "exec " + strings.Join(cmd, " ")
	if cols := os.Getenv("PERSONA_CODEX_COLS"); cols != "" {
		line = "stty cols " + cols + " rows 32; " + line
	}
	if err := lc.c.PaneRun(bg, lc.pane, line); err != nil {
		t.Fatal(err)
	}
	// Up: past the folder trust dialog (written to ~/.codex/config.toml; the script removes it), at its input box.
	if err := lc.until(60*time.Second, "the input box", func() bool {
		text := lc.plain()
		if strings.Contains(text, "Trust this folder") || strings.Contains(text, "Do you trust the contents") {
			_ = lc.keys("enter")
			time.Sleep(time.Second)
			return false
		}
		s, _ := lc.n.codexScreen(bg, lc.pane)
		return s.View == screen.CodexComposer
	}); err != nil {
		t.Fatalf("%v\n%s", err, lc.plain())
	}
	// The thread: its rollout starts with the first message (typed again if it did not take).
	for try := 0; lc.id == "" && try < 3; try++ {
		if s, _ := lc.n.codexScreen(bg, lc.pane); try > 0 && s.View == screen.CodexComposer && s.Draft != "" {
			_ = lc.keys("ctrl+c") // the last try's text still in the box: not twice in one message
			time.Sleep(500 * time.Millisecond)
		}
		typed := time.Now().Add(-time.Second)
		if err := lc.type_("Reply with the single word: ready"); err != nil {
			t.Fatal(err)
		}
		_ = lc.until(30*time.Second, "its rollout", func() bool {
			for _, r := range rollouts(lc.n.codexHome()) {
				if r.cwd == lc.dir && r.mtime.After(typed) { // not an earlier run's thread in the same directory
					lc.id = r.id
					return true
				}
			}
			return false
		})
	}
	if lc.id == "" {
		t.Fatalf("no rollout in %s\n%s", lc.dir, lc.plain())
	}
	if err := lc.idle(90 * time.Second); err != nil {
		t.Fatalf("%v\n%s", err, lc.plain())
	}
	t.Logf("LIVE codex %s in %s, session %s", lc.version, lc.pane, lc.id)
	t.Cleanup(func() {
		_ = lc.keys("ctrl+c")
		time.Sleep(300 * time.Millisecond)
		_ = lc.keys("ctrl+c")
		time.Sleep(time.Second)
		_ = lc.raw("workspace.close", map[string]any{"workspace_id": lc.ws})
	})
	return lc
}

func liveRun(name string, args ...string) (string, error) {
	out, err := osexec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// raw is one request on the herdr socket (scroll, split, focus: calls the client does not wrap).
func (lc *liveCodex) raw(method string, params map[string]any) error {
	c, err := net.Dial("unix", herdr.SocketPath())
	if err != nil {
		return err
	}
	defer c.Close()
	b, _ := json.Marshal(map[string]any{"id": "live", "method": method, "params": params})
	if _, err := c.Write(append(b, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp struct {
		Error *herdr.Error `json:"error"`
	}
	_ = json.Unmarshal(line, &resp)
	if resp.Error != nil {
		return resp.Error
	}
	return nil
}

func (lc *liveCodex) keys(keys ...string) error { return lc.c.PaneSendKeys(bg, lc.pane, keys...) }

func (lc *liveCodex) plain() string {
	text, _ := lc.c.PaneRead(bg, lc.pane, "recent", 40)
	return text
}

// type_ is the user typing a message at the computer.
func (lc *liveCodex) type_(text string) error {
	if err := lc.c.PaneSendText(bg, lc.pane, pasteStart+text+pasteEnd); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return lc.keys("enter")
}

func (lc *liveCodex) until(d time.Duration, what string, ok func() bool) error {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(400 * time.Millisecond) {
		if ok() {
			return nil
		}
	}
	return fmt.Errorf("timed out after %s waiting for %s", d, what)
}

// sync gives the node the agent as herdr sees it now.
func (lc *liveCodex) sync() string {
	a, err := lc.c.AgentGet(bg, lc.pane)
	status := proto.StatusUnknown
	if err == nil {
		status = a.AgentStatus
	}
	lc.n.mu.Lock()
	lc.n.agents[lc.id] = host.Agent{Agent: "codex", PaneID: lc.pane, Cwd: lc.dir, AgentStatus: status}
	lc.n.mu.Unlock()
	return status
}

func (lc *liveCodex) cards() []codexCard {
	lc.sync()
	return lc.n.codexCardsNow(bg, lc.id)
}

// card waits for a card of kind (and, for a question, async or not).
func (lc *liveCodex) card(kind string, async bool, d time.Duration) (codexCard, error) {
	var found codexCard
	err := lc.until(d, "a "+kind+" card", func() bool {
		for _, c := range lc.cards() {
			if c.ix.Kind == kind && (c.ask == nil || c.ask.Async == async) {
				found = c
				return true
			}
		}
		return false
	})
	return found, err
}

func (lc *liveCodex) noCards(d time.Duration) error {
	return lc.until(d, "no cards", func() bool { return len(lc.cards()) == 0 })
}

// idle waits for the turn to end: herdr says idle / done and the input box is back.
func (lc *liveCodex) idle(d time.Duration) error {
	return lc.until(d, "the turn to end", func() bool {
		st := lc.sync()
		s, _ := lc.n.codexScreen(bg, lc.pane)
		return (st == proto.StatusIdle || st == proto.StatusDone) && s.View == screen.CodexComposer && !codexWorking(lc.plain())
	})
}

func codexWorking(plain string) bool { return strings.Contains(plain, "esc to interrupt") }

// rolloutAfter waits for a rollout line after off containing all of want.
func (lc *liveCodex) rolloutAfter(off int64, d time.Duration, want ...string) error {
	path := lc.n.codexPath(lc.id)
	return lc.until(d, fmt.Sprintf("the rollout to have %q", want), func() bool {
		b, err := os.ReadFile(path)
		if err != nil || int64(len(b)) < off {
			return false
		}
		for _, l := range strings.Split(string(b[off:]), "\n") {
			all := true
			for _, w := range want {
				all = all && strings.Contains(l, w)
			}
			if all && l != "" {
				return true
			}
		}
		return false
	})
}

func (lc *liveCodex) size() int64 {
	st, err := os.Stat(lc.n.codexPath(lc.id))
	if err != nil {
		return 0
	}
	return st.Size()
}

// recover brings Codex back to an idle, empty box after a failed case.
func (lc *liveCodex) recover() {
	for i := 0; i < 4; i++ {
		s, _ := lc.n.codexScreen(bg, lc.pane)
		switch {
		case s.View == screen.CodexTranscript:
			_ = lc.keys("q")
		case s.View == screen.CodexComposer && s.Draft != "":
			_ = lc.keys("ctrl+c")
		case s.View == screen.CodexComposer && !codexWorking(lc.plain()):
			return
		default:
			_ = lc.keys("esc")
		}
		time.Sleep(time.Second)
	}
	_ = lc.idle(60 * time.Second)
}

// serviceError is the error Codex shows when its model service failed a turn ("" when none is on screen).
func serviceError(plain string) string {
	for _, l := range strings.Split(plain, "\n") {
		low := strings.ToLower(l)
		for _, m := range []string{"at capacity", "high demand", "stream disconnected", "rate limit", "service unavailable"} {
			if strings.Contains(low, m) {
				return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "■"))
			}
		}
	}
	return ""
}

func ask(q string, opts ...string) string {
	return fmt.Sprintf("Use request_user_input_async to ask me exactly one question: %q with the options %s. Do nothing else, and end your turn right after asking.",
		q, strings.Join(opts, ", "))
}

func approve(host string, n int) string {
	return fmt.Sprintf("Run this exact shell command, requesting escalated permissions (it needs network): curl -sI https://%s/c%d | head -1", host, n)
}

func answer(c codexCard, a string) proto.Resolution {
	return proto.Resolution{Answers: map[string]string{c.ix.Questions[0].Question: a}}
}

func code(err error) string { return refusalCode(err) }

// --- cases ---

func (lc *liveCodex) caseAsync(n int) error {
	if err := lc.type_(ask("Which fruit should the demo use?", "Apple", "Pear", "Plum")); err != nil {
		return err
	}
	c, err := lc.card(proto.IxQuestion, true, 90*time.Second)
	if err != nil {
		return err
	}
	if len(c.ix.Questions) != 1 || len(c.ix.Questions[0].Options) != 3 {
		return fmt.Errorf("card %+v", c.ix.Questions)
	}
	if err := lc.idle(60 * time.Second); err != nil {
		return err
	}
	off := lc.size()
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, answer(c, "Pear")); err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	if err := lc.rolloutAfter(off, 20*time.Second, "send_user_message_question_reply", "Pear", c.ask.CallID); err != nil {
		return err
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	return lc.noCards(5 * time.Second)
}

// A draft at the computer: refused with it untouched; once it is cleared the answer goes in.
func (lc *liveCodex) caseAsyncDraft(n int) error {
	if err := lc.type_(ask("Which port should the server use?", "8080", "9090")); err != nil {
		return err
	}
	c, err := lc.card(proto.IxQuestion, true, 90*time.Second)
	if err != nil {
		return err
	}
	if err := lc.idle(60 * time.Second); err != nil {
		return err
	}
	if err := lc.c.PaneSendText(bg, lc.pane, "half typed note"); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, answer(c, "9090")); code(err) != screen.InputDraft {
		return fmt.Errorf("with a draft: %v, want %s", err, screen.InputDraft)
	}
	s, _ := lc.n.codexScreen(bg, lc.pane)
	if s.Draft != "half typed note" {
		return fmt.Errorf("the draft became %q", s.Draft)
	}
	_ = lc.keys("ctrl+c")
	time.Sleep(500 * time.Millisecond)
	off := lc.size()
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, answer(c, "9090")); err != nil {
		return fmt.Errorf("after clearing: %w", err)
	}
	if err := lc.rolloutAfter(off, 20*time.Second, "send_user_message_question_reply", "9090"); err != nil {
		return err
	}
	return lc.idle(90 * time.Second)
}

// 0.163: the question asked while the turn goes on, its pane opened over the box at the computer (⇧←).
func (lc *liveCodex) caseAsyncPane(n int) error {
	if strings.HasPrefix(lc.version, "0.153") {
		return errSkip // no question pane in 0.153
	}
	prompt := "Use request_user_input_async to ask me which logo to use (options Round, Square). Then, without waiting for my reply, run the shell command: sleep 25 ; and then say done."
	if err := lc.type_(prompt); err != nil {
		return err
	}
	c, err := lc.card(proto.IxQuestion, true, 90*time.Second)
	if err != nil {
		return err
	}
	if err := lc.until(20*time.Second, "the question pane", func() bool { return strings.Contains(lc.plain(), "to answer") }); err != nil {
		return err
	}
	_ = lc.keys("shift+left")
	time.Sleep(500 * time.Millisecond)
	if s, _ := lc.n.codexScreen(bg, lc.pane); s.View != screen.CodexAsyncPane {
		return fmt.Errorf("the pane did not open: %q", s.View)
	}
	off := lc.size()
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, answer(c, "Square")); err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	if err := lc.rolloutAfter(off, 40*time.Second, "send_user_message_question_reply", "Square"); err != nil {
		return err
	}
	return lc.idle(90 * time.Second)
}

// Answered at the computer first: the card goes, and the phone's answer is refused with nothing typed.
func (lc *liveCodex) caseAsyncAtComputer(n int) error {
	if err := lc.type_(ask("Which color should the footer be?", "Gray", "Black")); err != nil {
		return err
	}
	c, err := lc.card(proto.IxQuestion, true, 90*time.Second)
	if err != nil {
		return err
	}
	if err := lc.idle(60 * time.Second); err != nil {
		return err
	}
	if err := lc.type_("Gray"); err != nil {
		return err
	}
	if err := lc.noCards(20 * time.Second); err != nil {
		return err
	}
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, answer(c, "Black")); code(err) != screen.PromptChanged {
		return fmt.Errorf("after the computer answered: %v", err)
	}
	return lc.idle(90 * time.Second)
}

func (lc *liveCodex) approval(n int, host string) (codexCard, error) {
	if err := lc.type_(approve(host, n)); err != nil {
		return codexCard{}, err
	}
	c, err := lc.card(proto.IxPermission, false, 90*time.Second)
	if err != nil {
		return c, err
	}
	if !strings.Contains(c.ix.Summary, "curl -sI https://"+host) || c.ix.ToolName != "Bash" {
		return c, fmt.Errorf("card %q %q", c.ix.ToolName, c.ix.Summary)
	}
	return c, nil
}

func (lc *liveCodex) allowed(c codexCard, off int64) error {
	yes := true
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, proto.Resolution{Allow: &yes}); err != nil {
		return fmt.Errorf("allow: %w", err)
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	if err := lc.rolloutAfter(off, 5*time.Second, "HTTP/"); err != nil {
		return fmt.Errorf("the command did not run: %w", err)
	}
	return lc.noCards(5 * time.Second)
}

func (lc *liveCodex) caseApproval(n int) error {
	off := lc.size()
	c, err := lc.approval(n, "example.com")
	if err != nil {
		return err
	}
	return lc.allowed(c, off)
}

// The pane scrolled back at the computer: the dialog is read off the end of the output all the same.
func (lc *liveCodex) caseApprovalScrolled(n int) error {
	off := lc.size()
	if err := lc.type_(approve("example.org", n)); err != nil {
		return err
	}
	time.Sleep(time.Second)
	_ = lc.raw("pane.scroll", map[string]any{"pane_id": lc.pane, "offset_from_bottom": 12})
	defer lc.raw("pane.scroll", map[string]any{"pane_id": lc.pane, "offset_from_bottom": 0})
	c, err := lc.card(proto.IxPermission, false, 90*time.Second)
	if err != nil {
		return err
	}
	return lc.allowed(c, off)
}

// Another tab of the space in front at the computer: the pane still takes the keys.
func (lc *liveCodex) caseApprovalOtherTab(n int) error {
	_ = lc.raw("tab.create", map[string]any{"workspace_id": lc.ws, "cwd": lc.dir, "label": "other", "focus": true})
	defer func() {
		if snap, err := lc.c.Snapshot(bg); err == nil {
			for _, tab := range snap.Tabs {
				if tab.WorkspaceID == lc.ws && tab.Label == "other" {
					_ = lc.c.TabClose(bg, tab.ID)
				}
			}
		}
	}()
	off := lc.size()
	c, err := lc.approval(n, "example.net")
	if err != nil {
		return err
	}
	return lc.allowed(c, off)
}

// The transcript pager open when the approval comes (0.153 hides the dialog under it): the node closes it; refused
// with a reason, the turn stops and the reason is the next message.
func (lc *liveCodex) caseApprovalPagerDeny(n int) error {
	prompt := fmt.Sprintf("First run: sleep 4 (no escalation needed). Then run this exact command requesting escalated permissions (network): curl -sI https://example.edu/c%d | head -1", n)
	if err := lc.type_(prompt); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	_ = lc.keys("ctrl+t")
	c, err := lc.card(proto.IxPermission, false, 90*time.Second)
	if err != nil {
		return err
	}
	if strings.HasPrefix(lc.version, "0.163") {
		_ = lc.keys("ctrl+t") // 0.163's Ctrl+T only folds details: back as it was
	}
	off := lc.size()
	no := false
	reason := fmt.Sprintf("Skip the network check %d and reply with the single word: skipped", n)
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, proto.Resolution{Allow: &no, Message: reason}); err != nil {
		return fmt.Errorf("deny: %w", err)
	}
	if err := lc.rolloutAfter(off, 20*time.Second, "turn_aborted"); err != nil {
		return err
	}
	if err := lc.rolloutAfter(off, 20*time.Second, "UserMessage", reason); err != nil {
		return err
	}
	return lc.idle(90 * time.Second)
}

func (lc *liveCodex) caseApprovalAtComputer(n int) error {
	off := lc.size()
	c, err := lc.approval(n, "example.com")
	if err != nil {
		return err
	}
	_ = lc.keys("1") // the person at the computer allows it
	if err := lc.until(10*time.Second, "the card to go", func() bool {
		for _, x := range lc.cards() {
			if x.ix.ID == c.ix.ID {
				return false
			}
		}
		return true
	}); err != nil {
		return err
	}
	yes := true
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, proto.Resolution{Allow: &yes}); code(err) != screen.PromptChanged {
		return fmt.Errorf("after the computer answered: %v", err)
	}
	if err := lc.idle(90 * time.Second); err != nil {
		return err
	}
	return lc.rolloutAfter(off, 5*time.Second, "HTTP/")
}

// A question and right after it an approval: two cards; the question waits behind the approval's dialog.
func (lc *liveCodex) caseQuestionThenApproval(n int) error {
	prompt := fmt.Sprintf("First use request_user_input_async to ask me which region to deploy to (options East, West). Then, without waiting for my reply, run this exact command requesting escalated permissions (network): curl -sI https://example.com/r%d | head -1", n)
	off := lc.size()
	if err := lc.type_(prompt); err != nil {
		return err
	}
	q, err := lc.card(proto.IxQuestion, true, 90*time.Second)
	if err != nil {
		return err
	}
	a, err := lc.card(proto.IxPermission, false, 90*time.Second)
	if err != nil {
		return err
	}
	if err := lc.n.codexAnswer(bg, lc.id, q.ix.ID, answer(q, "West")); code(err) != screen.DialogOpen {
		return fmt.Errorf("question under the approval: %v, want %s", err, screen.DialogOpen)
	}
	yes := true
	if err := lc.n.codexAnswer(bg, lc.id, a.ix.ID, proto.Resolution{Allow: &yes}); err != nil {
		return fmt.Errorf("allow: %w", err)
	}
	if err := lc.until(30*time.Second, "the box", func() bool {
		s, _ := lc.n.codexScreen(bg, lc.pane)
		return s.View == screen.CodexComposer
	}); err != nil {
		return err
	}
	if err := lc.n.codexAnswer(bg, lc.id, q.ix.ID, answer(q, "West")); err != nil {
		return fmt.Errorf("question after the approval: %w", err)
	}
	if err := lc.rolloutAfter(off, 40*time.Second, "send_user_message_question_reply", "West"); err != nil {
		return err
	}
	return lc.idle(90 * time.Second)
}

// Plan mode's request_user_input: two questions on the overlay, one by its option, one in words.
func (lc *liveCodex) casePlanQuestions(n int) error {
	if err := lc.type_("/plan"); err != nil {
		return err
	}
	defer lc.leavePlan()
	if err := lc.until(10*time.Second, "Plan mode", lc.inPlan); err != nil {
		return err
	}
	prompt := "Before planning, use request_user_input to ask me exactly two questions at once: (1) header 'Color', question 'Which color should the banner be?', options Red (warm) and Blue (calm); (2) header 'Size', question 'How tall should it be?', options Small (one line) and Large (two lines). Ask nothing else."
	if err := lc.type_(prompt); err != nil {
		return err
	}
	c, err := lc.card(proto.IxQuestion, false, 90*time.Second)
	if err != nil {
		return err
	}
	if len(c.ix.Questions) != 2 {
		return fmt.Errorf("questions %+v", c.ix.Questions)
	}
	if err := lc.until(10*time.Second, "the overlay", func() bool {
		s, _ := lc.n.codexScreen(bg, lc.pane)
		return s.View == screen.CodexQuestion
	}); err != nil {
		return err
	}
	off := lc.size()
	words := fmt.Sprintf("Medium, about %d pixels", 40+n%10)
	r := proto.Resolution{Answers: map[string]string{c.ix.Questions[0].Question: "Blue", c.ix.Questions[1].Question: words}}
	if err := lc.n.codexAnswer(bg, lc.id, c.ix.ID, r); err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	return lc.rolloutAfter(off, 20*time.Second, "function_call_output", c.ask.CallID, "Blue", "user_note: "+words)
}

// leavePlan stops the plan turn and cycles back to the default mode.
func (lc *liveCodex) leavePlan() {
	_ = lc.keys("esc")
	_ = lc.idle(30 * time.Second)
	for i := 0; i < 4 && lc.inPlan(); i++ {
		_ = lc.keys("shift+tab")
		time.Sleep(time.Second)
	}
}

// inPlan: the footer (not the history above it: "Model changed … for Plan mode") says Plan mode.
func (lc *liveCodex) inPlan() bool {
	lines := strings.Split(strings.TrimRight(lc.plain(), "\n "), "\n")
	return strings.Contains(strings.Join(lines[max(0, len(lines)-2):], "\n"), "Plan mode")
}
