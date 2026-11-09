package extmgr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extension"
)

// What the hub knows before it asks an extension to take a row back, and asks nobody when it does not know enough: the
// answer is not_recallable, before the user is asked, and nothing is sent.
//
// A write the node's gate proxy reported (lark.sent, RecordNodeSent) is a row of connector "lark" whatever gate it went
// through: the connector is a constant, not the gate's own, and the row names no extension. It names the gate, by the
// command it was called through (target.command: every such row has one, written since the first version of the report).
// So such a row goes to the extension that provides the row's connector only when that extension has the gate and no
// other extension has a gate of the same command; for a gate row, then, an extension is not given the message id of a
// send that was not its. The count is over the extensions the assistant is given the gates of (enabled, with no problem,
// whether or not they run a program) that apply has run for: one not applied yet, as at hub start, is not counted. A
// command is checked for its format only, so two extensions can have one; the node then uses one of them, and the row
// cannot say which. A row whose command cannot be read is not told either.

// gateCommandOf is the command a gate-proxy row went through and whether the row is one at all: "" with true is a row
// that names none, or whose target cannot be read (null, an array, a number, no command).
func gateCommandOf(row db.SentAsUser) (command string, gateRow bool) {
	if !isGateCall(row.Action) { // "METHOD /path", as the per-chat rules take it (chat_rules.go): not a tool's name or sent.record
		return "", false
	}
	var t struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(row.Target, &t)
	return t.Command, true
}

// recallOwnerCheck says why row, of the connector c, cannot be taken back for want of knowing whose it was, nil when the
// extension that provides c may be asked. It is part of recallCheck, so that a page of the ledger offers a recall only for
// what a recall can ask about. A connector that no extension provides (a built-in one) passes unless the row is a gate
// proxy's.
func (h *Manager) recallOwnerCheck(c connector.Connector, row db.SentAsUser) *RecallError {
	owner := h.extensions.ownerOf(c)
	// The connector checks it again when it is asked (proc.Config.RecallNotBefore): this one is for the ledger's page.
	if owner != nil && !owner.recallNotBefore.IsZero() && row.At.Before(owner.recallNotBefore) {
		return recallErr(RecallNotRecallable, "this was sent before this install of the extension that provides "+c.Name())
	}
	command, gateRow := gateCommandOf(row)
	switch {
	case !gateRow:
		return nil
	case command == "":
		return recallErr(RecallNotRecallable, "the gate this was sent through is not known, so whose it was cannot be told")
	case owner == nil || !slices.Contains(owner.gateCommands, command):
		return recallErr(RecallNotRecallable, "the gate this was sent through ("+command+") is not one of the extension that provides "+c.Name())
	case h.extensions.gateHolders(command) != 1:
		return recallErr(RecallNotRecallable, "more than one extension has the gate "+command+", so whose it was cannot be told")
	}
	return nil
}

// ownerOf is the extension that runs c now (nil: none, c is not an extension's connector).
func (ps *extensions) ownerOf(c connector.Connector) *hubExtension {
	if ps == nil {
		return nil
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, hp := range ps.byID {
		for _, own := range hp.conns {
			if connector.Connector(own) == c {
				return hp
			}
		}
	}
	return nil
}

// gateHolders is how many extensions have an assistant gate with this command: the enabled ones without a problem, as
// assistantGates gives the node, whether or not they run a program.
func (ps *extensions) gateHolders(command string) int {
	if ps == nil {
		return 0
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	n := 0
	for _, hp := range ps.byID {
		if slices.Contains(hp.gateCommands, command) {
			n++
		}
	}
	return n
}

// gateCommandsOf are the commands of p's assistant gates (the same files assistantGates reads when the assistant starts).
func gateCommandsOf(p *extension.Extension) []string {
	if p == nil || p.Dir == "" || p.Manifest == nil {
		return nil
	}
	var out []string
	for _, a := range p.Manifest.Assistants() {
		if a.Gate == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(p.Dir, filepath.FromSlash(a.Gate)))
		if err != nil {
			continue
		}
		if gf, err := extension.ParseGate(raw); err == nil {
			out = append(out, gf.Command)
		}
	}
	return out
}
