package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// runDevs answers model.run as a node would, or with err.
type runDevs struct {
	viaDevs
	saw   []proto.ModelRunParams
	usage ai.Usage
}

func (d *runDevs) CallNode(ctx context.Context, id, method string, params json.RawMessage) (json.RawMessage, error) {
	if method != proto.MethodModelRun || id != "server" {
		return nil, fmt.Errorf("unexpected %s on %s", method, id)
	}
	var p proto.ModelRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	d.saw = append(d.saw, p)
	if d.err != nil {
		return nil, d.err
	}
	return json.Marshal(proto.ModelRunAnswer{Text: "answer to " + p.Prompt, Model: "m-run", Usage: d.usage})
}

// An API purpose may pick plain claude, codex, or agy (no gateway, no key): Model configuration offers them with the
// main chat's computer, and requests run there as model.run with the system prompt, the turns and the chosen model;
// "off" leaves the effort to the launcher. Offline, logged out and old nodes say so; with no main chat computer the
// status says to choose one; unsupported CLIs (such as omp) are not offered, and a launcher since deleted is an error,
// not a run.
func TestAPIPurposeRunsOnALauncher(t *testing.T) {
	s, f, _ := modelService(t)
	devs := &runDevs{}
	s.devs = devs

	p := purposeOf(t, s, "kb")
	if p.MachineID != "server" || !slices.Contains(p.ProviderIDs, "launcher.claude") {
		t.Fatalf("kb offers %v on %q", p.ProviderIDs, p.MachineID)
	}
	if r := modelRequest(s, "PATCH", "kb", `{"revision":0,"provider":"launcher.claude","model":"bad model"}`); r.Code != 400 {
		t.Fatalf("bad model id: %d %s", r.Code, r.Body.String())
	}
	if r := modelRequest(s, "PATCH", "kb", `{"revision":0,"provider":"launcher.claude","model":"m-1"}`); r.Code != 200 {
		t.Fatalf("pick claude: %d %s", r.Code, r.Body.String())
	}
	if p := purposeOf(t, s, "kb"); p.Status != modelUnverified {
		t.Fatalf("status: %q", p.Status)
	}
	text, err := s.purposeModel("kb.compress").Complete(context.Background(), "sys", "hi", 64)
	if err != nil || text != "answer to hi" || len(devs.saw) != 1 {
		t.Fatalf("complete: %q %v %d", text, err, len(devs.saw))
	}
	if got := devs.saw[0]; got.Launcher != "claude" || got.Model != "m-1" || got.System != "sys" || got.TimeoutMS <= 0 {
		t.Fatalf("params: %+v", got)
	}
	reply, err := s.purposeModel("kb").Chat(context.Background(), ai.Request{Messages: []ai.Message{
		{Role: ai.RoleUser, Content: "first"}, {Role: ai.RoleAssistant, Content: "reply"}, {Role: ai.RoleUser, Content: "second"}}})
	if err != nil || reply.Model != "m-run" {
		t.Fatalf("chat: %+v %v", reply, err)
	}
	if in := devs.saw[1].Prompt; !strings.Contains(in, "User:\nfirst") || !strings.Contains(in, "Assistant:\nreply") || !strings.HasSuffix(strings.TrimSpace(strings.Split(in, "second")[0]), "User:") {
		t.Fatalf("transcript: %q", in)
	}

	// Triage defaults to "off", an API word: the launcher's own effort.
	if r := modelRequest(s, "PATCH", "inbox.triage", `{"revision":0,"provider":"launcher.claude"}`); r.Code != 200 {
		t.Fatalf("triage: %d %s", r.Code, r.Body.String())
	}
	if _, err := s.purposeModel("inbox.judge").Complete(context.Background(), "", "x", 16); err != nil || devs.saw[2].Effort != "" {
		t.Fatalf("triage run: %v %+v", err, devs.saw[2])
	}

	lang := s.lang(context.Background())
	args := i18n.Args{"name": "server", "launcher": "claude"}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("server: %w", apierr.ErrOffline), i18n.T(lang, "assistant-models.err_run_offline", args)},
		{&rpc.Error{Code: proto.CodeModelRunLoggedOut, Message: "x"}, i18n.T(lang, "assistant-models.err_run_logged_out", args)},
		{&rpc.Error{Code: proto.CodeModelRunTimeout, Message: "x"}, i18n.T(lang, "assistant-models.err_run_timeout", args)},
		{&rpc.Error{Code: proto.CodeNodeOutdated, Message: "Update the node"}, "Update the node"},
	} {
		devs.err = tc.err
		if _, err := s.purposeModel("kb").Complete(context.Background(), "", "x", 16); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v, want %q", tc.err, err, tc.want)
		}
	}

	// Codex and Antigravity launchers are offered and callable via model.run (#12027); unsupported CLIs (such as omp)
	// and deleted launchers remain rejected.
	devs.err = nil
	putLaunchers(t, s,
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "anti", Agent: "agy", API: "responses"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "cx", Agent: "codex", API: "responses"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "myomp", Agent: "omp", API: "responses"}},
	)
	p = purposeOf(t, s, "kb")
	for _, wantID := range []string{"launcher.claude", "launcher.anti", "launcher.cx"} {
		if !slices.Contains(p.ProviderIDs, wantID) {
			t.Fatalf("%s not offered: %v", wantID, p.ProviderIDs)
		}
	}
	if slices.Contains(p.ProviderIDs, "launcher.myomp") {
		t.Fatalf("unsupported omp launcher offered: %v", p.ProviderIDs)
	}
	for _, tc := range []struct {
		name, model, effort string
	}{
		{name: "cx", model: "gpt-6.1-terra", effort: "high"},
		{name: "anti", model: "gemini-4-hydrogen-low", effort: "low"},
	} {
		cur := purposeOf(t, s, "kb")
		body := fmt.Sprintf(`{"revision":%d,"provider":"launcher.%s","model":%q,"effort":%q}`, cur.Revision, tc.name, tc.model, tc.effort)
		if r := modelRequest(s, "PATCH", "kb", body); r.Code != 200 {
			t.Fatalf("pick %s: %d %s", tc.name, r.Code, r.Body.String())
		}
		before := len(devs.saw)
		text, err := s.purposeModel("kb").Complete(context.Background(), "sys-"+tc.name, "hi-"+tc.name, 64)
		if err != nil || text != "answer to hi-"+tc.name || len(devs.saw) != before+1 {
			t.Fatalf("%s complete: %q %v", tc.name, text, err)
		}
		got := devs.saw[len(devs.saw)-1]
		if got.Launcher != tc.name || got.Model != tc.model || got.Effort != tc.effort || got.System != "sys-"+tc.name {
			t.Fatalf("%s params: %+v", tc.name, got)
		}
	}
	// Restore kb back to launcher.claude for the no-machine status check below.
	cur := purposeOf(t, s, "kb")
	if r := modelRequest(s, "PATCH", "kb", fmt.Sprintf(`{"revision":%d,"provider":"launcher.claude","model":"m-1"}`, cur.Revision)); r.Code != 200 {
		t.Fatalf("restore claude: %d %s", r.Code, r.Body.String())
	}
	for _, name := range []string{"myomp", "gone"} {
		if _, err := s.apiConfig(context.Background(), ModelBinding{Provider: "launcher." + name}); err == nil {
			t.Fatalf("%s runs", name)
		}
	}

	// No main chat computer (the only one is a work computer): still offered, and the status names the setting.
	f.hello.Assistant.WorkMachines = []string{"server"}
	s.settingsChanged(context.Background())
	if p := purposeOf(t, s, "kb"); !slices.Contains(p.ProviderIDs, "launcher.claude") || p.Status != i18n.T(lang, "assistant-models.err_run_no_machine") {
		t.Fatalf("no machine: %v %q", p.ProviderIDs, p.Status)
	}
}

func TestInboxGroupUsageReportsBothRoutes(t *testing.T) {
	s, hub, _ := modelService(t)
	ctx := context.Background()
	var logs logBuffer
	s.logf = logs.logf
	usage := ai.Usage{InputTokens: 14, OutputTokens: 7, CacheReadInputTokens: 95, CacheCreationInputTokens: 83}
	s.devs = &runDevs{usage: usage}

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "relay-model", "usage": usage,
			"content": []map[string]string{{"type": "text", "text": "relay reply"}},
		})
	}))
	defer relay.Close()
	hub.hello.Models.AI = ai.Config{BaseURL: relay.URL, APIKey: "private-test-key", Model: "relay-model"}

	call := func(want string) {
		t.Helper()
		text, err := s.purposeModelFor(purposeTriage, "inbox_group").Complete(ctx, "", "private-test-prompt", 64)
		if err != nil || text != want {
			t.Fatalf("reply: %q %v, want %q", text, err, want)
		}
	}
	call("relay reply")
	if r := modelRequest(s, "PATCH", "inbox.triage", `{"revision":0,"provider":"launcher.claude"}`); r.Code != http.StatusOK {
		t.Fatalf("pick claude: %d %s", r.Code, r.Body.String())
	}
	call("answer to private-test-prompt")

	got := logs.String()
	if strings.Count(got, "model_usage ") != 2 || strings.Contains(got, "private-test-") {
		t.Fatalf("usage log: %s", got)
	}
	const fields = `result=ok usage_reported=true input_tokens=14 output_tokens=7 cache_read_input_tokens=95 cache_creation_input_tokens=83`
	for _, want := range []string{
		`model_usage purpose="inbox.triage" operation="inbox_group" route=api provider="legacy.triage" model="relay-model" ` + fields,
		`model_usage purpose="inbox.triage" operation="inbox_group" route=cli provider="launcher.claude" model="m-run" ` + fields,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// An unconfigured API purpose uses the neutral no-model explanation (listing Claude Code, Codex, and Antigravity
// launchers as explicit choices alongside keyed API connections) in each language, even when Codex is installed.
func TestCodexOnlyAPIPurposeStatusExplainsTheLimitation(t *testing.T) {
	s, _, _ := modelService(t)
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "codex", Agent: "codex", API: "responses"}})
	for _, lang := range []i18n.Lang{i18n.Default, i18n.En, i18n.Lang("ja")} {
		ctx := i18n.With(context.Background(), lang)
		p, err := s.modelPurpose(ctx, "inbox.judge", s.modelProviders(ctx))
		if err != nil {
			t.Fatal(err)
		}
		want := mc(lang, modelNoAPIStatus)
		if p.Status != want || !strings.Contains(p.Status, "Codex") || !strings.Contains(p.Status, "Claude Code") || !strings.Contains(p.Status, "Antigravity") {
			t.Errorf("%s: status %q, want %q naming Claude Code, Codex, and Antigravity", lang, p.Status, want)
		}
	}
}

// noAPIStatus does not trigger an online node refresh.
func TestNoAPIStatusUsesStoredNodeLauncherSnapshot(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	if err := s.store().PutSetting(ctx, launchersZshrcKey, map[string][]proto.Launcher{
		"server": {{Name: "cx", Source: "zshrc", Agent: "codex"}},
	}); err != nil {
		t.Fatal(err)
	}
	before := len(f.calls)
	if got := s.noAPIStatus(ctx); got != modelNoAPIStatus {
		t.Fatalf("status key %q, want %q", got, modelNoAPIStatus)
	}
	for _, call := range f.calls[before:] {
		if call.Method == DeviceLaunchersList {
			t.Fatalf("status selection refreshed launchers on %s", call.Device)
		}
	}
}

// Without a model the done judge leaves a visible waiting reason on the task instead of only logging it.
func TestUnconfiguredDoneJudgeLeavesTheReasonOnTheTask(t *testing.T) {
	s, f, _ := modelService(t)
	f.hello.Locale = "en"
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "codex", Agent: "codex", API: "responses"}})
	ctx := i18n.With(context.Background(), i18n.En)
	todo, err := s.createTodo(ctx, "Check the result", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	_, judgeErr := s.purposeModel("inbox.judge").Complete(ctx, "", "reply", 16)
	if !ai.IsNotConfigured(judgeErr) {
		t.Fatalf("judge error: %v", judgeErr)
	}
	s.applyTaskDone(ctx, todo, todo.CreatedAt, false, taskDoneCheck{}, judgeErr)
	got := todoNow(s, todo.ID)
	want := mc(i18n.En, modelNoAPIStatus)
	if got.DoneAt != nil || got.Check == nil || got.Check.Verdict != verdictWaiting || got.Check.Reason != want {
		t.Fatalf("task: done %v, check %+v, want waiting with %q", got.DoneAt, got.Check, want)
	}
}

// An install without Codex gets the same no-model explanation naming all supported launcher choices.
func TestNoAPIStatusDoesNotBlameCodexWithoutIt(t *testing.T) {
	s, _, _ := modelService(t)
	for _, lang := range []i18n.Lang{i18n.Default, i18n.En, i18n.Lang("ja")} {
		ctx := i18n.With(context.Background(), lang)
		p, err := s.modelPurpose(ctx, "inbox.judge", s.modelProviders(ctx))
		if err != nil {
			t.Fatal(err)
		}
		if want := mc(lang, modelNoAPIStatus); p.Status != want {
			t.Errorf("%s: status %q, want %q", lang, p.Status, want)
		}
	}
}

// The task reason uses the neutral no-model explanation when no model is configured.
func TestUnconfiguredDoneJudgeUsesNeutralReasonWithoutCodex(t *testing.T) {
	s, f, _ := modelService(t)
	f.hello.Locale = "en"
	ctx := i18n.With(context.Background(), i18n.En)
	todo, err := s.createTodo(ctx, "Check the result", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	_, judgeErr := s.purposeModel("inbox.judge").Complete(ctx, "", "reply", 16)
	if !ai.IsNotConfigured(judgeErr) {
		t.Fatalf("judge error: %v", judgeErr)
	}
	s.applyTaskDone(ctx, todo, todo.CreatedAt, false, taskDoneCheck{}, judgeErr)
	got := todoNow(s, todo.ID)
	if got.Check == nil || got.Check.Verdict != verdictWaiting || got.Check.Reason != mc(i18n.En, modelNoAPIStatus) {
		t.Fatalf("task check: %+v", got.Check)
	}
}

// The done judge does not refresh nodes just to explain an unavailable model.
func TestUnconfiguredDoneJudgeDoesNotRefreshLaunchers(t *testing.T) {
	s, f, _ := modelService(t)
	f.hello.Locale = "en"
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "codex", Agent: "codex", API: "responses"}})
	ctx := i18n.With(context.Background(), i18n.En)
	todo, err := s.createTodo(ctx, "Check the result", "", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	_, judgeErr := s.purposeModel("inbox.judge").Complete(ctx, "", "reply", 16)
	if !ai.IsNotConfigured(judgeErr) {
		t.Fatalf("judge error: %v", judgeErr)
	}
	before := len(f.calls)
	s.applyTaskDone(ctx, todo, todo.CreatedAt, false, taskDoneCheck{}, judgeErr)
	for _, call := range f.calls[before:] {
		if call.Method == DeviceLaunchersList {
			t.Fatalf("done judge refreshed launchers on %s just to choose its reason", call.Device)
		}
	}
	if got := todoNow(s, todo.ID); got.Check == nil || got.Check.Reason != mc(i18n.En, modelNoAPIStatus) {
		t.Fatalf("task check: %+v", got.Check)
	}
}

// Several turns go to the CLI as one transcript (tools/launcher_run_input).
func TestGoldenLauncherRunInput(t *testing.T) {
	golden(t, "launcher_run_input", runPrompt([]ai.Message{{Role: ai.RoleUser, Content: "first"}, {Role: ai.RoleAssistant, Content: "reply"}, {Role: ai.RoleUser, Content: "second"}}))
}
