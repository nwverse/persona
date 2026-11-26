package larkgateport_test

import (
	"testing"

	"github.com/nwverse/persona/server/internal/archguard/domainrules"
	"github.com/nwverse/persona/server/internal/moduleswitch"
	"github.com/nwverse/persona/server/internal/node/larkgateport"
)

// The domain rules for this package and the code that imports it (docs/server/architecture.md "Domain model").
func TestDomainRules(t *testing.T) { domainrules.Check(t) }

func TestGetReturnsNilWhenUnprovided(t *testing.T) {
	if got := larkgateport.Get(moduleswitch.Set{}); got != nil {
		t.Fatalf("Get without composition root = %v, want nil", got)
	}
}
