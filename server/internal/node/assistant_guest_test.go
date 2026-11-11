package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/server/internal/compat"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A guest conversation's process (#5195): Persona's MCP server alone, told it is a guest's, no built-in tools, only
// its own tools allowed, none of the extensions' servers or deny rules. An ordinary one keeps everything.
func TestGuestProcessFlags(t *testing.T) {
	n, _ := testNode(t)
	bin := writeFakeMCPBin(t, t.TempDir(), "example-agent", "Usage: example-agent mcp-server --control")
	t.Setenv("PERSONA_EXAMPLE_AGENT", bin)
	n.larkCfg, n.larkCfgAt = proto.AssistantLark{MCPServers: []proto.AssistantMCPServer{exampleMCP()}}, time.Now().Add(time.Hour)
	n.state.put("guest-1", sessionState{Launcher: "claude", Guest: []string{"lark_agent_reply"}})

	servers := func(mcpJSON string) map[string]map[string]any {
		var cfg struct {
			MCPServers map[string]map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(mcpJSON), &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg.MCPServers
	}
	flagValue := func(flags []string, name string) (string, bool) {
		i := slices.Index(flags, name)
		if i < 0 || i+1 >= len(flags) {
			return "", false
		}
		return flags[i+1], true
	}

	mcpJSON, flags := n.claudeAssistantMCPAndTools("guest-1", t.TempDir(), true)
	got := servers(mcpJSON)
	if len(got) != 2 || got["persona"] == nil || got[compat.MCPServer] == nil {
		t.Fatalf("servers = %v, want Persona's alone", got)
	}
	env, _ := got["persona"]["env"].(map[string]any)
	if env["PERSONA_GUEST"] != "1" || env["PERSONA_SESSION_ID"] != "guest-1" || env["PERSONA_ASSISTANT"] != "1" {
		t.Fatalf("persona env = %v", env)
	}
	if v, ok := flagValue(flags, "--tools"); !ok || v != "" {
		t.Fatalf("--tools = %q (%v), want no built-in tools: %q", v, ok, flags)
	}
	if v, _ := flagValue(flags, "--allowedTools"); v != strings.Join(compat.MCPToolNames([]string{"lark_agent_reply"}), ",") {
		t.Fatalf("--allowedTools = %q, want only the guest's tool", v)
	}
	if !slices.Contains(flags, "--strict-mcp-config") || slices.Contains(flags, "--disallowedTools") {
		t.Fatalf("flags = %q", flags)
	}

	mcpJSON, flags = n.claudeAssistantMCPAndTools("ordinary-1", t.TempDir(), true)
	builtin, hasBuiltin := flagValue(flags, "--tools")
	if servers(mcpJSON)["example"] == nil || !hasBuiltin || builtin != assistantBuiltinTools || slices.Contains(flags, "--strict-mcp-config") {
		t.Fatalf("an ordinary conversation changed: %s %q", mcpJSON, flags)
	}
	if v, _ := flagValue(flags, "--allowedTools"); !slices.Contains(strings.Split(v, ","), assistantAllowedTools) {
		t.Fatalf("an ordinary conversation lost %s: %q", assistantAllowedTools, v)
	}
}

// Started as a guest: a new conversation with its tools kept in its state, never the warm spare, its process started
// with the guest's flags; a later message keeps it on what it started on whatever launcher or runtime it names.
func TestGuestConversationStarts(t *testing.T) {
	fakeClaudeHome(t, "for a in \"$@\"; do printf ' [%s]' \"$a\"; done > \"$HOME/args-$$\"\nread line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	n := codexTestNode(t)
	withFakeHub(t, n)
	cwd := t.TempDir()
	if err := n.assistantWarm(cwd, "claude"); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	spare := n.spare
	n.mu.Unlock()
	if spare == nil {
		t.Fatal("no spare to leave alone")
	}
	t.Cleanup(spare.stop)

	id, err := n.assistantSend(withGuest(context.Background(), []string{"lark_agent_reply"}), "", cwd, "claude", "someone asks", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	if id == spare.id {
		t.Fatal("a guest conversation took the warm spare")
	}
	n.mu.Lock()
	still := n.spare == spare
	n.mu.Unlock()
	if !still {
		t.Fatal("the spare was handed out")
	}
	if st, _ := n.state.get(id); !slices.Equal(st.Guest, []string{"lark_agent_reply"}) || st.Cwd != cwd {
		t.Fatalf("state = %+v", st)
	}
	var line string
	files, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), "args-*"))
	for _, f := range files {
		if raw, _ := os.ReadFile(f); strings.Contains(string(raw), "["+id+"]") {
			line = string(raw)
		}
	}
	for _, want := range []string{"[--tools] []", "[--strict-mcp-config]", "[--allowedTools] [" + strings.Join(compat.MCPToolNames([]string{"lark_agent_reply"}), ",") + "]"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the guest's process has no %s: %s", want, line)
		}
	}

	// A later message naming another launcher and the Agent SDK still goes to it as it is.
	if _, err := n.assistantSend(context.Background(), id, "", "codex", "more", nil, "", "", modelExecution{Runtime: proto.AssistantRuntimeAgentSDK}); err != nil {
		t.Fatalf("a later message: %v", err)
	}
	if st, _ := n.state.get(id); st.Launcher != "claude" || st.Runtime != "" || st.Guest == nil {
		t.Fatalf("state after a later message = %+v", st)
	}
}

// Only Claude Code on the CLI runs a guest: the Agent SDK is refused before anything starts.
func TestGuestRefusesTheAgentSDK(t *testing.T) {
	fakeClaudeHome(t, "read line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	n := codexTestNode(t)
	withFakeHub(t, n)
	_, err := n.assistantSend(withGuest(context.Background(), []string{"lark_agent_reply"}), "", t.TempDir(), "claude", "hi", nil, "", "",
		modelExecution{Runtime: proto.AssistantRuntimeAgentSDK})
	if !errors.Is(err, errGuestAgent) {
		t.Fatalf("err = %v, want errGuestAgent", err)
	}
	n.mu.Lock()
	procs := len(n.procs)
	n.mu.Unlock()
	if procs != 0 {
		t.Fatal("a process started")
	}
}

// Its directory holds only its instructions; a path out of the home is refused. Tool names are plain identifiers.
func TestGuestPrepareAndTools(t *testing.T) {
	n, _ := testNode(t)
	out, err := n.assistantGuestPrepare(json.RawMessage(`{"dir":".persona/guest","claude_md":"# guest"}`))
	if err != nil {
		t.Fatal(err)
	}
	cwd := out.(map[string]any)["cwd"].(string)
	if cwd != filepath.Join(n.home, ".persona", "guest") {
		t.Fatalf("cwd = %q", cwd)
	}
	entries, _ := os.ReadDir(cwd)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"AGENTS.md", "CLAUDE.md"}) {
		t.Fatalf("the directory holds %v", names)
	}
	for _, bad := range []string{`{"dir":"../x"}`, `{"dir":"/tmp/x"}`, `{"dir":""}`} {
		if _, err := n.assistantGuestPrepare(json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	for _, c := range []struct {
		tools []string
		ok    bool
	}{
		{[]string{"lark_agent_reply"}, true},
		{nil, false},
		{[]string{"lark_agent_reply,Bash"}, false},
		{[]string{"mcp__persona__x"}, true}, // a name; the hub says which are a guest's
		{[]string{"Bash"}, false},
		{[]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, false},
	} {
		if err := checkGuestTools(c.tools); (err == nil) != c.ok {
			t.Errorf("%q: err %v, want ok %v", c.tools, err, c.ok)
		}
	}
}

// The node relays its MCP server's calls with the guest's word for it: what the server says, or what the node knows of
// the conversation (a guest's process started before the server said so is one all the same).
func TestGuestRelayTellsTheHub(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	n.state.put("guest-1", sessionState{Launcher: "claude", Guest: []string{"lark_agent_reply"}})
	relay := func(req map[string]any) {
		t.Helper()
		a, b := net.Pipe()
		go n.serveSock(context.Background(), a)
		line, _ := json.Marshal(req)
		if _, err := b.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
		if _, err := bufio.NewReader(b).ReadBytes('\n'); err != nil {
			t.Fatal(err)
		}
		_ = b.Close()
	}
	relay(map[string]any{"kind": "mcp.tools", "assistant": true, "session_id": "guest-1"})
	relay(map[string]any{"kind": "mcp.call", "assistant": true, "session_id": "guest-1", "name": "lark_agent_reply"})
	relay(map[string]any{"kind": "mcp.tools", "assistant": true, "session_id": "other", "guest": true})
	relay(map[string]any{"kind": "mcp.call", "assistant": true, "session_id": "ordinary", "name": "kb_search"})
	fh.mu.Lock()
	defer fh.mu.Unlock()
	want := []bool{true, true, true, false}
	if len(fh.mcp) != len(want) {
		t.Fatalf("relayed %v", fh.mcp)
	}
	for i, p := range fh.mcp {
		if got, _ := p["guest"].(bool); got != want[i] {
			t.Errorf("%v: guest %v, want %v", p, got, want[i])
		}
	}
}

// A meeting's room (#3862): started under the id the hub gives, never the warm spare, its tools and kind kept in its
// state; every process it runs in has a guest's flags plus only the project's settings sources and the room's own
// --settings, which excludes the CLAUDE.md files of every directory above it and turns auto-memory off. A start under
// an id the node already has is that conversation: nothing is sent again.
func TestMeetingRoomStartsUnderItsID(t *testing.T) {
	fakeClaudeHome(t, "for a in \"$@\"; do printf ' [%s]' \"$a\"; done > \"$HOME/args-$$\"\nread line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	n := codexTestNode(t)
	n.dir = filepath.Join(os.Getenv("HOME"), ".persona")
	if err := os.MkdirAll(n.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	withFakeHub(t, n)
	rec := "0f8fad5b-d9cb-469f-a165-70867728950e"
	out, err := n.assistantGuestPrepare(json.RawMessage(`{"dir":".persona/guest-meeting/` + rec + `","claude_md":"# room","kind":"meeting"}`))
	if err != nil {
		t.Fatal(err)
	}
	prep := out.(map[string]any)
	cwd, _ := prep["cwd"].(string)
	if prep["kind"] != guestMeeting || prep["start_ids"] != true || !strings.HasSuffix(cwd, filepath.Join("guest-meeting", rec)) {
		t.Fatalf("prepare = %v", prep)
	}
	for _, bad := range []string{`{"dir":".persona/guest","kind":"meeting"}`, `{"dir":".persona/guest-meeting/x","kind":"meeting"}`,
		`{"dir":".persona/guest-meeting/` + rec + `","kind":"other"}`} {
		if _, err := n.assistantGuestPrepare(json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}

	room := "7d4a3c1e-1111-4222-8333-444455556666"
	tools := []string{"meeting_lines", "meeting_earlier"}
	ctx := withNewID(withGuestKind(context.Background(), tools, guestMeeting), room)
	id, err := n.assistantSend(ctx, "", cwd, "claude", "the meeting's header", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	if id != room {
		t.Fatalf("started as %s, want %s", id, room)
	}
	if st, _ := n.state.get(id); !slices.Equal(st.Guest, tools) || st.GuestKind != guestMeeting || st.Cwd != cwd {
		t.Fatalf("state = %+v", st)
	}
	if !n.knowsConversation(room) {
		t.Fatal("the node does not know the room it started")
	}
	var line string
	deadline := time.Now().Add(5 * time.Second)
	for line == "" && time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), "args-*"))
		for _, f := range files {
			if raw, _ := os.ReadFile(f); strings.Contains(string(raw), "["+id+"]") {
				line = string(raw)
			}
		}
		if line == "" {
			<-time.After(20 * time.Millisecond) // the process writes its arguments as it starts
		}
	}
	for _, want := range []string{"[--tools] []", "[--strict-mcp-config]", "[--setting-sources] [project]", "[--session-id] [" + room + "]",
		"[--allowedTools] [" + strings.Join(compat.MCPToolNames(tools), ",") + "]"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the room's process has no %s: %s", want, line)
		}
	}
	_, after, _ := strings.Cut(line, "[--settings] [")
	settingsPath, _, _ := strings.Cut(after, "]")
	if filepath.Base(settingsPath) != "assistant-room-"+room+".json" {
		t.Fatalf("the room's own settings are not its --settings: %s", line)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Excludes []string          `json:"claudeMdExcludes"`
		Env      map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	for _, want := range []string{filepath.Join(home, "CLAUDE.md"), filepath.Join(home, ".persona", "CLAUDE.md"),
		filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(filepath.Dir(cwd), "CLAUDE.md")} {
		if !slices.Contains(settings.Excludes, want) {
			t.Errorf("%s is not excluded: %v", want, settings.Excludes)
		}
	}
	if slices.Contains(settings.Excludes, filepath.Join(cwd, "CLAUDE.md")) || settings.Env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] != "1" {
		t.Errorf("settings %s", raw)
	}
	if meetingRoomArgs()[0] != "--setting-sources" {
		t.Fatal("args changed")
	}
}

// assistant.send with new_session_id: an id the node has is that conversation, answered without sending anything;
// one that is not a session id is refused, as is a new id beside a session's.
func TestNewSessionIDStartsOnce(t *testing.T) {
	n := codexTestNode(t)
	room := "7d4a3c1e-1111-4222-8333-444455556666"
	n.state.put(room, sessionState{Launcher: "claude", Guest: []string{"meeting_lines"}, GuestKind: guestMeeting})
	out, err := n.handle(context.Background(), "assistant.send", json.RawMessage(`{"new_session_id":"`+room+`","text":"again","guest":{"tools":["meeting_lines"],"kind":"meeting"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := out.(map[string]any); m["session_id"] != room || m["existed"] != true {
		t.Fatalf("answer %v", m)
	}
	if _, err := n.handle(context.Background(), "assistant.send", json.RawMessage(`{"new_session_id":"not an id","text":"x"}`)); err == nil {
		t.Error("a bad id accepted")
	}
	if _, err := n.handle(context.Background(), "assistant.send", json.RawMessage(`{"session_id":"`+room+`","new_session_id":"`+room+`","text":"x"}`)); err == nil {
		t.Error("a new id beside a session's accepted")
	}
	if _, err := n.handle(context.Background(), "assistant.send", json.RawMessage(`{"text":"x","guest":{"tools":["meeting_lines"],"kind":"other"}}`)); err == nil {
		t.Error("an unknown guest kind accepted")
	}
}

// assistant.guest_delete removes a room's transcript, what Claude Code kept beside it and its directory; it refuses a
// session that is not a meeting's room, and what is gone already is no error.
func TestGuestDeleteRemovesOnlyARoom(t *testing.T) {
	n, home := testNode(t)
	n.dir = filepath.Join(home, ".persona")
	rec := "0f8fad5b-d9cb-469f-a165-70867728950e"
	rel := ".persona/guest-meeting/" + rec
	dir := n.assistantDirOf(rel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	room, other := "7d4a3c1e-1111-4222-8333-444455556666", "8e5b4d2f-2222-4333-8444-555566667777"
	projects := filepath.Join(home, ".claude", "projects")
	roomProject := filepath.Join(projects, claudeProjectName(dir))
	otherProject := filepath.Join(projects, "-home-me-code")
	for _, p := range []string{filepath.Join(roomProject, room+".jsonl"), filepath.Join(roomProject, room, "tool-results", "x.txt"), filepath.Join(otherProject, other+".jsonl")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n.state.put(room, sessionState{Launcher: "claude", Cwd: dir, Guest: []string{"meeting_lines"}, GuestKind: guestMeeting})
	n.state.put(other, sessionState{Launcher: "claude", Cwd: "/home/me/code"})

	if _, err := n.assistantGuestDelete(json.RawMessage(`{"dir":"` + rel + `","session_id":"` + other + `"}`)); err == nil {
		t.Fatal("deleted a conversation that is not the room")
	}
	if _, err := os.Stat(filepath.Join(otherProject, other+".jsonl")); err != nil {
		t.Fatalf("the other conversation's transcript: %v", err)
	}
	if _, err := n.assistantGuestDelete(json.RawMessage(`{"dir":".persona/guest","session_id":"` + room + `"}`)); err == nil {
		t.Fatal("a directory that is not a room's accepted")
	}
	out, err := n.assistantGuestDelete(json.RawMessage(`{"dir":"` + rel + `","session_id":"` + room + `"}`))
	if err != nil || out.(map[string]any)["deleted"] != true {
		t.Fatalf("delete %v %v", out, err)
	}
	for _, gone := range []string{filepath.Join(roomProject, room+".jsonl"), filepath.Join(roomProject, room), dir} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}
	if _, err := n.assistantGuestDelete(json.RawMessage(`{"dir":"` + rel + `","session_id":"` + room + `"}`)); err != nil {
		t.Errorf("deleted again: %v", err)
	}
}

// A room's transcript is an assistant conversation's, also when no process runs it: its projects folder, either
// spelling of the state directory, and its state.
func TestMeetingRoomIsAnAssistantConversation(t *testing.T) {
	rec := "0f8fad5b-d9cb-469f-a165-70867728950e"
	for _, base := range []string{"-home-me--persona-guest-meeting-" + rec, "-home-me-" + nonAlnum.ReplaceAllString(compat.StateDir, "-") + "-guest-meeting-" + rec} {
		if !isAssistantProject(base, "-home-me--persona-assistant") {
			t.Errorf("%s is not an assistant's", base)
		}
	}
	for _, base := range []string{"-home-me--persona-guest", "-home-me-code-guest-meeting-notes", "-home-me--persona-guest-meeting-x"} {
		if isAssistantProject(base, "-home-me--persona-assistant") {
			t.Errorf("%s is an assistant's", base)
		}
	}
	n, _ := testNode(t)
	room := "7d4a3c1e-1111-4222-8333-444455556666"
	n.state.put(room, sessionState{Launcher: "claude", Cwd: "/somewhere/guest-meeting/" + rec, Guest: []string{"meeting_lines"}, GuestKind: guestMeeting})
	if !n.isAssistantSession(room) {
		t.Fatal("a room with no process is not an assistant conversation")
	}
}
