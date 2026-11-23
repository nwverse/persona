package assistant

import (
	"reflect"
	"testing"
)

// What the plan keeps of a start (storedLaunch): the task's own launcher, model, effort, speed and tier, never what the
// try resolved for it; what the try put in place of the resolved launch (a launcher the computer has for a default it
// lacks, what an engine ran instead) unless a policy decided it; the permission the try settled unless a policy's preset
// brought it; the task's own ultracode where a policy filled it in.
func TestStoredLaunchIsTheTasksOwn(t *testing.T) {
	on := true
	for _, c := range []struct {
		name   string
		task   PlanTask // what the try left on the task (resolved, fitted, run)
		own    startPick
		filled startFill
		named  startNamed
		want   startPick
	}{
		{"read back before any try", PlanTask{Launcher: "first", Model: "model-one", Tier: "deep"}, startPick{}, startFill{}, startNamed{},
			startPick{launcher: "first", model: "model-one", tier: "deep"}},
		{"a tier or the default resolved it",
			PlanTask{Launcher: "first", Model: "model-one", Effort: "high", Speed: "fast", Tier: "deep", Permission: "bypassPermissions"},
			startPick{tier: "deep", permission: "default"}, startFill{}, startNamed{},
			startPick{tier: "deep", permission: "bypassPermissions"}},
		{"the computer lacked the default", PlanTask{Launcher: "claude", Effort: "low"},
			startPick{model: "model-three", effort: "low"}, startFill{}, startNamed{launcher: true, noModel: true},
			startPick{launcher: "claude", effort: "low"}},
		{"an engine stood in with the model it offers", PlanTask{Launcher: "second", Model: "model-one", Effort: "high"},
			startPick{tier: "deep"}, startFill{}, startNamed{launcher: true},
			startPick{launcher: "second"}},
		{"an engine ran its own default model", PlanTask{Launcher: "first", Effort: "high"},
			startPick{model: "model-three"}, startFill{}, startNamed{noModel: true},
			startPick{}},
		{"an engine ran another effort", PlanTask{Launcher: "first", Model: "model-one", Effort: "low"},
			startPick{effort: "high"}, startFill{}, startNamed{effort: true},
			startPick{effort: "low"}},
		{"an engine lowered the policy's effort", PlanTask{Launcher: "first", Effort: "low"},
			startPick{}, startFill{effort: true}, startNamed{effort: true},
			startPick{}},
		{"an engine replaced a policy preset's launcher", PlanTask{Launcher: "second", Model: "model-four", Tier: "", Permission: "bypassPermissions"},
			startPick{permission: "acceptEdits"}, startFill{preset: true}, startNamed{launcher: true},
			startPick{permission: "acceptEdits"}},
		{"a policy's ultracode", PlanTask{Launcher: "first", Ultracode: &on}, startPick{}, startFill{ultracode: true}, startNamed{},
			startPick{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			task := c.task
			if c.name != "read back before any try" {
				own := c.own
				task.callerPick = &own
			}
			task.startFilled, task.startNamed = c.filled, c.named
			if got := task.storedLaunch(); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("stored %+v, want %+v", got, c.want)
			}
		})
	}
}
