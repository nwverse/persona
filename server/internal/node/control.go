package node

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/screen"
	"github.com/nwverse/persona/server/internal/transcript"
)

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// start launches the selected agent in a new herdr tab and waits for its session: `<launcher> --session-id …` (or
// --resume) for Claude Code, waiting until claude is ready; a Codex launcher of the login shell with the first message
// (codex_start.go); a compatible CLI, or a launcher the user says runs another agent, without Claude's flags (start_agent.go).
// workspaceID puts the tab into an existing space (e.g. "new tab next to this agent"); otherwise the workspace named
// after the directory is reused or created.
// tabName names the herdr tab (hub picks one from the first message when the user gave none); empty: agent-<n>.
// newID is the id a new session gets (startAsync picked it already); empty: a fresh one.
// agentType is the agent the user picked for the launcher (claude, codex, traex); empty: what the launcher runs.
func (n *Node) start(ctx context.Context, cwd, launcher, prompt, resume, workspaceID, mode, effort, tabName, newID, agentType string, options ...modelExecution) (*proto.Session, error) {
	execution := executionOption(options)
	if err := execution.check(); err != nil {
		return nil, err
	}
	if resume != "" && validID(resume) && n.codexPath(resume) != "" && !n.isAssistantSession(resume) {
		if _, live := n.agent(resume); !live { // a Codex thread nothing runs (codex_resume.go)
			return n.resumeCodexSession(ctx, resume, launcher, prompt)
		}
	}
	if resume != "" && n.externalCodex(resume) {
		return nil, refusal("codex_read_only", "不能用启动器恢复外部 Codex 会话", nil)
	}
	cwd, launcher, err := n.checkStart(cwd, launcher, resume, mode, effort)
	if err != nil {
		return nil, err
	}
	kind := agentType
	if kind == "" {
		kind = launcherAgentType(launcher)
	}
	if kind != "claude" && kind != "codex" && kind != traexAgent {
		return nil, fmt.Errorf("unsupported agent type %q", kind)
	}
	if err := n.checkUltracodeStart(ctx, execution.Ultracode, kind); err != nil {
		return nil, err
	}
	if execution.Model != "" {
		agent, err := n.launcherModelCheck(ctx, launcher, execution.Model)
		if err != nil {
			return nil, err
		}
		if agent != "claude" && agent != "codex" {
			return nil, errors.New("普通 Agent 的启动只支持 Claude Code 或 Codex 启动器")
		}
	}
	if resume != "" {
		if a, ok := n.agent(resume); ok {
			return n.sessionFor(resume, a), nil // already running
		}
		// A claude still alive on the session (Claude's desktop app, another terminal, suspended there) refuses: two
		// writers would corrupt its transcript. Otherwise a Claude session resumes in the start's host, Orca like herdr: a
		// terminal opened in the session's worktree (#5548).
		if r := n.openElsewhereRefusal(resume); r != nil { // open in Claude's desktop app or a terminal (openelsewhere.go)
			return nil, r
		}
		if _, _, known := n.runtimes.belongs(resume); known { // an Orca native chat and the like: its runtime's, not a terminal's
			return nil, refusal("runtime_session", "这个会话属于另一个工具（例如 Orca 的原生对话），请在那里继续，这次没有启动。", nil)
		}
		if n.startKind(ctx) == proto.EngineOrca && kind != "claude" { // the start's host
			return nil, refusal("orca_resume", "Orca 上只能从手机继续 Claude 会话：其它 Agent 请在电脑上恢复，这次没有启动。", nil)
		}
	}

	// New sessions start in the host the start named, else the default host (herdr-core cannot start them).
	launch, err := hostCapFor[host.Launcher](n, n.startKind(ctx))
	if err != nil {
		return nil, err
	}
	ws := workspaceID
	if ws == "" {
		if ws, err = n.workspaceFor(ctx, launch, cwd); err != nil {
			return nil, err
		}
	}
	if argv, ok := n.codexLauncher(ctx, launcher); ok && kind != traexAgent { // codex_start.go
		if resume != "" { // a Codex thread is resumed above: this id is not one
			return nil, refusal(codexResumeRefused, "找不到这个 Codex 会话的记录，没法接着它：请在电脑上恢复（codex resume）。", nil)
		}
		if execution.Model != "" {
			argv = append(withoutModelArgs(argv), "--model", execution.Model)
		}
		if execution.Effort != "" {
			return nil, errors.New("新建 Codex 的思考强度由其启动器管理，请将此用途的思考强度恢复继承")
		}
		return n.startCodex(ctx, launch, cwd, launcher, argv, prompt, ws, tabName)
	}
	if kind != "claude" { // start_agent.go
		if mode != "" || effort != "" {
			return nil, fmt.Errorf("permission mode and effort are only supported for Claude starts")
		}
		return n.startOtherAgent(ctx, launch, cwd, launcher, kind, prompt, resume, ws, tabName)
	}
	if err := n.codexFunctionLauncher(ctx, launcher, true); err != nil {
		return nil, err
	}
	id := resume
	if id == "" {
		id = newID
	}
	if id == "" {
		id = newUUID()
	}
	created, err := n.newTab(ctx, launch, cwd, ws, tabName, id) // retire.go
	if err != nil {
		return nil, err
	}
	pane := created.Pane.String()
	if name := n.starts.tabCreated(id, created.SlotID); name != "" { // named while the tab was being made
		_ = renameSlot(ctx, launch, created.SlotID, name)
	}

	// A session whose first start failed (claude never came up) has no transcript to resume: start it under its id.
	args := "--session-id " + id
	if resume != "" && n.transcriptPath(id) != "" {
		args = "--resume " + id
	}
	// Persona's hooks: a plugin, or in --settings for a claude that loads none (hooksplugin.go).
	settings, hooks := n.claudeStartSettings(ctx, launcher, "claude", cwd)
	spec := claudeLaunch{Launcher: launcher, Settings: settings, Hooks: hooks, Model: execution.Model, Plugins: n.claudeLoadsPlugins(ctx)}
	// herdr told it is Claude (hostherdr.go): a launcher that wraps another binary is otherwise not seen as one. The
	// node's directory in front, the assistant's environment left out (agentenv.go).
	shells := paneShells(ctx)
	cmd := withHerdrAgent(typedHerdrAgent(launch, "claude", shells...), n.claudeCommandLine(spec, args+modeFlags(mode, effort), shellQuote))
	cmd = n.agentCommand(cmd, shellQuote, shells...)
	st := sessionState{Pane: pane, Launcher: launcher, Cwd: cwd, StartedAt: time.Now().UTC(), Model: execution.Model, ModelFingerprint: execution.Fingerprint}
	if prev, ok := n.state.get(id); ok && resume != "" {
		if !prev.StartedAt.IsZero() { // a branch (fork.go) has none: it starts now
			st.StartedAt = prev.StartedAt
		}
		st.Ultracode = prev.Ultracode
	}
	n.state.put(id, st)
	n.noteModes(id, mode, effort) // shown before the first prompt writes them down
	// Give the fresh shell a moment to load ~/.zshrc; typed input is buffered either way.
	time.Sleep(300 * time.Millisecond)
	n.beginUltracodeProcess(id, resume != "", true, time.Now())
	if err := launch.Run(ctx, created.Pane, cmd); err != nil {
		return nil, fmt.Errorf("run launcher: %w", err)
	}

	a, err := n.waitReady(ctx, pane)
	if err != nil {
		screen, _ := n.readPane(ctx, pane, "recent-unwrapped", 30)
		// Close the tab: a claude that comes up after we gave up would sit there unbound, and a retry opens another.
		if org, ok := launch.(host.Organizer); ok && adoptedPane(ctx) == nil { // a pane the user picked stays
			space := ws
			if space == "" {
				space = created.SpaceID
			}
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			_ = org.Close(cctx, created.Pane, space, created.SlotID)
			cancel()
		}
		return nil, fmt.Errorf("%v\n%s", err, lastLines(screen, 12))
	}
	n.bindPane(pane, id)
	_ = launch.NameAgent(ctx, created.Pane, "j-"+id[:6])
	n.holdResume(ctx, launch, pane, id, spec) // herdr's restore resumes it through the launcher (herdrresume.go)
	// Before the first prompt; never a failed start: Claude is up, and ultracode_note says why it is not on.
	if n.startUltracode(ctx, id, pane, execution.Ultracode) && prompt != "" {
		// The command may still sit in the input box: the first prompt would be typed after it and sent with it. Left
		// to the user as one held back by a dialog is (firstPrompt).
		if err := n.ultracodeBoxClear(ctx, pane); err != nil {
			why := lostUltracode
			if rpcCode(err) == screen.DialogOpen {
				why = lostDialog
			}
			log.Printf("start %s: first message held back after ultracode: %v", id, err)
			n.lostFirst.set(id, why, time.Now())
			prompt = ""
		}
	}
	if prompt != "" {
		// Checked against the transcript and typed again when it was dropped; one that never goes in leaves the
		// session needing the user, with why, not idle (firstprompt.go).
		if err := n.firstPrompt(ctx, pane, id, prompt, a); err != nil {
			return nil, err
		}
	}
	return n.sessionFor(id, *a), nil
}

// checkStart validates a start's arguments and returns the directory (~ expanded, home when empty) and launcher.
func (n *Node) checkStart(cwd, launcher, resume, mode, effort string) (string, string, error) {
	if launcher == "" {
		launcher = "claude"
	}
	if !nameRe.MatchString(launcher) {
		return "", "", fmt.Errorf("invalid launcher %q", launcher)
	}
	if resume != "" && !validID(resume) {
		return "", "", fmt.Errorf("invalid session id %q", resume)
	}
	if err := validModes(mode, effort); err != nil {
		return "", "", err
	}
	cwd = proto.ExpandHome(cwd, n.home)
	if cwd == "" {
		cwd = n.home
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return "", "", fmt.Errorf("directory %s does not exist", cwd)
	}
	return cwd, launcher, nil
}

// tabLabels are the names of the workspace's tabs.
func (n *Node) tabLabels(ctx context.Context, launch host.Launcher, ws string) map[string]bool {
	taken := map[string]bool{}
	h, ok := launch.(host.Host)
	if !ok {
		return taken
	}
	if snap, err := h.Snapshot(ctx); err == nil {
		for _, t := range snap.Slots {
			if t.SpaceID == ws {
				taken[t.Label] = true
			}
		}
	}
	return taken
}

// tabLabel is the name a new tab gets: the given one (one line, at most 32 characters), else agent-<n>; a name the
// workspace already has gets -2, -3….
func tabLabel(name string, taken map[string]bool) string {
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > 32 {
		name = strings.TrimSpace(string(r[:32]))
	}
	if name == "" {
		for i := 1; ; i++ {
			if l := fmt.Sprintf("agent-%d", i); !taken[l] {
				return l
			}
		}
	}
	if !taken[name] {
		return name
	}
	for i := 2; ; i++ {
		if l := fmt.Sprintf("%s-%d", name, i); !taken[l] {
			return l
		}
	}
}

// workspaceFor reuses the workspace named after the project directory (the user keeps one workspace per project).
func (n *Node) workspaceFor(ctx context.Context, launch host.Launcher, cwd string) (string, error) {
	var startErr error
	if _, ok := launch.(*herdrHost); ok {
		startErr = n.ensureHerdr(ctx, "start or resume a session") // herdrstart.go
	}
	wss, err := launch.Spaces(ctx)
	if err != nil {
		// Nothing was started or typed: say so, and what to do, instead of herdr's own words (the app showed
		// "herdr workspace list: exec: \"herdr\": executable file not found").
		name := n.defaultEngineName()
		if k := n.startKind(ctx); proto.EngineNames[k] != "" && k != n.defaultKind() {
			name = proto.EngineNames[k] // a start that named its host
		}
		msg := fmt.Sprintf("这台电脑上的 %s 没在运行（或没装），不能在这里新开或接着会话，这次没有发送。"+
			"要从手机接着电脑上开着的会话，在那边继续；要从手机新开，先在这台电脑上装好并打开 %s。", name, name)
		if startErr != nil && !errors.Is(startErr, errHerdrNotInstalled) {
			msg = fmt.Sprintf("这台电脑上的 %s 没在运行，自动启动也没成功，不能在这里新开或接着会话，这次没有发送。"+
				"请在这台电脑上打开 %s 再试；原因见详情。", name, name)
		}
		return "", refusal(engineMissing, msg,
			map[string]any{"detail": herdrMissingDetail(err, startErr)})
	}
	base := filepath.Base(cwd)
	for _, w := range wss {
		if w.Label == base {
			return w.ID, nil
		}
	}
	return "", nil
}

// claudeStartWait is how long a new tab's shell may take to run claude: on a machine busy with a dozen agents,
// loading ~/.zshrc took most of a minute (#1396). claudeReadyWait is how long claude then has to show its input box.
// A node's claudeStart and claudeReady replace them when set (tests shorten both): fields, not package variables a
// test reassigns, since a start's goroutine reads them after the request that began it has returned (#4800).
const claudeStartWait, claudeReadyWait = 3 * time.Minute, time.Minute

// claudeWaits is how long this node's starts wait for the shell to run claude, then for claude's input box.
func (n *Node) claudeWaits() (start, ready time.Duration) {
	start, ready = claudeStartWait, claudeReadyWait
	if n.claudeStart > 0 {
		start = n.claudeStart
	}
	if n.claudeReady > 0 {
		ready = n.claudeReady
	}
	return start, ready
}

// startWait bounds a whole start: the shell, claude, and the first message after.
func (n *Node) startWait() time.Duration {
	start, ready := n.claudeWaits()
	return start + ready + 30*time.Second
}

// waitReady polls until claude's input box is on screen, accepting the folder-trust dialog on the way.
// herdr reports the trust dialog as idle, so the screen is the source of truth. The wait for the input box starts
// once claude runs in the pane: a slow shell before it is not claude being slow. A wait in a herdr pane that ends with
// the input box on screen and herdr knowing no agent there (agent_not_found, or none named) says so: herdr does not
// know that program as Claude (#3454).
func (n *Node) waitReady(ctx context.Context, pane string) (*host.Agent, error) {
	startWait, readyWait := n.claudeWaits()
	deadline := time.Now().Add(startWait)
	running := false
	h, _, _ := n.paneHost(pane)
	_, onHerdr := h.(*herdrHost)
	started := func() {
		if !running {
			running = true
			deadline = time.Now().Add(readyWait)
		}
	}
	readyStreak := 0
	unrecognised := false // at the last look herdr knew no claude in the pane while its input box was on screen
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		screen, err := n.readPane(ctx, pane, "visible", 60)
		if err != nil {
			continue
		}
		if keys := trustKeys(screen); keys != nil {
			started()
			_ = n.sendKeys(ctx, pane, keys...)
			readyStreak = 0
			time.Sleep(time.Second)
			continue
		}
		// A machine that never ran claude stops at its first-run screens: the style picker is answered with its default
		// and the security notes with Enter (the folder's trust dialog comes next, above), and a screen that waits for a
		// person ends the wait now instead of at the timeout.
		if keys := firstRunKeys(screen); keys != nil {
			_ = n.sendKeys(ctx, pane, keys...)
			readyStreak = 0
			time.Sleep(time.Second)
			continue
		}
		if reason := firstRunScreen(unframed(screen)); reason != "" {
			return nil, errors.New(reason)
		}
		a, err := n.paneAgent(ctx, pane)
		if err != nil || a.Agent != "claude" {
			if onHerdr && (err == nil || errors.Is(err, host.ErrNoAgent)) {
				unrecognised = inputBoxVisible(screen)
			}
			readyStreak = 0
			continue
		}
		unrecognised = false
		started()
		if a.AgentStatus == proto.StatusBlocked {
			return a, nil // some other dialog; the phone shows it as a screen interaction
		}
		if (a.AgentStatus == proto.StatusIdle || a.AgentStatus == proto.StatusDone) && inputBoxVisible(screen) {
			readyStreak++
			if readyStreak >= 2 {
				return a, nil
			}
			continue
		}
		readyStreak = 0
	}
	if unrecognised {
		wait := startWait
		if running { // the trust dialog was answered: the wait after it
			wait = readyWait
		}
		return nil, n.unrecognisedClaude(ctx, pane, wait)
	}
	if !running {
		return nil, fmt.Errorf("claude did not start within %s: the tab's shell had not run it yet (is the machine busy?)", startWait)
	}
	return nil, fmt.Errorf("claude did not become ready within %s of starting", readyWait)
}

// unrecognisedClaude is why a start in a herdr pane failed whose claude showed its input box while herdr never
// reported it, with the programs herdr sees in front of the pane: herdr knows Claude by the program's name or by
// HERDR_AGENT (hostherdr.go), and a wrapper whose binary has another name gets neither when herdr does not read the
// variable, the pane's shell is not one the node types it for, or the launcher runs another command before claude
// (#3454).
func (n *Node) unrecognisedClaude(ctx context.Context, pane string, wait time.Duration) error {
	sees := ""
	if h, ref, err := n.paneHost(pane); err == nil {
		if f, ok := h.(host.ForegroundNamer); ok {
			if names, err := f.PaneForeground(ctx, ref); err == nil && len(names) > 0 {
				sees = " (herdr sees " + strings.Join(names, ", ") + " in front)"
			}
		}
	}
	return fmt.Errorf("claude's input box is on screen but herdr did not recognise the agent within %s%s: is claude a wrapper? "+
		"herdr knows Claude by its process name, or by HERDR_AGENT=claude, which the node types in front of the launcher "+
		"in a bash, zsh, fish, sh, dash or ksh pane; herdr on Windows and before 0.7.1 (0.7.5 on macOS) ignores it, and a "+
		"launcher that runs another command first (an alias starting with export or cd) does not pass it on to claude", wait, sees)
}

// inputBoxVisible reports whether Claude Code's prompt footer is rendered.
func inputBoxVisible(screen string) bool {
	return strings.Contains(screen, "shift+tab") || strings.Contains(screen, "? for shortcuts") ||
		strings.Contains(screen, "bypass permissions") || strings.Contains(screen, "esc to interrupt") ||
		// Claude Code 2.1.206 in default (manual) mode: "⏸ manual mode on · ← for agents", no shift+tab hint.
		strings.Contains(screen, "manual mode on") || strings.Contains(screen, "← for agents")
}

// trustKeys returns the keys that select "Yes, I trust this folder" on Claude Code's trust dialog.
func trustKeys(screen string) []string {
	if !strings.Contains(screen, "trust this folder") && !strings.Contains(screen, "Do you trust") {
		return nil
	}
	lines := strings.Split(screen, "\n")
	cur, yes := -1, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "❯") {
			cur = i
		}
		if strings.Contains(t, "Yes, I trust") || strings.Contains(t, "Yes, proceed") {
			yes = i
		}
	}
	if yes < 0 {
		return nil
	}
	if cur < 0 {
		cur = yes
	}
	var keys []string
	for i := cur; i < yes; i++ {
		keys = append(keys, "down")
	}
	for i := cur; i > yes; i-- {
		keys = append(keys, "up")
	}
	return append(keys, "enter")
}

// themePickerQuestion is the first-run style picker's prompt (Claude Code 2.1.318, "Choose the text style that looks
// best with your terminal"). The node accepts its default so an agent can start on a machine where claude never ran.
const themePickerQuestion = "Choose the text style that looks best with your terminal"

// firstRunKeys returns the keys that get past one of Claude Code's first-run screens the node can answer by itself:
// the style picker, then the security notes. A fresh 2.1.318 shows them in that order, then the folder's trust dialog.
func firstRunKeys(screen string) []string {
	if keys := themePickerKeys(screen); keys != nil {
		return keys
	}
	return securityNotesKeys(screen)
}

// themePickerKeys returns the keys that accept a theme on Claude Code's first-run style picker. With numbered choices
// the cursor is walked back to the first one and Enter pressed. Claude Code 2.1.318 draws its choices without numbers
// ("Auto (match terminal)" first, the cursor on its default "✔ Dark mode"), so the old match never fired and the start
// timed out on the picker: there Enter takes the highlighted choice. Which theme it is does not matter.
func themePickerKeys(screen string) []string {
	if !strings.Contains(unframed(screen), themePickerQuestion) {
		return nil
	}
	cur, top, question := -1, -1, -1
	for i, l := range strings.Split(screen, "\n") {
		if question < 0 && strings.Contains(l, "Choose the text") {
			question = i
		}
		if question < 0 {
			continue // above the picker: the shell's own prompt may draw a ❯
		}
		if top < 0 && styleChoice(l) != "" {
			top = i
		}
		if strings.Contains(l, "❯") {
			cur = i
		}
	}
	if top < 0 {
		if cur < 0 {
			return nil // unnumbered choices whose cursor is not drawn yet
		}
		return []string{"enter"}
	}
	if cur < top {
		cur = top // no cursor rendered yet: the first choice is already selected
	}
	var keys []string
	for i := cur; i > top; i-- {
		keys = append(keys, "up")
	}
	return append(keys, "enter")
}

// securityNotesKeys returns Enter for Claude Code's first-run "Security notes" page, which waits only for it ("Press
// Enter to continue…", 2.1.318).
func securityNotesKeys(screen string) []string {
	if !strings.Contains(screen, "Security notes:") || !strings.Contains(screen, "Press Enter to continue") {
		return nil
	}
	return []string{"enter"}
}

// unframed is the screen with a box's borders and padding taken out and its wrapped lines joined, which is how the
// picker's question spans two lines inside its frame.
func unframed(screen string) string {
	var b strings.Builder
	for _, l := range strings.Split(screen, "\n") {
		t := strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "│|╭╮╰╯─"))
		if t == "" {
			continue
		}
		b.WriteString(t)
		b.WriteByte(' ')
	}
	return b.String()
}

// styleChoice is the option text of one line of the picker, or "" when the line is not one. The picker may be drawn
// inside a box, so the choice is found anywhere in the line.
func styleChoice(line string) string {
	t := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│|"))
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '9' {
			continue
		}
		j := i
		for j < len(t) && t[j] >= '0' && t[j] <= '9' {
			j++
		}
		if j+1 < len(t) && t[j] == '.' && t[j+1] == ' ' {
			return strings.TrimSpace(strings.TrimRight(t[j+1:], "│| "))
		}
		i = j
	}
	return ""
}

// firstRunScreen names the first-run screen the node cannot complete on its own, or "" when the screen is not one.
// These wait for a person, so the caller reports them at once instead of polling until the timeout.
func firstRunScreen(screen string) string {
	switch {
	case strings.Contains(screen, "Select login method:"):
		return "this machine's Claude Code has not been set up yet: it is showing the sign-in screen, run claude once in a terminal there"
	case strings.Contains(screen, "Welcome to Claude Code") && strings.Contains(screen, "Select"):
		return "this machine's Claude Code has not been set up yet: it is showing its first-run screen, run claude once in a terminal there"
	}
	return ""
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (n *Node) sessionFor(id string, a host.Agent) *proto.Session {
	s := &proto.Session{
		SessionID: id, Cwd: a.Cwd, Status: a.AgentStatus, PaneID: a.PaneID, TabID: a.TabID,
		WorkspaceID: a.WorkspaceID, AgentName: a.Name, Title: a.Title, Agent: a.Agent, UpdatedAt: time.Now().UTC(), // just started
	}
	_ = n.refreshHosts(context.Background())
	n.placeIn(s, a)
	n.decorate(s)
	s.PermissionMode, s.Effort = n.modesOf(id)
	if why := n.lostFirstPrompt(id, s.Status); why != "" { // firstprompt.go
		s.Status, s.AttentionReason = proto.StatusBlocked, why
	}
	return s
}

// paneFor returns the pane hosting a live session, refreshing herdr state once if needed.
func (n *Node) paneFor(ctx context.Context, id string) (string, error) {
	if a, ok := n.agent(id); ok {
		return a.PaneID, nil
	}
	if err := n.refreshHosts(ctx); err == nil { // the whole snapshot: agents the host does not detect (a compatible CLI) too
		if a, ok := n.agent(id); ok {
			return a.PaneID, nil
		}
	}
	// A botmux session parked for too long to be listed is still botmux's: a reply goes to botmux, which resumes it,
	// not to a claude --resume of ours.
	if h, ok := n.hosts.Get(host.Botmux).(*botmuxHost); ok {
		if _, a, err := h.row(ctx, id); err == nil {
			return a.PaneID, nil
		}
	}
	return "", fmt.Errorf("session %s is not running", id)
}

func (n *Node) send(ctx context.Context, id, text string, images []proto.Image, opts sendOpts) error {
	// Two sends at once (quick taps, two phones) would both find the box empty and type into each other, or the second
	// would see the first still in the box and be refused as a draft: one at a time, in the order they came.
	unlock, err := n.inputTurn(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	if n.externalCodex(id) {
		if n.codexResumable(id) { // nothing runs it: resumed in a new tab with the message (codex_resume.go)
			return n.resumeCodexThread(ctx, id, opts.Launcher, text, images) // a refusal says nothing was sent
		}
		return n.sendCodex(ctx, id, text, images)
	}
	if r := n.agentsListRefusal(id); r != nil { // claudebg.go
		return r
	}
	if _, live := n.agent(id); !live {
		// A claude outside Persona's panes has it open (the desktop app, another terminal): its peer inbox, or a
		// refusal saying why not; never a second claude on the same transcript (claudebg.go).
		if handled, err := n.sendOutside(ctx, id, text, images); handled {
			return err
		}
	}
	n.dropSuggestion(id)
	if exitCommand(text) && len(images) == 0 && !n.isAssistantSession(id) && !n.runtimeAssistant(id) {
		return n.exitAgent(ctx, id)
	}
	pane, err := n.paneFor(ctx, id)
	if err != nil && (n.codexPath(id) != "" || n.traexPath(id) != "") {
		return n.resumeOther(ctx, id, text, images) // retire.go
	}
	kind := "claude"
	if err == nil {
		if a, ok := n.agent(id); ok && a.Agent != "" {
			kind = a.Agent
		}
		if kind == "codex" && n.codexJoined(id) {
			// On its app-server (codex_daemon.go): a turn or a steer, nothing typed — a draft in its box stays as it is.
			return n.sendCodex(ctx, id, text, images)
		}
		if err := n.checkPaneFor(ctx, pane, kind, opts); err != nil { // sendsafe.go
			return err
		}
	} else {
		// On a machine whose default host is a runtime (Paseo) the session is continued there (runtimecontrol.go).
		if _, ok := engineRuntimes[n.defaultKind()]; ok {
			return n.continueInRuntime(ctx, id, text, images, opts.ClientMsgID)
		}
		if r := n.notRunningRefusal(ctx, id); r != nil {
			return r
		}
		// Offline: resume (with the launcher that started it, see resumeLauncher) and then send.
		st, _ := n.state.get(id)
		launcher, execution := n.resumeLauncher(st, id, opts)
		cwd := st.Cwd
		if cwd == "" {
			if p := n.transcriptPath(id); p != "" {
				if fi, err := os.Stat(p); err == nil {
					cwd = n.meta(p, fi).cwd
				}
			}
		}
		mode, effort := n.modesOf(id) // resume with what it ran with: modes, model, and the tab's name from its title
		s, err := n.start(ctx, cwd, launcher, "", id, "", mode, effort, n.resumeTabName(id), "", "", execution)
		if err != nil {
			return err
		}
		pane = s.PaneID
		// start returns early when Claude comes up blocked (a dialog of its own): the text would answer it.
		if err := n.checkPane(ctx, pane, "claude", ""); err != nil {
			return err
		}
	}
	if kind == "claude" {
		// A queued ultracode switch goes in before this message while Claude rests; at work, the message joins Claude's
		// own queue and the switch waits for the turn to end (ultracode.go). Whatever becomes of the switch (its
		// ultracode_note says), the message is sent: only a command that may still sit in the input box is not typed
		// over.
		if typed, err := n.applyUltracode(ctx, id, pane); typed && err != nil {
			if err := n.ultracodeBoxClear(ctx, pane); err != nil {
				return err
			}
		}
	}
	paths, err := n.saveImages(images)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		// In a tag so it never mixes with what the user typed; the phone shows these as thumbnails.
		text = strings.TrimSpace(text) + "\n\n<attached_images>\n" + strings.Join(paths, "\n") + "\n</attached_images>"
	}
	var watch *promptWatch
	if kind == "claude" {
		text = keepImagePaths(text)
		watch = n.watchPrompt(id, text) // where the transcript ends before the typing: what comes after may be this (sendsafe.go)
	}
	typedAt := time.Now()
	err = n.prompt(ctx, pane, text)
	if errors.Is(err, host.ErrAgentBlocked) && (kind == "codex" || kind == traexAgent) {
		// herdr refuses to prompt a Codex with an async question open; its box (checked above) takes the message.
		err = n.codexType(ctx, pane, text, func(draft string) bool { return stillPrompted(draft, text) || pastedRE.MatchString(draft) })
	}
	if r := deliveryRefusal(err); r != nil { // the host refused it with nothing typed (herdr-core, Orca's front)
		log.Printf("send %s: the host refused it, nothing typed: %v", id, err) // which check refused, in its words
		return r
	}
	if err == nil {
		n.typing.typed(pane, text)
		n.lostFirst.clear(id) // the message the start could not deliver went in now, or another one
		if kind == "claude" {
			return n.afterPrompt(ctx, id, pane, text, typedAt, watch) // sendsafe.go
		}
	}
	return uncertain(err)
}

// prompt submits text to the agent in a pane. A host only prompts agents it detects (herdr); for the others (a compatible CLI)
// the text is pasted into the pane and enter pressed, only while a program other than a shell runs in front of it
// (typeable).
func (n *Node) prompt(ctx context.Context, pane, text string) error {
	err := n.hostPrompt(ctx, pane, text)
	if err == nil || !errors.Is(err, host.ErrNoAgent) {
		return err
	}
	if err := n.typeable(ctx, pane); err != nil {
		return err
	}
	paste := pasteStart + strings.ReplaceAll(text, "\x1b", inertEscape) + pasteEnd
	if err := n.sendText(ctx, pane, paste); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond) // the TUI takes the pasted text before the enter that submits it
	return n.sendKeys(ctx, pane, "enter")
}

// errShellOnly: the pane runs nothing but a shell in front (its agent exited), which would run typed text as a
// command; nothing was typed (#1185).
var errShellOnly = errors.New("only a shell in the pane")

// typeable: a pane whose agent the host does not know takes typed text only while a program other than a shell runs
// in front of it. The shell itself is in front once the agent exits, so "any process" is not enough, and pasting does
// not help: the enter after it runs the whole paste. A host that cannot say, or a failed check, types nothing.
func (n *Node) typeable(ctx context.Context, pane string) error {
	h, ref, err := n.paneHost(pane)
	if err != nil {
		return err
	}
	f, ok := h.(host.ForegroundNamer)
	if !ok {
		return host.Mark(fmt.Errorf("%s cannot say what runs in %s", h.Kind(), pane), host.ErrNotDelivered)
	}
	names, err := f.PaneForeground(ctx, ref)
	if err != nil {
		return host.Mark(fmt.Errorf("cannot check what runs in %s: %w", pane, err), host.ErrNotDelivered)
	}
	for _, name := range names {
		if !shellProgram(name) {
			return nil
		}
	}
	return host.Mark(host.Mark(fmt.Errorf("no agent runs in %s, only %q", pane, names), host.ErrNotDelivered), errShellOnly)
}

func (n *Node) saveImages(images []proto.Image) ([]string, error) {
	if len(images) == 0 {
		return nil, nil
	}
	dir := filepath.Join(n.dir, "uploads", time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var paths []string
	for _, img := range images {
		data, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			return nil, fmt.Errorf("bad image data: %w", err)
		}
		ext := ".jpg"
		switch img.Mime {
		case "image/png":
			ext = ".png"
		case "image/heic":
			ext = ".heic"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		}
		p := filepath.Join(dir, newUUID()[:8]+ext)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// imagePathLine starts the line that repeats a photo's path in an <attached_images> block as text (keepImagePaths).
const imagePathLine = "path: "

// keepImagePaths gives each path in the <attached_images> block that ends text (the node's own, or one a client wrote
// after uploading the photos itself, as Android's composer does) a second line, "path: <path>". Claude Code's paste
// turns a line that is only a path to a readable image into an `[Image #n]` and drops it; a line that starts with a
// word stays text, so the agent keeps the path. The apps take only lines starting with "/" or "~" as photos. A block
// with any other line (prose that ends in the tag, or path: lines already added) is left as it is. With words before
// it, the block makes Claude fold the paste into a `[Pasted text #n]` placeholder (longPaste).
func keepImagePaths(text string) string {
	const open = "<attached_images>"
	rest, ok := strings.CutSuffix(strings.TrimRight(text, " \t\r\n"), "</attached_images>")
	i := strings.LastIndex(rest, open)
	if !ok || i < 0 || strings.TrimSpace(rest[i+len(open):]) == "" {
		return text
	}
	var b strings.Builder
	b.WriteString(rest[:i+len(open)] + "\n")
	for _, l := range strings.Split(rest[i+len(open):], "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if !filepath.IsAbs(l) && !strings.HasPrefix(l, "~") {
			return text
		}
		b.WriteString(l + "\n" + imagePathLine + l + "\n")
	}
	return b.String() + "</attached_images>"
}

func (n *Node) keys(ctx context.Context, id string, keys []string, text string) error {
	n.dropSuggestion(id) // whatever goes in replaces or sends it
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return err
	}
	if text != "" && !n.canTypeText(pane) {
		// A host that cannot type without submitting: text and then enter is a prompt; anything else was refused
		// before any key was pressed (typingRefusal).
		if len(keys) != 1 || keys[0] != "enter" {
			return refusal(cannotType, cannotTypeMessage, nil)
		}
		err := n.hostPrompt(ctx, pane, text)
		if r := deliveryRefusal(err); r != nil {
			return r
		}
		return err
	}
	if text != "" {
		if err := n.sendText(ctx, pane, text); err != nil {
			return err
		}
	}
	if len(keys) > 0 {
		return n.sendKeys(ctx, pane, keys...)
	}
	return nil
}

// screen reads the session's terminal. source "visible" is the screen; "recent" also reaches back into the pane's
// scrollback (the phone pages it: ask for more lines until fewer come back than were asked for). ansi keeps the colors
// (SGR escapes; rows end in \r\n).
func (n *Node) screen(ctx context.Context, id string, lines int, source string, ansi bool) (string, string, error) {
	if n.isAssistantSession(id) {
		return "", proto.StatusIdle, nil // headless: no terminal; live text arrives as session.delta
	}
	if source != "recent" {
		source = "visible"
	}
	if limit := map[string]int{"visible": 400, "recent": 20000}[source]; lines <= 0 || lines > limit {
		lines = min(max(lines, 60), limit)
	}
	a, ok := n.agent(id)
	if !ok {
		pane, err := n.terminalPane(ctx, id) // also a "pane:" address (panes.go)
		if err != nil {
			return "", proto.StatusOffline, nil
		}
		a, _ = n.agent(id)
		a.PaneID = pane
	}
	if ansi && n.plainScreen(a.PaneID) {
		// No colors in this host: the plain rows, ended as colored ones are.
		text, err := n.readPane(ctx, a.PaneID, source, lines)
		if err == nil && text != "" {
			text = strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", "\r\n") + "\r\n"
		}
		return text, a.AgentStatus, err
	}
	if ansi {
		text, err := n.readPaneANSI(ctx, a.PaneID, source, lines)
		return text, a.AgentStatus, err
	}
	if lines == screen.DialogLines && source == "visible" { // a screen card's read: what the answer will read too (#6318)
		text, err := n.dialogScreen(ctx, a.PaneID)
		return text, a.AgentStatus, err
	}
	text, err := n.readPane(ctx, a.PaneID, source, lines)
	return text, a.AgentStatus, err
}

// close quits any Claude running in the machine's engine (started by Persona or by hand) and closes its pane: herdr
// closes its tab, or in a split tab only its own pane, so the other panes survive; herdr-core ends its terminal.
func (n *Node) close(ctx context.Context, id string) error {
	a, ok := n.agent(id)
	if !ok {
		if _, mine := n.state.get(id); mine {
			return nil // already gone
		}
		return fmt.Errorf("这个 Agent 不在这台电脑的终端里运行，没法从手机关闭")
	}
	org, ref, err := paneCap[host.Organizer](n, a.PaneID) // a host that cannot close it is not interrupted first
	if err != nil {
		return err
	}
	_ = n.sendKeys(ctx, a.PaneID, "ctrl+c")
	time.Sleep(200 * time.Millisecond)
	_ = n.sendKeys(ctx, a.PaneID, "ctrl+c")
	time.Sleep(500 * time.Millisecond)
	return org.Close(ctx, ref, a.WorkspaceID, a.TabID)
}

// exitCommand: the message is Claude's or Codex's own command to quit (/exit, /quit).
func exitCommand(text string) bool {
	switch strings.TrimSpace(text) {
	case "/exit", "/quit":
		return true
	}
	return false
}

// exitAgent is a message that only says to quit: typed in, the agent would exit and leave its tab holding a bare
// shell, and one already closed would be resumed in a new tab just to quit there. A running agent is closed as
// session.close does (its tab goes with it); one not running is left as it is.
func (n *Node) exitAgent(ctx context.Context, id string) error {
	if _, ok := n.agent(id); !ok {
		return nil
	}
	return n.close(ctx, id)
}

// watch streams new transcript items of a session to the hub.
type watch struct {
	cancel context.CancelFunc
}

// maxSubagentWatches bounds the subagent transcripts followed at once (a page open on one follows it, 4 reads a second
// each): sessions are few, but every subagent a person opens would be one more reader for as long as its page stays.
const maxSubagentWatches = 16

// subagentWatches counts the followed subagent transcripts. Callers hold n.mu.
func (n *Node) subagentWatches() int {
	count := 0
	for id := range n.watches {
		if _, _, sub := splitSubagent(id); sub {
			count++
		}
	}
	return count
}

// watch keeps a session's transcript streaming to the hub. Outside of a hub connection (tests that drive the method
// directly) there is nothing to end it but session.unwatch; Serve's calls go through watchFor, so the stream stops
// when its connection does.
func (n *Node) watch(id string, since int) error {
	return n.watchFor(context.Background(), id, since)
}

// watchFor is watch for the hub connection that owns parent: the transcript loop ends with that connection, so a
// resident node that opens a new Node per reconnect is not left with a reader per watch from the one before.
func (n *Node) watchFor(parent context.Context, id string, since int) error {
	f, err := n.file(id)
	if err != nil {
		if !n.startingSession(id) {
			return err
		}
		// A session that just started has no transcript until claude writes its first line: watch once it exists,
		// sending everything written before the watch began (the phone's backlog was empty). The wait is kept in
		// n.waiting, so an unwatch meanwhile ends it and another watch does not start a second one (#5389).
		n.mu.Lock()
		if _, ok := n.watches[id]; ok || n.waiting[id] != nil {
			n.mu.Unlock()
			return nil
		}
		wctx, cancel := context.WithCancel(parent)
		pw := &watch{cancel: cancel}
		if n.waiting == nil {
			n.waiting = map[string]*watch{}
		}
		n.waiting[id] = pw
		n.mu.Unlock()
		go func() {
			defer func() {
				n.mu.Lock()
				if n.waiting[id] == pw {
					delete(n.waiting, id)
				}
				n.mu.Unlock()
			}()
			for i := 0; i < 120; i++ {
				select {
				case <-wctx.Done(): // unwatched, or the connection ended, before the transcript appeared
					return
				case <-time.After(500 * time.Millisecond):
				}
				f, err := n.file(id)
				if err != nil {
					continue
				}
				mu := n.pushLock(id)
				mu.Lock()
				if _, err := f.Refresh(); err == nil {
					since = n.catchUp(id, f, since)
				}
				mu.Unlock()
				// Under wctx: an unwatch that comes before this goroutine is done still cancels the watch it installs.
				_ = n.watchFor(wctx, id, since)
				return
			}
			cancel()
		}()
		return nil
	}
	n.mu.Lock()
	if _, ok := n.watches[id]; ok {
		n.mu.Unlock()
		return nil
	}
	if _, _, sub := splitSubagent(id); sub && n.subagentWatches() >= maxSubagentWatches {
		n.mu.Unlock()
		return fmt.Errorf("too many subagent conversations followed (%d): close one first", maxSubagentWatches)
	}
	ctx, cancel := context.WithCancel(parent)
	w := &watch{cancel: cancel}
	n.watches[id] = w
	n.mu.Unlock()

	// Catch up on lines appended after the hub's last seen seq, then stream. Without a seq (the hub has not seen this
	// session since it started) the lines this Refresh appends still go out — watchLoop only pushes what later
	// Refreshes find — unless it is the first read of the file: that is the whole history, the subscriber's backlog.
	mu := n.pushLock(id)
	mu.Lock()
	readBefore := f.LastSeq() > 0
	if fresh, span, err := f.RefreshSpan(); err == nil {
		if since > 0 {
			n.catchUp(id, f, since)
		} else if readBefore && span.To > span.From {
			n.pushItems(id, fresh, span)
		}
	}
	mu.Unlock()
	go n.watchLoop(ctx, id, f, w)
	go n.watchLive(ctx, id) // claudestatus.go
	return nil
}

// catchUp pushes every item after since, a page at a time (one page used to be all: after 500 the rest was lost), and
// returns the last seq sent.
func (n *Node) catchUp(id string, f *transcript.File, since int) int {
	for {
		items, more, span := f.After(since, 500)
		n.pushItems(id, f.WithImages(items), span) // the hub sends them to every subscriber, lazy or not
		since = span.To
		if !more {
			return since
		}
	}
}

func (n *Node) pushItems(id string, items []proto.Item, span transcript.Span) {
	if n.peer == nil {
		return
	}
	n.mu.Lock()
	f := n.files[id]
	n.mu.Unlock()
	items = n.echoed(id, f, items)
	_ = n.peer.Notify("session.items", withSpan(map[string]any{"session_id": id, "items": nonNil(items)}, span, true))
}

// pushLock serializes reading a session's new lines with pushing them, so its batches reach the hub in order.
func (n *Node) pushLock(id string) *sync.Mutex {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.pushLocks == nil {
		n.pushLocks = map[string]*sync.Mutex{}
	}
	mu := n.pushLocks[id]
	if mu == nil {
		mu = &sync.Mutex{}
		n.pushLocks[id] = mu
	}
	return mu
}

func (n *Node) unwatch(id string) {
	n.mu.Lock()
	w, pw := n.watches[id], n.waiting[id]
	delete(n.watches, id)
	delete(n.waiting, id)
	n.mu.Unlock()
	if w != nil {
		w.cancel()
	}
	if pw != nil {
		pw.cancel()
	}
}

// refresh parses new transcript lines and pushes them to the hub when the session is watched — also when the new
// lines hold no items (titles, modes): the batch still tells clients they have everything up to its to_seq, so the
// next one does not look like it skipped something.
func (n *Node) refresh(id string, f *transcript.File) {
	mu := n.pushLock(id)
	mu.Lock()
	defer mu.Unlock()
	fresh, span, err := f.RefreshSpan()
	if err != nil || span.To == span.From {
		return
	}
	n.mu.Lock()
	_, watched := n.watches[id]
	n.mu.Unlock()
	if watched {
		n.pushItems(id, fresh, span)
	}
}

// watchLoop pushes new transcript lines four times a second. (It used to read the pane's screen every 400 ms too, a
// herdr call per watched session, for a live preview no view showed; the terminal pane reads the screen itself.)
func (n *Node) watchLoop(ctx context.Context, id string, f *transcript.File, w *watch) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// Drop the entry so the session is not left looking watched; a watch that ended with its connection has
			// nobody to send to, and an unwatch for it never comes. Only drop our own: after an unwatch, a new watch
			// may already hold the id, and deleting it would silently stop that stream.
			n.mu.Lock()
			if n.watches[id] == w {
				delete(n.watches, id)
			}
			n.mu.Unlock()
			return
		case <-t.C:
			n.refresh(id, f)
		}
	}
}

// editQueue takes the queued input back out of claude (the TUI's "press up to edit queued messages" pulls every
// queued message into the input box), clears the input, and re-queues the ones listed in keep, in order.
// It returns the messages that were queued so the phone can put the edited one in its composer.
func (n *Node) editQueue(ctx context.Context, id string, keep []int) ([]string, error) {
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return nil, err
	}
	p := n.transcriptPath(id)
	if p == "" {
		return nil, fmt.Errorf("transcript for session %s not found", id)
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	queue := n.meta(p, st).queue
	if len(queue) == 0 {
		return nil, fmt.Errorf("nothing is queued")
	}
	if err := n.sendKeys(ctx, pane, "up"); err != nil {
		return nil, err
	}
	time.Sleep(300 * time.Millisecond)
	// ctrl+u only clears the current line: walk back through every line of the pulled-back text.
	lines := 0
	for _, q := range queue {
		lines += strings.Count(q, "\n") + 1
	}
	for i := 0; i < lines; i++ {
		_ = n.sendKeys(ctx, pane, "ctrl+u", "backspace")
	}
	_ = n.sendKeys(ctx, pane, "ctrl+u")
	for _, k := range keep {
		if k >= 0 && k < len(queue) {
			if err := n.prompt(ctx, pane, queue[k]); err != nil {
				return queue, fmt.Errorf("re-queue: %w", err)
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	return queue, nil
}

// sendNowHint is what Claude Code (2.1.31x) shows under a queued message: its own "send now" (#6088).
const sendNowHint = "ctrl+x ctrl+s to send now"

// offersSendNow: the screen shows a queued message with Claude Code's send-now beside it.
func offersSendNow(screen string) bool {
	return strings.Contains(strings.Join(strings.Fields(screen), " "), sendNowHint)
}

// sendNowWait is how long the node looks for the send-now hint to go once the key is pressed.
var sendNowWait = 3 * time.Second

// sendQueuedNow gives the agent what waits in Claude Code's queue now (#6088), with Claude Code's own send-now (ctrl+x
// ctrl+s): a command the agent runs moves to the background and goes on, a reply it is writing is asked again with the
// message (measured on 2.1.319; Esc would stop the command). Pressed only while the screen offers it: that is the queue
// right now (the transcript says a message was taken about a second late, and a key pressed then would hit a turn that
// already has it), and an older Claude without it offers nothing.
func (n *Node) sendQueuedNow(ctx context.Context, id string) error {
	if a, ok := n.agent(id); ok && a.Agent != "claude" {
		return refusal("send_now_unsupported", "only Claude Code can send queued messages now", nil)
	}
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return err
	}
	screen, err := n.readPane(ctx, pane, "visible", 80)
	if err != nil {
		return err
	}
	if !offersSendNow(screen) {
		if p := n.transcriptPath(id); p != "" {
			if st, err := os.Stat(p); err == nil && len(n.meta(p, st).queue) > 0 {
				return refusal("send_now_unsupported", "this Claude Code shows no send-now for its queued messages (update it)", nil)
			}
		}
		return refusal("nothing_queued", "nothing is queued for the agent any more", nil)
	}
	if err := n.sendKeys(ctx, pane, "ctrl+x", "ctrl+s"); err != nil {
		return err
	}
	for deadline := time.Now().Add(sendNowWait); time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		if s, err := n.readPane(ctx, pane, "visible", 80); err == nil && !offersSendNow(s) {
			return nil
		}
	}
	// the key went in and the queue is still shown: perhaps it was taken, perhaps not (not "none", as a refusal says)
	return &rpc.Error{Code: "send_now_in_doubt", Message: "the queued messages are still shown as queued", Data: json.RawMessage(`{"delivered":"unknown"}`)}
}

// renameSpace renames a herdr workspace (what the phone calls a space).
func (n *Node) renameSpace(ctx context.Context, workspaceID, name string) error {
	name = strings.TrimSpace(name)
	if workspaceID == "" || name == "" {
		return fmt.Errorf("workspace and name are required")
	}
	org, err := hostCap[host.Organizer](n) // space ids carry no host kind yet: the engine's
	if err != nil {
		return err
	}
	return org.RenameSpace(ctx, workspaceID, name)
}

// renameSession renames the herdr tab a session runs in and / or the Claude session itself. A live claude gets
// `/rename <title>` (it records a custom-title, shown in its prompt, /resume and our lists); for one that is not
// running we append the same custom-title line Claude Code writes.
func (n *Node) renameSession(ctx context.Context, id string, tab, title *string) error {
	if tab != nil {
		if name := strings.TrimSpace(*tab); name != "" {
			if done, tabID := n.starts.nameTab(id, name); done {
				tab = nil
			} else if tabID != "" { // a tab start() made: in the host sessions start in
				org, err := hostCap[host.Organizer](n)
				if err != nil {
					return err
				}
				if err := org.RenameSlot(ctx, tabID, name); err != nil {
					return err
				}
				tab = nil
			}
		}
	}
	a, live := n.agent(id)
	if tab != nil {
		name := strings.TrimSpace(*tab)
		if name == "" || !live || a.TabID == "" {
			return fmt.Errorf("the tab can only be renamed while the session runs in herdr")
		}
		org, _, err := paneCap[host.Organizer](n, a.PaneID)
		if err != nil {
			return err
		}
		if err := org.RenameSlot(ctx, a.TabID, name); err != nil {
			return err
		}
	}
	if title != nil {
		t := strings.Join(strings.Fields(*title), " ")
		if t == "" {
			return fmt.Errorf("empty title")
		}
		if live && !n.isAssistantSession(id) {
			return n.prompt(ctx, a.PaneID, "/rename "+t)
		}
		return n.retitle(id, t)
	}
	return nil
}

// titleRetry is how often a title given before its conversation's transcript existed looks for it again, titleWait how
// long it looks (a conversation that has not written its first turn by then is not going to). Tests shorten them.
var (
	titleRetry = 500 * time.Millisecond
	titleWait  = 5 * time.Minute
)

// retitle appends the custom-title line Claude Code writes for /rename to the session's transcript. An assistant
// conversation renamed right after it started (the Feishu /agent names its conversation at once) has no transcript
// yet: Claude writes it with the first turn. Its title is kept and written once the transcript is there, the newest
// one if it is renamed again meanwhile; one whose process ends first is dropped (#5866). titleMu orders the writes, so
// a kept title never lands after a newer one written directly.
func (n *Node) retitle(id, title string) error {
	n.titleMu.Lock()
	defer n.titleMu.Unlock()
	n.mu.Lock()
	_, waiting := n.titles[id]
	if waiting {
		n.titles[id] = title
	}
	proc := n.procs[id]
	n.mu.Unlock()
	if waiting {
		return nil
	}
	if p := n.transcriptPath(id); p != "" {
		return appendCustomTitle(p, id, title)
	}
	if proc == nil {
		return fmt.Errorf("transcript for session %s not found", id)
	}
	n.mu.Lock()
	if n.titles == nil {
		n.titles = map[string]string{}
	}
	n.titles[id] = title
	n.mu.Unlock()
	go n.titleWhenWritten(id, proc.exited)
	return nil
}

// titleWhenWritten waits for the transcript of a conversation retitle kept a title for, then writes the newest one.
// When the process ends first, it looks once more (the last turn may have just been written) and drops the title.
func (n *Node) titleWhenWritten(id string, exited <-chan struct{}) {
	tick := time.NewTicker(titleRetry)
	defer tick.Stop()
	giveUp := time.NewTimer(titleWait)
	defer giveUp.Stop()
	for last := false; ; {
		if p := n.transcriptPath(id); p != "" {
			n.titleMu.Lock()
			n.mu.Lock()
			title := n.titles[id]
			delete(n.titles, id)
			n.mu.Unlock()
			if err := appendCustomTitle(p, id, title); err != nil {
				n.logf("rename %s: %v", id, err)
			}
			n.titleMu.Unlock()
			return
		}
		if last {
			n.mu.Lock()
			delete(n.titles, id)
			n.mu.Unlock()
			n.logf("rename %s: no transcript appeared, the title is dropped", id)
			return
		}
		select {
		case <-tick.C:
		case <-exited:
			last = true
		case <-giveUp.C:
			last = true
		}
	}
}

// appendCustomTitle appends to a transcript the custom-title line Claude Code writes for /rename.
func appendCustomTitle(p, id, title string) error {
	line, _ := json.Marshal(map[string]string{"type": "custom-title", "customTitle": title, "sessionId": id})
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// herdr keeps about 1000 lines per pane readable (pane read caps there whatever --lines asks); older output is only
// in Claude's transcript.
const herdrScrollbackLines = 1000

// screenBefore returns up to `lines` lines of the pane's scrollback right before the anchor (the first lines the phone
// already shows). Located by content, not by offset from the end, so output arriving meanwhile shifts nothing.
// loaded is how many lines the phone already has (from the end): Claude's redraws push the same screens into the
// scrollback again and again, so the anchor can match in several places — the one nearest len-loaded is ours.
// With ansi the page keeps its colors; the anchor is still matched on the plain text.
func (n *Node) screenBefore(ctx context.Context, id string, anchor []string, lines, loaded int, ansi bool) (map[string]any, error) {
	if lines <= 0 || lines > 500 {
		lines = 200
	}
	a, ok := n.agent(id)
	if !ok {
		pane, err := n.terminalPane(ctx, id) // also a "pane:" address (panes.go)
		if err != nil {
			return nil, err
		}
		a, _ = n.agent(id)
		a.PaneID = pane
	}
	text, err := n.readPane(ctx, a.PaneID, "recent", herdrScrollbackLines)
	if err != nil {
		return nil, err
	}
	all := strings.Split(strings.TrimRight(text, "\n"), "\n")
	at := nearestBlock(all, anchor, len(all)-loaded)
	if at < 0 { // the anchor scrolled out of the kept window: nothing older can be placed before it
		return map[string]any{"text": "", "has_more": false, "at_limit": len(all) >= herdrScrollbackLines-20}, nil
	}
	start := max(at-lines, 0)
	page := all[start:at]
	if ansi {
		// The same rows with their colors: herdr reads both from one grid, so row i is row i — unless output arrived
		// between the two reads, then the plain page is still right.
		if colored, err := n.readPaneANSI(ctx, a.PaneID, "recent", herdrScrollbackLines); err == nil {
			rows := strings.Split(strings.TrimRight(strings.ReplaceAll(colored, "\r\n", "\n"), "\n"), "\n")
			if len(rows) == len(all) {
				page = rows[start:at]
			}
		}
	}
	return map[string]any{
		"text":     strings.Join(page, "\n"),
		"has_more": start > 0,
		// The start of what herdr keeps: older output was dropped (long panes) or never existed (short ones).
		"at_limit": start == 0 && len(all) >= herdrScrollbackLines-20,
	}, nil
}

// nearestBlock is the match of block whose start is closest to want, -1 when it does not appear.
func nearestBlock(all, block []string, want int) int {
	best := -1
	for i := len(all) - len(block); i >= 0; {
		j := findBlock(all[:i+len(block)], block)
		if j < 0 {
			break
		}
		if best < 0 || abs(j-want) < abs(best-want) {
			best = j
		}
		i = j - 1
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// findBlock returns where the lines of block start in all (the last match), -1 if they do not appear.
func findBlock(all, block []string) int {
	if len(block) == 0 {
		return len(all)
	}
	for i := len(all) - len(block); i >= 0; i-- {
		match := true
		for j, b := range block {
			if strings.TrimRight(all[i+j], " ") != strings.TrimRight(b, " ") {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
