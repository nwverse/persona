package node

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Persona reads an agy herdr pane's conversation only once herdr's Antigravity integration reports it
// (reportedAgyConversation, agypane.go): without that hook the pane serves a notice for its whole run and its reply
// reaches no one (#13445). So an agy start that would open a herdr pane first asks the herdr server the pane would be in
// whether the hook is installed (integration.list, herdr 0.9 and later; the hook's files for a herdr without that
// method), and is refused with agy_integration_missing when it is not: no tab opens and nothing is typed, so the hub
// can start it without a terminal or say what to run. A herdr that cannot be asked leaves the start as it was. The
// launcher check (launchercheck.go) and `node doctor` (doctor.go, agy-herdr) say the same.

// agyHookInstalled is agyHook's state for a hook only its files show (a herdr without integration.list).
const agyHookInstalled = "installed"

// agyHookScript is the script herdr installs as its Antigravity hook, and agyHookMark the line that says so.
const (
	agyHookScript = "herdr-agent-state.sh"
	agyHookMark   = "HERDR_INTEGRATION_ID=antigravity_cli"
)

// agyHookWait bounds asking herdr: a local socket that answers in milliseconds.
const agyHookWait = 3 * time.Second

// agyHook is what herdr says of its Antigravity hook for a start in id (a workspace or pane; "" the own server) on
// launch: current, outdated or not_installed from the server's integration.list; for a herdr without that method or
// an answer that names no Antigravity integration, agyHookInstalled or not_installed from the hook's files in agy's
// config directory (agyHookFiles). "" when launch is no herdr host (the Herdr App, another engine) or herdr could not
// be asked. Asked at each start: one local call, and the start right after `herdr integration install
// antigravity-cli` goes through.
func (n *Node) agyHook(ctx context.Context, launch host.Launcher, id string) string {
	h, ok := launch.(*herdrHost)
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, agyHookWait)
	defer cancel()
	list, _, err := h.integrations(ctx, id)
	if err != nil && !herdr.UnknownMethod(err) {
		return ""
	}
	for _, in := range list {
		if in.Target == herdr.AntigravityIntegration {
			return in.State
		}
	}
	if agyHookFiles(agyHookDir(n.home)) {
		return agyHookInstalled
	}
	return herdr.IntegrationNotInstalled
}

// checkAgyPane refuses an agy start that would open a pane in id on launch (startWorkAgent) when herdr's Antigravity
// hook is not installed there (agyHook): agy_integration_missing, before any tab opens or anything is typed.
func (n *Node) checkAgyPane(ctx context.Context, launch host.Launcher, id string) error {
	if n.agyHook(ctx, launch, id) != herdr.IntegrationNotInstalled {
		return nil
	}
	return refusal(proto.CodeAgyIntegrationMissing, agyIntegrationMissingText(i18n.Default), nil)
}

func agyIntegrationMissingText(lang i18n.Lang) string {
	return i18n.T(lang, "rpc-errors.agy_integration_missing")
}

// agyHookDir is where herdr installs its Antigravity hook: agy's config directory, $ANTIGRAVITY_CLI_CONFIG_DIR as herdr
// reads it, else ~/.gemini/config. (agyDataRoot reads the same variable for agy's data directory.)
func agyHookDir(home string) string {
	if dir := os.Getenv("ANTIGRAVITY_CLI_CONFIG_DIR"); dir != "" {
		return dir
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".gemini", "config")
}

// agyHookFiles: herdr's Antigravity hook is installed in agy's config directory dir: its hooks.json runs herdr's
// script before each invocation, and the script carries herdr's Antigravity mark. herdr 0.8.0 (the hook's version 1)
// to 0.9.5 (version 4) install it the same way.
func agyHookFiles(dir string) bool {
	if dir == "" {
		return false
	}
	script, err := os.ReadFile(filepath.Join(dir, "hooks", agyHookScript))
	if err != nil || !bytes.Contains(script, []byte(agyHookMark)) {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
	if err != nil {
		return false
	}
	var groups map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &groups) != nil {
		return false
	}
	for _, events := range groups {
		var hooks []struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(events["PreInvocation"], &hooks) != nil {
			continue
		}
		for _, hook := range hooks {
			if strings.Contains(hook.Command, agyHookScript) {
				return true
			}
		}
	}
	return false
}

// checkAgyHerdrIn is node doctor's line for herdr's Antigravity hook (agy-herdr), only where agy is used with herdr: the
// server at sock answers and finds agy (integration.list's available), or a herdr without that method runs and agy or
// the hook's files are here. false leaves the line out: no herdr to ask (its own line says so), or no agy.
func checkAgyHerdrIn(ctx context.Context, sock, configDir string, lang i18n.Lang) (DoctorCheck, bool) {
	if sock == "" || configDir == "" {
		return DoctorCheck{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, doctorDialTimeout)
	defer cancel()
	list, err := (&herdr.Client{Socket: sock}).Integrations(ctx)
	if err != nil && !herdr.UnknownMethod(err) {
		return DoctorCheck{}, false
	}
	state, used := "", false
	for _, in := range list {
		if in.Target == herdr.AntigravityIntegration {
			state, used = in.State, in.Available || in.State != herdr.IntegrationNotInstalled
		}
	}
	if state == "" {
		files := agyHookFiles(configDir)
		state = herdr.IntegrationNotInstalled
		if files {
			state = agyHookInstalled
		}
		_, onPath := exec.LookPath("agy")
		used = files || onPath == nil
	}
	if !used {
		return DoctorCheck{}, false
	}
	c := DoctorCheck{Name: "agy-herdr", Items: []string{}}
	switch state {
	case herdr.IntegrationCurrent, agyHookInstalled:
		c.Status, c.Detail = DoctorOK, i18n.T(lang, "node-doctor.agy_herdr_ok")
	case herdr.IntegrationOutdated:
		c.Status, c.Detail = DoctorWarn, i18n.T(lang, "node-doctor.agy_herdr_outdated")
		c.Hint = i18n.T(lang, "node-doctor.agy_herdr_outdated_hint")
	case herdr.IntegrationNotInstalled:
		c.Status, c.Detail = DoctorWarn, i18n.T(lang, "node-doctor.agy_herdr_missing")
		c.Hint = i18n.T(lang, "node-doctor.agy_herdr_missing_hint")
	default: // a state this node does not know: what herdr said, as it is
		c.Status, c.Detail = DoctorWarn, state
		c.Hint = i18n.T(lang, "node-doctor.agy_herdr_outdated_hint")
	}
	return c, true
}
