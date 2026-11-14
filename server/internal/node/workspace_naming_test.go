package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/workspacenaming"
)

func TestWorkspaceNamingUsesTargetHomeWithoutEngineOrCreation(t *testing.T) {
	for range 2 {
		home := t.TempDir()
		n := &Node{home: home, dir: filepath.Join(home, ".persona")}
		source := workspacenaming.Source{TodoID: "todo_example", Title: "Example", PlannedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), Timezone: "UTC", Rules: workspacenaming.Defaults(), Layout: repository.LayoutRoot, Repositories: repository.PreparationSnapshot{Repositories: []repository.PreparationRepository{{RepoID: "web", RequestedRef: repository.RequestedRef{Kind: "default"}}}}}
		raw, _ := json.Marshal(source)
		out, err := n.handle(t.Context(), "workspace.naming.resolve", raw)
		if err != nil {
			t.Fatal(err)
		}
		preview := out.(workspacenaming.Preview)
		if preview.Plan == nil || preview.Plan.RootPath != filepath.Join(home, "workspace", "task-todo_example") || preview.Plan.Repositories[0].RelativePath != "." {
			t.Fatal(preview)
		}
		files, err := os.ReadDir(home)
		if err != nil || len(files) != 0 {
			t.Fatal("preview created resources", files, err)
		}
	}
}
