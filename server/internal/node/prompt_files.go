package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/promptcontent"
	"github.com/nwverse/persona/server/internal/prompts"
)

type taskInputKey struct{}

// selectedFiles uses the same path guard as the file viewer, on the executing node, on every turn.
// Files are references to current content, never stored snapshots or permission exemptions.
func (n *Node) selectedFiles(ctx context.Context, text, cwd, launcher, runtime string) ([]prompts.AssistantSelectedFile, error) {
	refs, err := promptcontent.Files(text)
	if err != nil {
		return nil, refusal("file_unavailable", i18n.T(i18n.From(ctx), "assistant-schedule.selected_file_unavailable"), nil)
	}
	if len(refs) == 0 {
		return nil, nil
	}
	if ctx.Value(taskInputKey{}) != true {
		return nil, refusal("file_unavailable", i18n.T(i18n.From(ctx), "assistant-schedule.selected_file_unavailable"), nil)
	}
	if n.assistantRuntime(launcher) != nil {
		return nil, refusal("file_unavailable", i18n.T(i18n.From(ctx), "assistant-schedule.file_runtime_unsupported"), nil)
	}
	n.mu.Lock()
	machine := n.machineID
	n.mu.Unlock()
	base, _, err := n.statSafe(cwd)
	if err != nil {
		return nil, refusal("file_unavailable", i18n.T(i18n.From(ctx), "assistant-schedule.selected_file_unavailable"), nil)
	}
	var out []prompts.AssistantSelectedFile
	for _, ref := range refs {
		if ref.Machine != machine || ref.Cwd != cwd || ref.Launcher != launcher || ref.Runtime != runtime {
			return nil, refusal("file_unavailable", i18n.T(i18n.From(ctx), "assistant-schedule.selected_file_unavailable"), nil)
		}
		path := n.resolvePath(ref.Path, cwd)
		if path == "" || strings.TrimSpace(ref.Path) != ref.Path {
			return nil, refusal("file_unavailable", i18n.T(i18n.From(ctx), "assistant-schedule.selected_file_unavailable"), nil)
		}
		real, st, e := n.statSafe(path)
		if e != nil || !st.Mode().IsRegular() {
			return nil, refusal("file_unavailable", i18n.T(i18n.From(ctx), "assistant-schedule.selected_file_unavailable"), nil)
		}
		if !withinTaskDirectory(base, real) || strings.ContainsAny(real, "\r\n") {
			return nil, refusal("file_unavailable", i18n.T(i18n.From(ctx), "assistant-schedule.selected_file_unavailable"), nil)
		}
		f, e := os.Open(real)
		if e != nil {
			return nil, refusal("file_unavailable", i18n.T(i18n.From(ctx), "assistant-schedule.selected_file_unavailable"), nil)
		}
		_ = f.Close()
		out = append(out, prompts.AssistantSelectedFile{Name: filepath.Base(real), Path: real})
	}
	return out, nil
}

func (n *Node) validatePromptContent(ctx context.Context, id, cwd, launcher, text string, execution modelExecution) error {
	if _, err := n.selectedFiles(ctx, text, cwd, launcher, execution.Runtime); err != nil {
		return err
	}
	_, err := n.selectedSkills(ctx, id, cwd, launcher, text, execution)
	return err
}
