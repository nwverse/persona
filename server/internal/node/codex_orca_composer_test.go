package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/orcart/orcarttest"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
)

func writeOrcaCodexRollout(t *testing.T, home, cwd string, lines ...string) string {
	t.Helper()
	day := time.Now()
	dir := filepath.Join(home, ".codex", "sessions", day.Format("2006"), day.Format("01"), day.Format("02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := fmt.Sprintf(`{"timestamp":"2026-11-01T13:40:00.000Z","type":"session_meta","payload":{"id":"%s","cwd":%q,"cli_version":"0.167.0"}}`, cxID, cwd)
	all := append([]string{meta}, lines...)
	path := filepath.Join(dir, "rollout-2026-11-01T21-40-06-"+cxID+".jsonl")
	if err := os.WriteFile(path, []byte(cxToday(strings.Join(all, "\n"))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertCodexLiveWritable(t *testing.T, n *Node, wantPane string) {
	t.Helper()
	// Use sessionList directly so asserting does not trigger another refreshHosts call.
	m := byID(n.sessionList(context.Background(), false))
	row, ok := m[cxID]
	if !ok {
		t.Fatalf("codex session %s missing from sessionList: %+v", cxID, m)
	}
	if row.PaneID != wantPane {
		t.Fatalf("codex session PaneID=%q SourceKind=%q Status=%q, want live PaneID=%q", row.PaneID, row.SourceKind, row.Status, wantPane)
	}
	if row.SourceKind != "" {
		t.Fatalf("live codex session regressed to SourceKind=%q", row.SourceKind)
	}
	if row.Capabilities == nil || !row.Capabilities.Send {
		t.Fatalf("live codex session became read-only: caps=%+v", row.Capabilities)
	}
}

// Delaying an older empty terminal-host snapshot until after a newer live snapshot commits
// must never overwrite the live Codex binding with read-only history or a stale layout (#2133).
func TestRefreshHostsDropsStaleOutOfOrderOrcaSnapshot(t *testing.T) {
	ctx := context.Background()
	n, _, o, procs, home := orcaNode(t)
	writeOrcaCodexRollout(t, home, orcaCwd, cxUserLine("hello"))

	codexTerm := orcaTerm("term_1", "tab1", "leaf1", "codex")
	procs.set("tab1:leaf1", orcaProc{pid: 5151, agent: "codex"})
	n.mu.Lock()
	n.panes["orca:tab1:leaf1"] = cxID
	n.mu.Unlock()
	o.SetWorktrees(orcarttest.Worktree("repo::"+orcaCwd, orcaCwd, "refs/heads/feature-x",
		orcarttest.Agent("tab1:leaf1", "codex", "done")))

	// Prime layout cache so the delayed snapshot performs a single terminal.list call.
	o.SetTerminals()
	if err := n.refreshHosts(ctx); err != nil {
		t.Fatalf("prime refreshHosts: %v", err)
	}

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var listCalls atomic.Int32
	var termClosed atomic.Bool
	o.Handle("terminal.list", func(map[string]any) any {
		if listCalls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return map[string]any{"terminals": []any{}}
		}
		if termClosed.Load() {
			return map[string]any{"terminals": []any{}}
		}
		return map[string]any{"terminals": []any{codexTerm}}
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- n.refreshHosts(ctx)
	}()
	<-firstStarted

	// Newer live snapshot arrives and commits while snapshot #1 is still delayed.
	if err := n.refreshHosts(ctx); err != nil {
		t.Fatalf("newer refreshHosts: %v", err)
	}
	assertCodexLiveWritable(t, n, "orca:tab1:leaf1")

	// Release the older delayed empty snapshot: it must be dropped as stale.
	close(releaseFirst)
	if err := <-errCh; err != nil {
		t.Fatalf("older refreshHosts returned unexpected error: %v", err)
	}
	assertCodexLiveWritable(t, n, "orca:tab1:leaf1")

	n.mu.Lock()
	paneCount := len(n.layouts[host.Orca].panes)
	n.mu.Unlock()
	if paneCount != 1 {
		t.Fatalf("orca layout panes=%d after stale snapshot, want 1", paneCount)
	}

	// A genuinely newer empty snapshot must still apply once the terminal actually closes.
	termClosed.Store(true)
	o.SetWorktrees()
	procs.set("tab1:leaf1")
	if err := n.refreshHosts(ctx); err != nil {
		t.Fatalf("subsequent empty refreshHosts: %v", err)
	}
	n.mu.Lock()
	_, stillLive := n.agents[cxID]
	afterPaneCount := len(n.layouts[host.Orca].panes)
	n.mu.Unlock()
	if stillLive || afterPaneCount != 0 {
		t.Fatalf("newer empty snapshot did not apply: stillLive=%v panes=%d", stillLive, afterPaneCount)
	}
}

// A delayed strict pane refresh must also yield to a newer snapshot instead of clobbering it (#2133).
func TestRefreshHostForPaneStrictDropsStaleSnapshot(t *testing.T) {
	ctx := context.Background()
	n, _, o, procs, home := orcaNode(t)
	writeOrcaCodexRollout(t, home, orcaCwd, cxUserLine("hello"))

	codexTerm := orcaTerm("term_1", "tab1", "leaf1", "codex")
	procs.set("tab1:leaf1", orcaProc{pid: 5152, agent: "codex"})
	n.mu.Lock()
	n.panes["orca:tab1:leaf1"] = cxID
	n.mu.Unlock()
	o.SetWorktrees(orcarttest.Worktree("repo::"+orcaCwd, orcaCwd, "refs/heads/feature-x",
		orcarttest.Agent("tab1:leaf1", "codex", "done")))

	// Prime the layout cache so the delayed call issues a single terminal.list.
	o.SetTerminals()
	h := n.hosts.Get(host.Orca)
	if err := n.refreshHosts(ctx, h); err != nil {
		t.Fatalf("prime refreshHosts: %v", err)
	}

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var listCalls atomic.Int32
	o.Handle("terminal.list", func(map[string]any) any {
		if listCalls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return map[string]any{"terminals": []any{}}
		}
		return map[string]any{"terminals": []any{codexTerm}}
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- n.refreshHostForPaneStrict(ctx, h, "orca:tab1:leaf1")
	}()
	<-firstStarted

	if err := n.refreshHosts(ctx, h); err != nil {
		t.Fatalf("newer refreshHosts: %v", err)
	}
	assertCodexLiveWritable(t, n, "orca:tab1:leaf1")

	close(releaseFirst)
	if err := <-errCh; err != nil {
		t.Fatalf("older strict refresh returned unexpected error: %v", err)
	}
	assertCodexLiveWritable(t, n, "orca:tab1:leaf1")

	oh := h.(*orcaHost)
	oh.mu.Lock()
	cachedTerm := oh.terms["tab1:leaf1"]
	oh.mu.Unlock()
	if cachedTerm.Handle != "term_1" {
		t.Fatalf("stale terminal.list clobbered orcaHost.terms cache: %+v", cachedTerm)
	}
}

// setAgentsAtGen must refuse to commit when a newer hostAgents generation has already been published (#2133).
func TestSetAgentsAtGenDropsSupersededCommit(t *testing.T) {
	n, _, _ := herdrNode(t)
	n.mu.Lock()
	epoch := n.engEpoch
	n.agentsGen = 2
	n.agents[cxID] = host.Agent{Agent: "codex", PaneID: "orca:tab1:leaf1", Cwd: orcaCwd}
	n.mu.Unlock()

	committed, epochChanged := n.setAgentsAtGen(nil, epoch, 1, map[string]bool{host.Orca: true})
	if committed || epochChanged {
		t.Fatalf("superseded gen committed=%v epochChanged=%v, want false, false", committed, epochChanged)
	}
	n.mu.Lock()
	got := n.agents[cxID]
	n.mu.Unlock()
	if got.PaneID != "orca:tab1:leaf1" {
		t.Fatalf("superseded setAgentsAtGen overwrote newer live binding: %+v", got)
	}
}

// Answering an asynchronous Codex question in Orca while a turn is active must ignore
// compact "tab to queue message" footers, respect genuine Orca Screen.Draft values,
// confirm numbered paste chips returned in Orca's draft field, and press Enter once (#2133).
func TestOrcaCodexAsyncQuestionActiveFooterAndPasteConfirmation(t *testing.T) {
	oldStep, oldPoll, oldConfirm := codexStep, codexPoll, codexConfirm
	codexStep, codexPoll, codexConfirm = 5*time.Millisecond, 10*time.Millisecond, time.Second
	t.Cleanup(func() { codexStep, codexPoll, codexConfirm = oldStep, oldPoll, oldConfirm })

	activeTail := strings.Join([]string{
		"• Working (12s • esc to interrupt) · 1 background terminal running",
		"",
		"• Queued follow-up inputs",
		"  ? 1 question",
		"    ⇧← to answer",
		"",
		"›",
		"  GPT-6.1-Sol xhigh · /work/repo · feature-x",
		"  tab to queue message · 92% context left",
	}, "\n")

	setup := func(t *testing.T) (*Node, *orcarttest.Fake, string) {
		t.Helper()
		n, _, o, procs, home := orcaNode(t)
		rollout := writeOrcaCodexRollout(t, home, orcaCwd, cxAsyncCall, cxAsyncOut)
		o.SetTerminals(orcaTerm("term_1", "tab1", "leaf1", "codex"))
		o.SetWorktrees(orcarttest.Worktree("repo::"+orcaCwd, orcaCwd, "refs/heads/feature-x",
			orcarttest.Agent("tab1:leaf1", "codex", "working")))
		procs.set("tab1:leaf1", orcaProc{pid: 5153, agent: "codex"})
		n.mu.Lock()
		n.panes["orca:tab1:leaf1"] = cxID
		n.mu.Unlock()
		if err := n.refreshHosts(context.Background()); err != nil {
			t.Fatalf("refreshHosts: %v", err)
		}
		assertCodexLiveWritable(t, n, "orca:tab1:leaf1")
		o.Reset()
		return n, o, rollout
	}

	t.Run("active_turn_footer_and_numbered_paste_chip", func(t *testing.T) {
		n, o, rollout := setup(t)
		card := onlyCard(t, n)

		var mu sync.Mutex
		submitted := false
		o.Handle("terminal.read", func(map[string]any) any {
			writes := orcaWritten(o)
			var pasted string
			sawEnter := false
			for _, w := range writes {
				switch {
				case strings.Contains(w, "send_user_message_question_reply"):
					pasted = unpaste(w)
				case w == "\r" && pasted != "":
					sawEnter = true
				}
			}
			if sawEnter {
				mu.Lock()
				if !submitted {
					submitted = true
					appendLine(t, rollout, cxUserLine(pasted))
				}
				mu.Unlock()
				return orcaRead(activeTail, "")
			}
			if pasted != "" {
				nRunes := len([]rune(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(pasted)))
				return orcaRead(activeTail, fmt.Sprintf("[Pasted Content %d chars] #2", nRunes))
			}
			return orcaRead(activeTail, "")
		})

		if err := n.codexAnswer(context.Background(), cxID, card.ix.ID, proto.Resolution{
			Answers: map[string]string{"Which color should the banner be?": "Blue"},
		}); err != nil {
			t.Fatalf("codexAnswer failed: %v", err)
		}
		enters := 0
		for _, w := range orcaWritten(o) {
			if w == "\r" {
				enters++
			}
		}
		if enters != 1 {
			t.Fatalf("enter count = %d, want 1 (writes=%q)", enters, orcaWritten(o))
		}
		if cards := n.codexCardsNow(context.Background(), cxID); len(cards) != 0 {
			t.Fatalf("question still open after answer: %+v", cards)
		}
	})

	t.Run("orca_genuine_draft_is_protected", func(t *testing.T) {
		n, o, _ := setup(t)
		card := onlyCard(t, n)
		o.SetBoxScreen("term_1", activeTail, "half typed answer")

		err := n.codexAnswer(context.Background(), cxID, card.ix.ID, proto.Resolution{
			Answers: map[string]string{"Which color should the banner be?": "Blue"},
		})
		if code := refusalCode(err); code != screen.InputDraft {
			t.Fatalf("err=%v (code=%q), want %q", err, code, screen.InputDraft)
		}
		if w := orcaWritten(o); len(w) != 0 {
			t.Fatalf("genuine draft was clobbered by writes: %v", w)
		}
	})
}
