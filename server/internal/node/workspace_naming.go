package node

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"github.com/nwverse/persona/server/internal/workspacenaming"
)

func (n *Node) resolveWorkspaceNaming(ctx context.Context, raw json.RawMessage) (any, error) {
	var source workspacenaming.Source
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	preview := workspacenaming.Resolve(source, workspacenaming.NodeContext{Home: n.home, PersonaHome: n.dir})
	if preview.Plan == nil {
		return preview, nil
	}
	if n.offLimits(preview.Plan.RootPath) != nil {
		preview.Plan = nil
		preview.Status = "invalid"
		preview.Diagnostics = append(preview.Diagnostics, workspacenaming.Problem{Code: "base_scope_denied", Field: "base_directory", Severity: "error"})
		return preview, nil
	}
	for _, repo := range preview.Plan.Repositories {
		checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := exec.CommandContext(checkCtx, "git", "check-ref-format", "--branch", repo.TaskBranch).Run()
		cancel()
		if err != nil {
			code := "git_validation_unavailable"
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = "branch_invalid"
			}
			preview.Plan = nil
			preview.Status = "invalid"
			preview.Diagnostics = append(preview.Diagnostics, workspacenaming.Problem{Code: code, Field: "branch_template", RepoID: repo.RepoID, Severity: "error"})
			break
		}
	}
	return preview, nil
}
