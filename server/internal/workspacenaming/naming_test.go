package workspacenaming

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/repository"
)

func source() Source {
	id, _ := repository.Parse("https://github.com/example/web", "")
	return Source{Manager: "example", TodoID: "todo_123", Title: "Fix / Login.. Bug", MachineID: "computer", PlannedAt: time.Date(2026, 11, 13, 16, 30, 0, 0, time.UTC), Timezone: "Asia/Shanghai", Rules: Defaults(), Layout: repository.LayoutContainer,
		Repositories: repository.PreparationSnapshot{SchemaVersion: 1, Manager: "example", TodoID: "todo_123", Repositories: []repository.PreparationRepository{{RepoID: "web", RequestedRef: repository.RequestedRef{Kind: "default"}, SourceSnapshot: repository.SourceSnapshot{Identity: id}}}}}
}
func TestDeterministicOperationAndLayouts(t *testing.T) {
	s := source()
	s.Rules.TaskDirectoryTemplate = "{yyyy}{mmdd}/{task-id}-{task-slug}"
	p := Resolve(s, NodeContext{Home: "/home/example", PersonaHome: "/home/example/.persona"})
	if p.Plan == nil {
		t.Fatal(p.Diagnostics)
	}
	if p.Plan.RootPath != "/home/example/workspace/20261114/todo_123-fix-login-bug" || p.Plan.Repositories[0].RelativePath != "web" {
		t.Fatal(p.Plan)
	}
	if p.Plan.Repositories[0].RequestedRef.Kind != "default" {
		t.Fatal("ref lost")
	}
	raw, _ := json.Marshal(p.Plan)
	var retry Plan
	if err := json.Unmarshal(raw, &retry); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*p.Plan, retry) {
		t.Fatal("persisted operation changed")
	}
	s.Title = "Changed tomorrow"
	s.PlannedAt = s.PlannedAt.Add(48 * time.Hour)
	if retry.RootPath != p.Plan.RootPath || retry.Digest != p.Plan.Digest {
		t.Fatal("retry renamed")
	}
	s = source()
	s.Layout = repository.LayoutRoot
	p = Resolve(s, NodeContext{Home: "/Users/example"})
	if p.Plan == nil || p.Plan.Repositories[0].RelativePath != "." || p.Plan.Repositories[0].AbsolutePath != p.Plan.RootPath {
		t.Fatal(p)
	}
}
func TestTypedRefsAndOnlyUsedFields(t *testing.T) {
	for _, ref := range []repository.RequestedRef{{Kind: "default"}, {Kind: "tag", Value: "v1"}, {Kind: "ref", Value: "refs/pull/7/head"}, {Kind: "commit", Value: strings.Repeat("a", 40)}, {Kind: "branch", Value: "feature/login"}} {
		s := source()
		s.Repositories.Repositories[0].RequestedRef = ref
		p := Resolve(s, NodeContext{Home: "/home/example"})
		if p.Plan == nil || p.Plan.Repositories[0].RequestedRef != ref {
			t.Fatal(ref, p)
		}
		s.Rules.BranchTemplate = "persona/{branch}/{base-branch}"
		p = Resolve(s, NodeContext{Home: "/home/example"})
		if ref.Kind == "branch" {
			if p.Plan == nil || p.Plan.Repositories[0].TaskBranch != "persona/feature-login/feature-login" {
				t.Fatal(p)
			}
		} else if p.Status != "incomplete" {
			t.Fatal(ref, p)
		}
	}
}
func TestTemplateBoundaries(t *testing.T) {
	for _, template := range []string{"../x", "a/../b", "/absolute", "a//b", "a\\b", "{repo}", "{branch}", "{unknown}", "{task-id", "$(whoami)", ".persona/x", "C:/x"} {
		s := source()
		s.Rules.TaskDirectoryTemplate = template
		if p := Resolve(s, NodeContext{Home: "/home/example"}); p.Plan != nil {
			t.Fatal("accepted", template, p)
		}
	}
	s := source()
	s.Rules.BaseDirectory = "/home/example/.persona/data"
	if Resolve(s, NodeContext{Home: "/home/example", PersonaHome: "/home/example/.persona"}).Plan != nil {
		t.Fatal("state directory accepted")
	}
	s = source()
	s.Repositories.Repositories = append(s.Repositories.Repositories, s.Repositories.Repositories[0])
	s.Layout = repository.LayoutRoot
	if Resolve(s, NodeContext{Home: "/home/example"}).Plan != nil {
		t.Fatal("multi root accepted")
	}
}
func TestRulePatchInheritanceAndAtomicErrors(t *testing.T) {
	cur := Rules{BranchTemplate: "custom/{task-id}"}
	got, err := Patch(cur, []byte(`{"base_directory":"/work","branch_template":null}`))
	if err != nil || got.BaseDirectory != "/work" || got.BranchTemplate != "" {
		t.Fatal(got, err)
	}
	effective, sources := Effective(got, Rules{BranchTemplate: "task/{task-id}"})
	if effective.BaseDirectory != "/work" || effective.BranchTemplate != "task/{task-id}" || sources["task_directory_template"] != "default" {
		t.Fatal(effective, sources)
	}
	for _, raw := range []string{`{"branch_template":""}`, `{"unknown":"x"}`, `{"branch_template":"a","branch_template":"b"}`, `[]`, `{"task_directory_template":"{branch}"}`} {
		if _, err := Patch(cur, []byte(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	if got, err := Patch(cur, []byte(`null`)); err != nil || got != (Rules{}) {
		t.Fatal(got, err)
	}
}

func TestFrozenPlanVerificationDoesNotUseManagerPathFlavor(t *testing.T) {
	p := Resolve(source(), NodeContext{Home: "/home/example"}).Plan
	p.Node = NodeContext{Home: `C:\Users\example`, PathFlavor: "windows"}
	p.BasePath = `C:\Users\example\workspace`
	p.RootPath = p.BasePath + `\task-todo_123`
	p.Repositories[0].AbsolutePath = p.RootPath + `\web`
	p.Digest = Digest(*p)
	if err := CheckPlan(*p); err != nil {
		t.Fatal("snapshot was re-resolved on the manager", err)
	}
	p.RootPath += "-tampered"
	if err := CheckPlan(*p); err == nil {
		t.Fatal("tampered snapshot accepted")
	}
}
