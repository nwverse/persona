package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connectorwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// historyToolService is an assistant whose hub offers the connector "chat" with history and the given chats.
func historyToolService(t *testing.T, cfg *assistantwire.KBHistory, chats []connectorwire.HistoryChat) (*Service, *fakeHub, string) {
	t.Helper()
	f := newFakeHub()
	f.hello.KB = &KBSettings{History: cfg}
	f.answerApp(AppConnectors, assistantwire.ConnectorsArchives, func(json.RawMessage) (any, error) {
		return assistantwire.Archives{Archives: []assistantwire.ArchiveSource{{ID: "chat", History: true}}}, nil
	})
	f.answerApp(AppConnectors, assistantwire.ConnectorsHistoryChats, func(json.RawMessage) (any, error) {
		return assistantwire.HistoryChatsResult{Chats: chats}, nil
	})
	dir := t.TempDir()
	s := kbServiceOn(t, f, newKBStore(), dir, nil)
	return s, f, kbEng(t, s).Store.Dir
}

func runHistoryTool(t *testing.T, s *Service, name, args string) string {
	t.Helper()
	res := s.Tool(context.Background(), ToolCall{Name: name, Arguments: json.RawMessage(args)})
	if res.IsError {
		t.Fatalf("%s %s: %+v", name, args, res)
	}
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

// The tools: listing makes the plan (nothing approved, nothing run), set changes modes, resume approves and clears a
// stop (slow keeps the slowest pace two hours), stop writes STOP, status says where it is.
func TestHistoryTools(t *testing.T) {
	f := false
	cfg := &assistantwire.KBHistory{Enabled: true, Sources: []assistantwire.KBHistorySource{{Name: "chat", NeedsSearchIndex: &f}}}
	chats := []connectorwire.HistoryChat{{ID: "d1", Name: "Direct One", Kind: "dm", Estimate: 1200}, {ID: "g1", Name: "Group One", Kind: "group", Estimate: 90000},
		{ID: "m1", Name: "Muted One", Kind: "group", Muted: true, Estimate: 500}}
	s, fh, data := historyToolService(t, cfg, chats)
	l := s.lang(context.Background())

	out := runHistoryTool(t, s, "kb_history_plan", `{}`)
	for _, want := range []string{"Direct One", "Group One", "Muted One", "d1", i18n.T(l, "assistant-kb_history.plan_not_approved"),
		i18n.T(l, "assistant-kb_history.group_full", i18n.Args{"count": 1, "messages": 1200})} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan lacks %q:\n%s", want, out)
		}
	}
	p, ok, err := loadHistoryPlan(data, "chat")
	if err != nil || !ok || p.ApprovedAt != nil || len(p.Chats) != 3 {
		t.Fatalf("plan %+v %v %v", p, ok, err)
	}
	if n := len(fh.appParams(AppConnectors, assistantwire.ConnectorsHistory)); n != 0 {
		t.Fatalf("listing read %d pages", n)
	}

	out = runHistoryTool(t, s, "kb_history_plan", `{"source":"chat","set":[{"id":"m1","mode":"full"},{"id":"g1","mode":"off"},{"id":"nope","mode":"full"}]}`)
	if !strings.Contains(out, "nope") {
		t.Fatalf("set: %s", out)
	}
	p, _, _ = loadHistoryPlan(data, "chat")
	if p.chat("m1").effective() != "full" || p.chat("g1").effective() != "off" {
		t.Fatalf("modes %+v", p.Chats)
	}
	runHistoryTool(t, s, "kb_history_plan", `{"source":"chat","set":[{"id":"g1","mode":null}]}`)
	p, _, _ = loadHistoryPlan(data, "chat")
	if p.chat("g1").Mode != nil {
		t.Fatalf("back to the default: %+v", p.chat("g1"))
	}

	out = runHistoryTool(t, s, "kb_history_stop", `{"reason":"checking the group Secret Name"}`)
	if st, stopped := readHistoryStop(data); !stopped || st.By != "tool" || st.Code != "asked" || st.Text != "checking the group Secret Name" || out != i18n.T(l, "assistant-kb_history.stop_done") {
		t.Fatalf("stop: %q %+v", out, st)
	}
	// The owner's words stay out of the log: they may name a chat or a person.
	if logs := readAllLogs(t, data); strings.Contains(logs, "Secret") || !strings.Contains(logs, `"event":"stop"`) {
		t.Fatalf("log %s", logs)
	}
	if out := runHistoryTool(t, s, "kb_history_stop", `{}`); !strings.Contains(out, "checking the group") {
		t.Fatalf("stop again: %s", out)
	}
	// Stopped: listing does not ask the source.
	before := len(fh.appParams(AppConnectors, assistantwire.ConnectorsHistoryChats))
	out = runHistoryTool(t, s, "kb_history_plan", `{"source":"chat"}`)
	if len(fh.appParams(AppConnectors, assistantwire.ConnectorsHistoryChats)) != before || !strings.Contains(out, i18n.T(l, "assistant-kb_history.refresh_stopped")) {
		t.Fatalf("listed while stopped: %s", out)
	}

	out = runHistoryTool(t, s, "kb_history_resume", `{"source":"chat","slow":true}`)
	if out != i18n.T(l, "assistant-kb_history.resume_done_slow", i18n.Args{"source": "chat"}) {
		t.Fatalf("resume: %s", out)
	}
	if _, stopped := readHistoryStop(data); stopped {
		t.Fatal("STOP stayed")
	}
	p, _, _ = loadHistoryPlan(data, "chat")
	if p.ApprovedAt == nil {
		t.Fatal("not approved")
	}
	// The two slow hours start with the first call, not with the approval.
	if st := readHistoryStatus(data)["chat"]; !st.SlowPending || !st.SlowUntil.IsZero() {
		t.Fatalf("slow start %+v", st)
	}
	if out := runHistoryTool(t, s, "kb_history_status", `{}`); !strings.Contains(out, i18n.T(l, "assistant-kb_history.status_slow_pending")) {
		t.Fatalf("status of a slow start:\n%s", out)
	}

	out = runHistoryTool(t, s, "kb_history_status", `{}`)
	for _, want := range []string{"chat", i18n.T(l, "assistant-kb_history.plan_approved", i18n.Args{"at": p.ApprovedAt.In(s.location(context.Background())).Format("2006-01-02 15:04")})} {
		if !strings.Contains(out, want) {
			t.Fatalf("status lacks %q:\n%s", want, out)
		}
	}

	// A refresh that adds chats to import takes the approval back.
	fh.answerApp(AppConnectors, assistantwire.ConnectorsHistoryChats, func(json.RawMessage) (any, error) {
		return assistantwire.HistoryChatsResult{Chats: append(chats, connectorwire.HistoryChat{ID: "d2", Name: "Direct Two", Kind: "dm"})}, nil
	})
	out = runHistoryTool(t, s, "kb_history_plan", `{"source":"chat"}`)
	p, _, _ = loadHistoryPlan(data, "chat")
	if p.ApprovedAt != nil || !strings.Contains(out, i18n.T(l, "assistant-kb_history.plan_changed", i18n.Args{"count": 1})) {
		t.Fatalf("approval after new chats: %s", out)
	}

	// A list call that times out stops the whole import like the driver's own calls: STOP, a push, and the next
	// listing asks nothing.
	fh.answerApp(AppConnectors, assistantwire.ConnectorsHistoryChats, func(json.RawMessage) (any, error) {
		return nil, &rpc.Error{Code: "UPSTREAM_TIMEOUT", Message: "slow"}
	})
	out = runHistoryTool(t, s, "kb_history_plan", `{"source":"chat"}`)
	stop, stopped := readHistoryStop(data)
	if !stopped || stop.Code != "call_timeout" || stop.Args["method"] != "history_chats" || !strings.Contains(out, i18n.T(l, "assistant-kb_history.reason_call_timeout", i18n.Args{"method": "history_chats", "code": "upstream_timeout"})) {
		t.Fatalf("a timed-out list: %+v %v\n%s", stop, stopped, out)
	}
	before = len(fh.appParams(AppConnectors, assistantwire.ConnectorsHistoryChats))
	runHistoryTool(t, s, "kb_history_plan", `{"source":"chat"}`)
	if len(fh.appParams(AppConnectors, assistantwire.ConnectorsHistoryChats)) != before {
		t.Fatal("listed again after the list timed out")
	}

	// Unknown source.
	if out := runHistoryTool(t, s, "kb_history_resume", `{"source":"other"}`); !strings.Contains(out, "other") {
		t.Fatalf("unknown source: %s", out)
	}
}

// Enabled without sources: the status says what is missing, not that it is off.
func TestHistoryStatusWithoutSources(t *testing.T) {
	s, _, _ := historyToolService(t, &assistantwire.KBHistory{Enabled: true}, nil)
	l := s.lang(context.Background())
	if out := runHistoryTool(t, s, "kb_history_status", `{}`); out != i18n.T(l, "assistant-kb_history.no_sources") {
		t.Fatalf("status: %s", out)
	}
}

// With kb.history off, the tools say so and ask no connector anything.
func TestHistoryToolsOff(t *testing.T) {
	s, fh, data := historyToolService(t, nil, []connectorwire.HistoryChat{{ID: "d1", Kind: "dm"}})
	l := s.lang(context.Background())
	for _, call := range [][2]string{{"kb_history_plan", `{}`}, {"kb_history_resume", `{"source":"chat"}`}, {"kb_history_status", `{}`}} {
		if out := runHistoryTool(t, s, call[0], call[1]); out != i18n.T(l, "assistant-kb_history.off") {
			t.Fatalf("%s: %s", call[0], out)
		}
	}
	if n := len(fh.appParams(AppConnectors, assistantwire.ConnectorsHistoryChats)) + len(fh.appParams(AppConnectors, assistantwire.ConnectorsArchives)); n != 0 {
		t.Fatalf("%d connector calls while off", n)
	}
	if _, ok, _ := loadHistoryPlan(data, "chat"); ok {
		t.Fatal("a plan was made while off")
	}
	// Stopping always works: it only writes STOP.
	if out := runHistoryTool(t, s, "kb_history_stop", `{}`); out != i18n.T(l, "assistant-kb_history.stop_done") {
		t.Fatalf("stop: %s", out)
	}
}

// kb.history in manager.json: off unless enabled, sources in the order written, profiles and overrides read.
func TestHistoryConfigFromManagerJSON(t *testing.T) {
	var c ownConfig
	raw := `{"kb":{"history":{"enabled":true,"sources":{"zeta":{"profile":"standard","needs_search_index":false,"interval_seconds":[4,9]},"alpha":{"page_limit":5,"quiet_hours":2}}}}}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	h := c.over(Hello{}).KB.History
	if !h.On() || len(h.Sources) != 2 || h.Sources[0].Name != "zeta" || h.Sources[1].Name != "alpha" {
		t.Fatalf("history %+v", h)
	}
	z, a := historyPaceFor(h.Sources[0]), historyPaceFor(h.Sources[1])
	if z.Profile != "standard" || z.Interval != (assistantwire.KBRange{4, 9}) || h.Sources[0].SearchIndexNeeded() {
		t.Fatalf("zeta %+v", z)
	}
	if a.Profile != "careful" || a.PageLimit != 5 || a.QuietHours != (assistantwire.KBRange{2, 2}) || !h.Sources[1].SearchIndexNeeded() {
		t.Fatalf("alpha %+v", a)
	}
	back, err := json.Marshal(h)
	if err != nil || !strings.HasPrefix(string(back), `{"enabled":true,"sources":{"zeta":`) {
		t.Fatalf("written back %s %v", back, err)
	}
	for _, off := range []string{`{"kb":{}}`, `{"kb":{"history":{"sources":{"x":{}}}}}`} {
		var c ownConfig
		if err := json.Unmarshal([]byte(off), &c); err != nil {
			t.Fatal(err)
		}
		if c.over(Hello{}).KB.History.On() {
			t.Fatalf("%s is on", off)
		}
	}
	for _, bad := range []string{`{"batch_calls":[9,3]}`, `{"interval_seconds":-5}`, `{"rest_minutes":[-1,3]}`, `{"slow_factor":-2}`} {
		if err := json.Unmarshal([]byte(`{"kb":{"history":{"sources":{"x":`+bad+`}}}}`), &c); err == nil {
			t.Fatalf("%s was taken", bad)
		}
	}
	// slow_factor stretches every interval and rest.
	var two assistantwire.KBHistory
	if err := json.Unmarshal([]byte(`{"sources":{"x":{"profile":"standard","slow_factor":2}}}`), &two); err != nil {
		t.Fatal(err)
	}
	if p := historyPaceFor(two.Sources[0]); p.Interval != (assistantwire.KBRange{6, 16}) || p.Rest != (assistantwire.KBRange{4, 20}) {
		t.Fatalf("slow factor %+v", p)
	}
}
