package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// desktopFirst is a start's first message as people write it: an instruction template before the task.
const desktopFirst = "用中文工作，最后用中文汇报。帮我修复 PDF 导出时丢失图片的问题，先看 export 模块。"

// desktopNamingFixture is namingFixture with session d1 as the node lists one it just started in Claude's desktop app:
// seeded under its folder's name, a custom title (node/claudedesktop_new.go).
func desktopNamingFixture(t *testing.T, answer func(http.ResponseWriter, *http.Request)) (*Service, *fakeHub, *atomic.Int32) {
	t.Helper()
	s, f, calls := namingFixture(t, answer)
	f.setSessions("server", desktopRow("report"))
	return s, f, calls
}

func desktopRow(title string) proto.Session {
	return proto.Session{SessionID: "d1", Title: title, TitleSource: proto.TitleSourceManual, TitleManual: true, Cwd: "/src/report",
		Agent: "claude", SourceKind: proto.SourceClaudeDesktop, Status: proto.StatusWorking, UpdatedAt: time.Now(), OutputTokens: 7}
}

// desktopStarted is the hub's session.start_ended for an app's start that made d1 in Claude's desktop app under seeded,
// named from text, decoded as it comes over the bus. It returns once StartEnded has returned, not once d1 is named.
func desktopStarted(t *testing.T, s *Service, seeded, text string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"machine_id": "server", "launcher": "claude-desktop", "ok": true,
		"started": map[string]any{"session_id": "d1", "title": seeded, "name_from": text}})
	var p StartEnded
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	s.StartEnded(context.Background(), p)
}

// desktopListed is d1's title as the apps' lists get it.
func desktopListed(s *Service, f *fakeHub) proto.Session {
	f.mu.Lock()
	row := f.sessions["server"][0]
	f.mu.Unlock()
	s.nameAgentRow("server", &row)
	return row
}

// noAgentInputBesidesRename: the only call that reached the session was the user's own rename.
func noAgentInputBesidesRename(t *testing.T, f *fakeHub) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	renames := 0
	for _, call := range f.calls {
		switch call.Method {
		case DeviceSessionRename:
			renames++
		case DeviceSessionSend:
			t.Fatalf("naming a desktop start typed into the session: %+v", call)
		}
	}
	if renames != 1 {
		t.Fatalf("%d renames reached the node, want the user's one", renames)
	}
}

func noAgentInput(t *testing.T, f *fakeHub) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.Method == DeviceSessionRename || call.Method == DeviceSessionSend {
			t.Fatalf("naming a desktop start touched the session: %+v", call)
		}
	}
}

// A session started in Claude's desktop app is named by the model from what the start asked, with the prompt and
// model of every automatic Agent name, not by its first message: the lists show the model's title over the placeholder
// the node seeded, as an automatic name, and nothing is typed into the session.
func TestADesktopStartIsNamedByTheModelNotItsFirstMessage(t *testing.T) {
	var input, system string
	s, f, calls := desktopNamingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			System   any    `json:"system"`
			Messages any    `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sys, _ := json.Marshal(body.System)
		in, _ := json.Marshal(body.Messages)
		system, input = string(sys), string(in)
		if body.Model != "sorting" {
			t.Errorf("model %q, want the Sorting purpose's (the automatic Agent names')", body.Model)
		}
		namingReply(w, "修复 PDF 导出丢图")
	})
	desktopStarted(t, s, "report", desktopFirst)
	s.agentNames.runs.Wait()
	if calls.Load() != 1 {
		t.Fatalf("%d model calls, want 1", calls.Load())
	}
	quoted := func(v string) string { b, _ := json.Marshal(v); return strings.Trim(string(b), `"`) }
	if !strings.Contains(system, quoted(strings.TrimSpace(prompts.RenderAgentSessionName()))) {
		t.Fatalf("not the automatic Agent name prompt: %s", system)
	}
	if !strings.Contains(input, quoted(desktopFirst)) || strings.Contains(input, quoted("正在检查导出图片的路径。")) {
		t.Fatalf("not named from the start's first message: %s", input)
	}
	row := desktopListed(s, f)
	if row.Title != "修复 PDF 导出丢图" || row.TitleSource != proto.TitleSourceAuto || row.TitleManual {
		t.Fatalf("listed as %q (source %q, manual %v), want the model's title as an automatic name", row.Title, row.TitleSource, row.TitleManual)
	}
	if strings.Contains(row.Title, "用中文工作") || strings.HasPrefix(desktopFirst, row.Title) {
		t.Fatalf("the title is taken from the first message: %q", row.Title)
	}
	if rec := agentNameRecordOf(s, "server", "d1"); rec.Title != "修复 PDF 导出丢图" || rec.RawTitle != "report" || !rec.Requested || rec.Manual || rec.LastFailure != "" {
		t.Fatalf("ledger: %+v", rec)
	}
	noAgentInput(t, f)
	// A rename in Claude later wins over it, as over any automatic name.
	f.setSessions("server", desktopRow("我自己起的名字"))
	if row := desktopListed(s, f); row.Title != "我自己起的名字" {
		t.Fatalf("a native rename lost to the automatic name: %q", row.Title)
	}
}

// The start does not wait for the name: StartEnded returns while the model is still out, the lists show the
// placeholder meanwhile, and the name follows.
func TestADesktopStartsNameDoesNotHoldTheStart(t *testing.T) {
	asked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s, f, _ := desktopNamingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(asked) })
		<-release
		namingReply(w, "修复 PDF 导出丢图")
	})
	returned := make(chan struct{})
	go func() {
		desktopStarted(t, s, "report", desktopFirst)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("StartEnded waited for the model")
	}
	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the model was never asked")
	}
	if row := desktopListed(s, f); row.Title != "report" {
		close(release)
		t.Fatalf("while the model is out the row is %q, want the placeholder", row.Title)
	}
	close(release)
	s.agentNames.runs.Wait()
	if row := desktopListed(s, f); row.Title != "修复 PDF 导出丢图" {
		t.Fatalf("named %q after the model answered", row.Title)
	}
}

// When the model cannot name it (not set up, an error, a timeout, an answer that is no title), the placeholder the node
// seeded stays: never the first message. The reason is logged and kept in the ledger; nothing is typed into the
// session, and nothing else is retried behind the user's back.
func TestADesktopStartKeepsItsPlaceholderWhenTheModelFails(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		answer       func(http.ResponseWriter, *http.Request)
		hang         bool // the model never answers
		prepare      func(*testing.T, *Service, *fakeHub)
	}{
		{name: "provider error", reason: "model_call_failed", answer: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream down"))
		}},
		{name: "timeout", reason: "naming_timeout", hang: true,
			prepare: func(t *testing.T, _ *Service, _ *fakeHub) {
				old := agentNamingTimeout
				agentNamingTimeout = 300 * time.Millisecond
				t.Cleanup(func() { agentNamingTimeout = old })
			}},
		{name: "no usable title", reason: "invalid_name", answer: func(w http.ResponseWriter, r *http.Request) { namingReply(w, "Claude Code") }},
		{name: "no model", reason: "model_not_configured", prepare: func(_ *testing.T, s *Service, f *fakeHub) {
			f.mu.Lock()
			f.hello.Models.AI, f.hello.Models.TriageAI = ai.Config{}, nil
			f.mu.Unlock()
			s.settingsChanged(context.Background())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer := tc.answer
			if answer == nil {
				answer = func(w http.ResponseWriter, r *http.Request) { namingReply(w, "修复 PDF 导出丢图") }
			}
			release := make(chan struct{})
			s, f, _ := desktopNamingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.hang {
					select {
					case <-release:
					case <-r.Context().Done():
					}
					return
				}
				answer(w, r)
			})
			t.Cleanup(func() { close(release) }) // after the fixture's: it runs before the model's server waits for its handlers
			var mu sync.Mutex
			var lines []string
			s.logf = func(format string, args ...any) {
				mu.Lock()
				lines = append(lines, fmt.Sprintf(format, args...))
				mu.Unlock()
			}
			if tc.prepare != nil {
				tc.prepare(t, s, f)
			}
			desktopStarted(t, s, "report", desktopFirst)
			s.agentNames.runs.Wait()
			if row := desktopListed(s, f); row.Title != "report" || strings.Contains(row.Title, "PDF") {
				t.Fatalf("listed as %q, want the placeholder", row.Title)
			}
			if rec := agentNameRecordOf(s, "server", "d1"); rec.Title != "" || rec.Requested || rec.LastFailure != tc.reason {
				t.Fatalf("ledger %+v, want no title and last failure %q", rec, tc.reason)
			}
			mu.Lock()
			log := strings.Join(lines, "\n")
			mu.Unlock()
			if !strings.Contains(log, `"d1" reason=`+tc.reason) || strings.Contains(log, "upstream down") || strings.Contains(log, "PDF") {
				t.Fatalf("log:\n%s", log)
			}
			noAgentInput(t, f)
		})
	}
}

// A start's name is asked for once: the same start reported twice shares one model call, a session already named or
// renamed by the user is not asked about again, and a start that made no desktop session, or with the automatic-naming
// switch off, asks nothing.
func TestADesktopStartIsNamedOnce(t *testing.T) {
	release := make(chan struct{})
	s, f, calls := desktopNamingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		namingReply(w, "修复 PDF 导出丢图")
	})
	arrived := make(chan struct{}, 2)
	s.agentNames.requestJoined = func() { arrived <- struct{}{} }
	desktopStarted(t, s, "report", desktopFirst)
	desktopStarted(t, s, "report", desktopFirst)
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("both reports did not ask for the name")
		}
	}
	close(release)
	s.agentNames.runs.Wait()
	s.agentNames.requestJoined = nil
	if calls.Load() != 1 || desktopListed(s, f).Title != "修复 PDF 导出丢图" {
		t.Fatalf("%d model calls, title %q", calls.Load(), desktopListed(s, f).Title)
	}
	// Named already: a later report is not asked about.
	desktopStarted(t, s, "report", desktopFirst)
	s.agentNames.runs.Wait()
	if calls.Load() != 1 {
		t.Fatalf("a named session was named again: %d calls", calls.Load())
	}

	// Renamed in the app before the report: the user's name stands.
	s2, f2, calls2 := desktopNamingFixture(t, func(w http.ResponseWriter, r *http.Request) { namingReply(w, "修复 PDF 导出丢图") })
	if _, err := s2.Rename(context.Background(), RenameParams{MachineID: "server", SessionID: "d1", Params: map[string]any{"title": "我的名字"}}); err != nil {
		t.Fatal(err)
	}
	desktopStarted(t, s2, "report", desktopFirst)
	s2.agentNames.runs.Wait()
	if calls2.Load() != 0 || !agentNameRecordOf(s2, "server", "d1").Manual {
		t.Fatalf("a session renamed in the app was named: %d calls, %+v", calls2.Load(), agentNameRecordOf(s2, "server", "d1"))
	}
	noAgentInputBesidesRename(t, f2)

	// The automatic-naming switch off, a start with nothing to name it from, an old hub's report with no session.
	s3, f3, calls3 := desktopNamingFixture(t, func(w http.ResponseWriter, r *http.Request) { namingReply(w, "修复 PDF 导出丢图") })
	setSettings(t, s3, func(v *Settings) { off := false; v.AgentAutoNaming = &off })
	desktopStarted(t, s3, "report", desktopFirst)
	s3.agentNames.runs.Wait()
	setSettings(t, s3, func(v *Settings) { v.AgentAutoNaming = nil })
	desktopStarted(t, s3, "report", "   ")
	s3.StartEnded(context.Background(), StartEnded{MachineID: "server", Launcher: "claude-desktop", OK: true})
	s3.agentNames.runs.Wait()
	if calls3.Load() != 0 || desktopListed(s3, f3).Title != "report" {
		t.Fatalf("named without a reason to: %d calls, %q", calls3.Load(), desktopListed(s3, f3).Title)
	}
}

// A rename in Claude while the model is out is the later wish: the name that comes back does not cover it.
func TestADesktopStartsNameYieldsToARenameMeanwhile(t *testing.T) {
	var f *fakeHub
	s, f, _ := desktopNamingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		f.setSessions("server", desktopRow("我自己起的名字"))
		namingReply(w, "修复 PDF 导出丢图")
	})
	desktopStarted(t, s, "report", desktopFirst)
	s.agentNames.runs.Wait()
	if row := desktopListed(s, f); row.Title != "我自己起的名字" {
		t.Fatalf("listed as %q, want the user's rename", row.Title)
	}
	if rec := agentNameRecordOf(s, "server", "d1"); rec.Title != "" || rec.Requested {
		t.Fatalf("ledger: %+v", rec)
	}
}

// A start in Claude's desktop app has no terminal tab: the manager does not hold it for a tab name from the model.
func TestAgentStartParamsAsksNoTabNameForTheDesktopApp(t *testing.T) {
	f := newFakeHub()
	f.sessions["box"] = nil // listed among the devices
	f.tabReply = "fix-login"
	f.launcherList = []proto.Launcher{{Name: "claude-desktop", Source: proto.SourceClaudeDesktop, Agent: "claude"}, {Name: "claude", Source: "zshrc", Agent: "claude"}}
	s := newTestService(t, f)
	ctx := context.Background()
	p, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"cwd": "/code", "launcher": "claude-desktop", "launch": "engine"},
		NameFrom: "修登录"})
	if err != nil || p["launcher"] != "claude-desktop" || p["tab_name"] != nil || len(f.tabAsked) != 0 {
		t.Fatalf("desktop start: %v %v (tab names asked %d)", p, err, len(f.tabAsked))
	}
	// A terminal start gets the local placeholder; the model names its tab once the hub has the session
	// (agents.tab_name_started), so the start asks for no name either.
	p, err = s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"cwd": "/code", "launcher": "claude", "launch": "engine"},
		NameFrom: "修登录"})
	if err != nil || p["tab_name"] != localTabName("修登录") || len(f.tabAsked) != 0 {
		t.Fatalf("terminal start: %v %v (tab names asked %d)", p, err, len(f.tabAsked))
	}
	// A resume is no new session: desktop_start_naming.go does not name it, so it gets the placeholder as any start does.
	p, err = s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"cwd": "/code", "launcher": "claude-desktop",
		"resume_session_id": "d1"}, NameFrom: "修登录"})
	if err != nil || p["tab_name"] != localTabName("修登录") || len(f.tabAsked) != 0 {
		t.Fatalf("resume: %v %v (tab names asked %d)", p, err, len(f.tabAsked))
	}
}

// Nor when the computer's launcher list cannot be read and the last one seen there does not name the launcher: what
// it runs is then unknown, and its name says it is the desktop app's. Another launcher's start gets its placeholder.
func TestAgentStartParamsAsksNoTabNameForTheDesktopAppItCannotList(t *testing.T) {
	f := newFakeHub()
	f.sessions["box"] = nil // listed among the devices
	f.tabReply = "fix-login"
	f.launcherList = nil // the list fails, and no list was seen there before
	s := newTestService(t, f)
	ctx := context.Background()
	p, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"cwd": "/code", "launcher": "claude-desktop", "launch": "engine"},
		NameFrom: "修登录"})
	if err != nil || p["launcher"] != "claude-desktop" || p["tab_name"] != nil || len(f.tabAsked) != 0 {
		t.Fatalf("desktop start: %v %v (tab names asked %d)", p, err, len(f.tabAsked))
	}
	p, err = s.AgentStartParams(ctx, AgentStartParams{MachineID: "box", Params: map[string]any{"cwd": "/code", "launcher": "relay", "launch": "engine"},
		NameFrom: "修登录"})
	if err != nil || p["tab_name"] != localTabName("修登录") || len(f.tabAsked) != 0 {
		t.Fatalf("terminal start: %v %v (tab names asked %d)", p, err, len(f.tabAsked))
	}
}

// A session the manager starts in Claude's desktop app itself (agent_start here; dispatch and its other starts send the
// same session.start) is named the same way: the hub reports only an app's start, so the manager's own call does. A
// start in a terminal, or a resume, is not.
func TestADesktopSessionTheManagerStartsIsNamedByTheModel(t *testing.T) {
	var naming atomic.Int32
	var input atomic.Value
	prompt, _ := json.Marshal(strings.TrimSpace(prompts.RenderAgentSessionName()))
	namingPrompt := strings.Trim(string(prompt), `"`)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			System   any `json:"system"`
			Messages any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if system, _ := json.Marshal(body.System); !strings.Contains(string(system), namingPrompt) {
			namingReply(w, "fix-pdf") // agent_start's own calls (its tab name), as before
			return
		}
		naming.Add(1)
		in, _ := json.Marshal(body.Messages)
		input.Store(string(in))
		namingReply(w, "修复 PDF 导出丢图")
	}))
	t.Cleanup(model.Close)
	f := newFakeHub()
	f.sessions["box"] = nil
	f.settings = &Settings{Timezone: "Asia/Shanghai"}
	f.ownModels = true
	f.hello.Models.AI = ai.Config{BaseURL: model.URL, APIKey: "fixture", Model: "processing"}
	f.hello.Models.TriageAI = &ai.Config{BaseURL: model.URL, APIKey: "fixture", Model: "sorting", ReasoningEffort: "off"}
	desktop := true
	f.startReply = func(_ map[string]any, sess *proto.Session) *proto.StartInstead {
		if desktop { // seeded under its folder's name, a custom title (node/claudedesktop_new.go)
			sess.SourceKind, sess.Title, sess.TitleSource, sess.TitleManual = proto.SourceClaudeDesktop, "app", proto.TitleSourceManual, true
		}
		sess.Status, sess.UpdatedAt = proto.StatusWorking, time.Now()
		f.sessions["box"] = []proto.Session{*sess} // the fake hub's lock is held
		return nil
	}
	s := storelessService(t, f)
	start := func(prompt string) proto.Session {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"machine": "box", "cwd": "/code/app", "prompt": prompt})
		if _, err := s.runTool(context.Background(), "server", "conv9", "agent_start", raw); err != nil {
			t.Fatal(err)
		}
		s.agentNames.runs.Wait()
		f.mu.Lock()
		row := f.sessions["box"][0]
		f.mu.Unlock()
		s.nameAgentRow("box", &row)
		return row
	}
	row := start(desktopFirst)
	if naming.Load() != 1 {
		t.Fatalf("%d naming calls, want 1", naming.Load())
	}
	first, _ := json.Marshal(desktopFirst)
	if in, _ := input.Load().(string); !strings.Contains(in, strings.Trim(string(first), `"`)) {
		t.Fatalf("not named from the start's prompt: %s", in)
	}
	if row.Title != "修复 PDF 导出丢图" || row.TitleSource != proto.TitleSourceAuto {
		t.Fatalf("listed as %q (source %q), want the model's title as an automatic name", row.Title, row.TitleSource)
	}
	noAgentInput(t, f)

	desktop = false
	if row := start("修登录"); naming.Load() != 1 || row.Title == "修复 PDF 导出丢图" {
		t.Fatalf("a terminal start was named as a desktop one: %d calls, %q", naming.Load(), row.Title)
	}
	desktop = true
	var res struct{}
	if err := s.call(context.Background(), "box", DeviceSessionStart, map[string]any{"cwd": "/code/app", "resume_session_id": "s1", "prompt": "继续"}, &res); err != nil {
		t.Fatal(err)
	}
	s.agentNames.runs.Wait()
	if naming.Load() != 1 {
		t.Fatalf("a resume was named: %d calls", naming.Load())
	}
}

// What the hub reports is at most what the manager reads of it.
func TestAStartedSessionCarriesWhatTheManagerReads(t *testing.T) {
	if assistantwire.StartedNameFromRunes < agentNamingFirstRunes {
		t.Fatalf("started sessions carry %d runes, the manager reads %d", assistantwire.StartedNameFromRunes, agentNamingFirstRunes)
	}
}
