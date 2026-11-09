package assistant

// A workspace the user @-mentioned in the assistant's composer (#5594, docs/assistant/assistant.md "Workspace mentions"): the
// client sends <persona-context kind="workspace" title="…" machine="<id>" cwd="<folder>"> in front of the message, and
// here the block becomes what the assistant needs to work there — the computer and folder to dispatch to, the
// knowledge base's projects there and the agents that worked there lately.

import (
	"context"
	"github.com/nwverse/persona/server/internal/compat"
	"html"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/textfmt"
)

// leadingContextRe is one attributed context block at the start of what is left of a message: the clients put the
// picked contexts in front, so a block further on (one quoted inside another's text) is never taken for a mention.
var leadingContextRe = lazyre.New(`(?s)^\s*<` + compat.ContextPattern() + `\b([^>]*)>(.*?)</` + compat.ContextPattern() + `>`)

// contextAttrRe is one name="value" on a context tag.
var contextAttrRe = lazyre.New(`([a-z_]+)="([^"]*)"`)

const (
	// workspaceSessionsMost is how many of the agents that worked in a workspace its block lists.
	workspaceSessionsMost = 5
	// workspaceListWait is how long a mention waits for the computer's sessions; past it the block goes without them.
	workspaceListWait = 3 * time.Second
)

// withWorkspaces is text with each workspace block at its start filled in (workspaceBlock); text as it is without any.
// A block naming a computer the hub doesn't know stays as the client wrote it.
func (s *Service) withWorkspaces(ctx context.Context, text string) string {
	if !strings.Contains(text, `kind="workspace"`) {
		return text
	}
	var b strings.Builder
	rest := text
	for {
		m := leadingContextRe.FindStringSubmatchIndex(rest)
		if m == nil {
			break
		}
		block := rest[:m[1]]
		attrs := contextAttrs(rest[m[2]:m[3]])
		if attrs["kind"] == "workspace" {
			if filled, ok := s.workspaceBlock(ctx, attrs); ok {
				lead := block[:len(block)-len(strings.TrimLeft(block, " \t\r\n"))]
				block = lead + filled
			}
		}
		b.WriteString(block)
		rest = rest[m[1]:]
	}
	b.WriteString(rest)
	return b.String()
}

// contextAttrs are a context tag's attributes, unescaped.
func contextAttrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range contextAttrRe.FindAllStringSubmatch(tag, -1) {
		out[m[1]] = html.UnescapeString(m[2])
	}
	return out
}

// workspaceBlock is the filled-in block for a mention with these attributes; false when it names no computer the hub
// knows or no folder.
func (s *Service) workspaceBlock(ctx context.Context, attrs map[string]string) (string, bool) {
	machine, cwd := strings.TrimSpace(attrs["machine"]), strings.TrimSpace(attrs["cwd"])
	if machine == "" || cwd == "" {
		return "", false
	}
	d, ok := s.device(ctx, machine)
	if !ok {
		return "", false
	}
	w := prompts.WorkspaceMention{Title: strings.TrimSpace(attrs["title"]), Machine: d.ID, MachineName: textfmt.FirstNonEmpty(d.Name, d.ID),
		Cwd: cwd, Branch: strings.TrimSpace(attrs["branch"]), Online: d.Online}
	if w.Title == "" {
		w.Title = path.Base(strings.TrimRight(cwd, `/\`))
	}
	if e, err := s.kbGet(ctx); err == nil && e != nil {
		for _, p := range e.ProjectsForWork(d.ID, cwd) {
			w.Projects = append(w.Projects, prompts.WorkspaceProject{Name: oneLine(p.Name), Slug: p.Slug, OneLine: clip(oneLine(p.OneLine), 200)})
		}
	}
	if d.Online {
		lctx, cancel := context.WithTimeout(ctx, workspaceListWait)
		list, err := s.sessions(lctx, d.ID)
		cancel()
		if err == nil {
			loc := s.location(ctx)
			for _, ss := range workspaceSessions(list, cwd) {
				ws := prompts.WorkspaceSession{Title: clip(oneLine(textfmt.FirstNonEmpty(ss.Title, path.Base(ss.Cwd))), 80), Agent: textfmt.FirstNonEmpty(ss.AgentLabel, ss.Agent, "claude"),
					Status: ss.Status, ID: ss.SessionID}
				if !ss.UpdatedAt.IsZero() {
					ws.When = ss.UpdatedAt.In(loc).Format("01-02 15:04")
				}
				if sub := sessionDir(ss); !sameDir(sub, cwd) {
					ws.Cwd = sub
				}
				w.Sessions = append(w.Sessions, ws)
			}
		} else {
			s.logf("assistant: workspace mention on %s: sessions: %v", d.ID, err)
		}
	}
	return prompts.RenderWorkspaceMention(w), true
}

// workspaceSessions are the agents (not the assistant's own conversations) whose folder is dir or under it, the most
// recently active first, at most workspaceSessionsMost.
func workspaceSessions(list []proto.Session, dir string) []proto.Session {
	var out []proto.Session
	for _, ss := range list {
		if ss.Assistant {
			continue
		}
		if d := sessionDir(ss); sameDir(d, dir) || underDir(d, dir) {
			out = append(out, ss)
		}
	}
	slices.SortStableFunc(out, func(a, b proto.Session) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	if len(out) > workspaceSessionsMost {
		out = out[:workspaceSessionsMost]
	}
	return out
}

// sessionDir is where a session works: the node-verified folder when it has one (docs/web/workspace-board.md).
func sessionDir(ss proto.Session) string {
	return textfmt.FirstNonEmpty(ss.WorkspaceCwd, ss.Cwd)
}

func sameDir(a, b string) bool {
	return a != "" && strings.TrimRight(a, `/\`) == strings.TrimRight(b, `/\`)
}

func underDir(sub, dir string) bool {
	d := strings.TrimRight(dir, `/\`)
	return d != "" && sub != "" && (strings.HasPrefix(sub, d+"/") || strings.HasPrefix(sub, d+`\`))
}
