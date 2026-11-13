package node

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Codex's project methods change metadata only; they do not load a thread or start
// a turn. Call them only for an explicit project operation. The app-server owns
// its database format and idempotency, so Persona never edits that database or the
// desktop app's global-state JSON itself.
type codexDesktopProject struct {
	ID       string                    `json:"id"`
	Name     string                    `json:"name"`
	Roots    []codexDesktopProjectRoot `json:"roots"`
	Position int64                     `json:"position"`
}

type codexDesktopProjectRoot struct {
	Path string `json:"path"`
}

// A separate seam for project tests: they must never start the user's Codex.
var runCodexProjectServer = withCodexAppServer

// listCodexDesktopProjects reads every page in the app-server's position order.
// A broken/repeated cursor is an error rather than an infinite startup loop.
func listCodexDesktopProjects(call codexAppCall) ([]codexDesktopProject, error) {
	var projects []codexDesktopProject
	var cursor *string
	seenCursors := map[string]bool{}
	seenIDs := map[string]bool{}
	for {
		var response struct {
			Data       []codexDesktopProject `json:"data"`
			NextCursor *string               `json:"nextCursor"`
		}
		if err := call("project/list", map[string]any{"cursor": cursor, "limit": 100, "sortKey": "position"}, &response); err != nil {
			return nil, err
		}
		for _, project := range response.Data {
			if strings.TrimSpace(project.ID) == "" {
				return nil, errors.New("project/list: project has no id")
			}
			if seenIDs[project.ID] {
				continue
			}
			seenIDs[project.ID] = true
			projects = append(projects, project)
		}
		if response.NextCursor == nil {
			return projects, nil
		}
		if len(seenCursors) >= 100 || *response.NextCursor == "" || seenCursors[*response.NextCursor] {
			return nil, errors.New("project/list: invalid or repeated pagination cursor")
		}
		seenCursors[*response.NextCursor] = true
		cursor = response.NextCursor
	}
}

// codexDesktopProjectIDForDir selects the most specific registered root. Windows
// spelling differences and symlinks are normalized as for the desktop catalog;
// sibling names with a shared prefix do not match. No project is created here.
func codexDesktopProjectIDForDir(projects []codexDesktopProject, cwd string) string {
	if !filepath.IsAbs(cwd) {
		return ""
	}
	cwd = folderKey(realPath(filepath.Clean(cwd)))
	var best codexDesktopProject
	bestRoot := ""
	for _, project := range projects {
		if project.ID == "" {
			continue
		}
		for _, root := range project.Roots {
			if !filepath.IsAbs(root.Path) {
				continue
			}
			path := folderKey(realPath(filepath.Clean(root.Path)))
			if !pathWithin(cwd, path) {
				continue
			}
			if len(path) > len(bestRoot) || len(path) == len(bestRoot) &&
				(project.Position < best.Position || project.Position == best.Position && project.ID < best.ID) {
				best, bestRoot = project, path
			}
		}
	}
	return best.ID
}

// createCodexDesktopProject binds an existing directory to a new native project.
// The caller must persist and reuse idempotencyKey when retrying the same action;
// generating a fresh key after a lost response could create a second project.
func (n *Node) createCodexDesktopProject(ctx context.Context, name, cwd, idempotencyKey string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("项目名称不能为空")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return "", errors.New("新建 Codex 项目需要稳定的请求标识")
	}
	workspace, err := n.resolveWorkspace(cwd)
	if err != nil {
		return "", err
	}
	cli, err := codexAppCLI()
	if err != nil {
		return "", fmt.Errorf("没找到 Codex，不能新建项目：%w", err)
	}
	var response struct {
		Project codexDesktopProject `json:"project"`
	}
	err = runCodexProjectServer(ctx, cli, n.codexHome(), workspace.Cwd, func(call codexAppCall) error {
		return call("project/create", map[string]any{
			"name": name, "roots": []codexDesktopProjectRoot{{Path: workspace.Cwd}}, "idempotencyKey": idempotencyKey,
		}, &response)
	})
	if err != nil {
		return "", err // no automatic retry after an ambiguous write
	}
	if strings.TrimSpace(response.Project.ID) == "" {
		return "", errors.New("project/create: response has no project id")
	}
	return response.Project.ID, nil
}

// updateCodexDesktopProject changes an existing project's name and/or directory
// bindings. A nil roots slice leaves its roots unchanged; an empty slice clears
// them. Unmentioned metadata is preserved by the app-server.
func (n *Node) updateCodexDesktopProject(ctx context.Context, projectID, name string, roots []string) error {
	if strings.TrimSpace(projectID) == "" {
		return errors.New("Codex 项目标识不能为空")
	}
	params := map[string]any{"projectId": projectID}
	name = strings.TrimSpace(name)
	if name != "" {
		params["name"] = name
	}
	cwd := n.home
	if roots != nil {
		resolved := make([]codexDesktopProjectRoot, 0, len(roots))
		seen := map[string]bool{}
		for _, root := range roots {
			workspace, err := n.resolveWorkspace(root)
			if err != nil {
				return err
			}
			key := folderKey(workspace.Cwd)
			if !seen[key] {
				seen[key] = true
				resolved = append(resolved, codexDesktopProjectRoot{Path: workspace.Cwd})
			}
		}
		params["roots"] = resolved
		if len(resolved) > 0 {
			cwd = resolved[0].Path
		}
	}
	if len(params) == 1 {
		return errors.New("没有要更新的 Codex 项目名称或目录")
	}
	cli, err := codexAppCLI()
	if err != nil {
		return fmt.Errorf("没找到 Codex，不能更新项目：%w", err)
	}
	var response struct {
		Project codexDesktopProject `json:"project"`
	}
	if err := runCodexProjectServer(ctx, cli, n.codexHome(), cwd, func(call codexAppCall) error {
		return call("project/update", params, &response)
	}); err != nil {
		return err
	}
	if response.Project.ID != projectID {
		return errors.New("project/update: response names a different project")
	}
	return nil
}

// readCodexProjectSelection is a read-only snapshot seam for selection tests.
var readCodexProjectSelection = codexDesktopProjectSnapshot

// parseCodexDesktopProjectID resolves a catalog source id to the current home's
// native API id. Migrated projects keep their legacy catalog identity, so a
// verified native-to-legacy alias must be reversed before using projectId.
func parseCodexDesktopProjectID(home, sourceID string) (string, error) {
	if sourceID == "" {
		return "", nil
	}
	canonicalHome := canonicalCodexHome(home, "")
	if canonicalHome == "" {
		return "", errors.New("invalid Codex home for project selection")
	}
	id, ok := strings.CutPrefix(sourceID, codexDesktopProjectID(canonicalHome, ""))
	if !ok && home != canonicalHome {
		id, ok = strings.CutPrefix(sourceID, codexDesktopProjectID(home, ""))
	}
	if !ok || strings.TrimSpace(id) == "" {
		return "", errors.New("the selected Codex project belongs to a different profile or has an invalid identifier")
	}
	snapshot, err := readCodexProjectSelection(canonicalHome)
	if err != nil {
		return "", fmt.Errorf("read selected Codex project: %w", err)
	}
	// A card created by Persona may still hold the native id after the desktop
	// migrates its catalog row to the equivalent legacy id.
	if _, exists := snapshot.NativeProjectIDs[id]; exists {
		return id, nil
	}
	if _, exists := snapshot.Projects[id]; !exists {
		return "", errors.New("the selected Codex project no longer exists")
	}
	nativeID := ""
	for native, canonical := range snapshot.NativeProjectIDs {
		if canonical != id {
			continue
		}
		if nativeID != "" && nativeID != native {
			return "", errors.New("the selected Codex project has ambiguous native project mappings")
		}
		nativeID = native
	}
	if nativeID == "" {
		return "", errors.New("the selected legacy Codex project is not available through the desktop project API yet; open or update it in Codex first")
	}
	return nativeID, nil
}

// selectCodexDesktopProject never substitutes another project for an explicit
// selection, even if multiple projects share a directory. Live API roots are
// checked immediately before thread/start so stale cards cannot misassign chats.
func selectCodexDesktopProject(projects []codexDesktopProject, cwd, projectID string) (string, error) {
	if projectID == "" {
		return codexDesktopProjectIDForDir(projects, cwd), nil
	}
	for _, project := range projects {
		if project.ID != projectID {
			continue
		}
		if codexDesktopProjectIDForDir([]codexDesktopProject{project}, cwd) == projectID {
			return projectID, nil
		}
		return "", errors.New("the selected directory is outside the selected Codex project's roots")
	}
	return "", errors.New("the selected Codex project is no longer available from the desktop project API")
}
