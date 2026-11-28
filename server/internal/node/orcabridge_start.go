package node

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/orcart"
	"github.com/nwverse/persona/server/internal/proto"
)

// Starting an Orca chat from Persona (#17047, docs/plans/plan-orca-support.md "Structured sessions"). The bridge offers
// a launcher for each provider Orca runs as a chat and found installed ("orca-claude", "orca-codex"…), on an Orca that
// starts chats (agent.launch's chat surface); none while Orca is not running, and none on a computer whose Orca host is
// off (the bridge does not run there). A start goes to the deepest local Orca worktree holding the folder (a Git
// repository not in Orca yet is added, as a terminal start adds it), never to an unrelated one, and the chat starts at
// that worktree's root. Its Orca session id is "persona-<the id the hub named>" (orcaStartPrefix), so a start the hub
// asks for again after losing the answer finds the chat it made instead of making another.

const (
	// orcaLauncherPrefix starts the name of each of the bridge's launchers; the rest is Orca's provider id.
	orcaLauncherPrefix = "orca-"
	// orcaAgentsMaxAge: the agents Orca found installed are read again at most this often.
	orcaAgentsMaxAge = time.Minute
	// orcaStartKeep: a started chat's row stands until Orca lists the chat, at most this long (its tab shows within
	// orcaTabsPoll); then the book lists it, not running, if Orca never did.
	orcaStartKeep = time.Minute
)

// orcaChatProviders are the providers Orca 1.4.277 runs as chats (its structured agents), in its order.
var orcaChatProviders = []string{"claude", "codex", "grok", "opencode", "opencode2", "omp", "copilot", "cursor", "kimi",
	"kiro", "pi", "antigravity", "hermes", "goose", "aug", "kilo", "mimo-code", "gemini", "droid", "qwen-code", "cline",
	"devin", "codebuddy"}

// orcaSessionIDRe is what Orca takes as a session id.
var orcaSessionIDRe = lazyre.New(`^[A-Za-z0-9_-]{8,128}$`)

// orcaStarted is a chat Persona started that Orca does not list yet: its row, until when it stands.
type orcaStarted struct {
	row   map[string]any
	until time.Time
}

// orcaProviderName is what the apps call a provider: Claude Code and Codex as everywhere, the rest by Orca's name.
func orcaProviderName(agent string) string {
	switch agent {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	return firstOf(orcaAgentNames[agent], agent)
}

// installed is the agents Orca found installed (preflight.installedAgents), read again at most every
// orcaAgentsMaxAge; the last ones while it cannot be read, none from an Orca without the method.
func (b *orcaBridge) installed(ctx context.Context) []string {
	b.mu.Lock()
	agents, at := b.agents, b.agentsAt
	b.mu.Unlock()
	if !at.IsZero() && time.Since(at) < orcaAgentsMaxAge {
		return agents
	}
	got, err := b.c.InstalledAgents(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agentsAt = time.Now()
	switch {
	case err == nil:
		b.agents = got
	case orcart.IsCode(err, "method_not_found") || orcart.IsCode(err, "method_not_supported"):
		b.agents = nil
	}
	return b.agents
}

// orcaLaunchers are the bridge's launchers (runtimes.md "launchers"): one per provider Orca runs as a chat and found
// installed, on an Orca whose agent.launch takes the chat surface; none on any other. A provider that is not Claude
// Code's lists the permission modes a start takes (plan aside: the bridge promises no read-only start), and each lists
// the models its open chats' options name (opts: the options of each provider's open chats), with the efforts Persona
// can send.
func orcaLaunchers(agents []string, st *orcart.Status, opts map[string][]*orcart.Options) []map[string]any {
	if !st.Has(orcart.CapChatLaunch) {
		return nil
	}
	var out []map[string]any
	for _, agent := range orcaChatProviders {
		if !slices.Contains(agents, agent) {
			continue
		}
		l := map[string]any{"name": orcaLauncherPrefix + agent, "agent": agent,
			"display_name": "Orca chat · " + orcaProviderName(agent)}
		if agent != "claude" {
			var modes []string
			for _, mode := range proto.PermissionModes {
				if mode != "plan" && orcaTakes(agent, orcaModes[mode]) {
					modes = append(modes, mode)
				}
			}
			if len(modes) > 0 {
				l["permission_modes"] = modes
			}
		}
		if models := orcaLauncherModels(opts[agent]); len(models) > 0 {
			l["models"] = models
		}
		out = append(out, l)
	}
	return out
}

// orcaLauncherModels are the models a provider's open chats list (each once, in the order they come, the first one a
// chat runs when none is picked as the default), with the efforts among Persona's levels (proto.EffortLevels).
func orcaLauncherModels(opts []*orcart.Options) []proto.AgentModel {
	var out []proto.AgentModel
	seen, def := map[string]bool{}, false
	for _, o := range opts {
		if o == nil {
			continue
		}
		for _, id := range o.Models {
			if seen[id] || len(out) >= maxLauncherModels {
				continue
			}
			seen[id] = true
			m := proto.AgentModel{ID: id, Name: firstOf(o.ModelNames[id], id), Default: !def && id == o.DefaultModel}
			def = def || m.Default
			for _, e := range o.Efforts[id] {
				if slices.Contains(proto.EffortLevels, e) {
					m.Efforts = append(m.Efforts, e)
				}
			}
			out = append(out, m)
		}
	}
	return out
}

// pushLaunchers tells the node the bridge's launchers when they changed (runtime.launchers).
func (b *orcaBridge) pushLaunchers(launchers []map[string]any) {
	raw, err := json.Marshal(launchers)
	if err != nil || string(raw) == b.launches {
		return
	}
	if launchers == nil {
		launchers = []map[string]any{}
	}
	if b.peer.Notify("runtime.launchers", map[string]any{"launchers": launchers}) == nil {
		b.launches = string(raw)
	}
}

// orcaStartParams are session.start's params (runtimes.md "What to implement").
type orcaStartParams struct {
	Cwd            string        `json:"cwd"`
	Prompt         string        `json:"prompt"`
	Images         []proto.Image `json:"images"`
	ClientMsgID    string        `json:"client_msg_id"`
	Launcher       string        `json:"launcher"`
	Model          string        `json:"model"`
	PermissionMode string        `json:"permission_mode"`
	Effort         string        `json:"effort"`
	SessionID      string        `json:"session_id"`
}

// start is session.start: a chat of the launcher's provider in the Orca worktree holding cwd, with the first message
// and the form's model, effort and permission mode, through agent.launch's chat surface (in the background, as
// Orca's own New chat), or agentSession.create for a first message with pictures or on an Orca whose agent.launch
// has no chat surface. A chat Orca already has under the id (a start asked for again) is answered with, not made
// again. Its answer is the chat's row, listed until Orca lists the chat.
func (b *orcaBridge) start(ctx context.Context, params json.RawMessage) (any, error) {
	var p orcaStartParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	none := func() map[string]any { return map[string]any{"delivered": "none"} }
	agent, ok := strings.CutPrefix(p.Launcher, orcaLauncherPrefix)
	if !ok || !slices.Contains(orcaChatProviders, agent) {
		return nil, orcaChatErr("unsupported", "orca_start_launcher", none(), p.Launcher)
	}
	options := map[string]string{}
	switch mode := p.PermissionMode; {
	case mode == "plan":
		return nil, readonlyStartUnsupported() // the node refuses it first: the bridge declares no caps.readonly
	case mode != "":
		if !orcaTakes(agent, orcaModes[mode]) {
			return nil, orcaChatErr("unsupported", "orca_mode_unsupported", none(), mode)
		}
		options["permissionMode"] = orcaModes[mode]
	}
	if p.Model != "" {
		options["model"] = p.Model
	}
	if p.Effort != "" {
		options["effort"] = p.Effort
	}
	persona := p.SessionID
	if !runtimeIDRe.MatchString(persona) || !orcaSessionIDRe.MatchString(orcaStartPrefix+persona) {
		persona = strings.ReplaceAll(newUUID(), "-", "") // the hub named none Orca takes: one of the bridge's own
	}
	orca := orcaStartPrefix + persona
	if row := b.startedRow(orca); row != nil {
		return map[string]any{"session": row}, nil // asked for again before Orca lists it
	}
	tree, err := b.chatWorktree(ctx, p.Cwd)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	row := orcaStartRow(persona, agent, p, tree, now)
	if _, err := b.c.Head(ctx, orca); err == nil {
		// Orca has it already: a start asked for again after its answer was lost. The row is the chat's own
		// once Orca lists it; meanwhile the one it was started with, idle, since its first message already went.
		row["status"] = proto.StatusIdle
		b.noteStarted(orca, persona, agent, row, tree, now)
		return map[string]any{"session": row}, nil
	}
	selector := "id:" + tree.WorktreeID
	if len(p.Images) == 0 && b.status(ctx).Has(orcart.CapChatLaunch) {
		err = b.c.LaunchChat(ctx, orca, orcart.OperationID(now, "launch:"+orca), selector, agent, p.Prompt, options)
	} else {
		var paths []string
		if paths, err = orcaImages(p.Images); err == nil {
			err = b.c.CreateChat(ctx, orca, p.ClientMsgID, selector, agent, p.Prompt, paths, options)
		}
	}
	if err != nil {
		if _, herr := b.c.Head(ctx, orca); herr != nil { // made all the same (Orca had it, or answered too late)
			return nil, orcaStartErr(err)
		}
	}
	b.noteStarted(orca, persona, agent, row, tree, now)
	return map[string]any{"session": row}, nil
}

// orcaStartErr is a failed start in the user's words: an Orca with no way to start a chat, else as for any write.
func orcaStartErr(err error) error {
	if orcart.IsCode(err, "method_not_found") || orcart.IsCode(err, "method_not_supported") ||
		orcart.IsCode(err, "agent_launch_unsupported") || orcart.IsCode(err, "agent_launch_surface_unavailable") {
		return orcaChatErr("unsupported", "orca_start_unsupported", map[string]any{"delivered": "none"}, "")
	}
	return orcaWriteErr(err)
}

// orcaStartRow is the row of a chat Persona starts, as it stands until Orca lists it.
func orcaStartRow(persona, agent string, p orcaStartParams, tree *orcart.Worktree, now time.Time) map[string]any {
	status := proto.StatusIdle
	if strings.TrimSpace(p.Prompt) != "" || len(p.Images) > 0 {
		status = proto.StatusWorking
	}
	row := map[string]any{"session_id": persona, "title": firstOf(firstLine(p.Prompt), filepath.Base(tree.Path)),
		"cwd": tree.Path, "status": status, "updated_at": now.UTC(), "rev": now.UnixMilli(), "space_id": tree.WorktreeID,
		"space_name": orcaSpaceLabel(tree.Branch, tree.Path), "launcher": p.Launcher, "started_by_persona": true}
	if parent := orcaParent(tree.WorktreeID, tree.ParentWorktreeID); parent != "" {
		row["parent_space_id"] = parent
	}
	orcaAgentFields(row, agent, "")
	for k, v := range map[string]string{"model": p.Model, "permission_mode": p.PermissionMode, "effort": p.Effort} {
		if v != "" {
			row[k] = v
		}
	}
	return row
}

// noteStarted keeps a started chat's row listed until Orca lists the chat (orcaStartKeep), puts it in the book under
// its id, and has the next refresh read the chat tabs (where Orca shows it first).
func (b *orcaBridge) noteStarted(orca, persona, agent string, row map[string]any, tree *orcart.Worktree, now time.Time) {
	b.book.note(orcaBookEntry{Orca: orca, Persona: persona, Agent: agent, Cwd: tree.Path, Space: tree.WorktreeID,
		Branch: tree.Branch, Parent: orcaParent(tree.WorktreeID, tree.ParentWorktreeID), Title: firstLine(row["title"].(string)),
		Updated: now.UnixMilli()}, now)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.starts == nil {
		b.starts = map[string]orcaStarted{}
	}
	b.starts[orca] = orcaStarted{row: row, until: now.Add(orcaStartKeep)}
	b.tabsAt, b.repush = time.Time{}, true
}

// startedRow is the row of a chat Persona started that Orca does not list yet; nil for any other. The rows go to the
// node again on the next refresh: the answer stands in for the chat's listed row until then.
func (b *orcaBridge) startedRow(orca string) map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.starts[orca]; ok && time.Now().Before(s.until) {
		b.repush = true
		return s.row
	}
	return nil
}

// startedRows are the rows of the chats Persona started that the bridge does not list yet from Orca (taken: the
// Persona ids it lists); the ones it lists now, or that stood orcaStartKeep, go.
func (b *orcaBridge) startedRows(taken map[string]string, now time.Time) (rows []map[string]any, orcas []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for orca, s := range b.starts {
		persona, _ := s.row["session_id"].(string)
		if !now.Before(s.until) || taken[persona] != "" {
			delete(b.starts, orca)
			continue
		}
		rows, orcas = append(rows, s.row), append(orcas, orca)
	}
	return rows, orcas
}

// chatWorktree is the Orca worktree a chat for folder cwd starts in: the deepest local one holding it; else, for a
// folder in a Git repository Orca does not have (not the home folder, the root or a temporary folder), the repository
// added to Orca (repo.add), as a terminal start adds it. Never an unrelated one: a folder no worktree holds is refused.
func (b *orcaBridge) chatWorktree(ctx context.Context, cwd string) (*orcart.Worktree, error) {
	trees, _, _, err := b.c.Worktrees(ctx, "")
	if err != nil {
		return nil, orcaWriteErr(err)
	}
	path := orcaLaunchPath(cwd)
	if w := orcaDeepest(trees, path); w != nil {
		return w, nil
	}
	if strings.TrimSpace(cwd) != "" && orcaCanAutoRegister(path) {
		if root, err := findGitRoot(path); err == nil && root != "" && b.c.RepoAdd(ctx, root, "git") == nil {
			if trees, _, _, err = b.c.Worktrees(ctx, ""); err == nil {
				if w := orcaDeepest(trees, path); w != nil {
					return w, nil
				}
			}
		}
	}
	return nil, orcaChatErr("unsupported", "orca_start_folder", map[string]any{"delivered": "none"}, "")
}

// orcaDeepest is the deepest of this computer's worktrees holding path; nil when none does.
func orcaDeepest(trees []orcart.Worktree, path string) *orcart.Worktree {
	var best *orcart.Worktree
	for i := range trees {
		w := &trees[i]
		if w.HostID != "" && w.HostID != "local" || w.Removing || w.Path == "" {
			continue
		}
		if wp := orcaLaunchPath(w.Path); isInsideOrEqual(path, wp) && (best == nil || len(wp) > len(orcaLaunchPath(best.Path))) {
			best = w
		}
	}
	return best
}
