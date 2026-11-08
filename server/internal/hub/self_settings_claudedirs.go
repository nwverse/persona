package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// persona_settings agents.blocked_claude_dirs: the Claude Code config directories kept out of Agents, per machine
// (claudedirs.go). Same as the machine settings page (PUT /api/v1/machines/{mid}/claude-dirs/blocked).

func (h *Hub) blockedClaudeDirsSetting() selfSetting {
	return selfSetting{key: "agents.blocked_claude_dirs", title: i18n.T(h.userLang(), "hub-self_settings_claudedirs.title"),
		about: "per machine, Claude Code config directories (CLAUDE_CONFIG_DIR) whose sessions are kept out of Agents: " +
			"not scanned, not remembered, their sessions not listed (for automations that run claude with a directory of " +
			"their own). get lists each machine's directories and patterns",
		value: `add: {"machine": machine id, "pattern": a path or glob starting with / or ~/ (~ is that machine's home), ` +
			`e.g. "~/.automation/*"}; remove: "<machine id>:<pattern>"`,
		get: h.getBlockedClaudeDirs, add: h.addBlockedClaudeDir, remove: h.removeBlockedClaudeDir}
}

func (h *Hub) getBlockedClaudeDirs(ctx context.Context) (string, error) {
	var b strings.Builder
	for _, m := range h.machinesNow() {
		st := m.claudeDirsState(ctx)
		fmt.Fprintf(&b, "%s (%s): blocked %s\n", m.cfg.ID, m.name(), orNone(strings.Join(st.Blocked, ", ")))
		for _, d := range st.Dirs {
			state := "listed"
			switch {
			case d.Default:
				state = "default, never blocked"
			case d.Blocked:
				state = "blocked"
			}
			fmt.Fprintf(&b, "  - %s (%s; %d sessions, %d running)\n", d.Path, state, d.Sessions, d.Running)
		}
	}
	if b.Len() == 0 {
		return "No machines.", nil
	}
	return b.String(), nil
}

func (h *Hub) addBlockedClaudeDir(ctx context.Context, v json.RawMessage) (*selfChange, error) {
	var a struct {
		Machine string `json:"machine"`
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(v, &a); err != nil {
		return nil, errors.New(`value must be {"machine", "pattern"}`)
	}
	m := h.localMachine(a.Machine)
	if m == nil {
		return nil, fmt.Errorf("no machine %q", a.Machine)
	}
	p := strings.TrimSuffix(strings.TrimSpace(a.Pattern), "/")
	if len(proto.CleanClaudeDirPatterns([]string{p})) == 0 || !proto.ValidClaudeDirPattern(p) {
		return nil, fmt.Errorf("bad pattern %q: a path or glob starting with / or ~/", a.Pattern)
	}
	now, err := m.readBlockedClaudeDirs(ctx)
	if err != nil {
		return nil, err
	}
	if slices.Contains(now, p) {
		return &selfChange{summary: p + " is already blocked on " + m.cfg.ID + "."}, nil
	}
	return &selfChange{summary: i18n.T(h.userLang(), "hub-self_settings_claudedirs.block", i18n.Args{"machine": m.name(), "path": p}),
		apply: func(ctx context.Context, _ map[string]string) (string, error) {
			now, err := m.readBlockedClaudeDirs(ctx)
			if err != nil {
				return "", err
			}
			if !slices.Contains(now, p) {
				now = append(now, p)
			}
			if err := m.setBlockedClaudeDirs(ctx, now); err != nil {
				return "", err
			}
			return "Blocked " + p + " on " + m.cfg.ID + ".", nil
		}}, nil
}

func (h *Hub) removeBlockedClaudeDir(ctx context.Context, id string) (*selfChange, error) {
	mid, p, ok := strings.Cut(id, ":")
	m := h.localMachine(strings.TrimSpace(mid))
	if !ok || m == nil {
		return nil, fmt.Errorf("remove takes \"<machine id>:<pattern>\", got %q", id)
	}
	p = strings.TrimSuffix(strings.TrimSpace(p), "/")
	now, err := m.readBlockedClaudeDirs(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(now, p) {
		return nil, fmt.Errorf("%s is not blocked on %s (get lists the patterns)", p, m.cfg.ID)
	}
	return &selfChange{summary: i18n.T(h.userLang(), "hub-self_settings_claudedirs.unblock", i18n.Args{"machine": m.name(), "path": p}),
		apply: func(ctx context.Context, _ map[string]string) (string, error) {
			now, err := m.readBlockedClaudeDirs(ctx)
			if err != nil {
				return "", err
			}
			rest := slices.DeleteFunc(now, func(s string) bool { return s == p })
			if err := m.setBlockedClaudeDirs(ctx, rest); err != nil {
				return "", err
			}
			return "Unblocked " + p + " on " + m.cfg.ID + ".", nil
		}}, nil
}
