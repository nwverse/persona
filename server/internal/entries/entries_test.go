package entries

import "testing"

func TestDecisionValidationDoesNotGuess(t *testing.T) {
	e := Entry{ID: "example", Type: "decision", Text: "Stage the change.", By: "agent", Trust: "agent", Status: "active", Scope: Scope{Executor: "assistant"}, Decision: &Decision{Options: []string{"Stage", "Release"}, Selected: []string{"Stage"}, Rejected: []string{"Release"}}}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	e.Decision.Rejected = []string{"Stage"}
	if e.Validate() == nil {
		t.Fatal("selected option also rejected")
	}
	e.Decision.Rejected = nil
	e.Decision.Selected = []string{"Unknown"}
	if e.Validate() == nil {
		t.Fatal("unknown option selected")
	}
	e.Decision = nil
	if e.Validate() == nil {
		t.Fatal("unstructured decision accepted")
	}
}
