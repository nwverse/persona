package hub

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

type runtimeDimensionsFields struct {
	ProjectID   string     `json:"project_id,omitempty"`
	ProjectName string     `json:"project_name,omitempty"`
	HarnessID   string     `json:"harness_id,omitempty"`
	HarnessName string     `json:"harness_name,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
}

func runtimeDimensions(mid string, s *proto.Session) runtimeDimensionsFields {
	projectID, projectName := runtimeProject(mid, s)
	harnessID, harnessName := runtimeHarness(s)
	fields := runtimeDimensionsFields{ProjectID: projectID, ProjectName: projectName, HarnessID: harnessID, HarnessName: harnessName}
	if s.Inventory != nil {
		fields.CreatedAt = s.Inventory.CreatedAt
	}
	return fields
}

func runtimeProject(mid string, s *proto.Session) (id, name string) {
	root := cmp.Or(s.RepoRoot, s.WorkspaceCwd, s.Cwd)
	projectName := cmp.Or(projectBase(root), strings.TrimSpace(s.SpaceName))
	if p := s.Inventory; p != nil {
		switch {
		case p.ProjectKind == "remote" && validProjectDigest(p.ProjectRepo):
			return "git:" + p.ProjectRepo, cmp.Or(projectName, "Unknown project")
		case p.ProjectKind == "local" && validProjectDigest(p.ProjectRepo):
			return "local:" + dimensionDigest(mid, p.ProjectRepo), cmp.Or(projectName, "Unknown project")
		case p.ProjectKind == "non_git" && projectName != "":
			return "name:" + dimensionDigest(strings.ToLower(projectName)), projectName
		}
	}
	// Older nodes and unavailable directories have no proof of non-Git.
	// Use the existing path or a machine-scoped space, never a display name.
	switch {
	case root != "":
		id = dimensionDigest(mid, "path", projectPath(root))
	case s.SpaceID != "" || s.WorkspaceID != "":
		id = dimensionDigest(mid, "space", cmp.Or(s.SpaceID, s.WorkspaceID))
	default:
		id = dimensionDigest(mid, "unknown")
	}
	return "unknown:" + id, cmp.Or(projectName, "Unknown project")
}

func validProjectDigest(s string) bool {
	return len(s) == sha256.Size*2 && strings.Trim(s, "0123456789abcdef") == ""
}

func dimensionDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func projectPath(p string) string {
	// A hub can receive a Windows checkout from a node on another OS.
	if strings.HasPrefix(p, `\\`) || len(p) >= 3 && p[1] == ':' && p[2] == '\\' {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	return path.Clean(p)
}

func projectBase(p string) string {
	if p == "" {
		return ""
	}
	base := path.Base(projectPath(p))
	if base == "." || base == "/" || len(base) == 2 && base[1] == ':' {
		return ""
	}
	return strings.TrimSpace(base)
}

func runtimeHarness(s *proto.Session) (id, name string) {
	id = strings.ToLower(strings.TrimSpace(s.Agent))
	label := strings.TrimSpace(s.AgentLabel)
	if id == "" {
		switch {
		case s.SourceKind == "codex" || strings.HasPrefix(s.SourceKind, "codex_") || s.SpaceKind == "codex_app":
			id = "codex"
		case s.SourceKind == proto.SourceRuntime:
			custom := cmp.Or(strings.TrimSpace(s.SourceInstanceID), label)
			if custom == "" {
				return "unknown", "Unknown harness"
			}
			return "runtime:" + dimensionDigest(strings.ToLower(custom)), cmp.Or(label, custom)
		default:
			id = "claude" // proto.Session's historical default
		}
	}
	switch id {
	case "claude", "claude-code", "claude_code":
		return "claude", "Claude Code"
	case "codex", "codex-cli", "codex_cli":
		return "codex", "Codex"
	case "opencode":
		return id, "OpenCode"
	case "pi":
		return id, "Pi"
	case "gemini", "gemini-cli":
		return "gemini", "Gemini CLI"
	case "cursor":
		return id, "Cursor"
	case "dsh":
		return id, "DSH"
	case "trae", "traex":
		return id, "TRAE"
	default:
		return id, cmp.Or(label, strings.TrimSpace(s.Agent))
	}
}
