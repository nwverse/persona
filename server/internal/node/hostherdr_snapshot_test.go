package node

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// largeHerdrSnapshot has a split tab, a shell pane, and an empty workspace. The
// repeated pane metadata makes the snapshot exceed 8 KB while each small reply fits.
func largeHerdrSnapshot(t *testing.T, f *fakeHerdr) {
	t.Helper()
	workspaces := []any{
		map[string]any{"workspace_id": "w1", "label": "project", "number": 1, "focused": true},
		map[string]any{"workspace_id": "w2", "label": "empty", "number": 2},
	}
	tabs := []any{map[string]any{"workspace_id": "w1", "tab_id": "w1:t1", "label": "review", "number": 1, "focused": true}}
	agents := []any{
		claudeAgent(sidA, proto.StatusWorking, "w1:p2", "w1:t1", "w1", "/tmp/project", strings.Repeat("a", 1300)),
		claudeAgent(sidB, proto.StatusWorking, "w1:p1", "w1:t1", "w1", "/tmp/project", strings.Repeat("b", 1300)),
	}
	var panes []any
	byPane := map[string]any{}
	for i, a := range agents {
		a := a.(map[string]any)
		a["name"] = []string{"reviewer", "builder"}[i]
		a["tokens"] = map[string]string{"task": "sample"}
		byPane[a["pane_id"].(string)] = a
		pane := maps.Clone(a)
		delete(pane, "name") // pane.list omits names that agent.get supplies.
		panes = append(panes, pane)
	}
	panes = append(panes, map[string]any{"pane_id": "w1:p3", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": "/tmp/project"})
	layout := herdr.TabLayout{
		TabID: "w1:t1", WorkspaceID: "w1", FocusedPaneID: "w1:p2", Zoomed: true,
		Panes: []herdr.LayoutPane{
			{PaneID: "w1:p2", Focused: true, Rect: herdr.Rect{X: 40, Width: 40, Height: 24}},
			{PaneID: "w1:p1", Rect: herdr.Rect{Width: 40, Height: 24}},
			{PaneID: "w1:p3", Rect: herdr.Rect{X: 80, Width: 40, Height: 24}},
		},
		Splits: []herdr.LayoutSplit{{ID: "split_1", Direction: "right", Ratio: 1.0 / 3, Rect: herdr.Rect{Width: 120, Height: 24}}},
	}
	snap := snapshot(workspaces, tabs, agents, panes)
	s := snap["snapshot"].(map[string]any)
	s["layouts"] = []herdr.TabLayout{layout}
	s["focused_workspace_id"], s["focused_tab_id"], s["focused_pane_id"] = "w1", "w1:t1", "w1:p2"
	if b, _ := json.Marshal(snap); len(b) <= 8192 {
		t.Fatalf("snapshot fixture is too small: %d bytes", len(b))
	}
	if b, _ := json.Marshal(map[string]any{"panes": panes}); len(b)+100 >= 8192 {
		t.Fatalf("pane list fixture is too large: %d bytes", len(b))
	}
	f.reply("session.snapshot", snap)
	f.reply("workspace.list", map[string]any{"workspaces": workspaces})
	for method, rows := range map[string][]any{"tab.list": tabs, "pane.list": panes} {
		key := strings.TrimSuffix(method, ".list") + "s"
		f.handle(method, func(p map[string]any) any {
			switch p["workspace_id"] {
			case "w1":
				return map[string]any{key: rows}
			case "w2":
				return map[string]any{key: []any{}}
			default:
				t.Errorf("%s must be scoped to a listed workspace: %v", method, p)
				return herdrFail{Code: "workspace_not_found"}
			}
		})
	}
	f.handle("pane.layout", func(p map[string]any) any {
		if p["pane_id"] != "w1:p1" && p["pane_id"] != "w1:p2" && p["pane_id"] != "w1:p3" {
			t.Errorf("layout must name a listed pane: %v", p)
			return herdrFail{Code: "pane_not_found"}
		}
		return map[string]any{"layout": layout}
	})
	f.handle("agent.get", func(p map[string]any) any {
		if a, ok := byPane[str(p["target"])]; ok {
			return map[string]any{"agent": a}
		}
		t.Errorf("agent.get must name a detected agent: %v", p)
		return herdrFail{Code: "agent_not_found"}
	})
	f.reply("agent.list", map[string]any{"agents": agents})
}

func TestHerdrTruncatedSnapshotKeepsNodeLayout(t *testing.T) {
	n, f, _ := herdrNode(t)
	largeHerdrSnapshot(t, f)
	h := n.hosts.Get(host.Herdr)
	if err := n.refreshHosts(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	want := n.hostAgents[host.Herdr]
	wantLayout := n.layouts[host.Herdr]
	f.limitReplies(8192)
	f.reset()
	if err := n.refreshHosts(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	got := n.hostAgents[host.Herdr]
	if !reflect.DeepEqual(got, want) {
		t.Fatal("reconstructed agents differ from the whole snapshot")
	}
	gotLayout := n.layouts[host.Herdr]
	gotLayout.at = wantLayout.at
	if !reflect.DeepEqual(gotLayout, wantLayout) {
		t.Fatal("reconstructed spaces, slots, panes or pane indexes differ from the whole snapshot")
	}
	if len(gotLayout.panes) != 3 || gotLayout.spaces["w1"].Label != "project" || gotLayout.slots["w1:t1"].Label != "review" ||
		!reflect.DeepEqual(gotLayout.paneIndex, map[string]int{"w1:p1": 0, "w1:p2": 1, "w1:p3": 2}) {
		t.Fatalf("incomplete layout: %+v", gotLayout)
	}
	if len(f.trace("agent.list")) != 0 || len(f.trace("pane.layout")) != 1 {
		t.Fatalf("fallback should read one layout per tab and avoid agent.list: %v", f.trace())
	}
	w := workspaceList(t, n)
	if len(w) != 2 || w[0].Title != "project" || w[0].Cwd != "/tmp/project" || w[1].Title != "empty" || w[1].Cwd != "" {
		t.Fatalf("workspace catalog lost names or pane directories: %+v", w)
	}
}

func TestHerdrTruncatedSnapshotFallsBackToAgentsWhenSmallCallFails(t *testing.T) {
	for _, method := range []string{"workspace.list", "tab.list", "pane.list", "pane.layout", "agent.get"} {
		for _, truncated := range []bool{false, true} {
			name := method + "/error"
			if truncated {
				name = method + "/truncated"
			}
			t.Run(name, func(t *testing.T) {
				n, f, _ := herdrNode(t)
				largeHerdrSnapshot(t, f)
				h := n.hosts.Get(host.Herdr)
				if err := n.refreshHosts(context.Background(), h); err != nil {
					t.Fatal(err)
				}
				want := n.hostAgents[host.Herdr]
				if len(n.layouts[host.Herdr].spaces) == 0 {
					t.Fatal("whole snapshot did not establish a layout")
				}
				previous := n.layouts[host.Herdr]
				f.limitReplies(8192)
				f.reply(method, herdrFail{Code: "internal", Message: "list unavailable"})
				if truncated {
					f.reply(method, map[string]any{"padding": strings.Repeat("x", 9000)})
				}
				f.reset()
				err := n.refreshHosts(context.Background(), h)
				got := n.hostAgents[host.Herdr]
				full := !n.layouts[host.Herdr].at.Equal(previous.at)
				if err != nil || full || !reflect.DeepEqual(got, want) {
					t.Fatalf("agent fallback: full=%v, err=%v, agents match=%v", full, err, reflect.DeepEqual(got, want))
				}
				if !reflect.DeepEqual(n.layouts[host.Herdr], previous) {
					t.Fatal("failed reconstruction replaced the last complete layout")
				}
				if len(f.trace(method)) != 1 || len(f.trace("agent.list")) != 1 {
					t.Fatalf("small call failure must use agent.list: %v", f.trace())
				}
			})
		}
	}
}
