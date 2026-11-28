package extmgr

// The assistant's cloud_hosts tools (docs/design/machine-cloud-ops.md, "The assistant's tools"): cloud_hosts reads the
// hosts and the computers bound to them, cloud_host_action acts on the instance a computer is bound to. Both go
// through the functions the routes use (cloudHosts, cloudMachineView, cloudAct, cloudSnapshots), so the gate, the
// re-checks and the audit are the routes' own. A confirm or destructive action first puts an approval card on the
// user's phone; only once it is allowed is the action asked again with confirm (and, when destructive, the bound
// instance's id as acknowledge) filled in, as actor assistant. The assistant cannot bind or unbind.
// Tool diagnostics are model instructions; the card's summary is the user's and goes through i18n.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	toolCloudHosts      = "cloud_hosts"
	toolCloudHostAction = "cloud_host_action"
	// cloudToolMost bounds a tool's answer (every instance of every provider at most).
	cloudToolMost = 60000
)

// cloudTools are offered only while some cloud_hosts provider is enabled and switched on.
func (h *Manager) cloudTools() []proto.Tool {
	if len(h.cloudProvidersOn()) == 0 {
		return nil
	}
	return []proto.Tool{mcpTool(toolCloudHosts), mcpTool(toolCloudHostAction)}
}

func isCloudTool(name string) bool { return name == toolCloudHosts || name == toolCloudHostAction }

// cloudToolHost is a host with the name of the computer bound to it.
type cloudToolHost struct {
	wire.CloudHost
	Machine string `json:"machine,omitempty"`
}

// cloudToolHosts is cloud_hosts' answer without a computer: wire.CloudHosts with each bound host's computer named.
type cloudToolHosts struct {
	Hosts    []cloudToolHost             `json:"hosts"`
	Problems []wire.CloudProviderProblem `json:"problems"`
	Partial  bool                        `json:"partial"`
}

// cloudToolMachine is one computer's view, naming the computer.
type cloudToolMachine struct {
	MachineID wire.CloudMachineID `json:"machine_id"`
	Machine   string              `json:"machine"`
	wire.CloudMachine
}

// callCloudTool runs cloud_hosts or cloud_host_action for the assistant conversation sessionID.
func (h *Manager) callCloudTool(ctx context.Context, sessionID, name string, args json.RawMessage) proto.ToolResult {
	lang := i18n.From(ctx).Or(h.userLang())
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var a struct {
		Machine   string                   `json:"machine"`
		Refresh   bool                     `json:"refresh"`
		Action    wire.CloudActionKind     `json:"action"`
		ID        wire.CloudCustomActionID `json:"id"`
		Name      string                   `json:"name"`
		Reconnect bool                     `json:"reconnect"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return proto.TextResult("bad arguments: "+err.Error(), true)
	}
	var m *Machine
	if a.Machine = strings.TrimSpace(a.Machine); a.Machine != "" {
		if m = h.machineByName(a.Machine); m == nil {
			return proto.TextResult(fmt.Sprintf("no computer %q: give a computer's id or name", a.Machine), true)
		}
	}
	if name == toolCloudHosts {
		if m == nil {
			hosts, f := h.cloudHosts(ctx, lang, a.Refresh)
			if f != nil {
				return cloudToolErr(f, lang)
			}
			return cloudToolJSON(h.cloudToolHosts(hosts))
		}
		view, f := h.cloudMachineView(ctx, lang, m, a.Refresh)
		if f != nil {
			return cloudToolErr(f, lang)
		}
		return cloudToolJSON(cloudToolMachine{MachineID: wire.CloudMachineID(m.cfg.ID), Machine: m.name(), CloudMachine: view})
	}
	if m == nil {
		return proto.TextResult("give machine: the id or name of the computer whose cloud server to act on", true)
	}
	if a.Action == wire.CloudActionKindSnapshotList {
		snaps, f := h.cloudSnapshots(ctx, lang, m)
		if f != nil {
			return cloudToolErr(f, lang)
		}
		return cloudToolJSON(snaps)
	}
	in := wire.CloudActionRequest{Action: a.Action, ID: a.ID, Name: a.Name, Reconnect: a.Reconnect}
	// Asked first as it is: a read action runs, a bad one or one the instance does not offer now is refused (no card
	// goes up for it), and one that needs a person stops at the gate (with its safety level) before the program hears
	// of the write.
	out, f := h.cloudAct(ctx, lang, actorAssistant, m, in, nil)
	if f == nil {
		return cloudToolJSON(out)
	}
	if f.code != codeCloudConfirmFirst {
		return cloudToolErr(f, lang)
	}
	safety := f.safety
	b, t, f := h.cloudBound(ctx, m)
	if f != nil {
		return cloudToolErr(f, lang)
	}
	summary := h.cloudCardSummary(h.userLang(), m, b, t, in, safety) // the card is the user's, in their language
	input, _ := json.Marshal(map[string]any{"machine": m.cfg.ID, "instance": b.Instance, "action": in.Action, "id": in.ID,
		"name": in.Name, "reconnect": in.Reconnect, "safety": safety})
	if ok, outcome := h.approveOutcome(ctx, h.assistantAsker(sessionID, m.cfg.ID), toolCloudHostAction, summary, input); !ok {
		h.logf("[%s] cloud_host_action %s %s", m.cfg.ID, in.Action, refusalWord(outcome))
		return notApproved(outcome)
	}
	in.Confirm = true
	if safety == wire.CloudSafetyDestructive {
		in.Acknowledge = string(b.Instance)
	}
	// Against the binding the user approved, compared under the read the action uses: a computer bound to another
	// instance meanwhile is not acted on.
	out, f = h.cloudAct(ctx, lang, actorAssistant, m, in, &b)
	if f != nil {
		return cloudToolErr(f, lang)
	}
	return cloudToolJSON(out)
}

// cloudToolHosts names the computer of each bound host.
func (h *Manager) cloudToolHosts(in wire.CloudHosts) cloudToolHosts {
	hosts := make([]cloudToolHost, 0, len(in.Hosts))
	for _, host := range in.Hosts {
		th := cloudToolHost{CloudHost: host}
		if host.MachineID != "" {
			th.Machine = h.machineName(string(host.MachineID))
		}
		hosts = append(hosts, th)
	}
	return cloudToolHosts{Hosts: hosts, Problems: in.Problems, Partial: in.Partial}
}

// cloudCardSummary is the approval card's line in lang: the action, the computer and the instance, worded as
// destructive when it is; a custom action adds the manifest's confirmation sentence, a start-and-reconnect says so.
func (h *Manager) cloudCardSummary(lang i18n.Lang, m *Machine, b wire.CloudBinding, t cloudTarget, in wire.CloudActionRequest, safety wire.CloudSafety) string {
	action, question := "", ""
	switch {
	case in.Action == wire.CloudActionKindCustom:
		action = string(in.ID)
		if c, ok := cloudCustomOf(t.p, in.ID); ok {
			action, question = c.Title, c.Confirm
		}
	case in.Action == wire.CloudActionKindSnapshotCreate && in.Name != "":
		action = i18n.T(lang, cloudI18n+"action_snapshot_create_named", i18n.Args{"name": in.Name}) // cloudAct held it to one line of at most 80
	case i18n.Has(cloudI18n + "action_" + string(in.Action)):
		action = i18n.T(lang, cloudI18n+"action_"+string(in.Action))
	default:
		action = string(in.Action)
	}
	instance := string(b.Instance)
	if b.DisplayName != "" && b.DisplayName != instance {
		instance = b.DisplayName + " (" + instance + ")"
	}
	key := "card_confirm"
	if safety == wire.CloudSafetyDestructive {
		key = "card_destructive"
	}
	lines := []string{i18n.T(lang, cloudI18n+key, i18n.Args{"instance": instance, "machine": m.name(), "action": action})}
	if question = cloudLine(question, cloudTextMost); question != "" {
		lines = append(lines, question)
	}
	if in.Reconnect {
		lines = append(lines, i18n.T(lang, cloudI18n+"card_reconnect", i18n.Args{"machine": m.name()}))
	}
	return strings.Join(lines, "\n")
}

// cloudToolErr is f for the model: its code, the error in lang, and what else it says.
func cloudToolErr(f *cloudFailure, lang i18n.Lang) proto.ToolResult {
	text := f.code + ": " + i18n.Localize(f.err, lang)
	if f.detail != "" {
		text += " (" + f.detail + ")"
	}
	if f.safety != "" {
		text += " [safety " + string(f.safety) + "]"
	}
	return proto.TextResult(text, true)
}

func cloudToolJSON(v any) proto.ToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return proto.TextResult(clip(string(b), cloudToolMost), false)
}
