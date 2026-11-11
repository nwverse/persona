package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
)

// The hand-off of secret values to one Agent run (docs/extensions/secret-providers.md "The hand-off to one Agent run",
// #3962): agent_start's secret_env names environment variables and the references their values come from.
//
//   - Before anything is asked (handoffPrepare): the names and references are checked, the hub must say it holds every
//     answer to the card (Hello.SecretHandoffGate), the computer must be one this manager connects whose node announced
//     secret_env, the launcher must be Claude Code's (a Claude-type managed launcher or plain claude), and each
//     reference's provider must be installed and on. Any of these failing is the tool's error: no card, no start.
//   - The card (handoffApprove): a permission card under the reserved tool name proto.SecretHandoffTool, naming each
//     item as its provider describes it, the variable, the Agent and the computer, never a reference or a value. Only
//     an approval the hub relayed with the device it vouched for counts (resolveApproved); a channel, the assistant, a
//     remembered answer or an older hub can only say no. "Always" keeps an Always rule per reference × launcher ×
//     computer (extmgr secret_handoff_rules.go), and a later hand-off they all cover asks nothing.
//   - After the approval (handoffResolve, handoffValues.stage): the values are resolved (purpose agent_run, approved by
//     the device or "rule"), sealed to a one-time key of the node and staged there over the manager's own connection
//     (secret.stage), and the start carries the ticket alone. They are never in the model's context, the tool's
//     answer, a log or an error, and the manager's copies are zeroed once the start is sent.

// handoffSecrets is what a hand-off asks of the provider extensions (extmgr.Manager).
type handoffSecrets interface {
	SecretProviderOf(reference string) (extension.SecretProvider, bool)
	DescribeSecret(ctx context.Context, reference string) (extmgr.SecretDescription, error)
	ResolveSecret(ctx context.Context, reference string, purpose wire.SecretPurpose, approvedBy string) (string, error)
	HandoffRuleCovers(ctx context.Context, reference, launcher, machine string) bool
	AddHandoffRules(ctx context.Context, rules []extmgr.HandoffRule) error
}

var _ handoffSecrets = (*extmgr.Manager)(nil)

// nodeAnnouncer is the devices' word on what a computer's node announced (devices.Devices).
type nodeAnnouncer interface {
	NodeAnnounced(id, capability string) bool
}

// errHandoffElsewhere: an approval of a hand-off card that did not come from a device the hub vouched for.
var errHandoffElsewhere = i18n.Err("hub-secret_handoff.approve_elsewhere")

// handoffCardWait is how long a hand-off card waits for its answer.
var handoffCardWait = 10 * time.Minute

// The statuses of a variable in agent_start's answer.
const (
	handoffGranted  = "granted"
	handoffDeclined = "declined"
	handoffExpired  = "expired"
	handoffFailed   = "failed"
)

// handoffVar is one variable to hand over.
type handoffVar struct {
	Name, Reference string
	Provider        extension.SecretProvider
	Title           string // the item as its provider describes it; "" when it could not say
}

// secretHandoff is one Agent start's hand-off, checked and ready to ask.
type secretHandoff struct {
	machine, machineName string
	launcher             string
	vars                 []handoffVar
}

// handoffResult is one variable's outcome, for the model: never a value or a reference.
type handoffResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
}

// handoffAnswer is agent_start's answer when the hand-off stopped the start.
func handoffAnswer(results []handoffResult, why string) string {
	b, _ := json.Marshal(results)
	return why + " No agent was started. secret_env: " + string(b)
}

func (s *Service) handoffSecrets() handoffSecrets {
	if h, ok := s.refs.(handoffSecrets); ok {
		return h
	}
	return nil
}

// handoffPrepare checks a start's secret_env before anything is asked: env (variable → reference) to launcher on
// machine, as the start's params have them once its model and computer are settled.
func (s *Service) handoffPrepare(ctx context.Context, machine, machineName string, params map[string]any, env map[string]string) (*secretHandoff, error) {
	if len(env) == 0 || len(env) > proto.SecretEnvMost {
		return nil, fmt.Errorf("secret_env takes 1 to %d variables", proto.SecretEnvMost)
	}
	h := &secretHandoff{machine: machine, machineName: machineName}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if why := compat.SecretEnvNameProblem(name); why != "" {
			return nil, fmt.Errorf("secret_env %q: %s", name, why)
		}
		ref := strings.TrimSpace(env[name])
		if _, ok := extension.ReferenceScheme(ref); !ok {
			return nil, fmt.Errorf("secret_env %s: the value must be a reference such as op://vault/item/field, never a secret itself", name)
		}
		h.vars = append(h.vars, handoffVar{Name: name, Reference: ref})
	}
	if !s.hello(ctx).SecretHandoffGate {
		return nil, errors.New("the hub is older than secret hand-offs (it does not check who approves them): update the hub first")
	}
	nodes, _ := s.devs.(nodeAnnouncer)
	if s.devs == nil || !s.devs.Has(machine) || nodes == nil {
		return nil, fmt.Errorf("%s is not connected by this manager: secrets can be handed only to an Agent on one of its own computers", machineName)
	}
	if !nodes.NodeAnnounced(machine, nodeproto.SecretEnv) {
		return nil, fmt.Errorf("%s cannot take secrets now: its node is offline, older than secret hand-offs, or not resident "+
			"(Persona Link or personad node --attach); on Windows not yet", machineName)
	}
	if rt, _ := params["runtime"].(string); rt != "" {
		return nil, errors.New("secrets can be handed only to Claude Code started by Persona, not to an agent runtime")
	}
	launcher, _ := params["launcher"].(string)
	launcher = s.startLauncher(ctx, launcher)
	if l, managed := s.managedNamed(ctx, launcher); managed && l.AgentType() != "claude" || !managed && launcher != "claude" {
		return nil, fmt.Errorf("secrets can be handed only to Claude Code with a provider Persona manages or plain claude, not %q", launcher)
	}
	h.launcher = launcher
	hs := s.handoffSecrets()
	if hs == nil {
		return nil, errors.New("this manager has no secret providers")
	}
	for i := range h.vars {
		v := &h.vars[i]
		prov, ok := hs.SecretProviderOf(v.Reference)
		if !ok {
			scheme, _ := extension.ReferenceScheme(v.Reference)
			return nil, fmt.Errorf("secret_env %s: no secret provider for %s:// is installed and on", v.Name, scheme)
		}
		v.Provider = prov
		if d, err := hs.DescribeSecret(ctx, v.Reference); err == nil {
			v.Title = clip(oneLine(strings.TrimSpace(strings.Join(handoffWords(d.Title, d.Detail), " › "))), 120)
		}
	}
	return h, nil
}

// handoffApprove has the hand-off approved: by Always rules that cover every variable, else on its card. approvedBy is
// "rule" or "device:<id>"; "" when it was not approved, with the tool's answer saying why.
func (s *Service) handoffApprove(ctx context.Context, convMachine, self string, h *secretHandoff, agent string) (approvedBy, answer string, err error) {
	hs := s.handoffSecrets()
	covered := true
	for _, v := range h.vars {
		if !hs.HandoffRuleCovers(ctx, v.Reference, h.launcher, h.machine) {
			covered = false
			break
		}
	}
	if covered {
		return "rule", "", nil
	}
	l := s.lang(ctx)
	input := proto.SecretHandoffInput{}
	var items, names []string
	for _, v := range h.vars {
		input.Variables = append(input.Variables, proto.SecretHandoffVariable{Name: v.Name, Provider: v.Provider.Extension, Title: v.Title})
		item := v.Provider.Name
		if v.Title != "" {
			item += " › " + v.Title
		}
		items = append(items, i18n.T(l, "assistant-secret_handoff.item", i18n.Args{"item": item, "name": v.Name}))
		names = append(names, v.Name)
	}
	raw, _ := json.Marshal(input)
	now := time.Now().UTC()
	expires := now.Add(handoffCardWait)
	ix := proto.Interaction{ID: "ix_" + randomHex(6), MachineID: convMachine, SessionID: self, SessionTitle: "Persona",
		Kind: proto.IxPermission, ToolName: proto.SecretHandoffTool, ToolInput: raw, ToolTitle: i18n.T(l, "assistant-secret_handoff.title"),
		Summary: i18n.T(l, "assistant-secret_handoff.summary", i18n.Args{"items": strings.Join(items, "; "), "agent": agent,
			"machine": h.machineName}),
		AlwaysLabel: i18n.T(l, "assistant-secret_handoff.always", i18n.Args{"launcher": h.launcher, "machine": h.machineName}),
		CreatedAt:   now, ExpiresAt: expires}
	defer s.forgetHandoffApprover(ix.ID)
	r, err := s.ask(ctx, ix)
	if err != nil {
		return "", "", err
	}
	results := func(status string) []handoffResult {
		out := make([]handoffResult, 0, len(names))
		for _, n := range names {
			out = append(out, handoffResult{Name: n, Status: status})
		}
		return out
	}
	switch {
	case r.Dismiss && !time.Now().Before(expires.Add(-time.Second)):
		return "", handoffAnswer(results(handoffExpired), "The hand-off card expired unanswered."), nil
	case proto.SecretHandoffDeclines(r):
		return "", handoffAnswer(results(handoffDeclined), "The user declined handing over the secrets."), nil
	}
	by, ok := s.handoffApprover(ix.ID)
	if !ok || r.AnsweredBy != "" || r.Remembered { // not an approval the hub vouched for: refused, as on the hub
		return "", handoffAnswer(results(handoffDeclined), "The hand-off was not approved on iPhone or the web."), nil
	}
	approvedBy = "device:" + by.DeviceID
	if r.Always {
		rules := make([]extmgr.HandoffRule, 0, len(h.vars))
		for _, v := range h.vars {
			rules = append(rules, extmgr.HandoffRule{Reference: v.Reference, Launcher: h.launcher, Machine: h.machine, By: approvedBy, At: now})
		}
		if err := hs.AddHandoffRules(context.WithoutCancel(ctx), rules); err != nil {
			s.logf("assistant: hand-off card %s: its Always was not kept: %v", ix.ID, err)
		}
	}
	return approvedBy, "", nil
}

// handoffApprover is the device whose approval claimed hand-off card id (resolveAnswer).
func (s *Service) handoffApprover(id string) (assistantwire.HandoffApprover, bool) {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	a, ok := s.fleet.handoffBy[id]
	return a, ok && a.DeviceID != ""
}

func (s *Service) forgetHandoffApprover(id string) {
	s.fleet.mu.Lock()
	delete(s.fleet.handoffBy, id)
	s.fleet.mu.Unlock()
}

// handoffValues are a hand-off's resolved values, in memory until the start is sent: stage seals them for one start,
// wipe zeroes them.
type handoffValues struct {
	s       *Service
	machine string
	env     []proto.SecretEnvVar
}

// handoffResolve resolves h's values, approved by approvedBy. A variable that fails stops the start: the answer says
// which, with its provider's code.
func (s *Service) handoffResolve(ctx context.Context, h *secretHandoff, approvedBy string) (*handoffValues, string) {
	hs := s.handoffSecrets()
	out := &handoffValues{s: s, machine: h.machine}
	results := make([]handoffResult, 0, len(h.vars))
	failed := false
	for _, v := range h.vars {
		val, err := hs.ResolveSecret(ctx, v.Reference, wire.SecretPurpose{Kind: "agent_run", ID: h.launcher, Machine: h.machine}, approvedBy)
		code := ""
		switch {
		case err != nil:
			code = "failed"
			var se *extmgr.SecretError
			if errors.As(err, &se) {
				code = se.Code
			}
		case len(val) < proto.SecretEnvValueLeast:
			code = "too_short" // the node hides only values it can tell apart from text
		case len(val) > proto.SecretEnvValueMost:
			code = "too_long"
		case !utf8.ValidString(val) || strings.ContainsRune(val, 0):
			code = "not_text"
		}
		if code != "" {
			failed = true
			results = append(results, handoffResult{Name: v.Name, Status: handoffFailed, Code: code})
			continue
		}
		out.env = append(out.env, proto.SecretEnvVar{Name: v.Name, Value: []byte(val)})
		results = append(results, handoffResult{Name: v.Name, Status: handoffGranted})
	}
	if failed {
		out.wipe()
		return nil, handoffAnswer(results, "A secret could not be read from its provider after the approval.")
	}
	return out, ""
}

// stage seals the values to a one-time key of the computer's node and stages them there, over the manager's own
// connection: the ticket a start carries.
func (v *handoffValues) stage(ctx context.Context) (string, error) {
	var key proto.SecretStageResult
	if err := v.s.nodeCall(ctx, v.machine, proto.MethodSecretStage, proto.SecretStageParams{Check: true}, &key); err != nil {
		return "", err
	}
	sealed, err := proto.SealSecretEnv(key.Key, key.KeyID, v.env)
	if err != nil {
		return "", err
	}
	var staged proto.SecretStageResult
	if err := v.s.nodeCall(ctx, v.machine, proto.MethodSecretStage, proto.SecretStageParams{KeyID: key.KeyID, Sealed: sealed}, &staged); err != nil {
		return "", err
	}
	if staged.Ticket == "" {
		return "", errors.New("the node gave no ticket")
	}
	return staged.Ticket, nil
}

// wipe zeroes the values.
func (v *handoffValues) wipe() {
	if v == nil {
		return
	}
	for i := range v.env {
		clear(v.env[i].Value)
	}
	v.env = nil
}

// granted is the answer's line for a start that took the values.
func (h *secretHandoff) granted(approvedBy string) string {
	results := make([]handoffResult, 0, len(h.vars))
	for _, v := range h.vars {
		results = append(results, handoffResult{Name: v.Name, Status: handoffGranted})
	}
	b, _ := json.Marshal(results)
	by := "approved on the user's device"
	if approvedBy == "rule" {
		by = "by the user's Always rule"
	}
	return fmt.Sprintf(" Its environment has the secrets for this run only (%s); never print or log them. secret_env: %s", by, b)
}

// handoffWords are the parts that say something.
func handoffWords(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
