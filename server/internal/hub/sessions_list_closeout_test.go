package hub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// A turn that is not finished reaches the lists as it is reported: an empty phase, its line, and the row's part of
// the closeout (docs/assistant/agent-work.md "Closeout"). The phase is written out, empty, for the apps that decode
// it as a plain string.
func TestListRowOfAnUnfinishedTurn(t *testing.T) {
	row := runtimeSummary(proto.Session{SessionID: "s1", State: proto.StateUnread, Work: &proto.WorkState{
		Line: "Not finished: the staging key",
		Closeout: &proto.Closeout{Status: proto.CloseoutNotDone, Missing: "the staging key", Did: []string{"moved the cache"},
			Verified: "untested", Merged: proto.CloseoutNo}}})
	w := row.Work
	if w == nil || w.Phase != "" || w.Line != "Not finished: the staging key" || row.State != proto.StateUnread {
		t.Fatalf("row work %+v state %q", w, row.State)
	}
	if c := w.Closeout; c == nil || c.Status != proto.CloseoutNotDone || c.Missing != "the staging key" || c.Merged != proto.CloseoutNo ||
		len(c.Did) != 0 || c.Verified != "" {
		t.Fatalf("row closeout %+v", w.Closeout)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"work":{"phase":"","line":"Not finished: the staging key"`) {
		t.Fatalf("wire: %s", raw)
	}
}
