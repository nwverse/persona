package assistant

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The hand-off of secret values to one Agent run (secret_handoff.go). The values are marked strings that must never
// show up in the tool's answer, the cards, what the hub was told or the logs; references must not show up on the card.

const handoffValue = "ghp-HANDOFF-VALUE-0123456789"

// handoffSecretsFake is the provider extensions: references of op:// resolve to marked values.
type handoffSecretsFake struct {
	mu       sync.Mutex
	resolved []string // "reference by approvedBy for purpose"
	rules    []extmgr.HandoffRule
	fail     map[string]string // reference → its error code
}

func (f *handoffSecretsFake) LauncherSecret(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (f *handoffSecretsFake) ForgetLauncherSecrets(string) {}
func (f *handoffSecretsFake) SecretReference(context.Context, string) *extmgr.SecretReferenceView {
	return nil
}
func (f *handoffSecretsFake) SecretProviderOf(ref string) (extension.SecretProvider, bool) {
	if !strings.HasPrefix(ref, "op://") {
		return extension.SecretProvider{}, false
	}
	return extension.SecretProvider{Extension: "example-vault", Contribution: "vault", Name: "Example Vault"}, true
}
func (f *handoffSecretsFake) DescribeSecret(_ context.Context, ref string) (extmgr.SecretDescription, error) {
	return extmgr.SecretDescription{Provider: "example-vault", ProviderName: "Example Vault", Title: "GitHub token", Detail: "credential"}, nil
}
func (f *handoffSecretsFake) ResolveSecret(_ context.Context, ref string, purpose wire.SecretPurpose, approvedBy string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, ref+" by "+approvedBy+" for "+purpose.Kind+"/"+purpose.ID+"@"+purpose.Machine)
	if code := f.fail[ref]; code != "" {
		return "", &extmgr.SecretError{Code: code}
	}
	return handoffValue + "-" + ref[len(ref)-1:], nil
}
func (f *handoffSecretsFake) HandoffRuleCovers(_ context.Context, ref, launcher, machine string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rules {
		if r.Reference == ref && r.Launcher == launcher && r.Machine == machine {
			return true
		}
	}
	return false
}
func (f *handoffSecretsFake) AddHandoffRules(_ context.Context, rules []extmgr.HandoffRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, rules...)
	return nil
}

// handoffDevs are the manager's computers, whose nodes announced secret_env unless told otherwise.
type handoffDevs struct {
	*fleetDevs
	mu      sync.Mutex
	without map[string]bool // nodes that did not announce secret_env
	foreign map[string]bool // computers another manager connects
}

func (d *handoffDevs) Has(id string) bool {
	d.mu.Lock()
	foreign := d.foreign[id]
	d.mu.Unlock()
	return !foreign && d.fleetDevs.Has(id)
}

func (d *handoffDevs) NodeAnnounced(id, capability string) bool {
	if capability != nodeproto.SecretEnv || !d.Has(id) {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.without[id]
}

// handoffRig is a manager whose Agent computer "box" has a node that stages sealed values with a key of its own.
type handoffRig struct {
	t       *testing.T
	s       *Service
	f       *fakeHub
	devs    *handoffDevs
	secrets *handoffSecretsFake
	priv    *ecdh.PrivateKey

	mu     sync.Mutex
	logs   strings.Builder
	staged [][]proto.SecretEnvVar // what each stage opened to
	stages int
}

func newHandoffRig(t *testing.T) *handoffRig {
	t.Helper()
	r := &handoffRig{t: t, f: newFakeHub(), secrets: &handoffSecretsFake{}}
	r.f.sessions["box"], r.f.sessions["server"] = nil, nil
	r.f.devices = []Device{{ID: "server", Name: "Server", Online: true, Reachable: true}, {ID: "box", Name: "Box", Online: true, Reachable: true}}
	r.f.settings = &Settings{Timezone: "Asia/Shanghai"}
	r.f.hello.SecretHandoffGate = true
	r.devs = &handoffDevs{fleetDevs: newFleetDevs(), without: map[string]bool{}, foreign: map[string]bool{}}
	r.devs.add("server", "Server")
	box := r.devs.add("box", "Box")
	var err error
	if r.priv, err = ecdh.P256().GenerateKey(rand.Reader); err != nil {
		t.Fatal(err)
	}
	box.call = r.node
	mem := newFleetMem()
	r.s = withSettings(t, r.f, New(Options{Hub: r.f, Store: func() Store { return mem }, Logf: r.logf, Devices: r.devs}))
	r.s.refs = r.secrets
	return r
}

func (r *handoffRig) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs.WriteString(format + "\n")
	for _, a := range args {
		b, _ := json.Marshal(a)
		r.logs.Write(b)
		r.logs.WriteString("\n")
	}
}

// node answers secret.stage on "box": a check gets its key, a stage is opened with it (as the node does).
func (r *handoffRig) node(method string, params json.RawMessage) (any, error) {
	if method != proto.MethodSecretStage {
		return map[string]any{}, nil
	}
	var p proto.SecretStageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Check {
		return proto.SecretStageResult{Key: base64.StdEncoding.EncodeToString(r.priv.PublicKey().Bytes()), KeyID: "k1"}, nil
	}
	env, err := proto.OpenSecretEnv(r.priv, p.KeyID, p.Sealed)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stages++
	r.staged = append(r.staged, env)
	return proto.SecretStageResult{Ticket: "tk-" + string(rune('0'+r.stages)), ExpiresAt: time.Now().Add(time.Minute)}, nil
}

// start runs agent_start as the main conversation on "server" calls it; its answer comes on the channel.
func (r *handoffRig) start(args string) chan [2]string {
	ctx, cancel := context.WithCancel(context.Background())
	r.t.Cleanup(cancel)
	done := make(chan [2]string, 1)
	go func() {
		res, err := r.s.runTool(ctx, "server", "s1", "agent_start", json.RawMessage(args))
		e := ""
		if err != nil {
			e = err.Error()
		}
		done <- [2]string{res, e}
	}()
	return done
}

func (r *handoffRig) answer(done chan [2]string) (string, string) {
	r.t.Helper()
	select {
	case got := <-done:
		return got[0], got[1]
	case <-time.After(5 * time.Second):
		r.t.Fatal("agent_start did not return")
		return "", ""
	}
}

// card waits for the hand-off card on the queue the apps answer.
func (r *handoffRig) card() proto.Interaction {
	r.t.Helper()
	var ix proto.Interaction
	waitFor(r.t, "the hand-off card on the queue", func() bool {
		qs := r.s.pendingQuestions()
		if len(qs) != 1 {
			return false
		}
		got, ok := r.s.pendingIx(qs[0].ID)
		ix = got
		return ok
	})
	waitAnnounced(r.t, r.f, ix.ID)
	return ix
}

// post answers card id as an app does through the hub, with the device the hub vouched for (nil: none).
func (r *handoffRig) post(id, body string, approver *assistantwire.HandoffApprover) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/interactions/"+id+"/resolve", strings.NewReader(body))
	if approver != nil {
		req = req.WithContext(assistantwire.WithHandoffApprover(req.Context(), *approver))
	}
	w := httptest.NewRecorder()
	r.s.fleetHandler(http.NotFoundHandler()).ServeHTTP(w, req)
	return w
}

// leaks fails when a value shows up anywhere the manager wrote, logged or told.
func (r *handoffRig) leaks(answer string) {
	r.t.Helper()
	r.f.mu.Lock()
	told, _ := json.Marshal(r.f.told)
	r.f.mu.Unlock()
	r.mu.Lock()
	logs := r.logs.String()
	r.mu.Unlock()
	for where, text := range map[string]string{"the answer": answer, "what the hub was told": string(told), "the logs": logs} {
		if strings.Contains(text, "HANDOFF-VALUE") {
			r.t.Fatalf("a value in %s:\n%s", where, text)
		}
	}
}

const handoffArgs = `{"machine":"box","cwd":"/code/app","prompt":"fix CI","secret_env":{"GITHUB_TOKEN":"op://Dev/GitHub/credential1","NPM_TOKEN":"op://Dev/npm/credential2"}}`

// The card names each item as its provider describes it, the variable, the Agent and the computer, never a reference;
// an approval without the device the hub vouched for is refused (the route's 403, a channel's or the assistant's
// resolve); the app's approval resolves the values for that device, seals and stages them on the node, and the start
// carries the ticket alone. "Always" keeps a rule per variable, and the next hand-off they cover asks nothing.
func TestHandoffApprovedOnTheCardStartsWithATicket(t *testing.T) {
	r := newHandoffRig(t)
	done := r.start(handoffArgs)
	ix := r.card()
	if ix.ToolName != proto.SecretHandoffTool || ix.Kind != proto.IxPermission || ix.AlwaysLabel == "" || !strings.Contains(ix.Summary, "iPhone") {
		t.Fatalf("card %+v", ix)
	}
	var in proto.SecretHandoffInput
	if err := json.Unmarshal(ix.ToolInput, &in); err != nil || len(in.Variables) != 2 || in.Variables[0].Name != "GITHUB_TOKEN" ||
		in.Variables[0].Provider != "example-vault" || in.Variables[0].Title != "GitHub token › credential" {
		t.Fatalf("tool input %s", ix.ToolInput)
	}
	card, _ := json.Marshal(ix)
	if strings.Contains(string(card), "op://") || strings.Contains(string(card), "HANDOFF-VALUE") {
		t.Fatalf("the card carries a reference or a value: %s", card)
	}
	// No device vouched for: refused on the route, and from a channel or the assistant.
	if w := r.post(ix.ID, `{"allow":true}`, nil); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), proto.CodeSecretHandoffElsewhere) {
		t.Fatalf("an approval without the hub's word: %d %s", w.Code, w.Body)
	}
	if err := r.s.resolve(ix.ID, proto.Resolution{Allow: new(true)}); !errors.Is(err, errHandoffElsewhere) {
		t.Fatalf("a channel's approval: %v", err)
	}
	if _, ok := r.s.pendingIx(ix.ID); !ok {
		t.Fatal("a refused approval took the card")
	}
	if w := r.post(ix.ID, `{"allow":true,"always":true}`, &assistantwire.HandoffApprover{DeviceID: "d1", DeviceName: "iPhone"}); w.Code != http.StatusOK {
		t.Fatalf("the app's approval: %d %s", w.Code, w.Body)
	}
	answer, errText := r.answer(done)
	if errText != "" || !strings.Contains(answer, "Started on Box") || !strings.Contains(answer, `"status":"granted"`) {
		t.Fatalf("answer %q, error %q", answer, errText)
	}
	r.f.mu.Lock()
	starts := r.f.starts
	r.f.mu.Unlock()
	if len(starts) != 1 || starts[0]["secret_ticket"] != "tk-1" {
		t.Fatalf("starts %v", starts)
	}
	r.mu.Lock()
	staged := r.staged
	r.mu.Unlock()
	if len(staged) != 1 || len(staged[0]) != 2 || staged[0][0].Name != "GITHUB_TOKEN" || string(staged[0][0].Value) != handoffValue+"-1" ||
		staged[0][1].Name != "NPM_TOKEN" || string(staged[0][1].Value) != handoffValue+"-2" {
		t.Fatalf("staged %v", staged)
	}
	r.secrets.mu.Lock()
	resolved, rules := r.secrets.resolved, r.secrets.rules
	r.secrets.mu.Unlock()
	if len(resolved) != 2 || resolved[0] != "op://Dev/GitHub/credential1 by device:d1 for agent_run/claude@box" {
		t.Fatalf("resolved %q", resolved)
	}
	if len(rules) != 2 || rules[0].Launcher != "claude" || rules[0].Machine != "box" || rules[0].By != "device:d1" {
		t.Fatalf("rules %+v", rules)
	}
	r.leaks(answer)

	// Covered by the rules: no card, approved by "rule".
	answer, errText = r.answer(r.start(handoffArgs))
	if errText != "" || !strings.Contains(answer, "Always rule") || len(r.s.pendingQuestions()) != 0 {
		t.Fatalf("answer %q, error %q", answer, errText)
	}
	r.secrets.mu.Lock()
	last := r.secrets.resolved[len(r.secrets.resolved)-1]
	r.secrets.mu.Unlock()
	if !strings.HasSuffix(last, "by rule for agent_run/claude@box") && !strings.Contains(last, "by rule") {
		t.Fatalf("resolved %q", last)
	}
	r.leaks(answer)
}

// A no, an expiry, or a yes no device was vouched for (the hub's manager.action straight to the answer) starts nothing
// and resolves nothing.
func TestHandoffNotApprovedStartsNothing(t *testing.T) {
	for _, c := range []struct {
		name, want string
		answer     func(r *handoffRig, ix proto.Interaction)
	}{
		{"declined", `"status":"declined"`, func(r *handoffRig, ix proto.Interaction) {
			if w := r.post(ix.ID, `{"allow":false}`, nil); w.Code != http.StatusOK {
				r.t.Fatalf("the no: %d %s", w.Code, w.Body)
			}
		}},
		{"a channel's no", `"status":"declined"`, func(r *handoffRig, ix proto.Interaction) {
			if err := r.s.resolve(ix.ID, proto.Resolution{Allow: new(false)}); err != nil {
				r.t.Fatal(err)
			}
		}},
		{"an answer around the hub", `"status":"declined"`, func(r *handoffRig, ix proto.Interaction) {
			if _, err := r.s.Action(context.Background(), Action{Op: "answer", InteractionID: ix.ID, Answer: json.RawMessage(`{"allow":true}`)}); err != nil {
				r.t.Fatal(err)
			}
		}},
		{"expired", `"status":"expired"`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newHandoffRig(t)
			if c.answer == nil {
				old := handoffCardWait
				handoffCardWait = 300 * time.Millisecond
				t.Cleanup(func() { handoffCardWait = old })
			}
			done := r.start(handoffArgs)
			ix := r.card()
			if c.answer != nil {
				c.answer(r, ix)
			}
			answer, errText := r.answer(done)
			if errText != "" || !strings.Contains(answer, c.want) || !strings.Contains(answer, "No agent was started") {
				t.Fatalf("answer %q, error %q", answer, errText)
			}
			r.f.mu.Lock()
			starts := len(r.f.starts)
			r.f.mu.Unlock()
			r.secrets.mu.Lock()
			resolved := len(r.secrets.resolved)
			r.secrets.mu.Unlock()
			if starts != 0 || resolved != 0 {
				t.Fatalf("started %d, resolved %d", starts, resolved)
			}
		})
	}
}

// What cannot take the values is refused before the card: no card, nothing resolved, nothing started.
func TestHandoffRefusedBeforeTheCard(t *testing.T) {
	for _, c := range []struct {
		name, args, want string
		setup            func(r *handoffRig)
	}{
		{"an older hub", handoffArgs, "update the hub", func(r *handoffRig) { r.f.hello.SecretHandoffGate = false }},
		{"a node without secret_env", handoffArgs, "cannot take secrets now", func(r *handoffRig) { r.devs.without["box"] = true }},
		{"a computer of another manager", handoffArgs, "not connected by this manager", func(r *handoffRig) { r.devs.foreign["box"] = true }},
		{"a value instead of a reference", `{"machine":"box","cwd":"/code/app","prompt":"x","secret_env":{"GITHUB_TOKEN":"ghp_plain"}}`, "must be a reference", nil},
		{"a reserved name", `{"machine":"box","cwd":"/code/app","prompt":"x","secret_env":{"PATH":"op://Dev/x/y1"}}`, "secret_env \"PATH\"", nil},
		{"no provider", `{"machine":"box","cwd":"/code/app","prompt":"x","secret_env":{"KEY":"vault9://Dev/x/y1"}}`, "no secret provider", nil},
		{"another agent", `{"machine":"box","cwd":"/code/app","prompt":"x","launcher":"codex","secret_env":{"KEY":"op://Dev/x/y1"}}`, "only to Claude Code", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newHandoffRig(t)
			if c.setup != nil {
				c.setup(r)
			}
			answer, errText := r.answer(r.start(c.args))
			if !strings.Contains(errText, c.want) || !strings.Contains(errText, "no agent was started") {
				t.Fatalf("answer %q, error %q, want %q", answer, errText, c.want)
			}
			r.f.mu.Lock()
			starts, told := len(r.f.starts), len(r.f.told)
			r.f.mu.Unlock()
			_ = told
			if starts != 0 || len(r.s.pendingQuestions()) != 0 || len(r.secrets.resolved) != 0 {
				t.Fatalf("started %d, cards %d, resolved %v", starts, len(r.s.pendingQuestions()), r.secrets.resolved)
			}
		})
	}
}

// A value the provider cannot give after the approval stops the start, naming the variable and the provider's code;
// nothing is staged. A start the computer refuses for ultracode stages again for the start without it: the refused
// start dropped its ticket.
func TestHandoffFailuresAfterTheApproval(t *testing.T) {
	r := newHandoffRig(t)
	r.secrets.rules = []extmgr.HandoffRule{{Reference: "op://Dev/GitHub/credential1", Launcher: "claude", Machine: "box"},
		{Reference: "op://Dev/npm/credential2", Launcher: "claude", Machine: "box"}}
	r.secrets.fail = map[string]string{"op://Dev/npm/credential2": "not_signed_in"}
	answer, errText := r.answer(r.start(handoffArgs))
	if errText != "" || !strings.Contains(answer, `{"name":"NPM_TOKEN","status":"failed","code":"not_signed_in"}`) ||
		!strings.Contains(answer, "No agent was started") {
		t.Fatalf("answer %q, error %q", answer, errText)
	}
	if r.stages != 0 || len(r.f.starts) != 0 {
		t.Fatalf("staged %d, started %d", r.stages, len(r.f.starts))
	}
	r.leaks(answer)

	r.secrets.fail = nil
	r.f.refuseUltra = &rpc.Error{Code: "ultracode_unsupported", Message: "ultracode is for Claude Code agents only"}
	answer, errText = r.answer(r.start(strings.Replace(handoffArgs, `"prompt":"fix CI"`, `"prompt":"fix CI","ultracode":true`, 1)))
	if errText != "" || !strings.Contains(answer, "without ultracode") || !strings.Contains(answer, `"status":"granted"`) {
		t.Fatalf("answer %q, error %q", answer, errText)
	}
	r.f.mu.Lock()
	starts := r.f.starts
	r.f.mu.Unlock()
	if len(starts) != 2 || starts[0]["secret_ticket"] != "tk-1" || starts[1]["secret_ticket"] != "tk-2" || r.stages != 2 {
		t.Fatalf("starts %v, stages %d", starts, r.stages)
	}
	r.leaks(answer)
}
