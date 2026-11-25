package agentref_test

import (
	"testing"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
)

// someRef is a valid session.
func someRef(t *testing.T) agentref.Ref {
	t.Helper()
	return domaintest.Ok(t, agentref.ParseRef("machine 1", "session 1"))
}

// someMachineID is a valid machine.
func someMachineID(t *testing.T) agentref.MachineID {
	t.Helper()
	return domaintest.Ok(t, agentref.ParseMachineID("machine 1"))
}

func TestTheZeroRefIsNoSessionParseRefMakes(t *testing.T) {
	var zero agentref.Ref
	if someRef(t) == zero || someRef(t).IsSame(zero) {
		t.Error("ParseRef made the zero Ref")
	}
	if zero.OnMachine(someMachineID(t)) {
		t.Error("the zero Ref is on a machine")
	}
}

func TestParseRefNamesEachEmptyPart(t *testing.T) {
	for _, c := range []struct {
		machine, session, want string
	}{
		{"", "session 1", "invalid agentref.machine.empty machine"},
		{"machine 1", " ", "invalid agentref.session.empty session"},
		{" ", "", "invalid agentref.machine.empty machine; invalid agentref.session.empty session"},
	} {
		if got := domaintest.Fails(t, agentref.ParseRef(c.machine, c.session)); got != c.want {
			t.Errorf("ParseRef(%q, %q) = %q, want %q", c.machine, c.session, got, c.want)
		}
	}
}

func TestParseMachineIDAndParseInteractionIDRefuseEmptyIDs(t *testing.T) {
	var noMachine agentref.MachineID
	if someMachineID(t) == noMachine {
		t.Error("ParseMachineID made the zero MachineID")
	}
	if got, want := domaintest.Fails(t, agentref.ParseMachineID(" ")), "invalid agentref.machine.empty machine"; got != want {
		t.Errorf("ParseMachineID = %q, want %q", got, want)
	}
	var noCard agentref.InteractionID
	if domaintest.Ok(t, agentref.ParseInteractionID("card 1")) == noCard {
		t.Error("ParseInteractionID made the zero InteractionID")
	}
	if got, want := domaintest.Fails(t, agentref.ParseInteractionID("")), "invalid agentref.card.empty card"; got != want {
		t.Errorf("ParseInteractionID = %q, want %q", got, want)
	}
}

func TestRefsKeyMaps(t *testing.T) {
	other := domaintest.Ok(t, agentref.ParseRef("machine 1", "session 2"))
	seen := map[agentref.Ref]int{someRef(t): 1, other: 2}
	if seen[someRef(t)] != 1 || seen[other] != 2 || len(seen) != 2 {
		t.Errorf("two sessions keyed %v", seen)
	}
	if !someRef(t).IsSame(someRef(t)) || someRef(t).IsSame(other) {
		t.Error("IsSame does not tell sessions apart by machine and session")
	}
}

func TestOnMachine(t *testing.T) {
	elsewhere := domaintest.Ok(t, agentref.ParseRef("machine 2", "session 1"))
	if !someRef(t).OnMachine(someMachineID(t)) || elsewhere.OnMachine(someMachineID(t)) {
		t.Error("OnMachine does not tell a session's machine")
	}
}

// refParts is the test's own visitor: it keeps the parts it is told.
type refParts struct {
	machine agentref.MachineID
	told    bool
}

func (r *refParts) VisitRef(machine agentref.MachineID, _ agentref.SessionID) {
	r.machine, r.told = machine, true
}

func TestAcceptTellsTheMachineAndTheSession(t *testing.T) {
	var got refParts
	someRef(t).Accept(&got)
	if !got.told || got.machine != someMachineID(t) {
		t.Error("Accept told another machine")
	}
}
