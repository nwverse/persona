package node

import (
	"testing"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestHostHierarchyUsesPublicPaneIdentity(t *testing.T) {
	list := []proto.Session{
		{SessionID: "public-parent", PaneID: "orca:tab:parent", Agent: "codex"},
		{SessionID: "public-worker", PaneID: "orca:tab:worker", Agent: "codex"},
		{SessionID: "grandchild", PaneID: "orca:tab:grandchild"},
		{SessionID: "missing", PaneID: "orca:tab:missing"},
		{SessionID: "other-machine", MachineID: "other", PaneID: "orca:tab:parent"},
	}
	agents := map[string]host.Agent{
		"public-worker": {PaneID: "orca:tab:worker", ParentPaneID: "orca:tab:parent"},
		"grandchild":    {PaneID: "orca:tab:grandchild", ParentPaneID: "orca:tab:worker"},
		"missing":       {PaneID: "orca:tab:missing", ParentPaneID: "orca:tab:gone"},
	}
	resolveHostHierarchy(list, agents)
	if list[1].ParentAgentID() != "public-parent" ||
		list[2].ParentAgentID() != "public-worker" || list[3].ParentAgentID() != "" {
		t.Fatalf("incorrect public parent identities: %+v", list)
	}
	if list[1].IsCodexSubagent() || list[1].ParentSessionID != "" {
		t.Fatal("an Orca worker became a provider-native subagent")
	}
	for _, patch := range []proto.Session{
		{SessionID: "second-parent", PaneID: "orca:tab:parent"},
		{SessionID: "second-manager", Manager: "other", PaneID: "orca:tab:parent"},
	} {
		rows := append([]proto.Session{list[1], patch}, list[3])
		rows[0].SetParentAgentID("")
		resolveHostHierarchy(rows, agents)
		want := "second-parent"
		if patch.Manager != "" {
			want = ""
		}
		if rows[0].ParentAgentID() != want {
			t.Fatalf("manager identity not respected: %+v", rows)
		}
	}
	list = append(list, proto.Session{SessionID: "ambiguous", PaneID: "orca:tab:parent"})
	list[1].SetParentAgentID("") // each list build starts from unlinked rows
	resolveHostHierarchy(list, agents)
	if list[1].ParentAgentID() != "" {
		t.Fatal("ambiguous pane accepted as parent")
	}
}
