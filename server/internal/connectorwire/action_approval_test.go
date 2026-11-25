package connectorwire

import "testing"

func TestActionApprovalIDMatchesTheAgentAPIConnector(t *testing.T) {
	for seed, want := range map[string]string{
		"action:req-1": "odc_act2e4543bb02f8be7e3df11f9d03a5b176",
		"reply:c-1":    "odc_act3377cd5cf4a6521782b84569c214e914",
	} {
		if got := ActionApprovalID(seed); got != want {
			t.Fatalf("ActionApprovalID(%q) = %q, want %q", seed, got, want)
		}
	}
}
