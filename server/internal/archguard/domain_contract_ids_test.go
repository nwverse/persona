package archguard

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/archguard/domainrules"
)

// contract-bare-id counts the contract layer's plain string and []string fields named *ID, *Id and *IDs under one key,
// "contracts", so a type that moves between contract packages changes no count and a new bare ID field anywhere in
// the layer raises it.
func TestContractBareID(t *testing.T) {
	contracts := []string{"internal/awire", "internal/bwire"}
	wire := func(pkg, body string) map[string]string {
		files := pkgFiles("internal/"+pkg, "contract", body)
		delete(files, "internal/"+pkg+"/rules_test.go")
		return files
	}
	const row = "// Row is a row.\ntype Row struct {\n\tOwnerID string\n\tPeerIDs []string\n\tName    string\n}\n"
	const named = "// PeerID names a peer.\ntype PeerID string\n\n// Link is a link.\ntype Link struct{ PeerID PeerID }\n"
	run := func(files map[string]string, baseFiles map[string]string, baseline, baseBaseline string) domainrules.Verdict {
		t.Helper()
		measure := func(f map[string]string) ([]domainrules.Finding, []domainrules.Count) {
			_, findings, counts := domainCase(t, f, contracts)
			return findings, counts
		}
		findings, counts := measure(files)
		pkgs, err := domainrules.LoadFiles(serverModule, merged(plantedKit(t), files))
		if err != nil {
			t.Fatal(err)
		}
		lists := domainrules.ParseLists("", "", "", baseline)
		var base *domainrules.Base
		if baseFiles != nil {
			bf, bc := measure(baseFiles)
			base = &domainrules.Base{Measure: domainrules.Measured(bf, bc), Lists: domainrules.ParseLists("", "", "", baseBaseline)}
		}
		return domainrules.Judge(findings, counts, pkgs, lists, base, "")
	}
	const line = "contracts contract-bare-id 3\n"
	two := merged(wire("awire", row), wire("bwire", named))

	// The count is the string and []string fields named like an id: OwnerID and PeerIDs, not Name, not a named type.
	if v := run(two, nil, "contracts contract-bare-id 2\n", ""); len(v.Failures) != 0 {
		t.Errorf("a layer at its count: %q", v.Failures)
	}

	// A new bare ID field fails with what is wrong and the fix, naming the field; no ignore marker exists.
	more := merged(wire("awire", row+"\n// Extra is a record.\ntype Extra struct{ MachineID string }\n"), wire("bwire", named))
	v := run(more, two, "contracts contract-bare-id 2\n", "contracts contract-bare-id 2\n")
	if len(v.Failures) != 1 {
		t.Fatalf("a new bare ID field: %q", v.Failures)
	}
	for _, want := range []string{"RULE contract-bare-id contracts adds a struct field of plain string or []string named *ID",
		"(count 3 > 2)", "FIX use a named ID type: the contract ID types from schema/wire/ids.schema.json once it lands (#15397), " +
			"or a named `type XID string` in the contract package", "New since the merge base: Extra.MachineID."} {
		if !strings.Contains(v.Failures[0], want) {
			t.Errorf("the failure lacks %q:\n%s", want, v.Failures[0])
		}
	}
	if strings.Contains(v.Failures[0], "persona:ignore") || strings.Contains(strings.ToLower(v.Failures[0]), "ignore") {
		t.Errorf("the rule offers an ignore marker: %s", v.Failures[0])
	}

	// A named ID type passes: the field is no longer plain string.
	namedExtra := merged(wire("awire", row+"\n// MachineID names a machine.\ntype MachineID string\n\n// Extra is a record.\n"+
		"type Extra struct{ MachineID MachineID }\n"), wire("bwire", named))
	if v := run(namedExtra, two, "contracts contract-bare-id 2\n", "contracts contract-bare-id 2\n"); len(v.Failures) != 0 {
		t.Errorf("a named ID type: %q", v.Failures)
	}

	// A type moving between two contract packages keeps the count: the key is the layer, not the package.
	moved := merged(wire("awire", ""), wire("bwire", named+"\n"+row))
	if v := run(moved, two, "contracts contract-bare-id 2\n", "contracts contract-bare-id 2\n"); len(v.Failures) != 0 {
		t.Errorf("a move between contract packages: %q", v.Failures)
	}

	// The rule landing: the merge base's baseline has no line yet, and its tree measures 2, so the line may start there
	// and no higher.
	if v := run(two, two, "contracts contract-bare-id 2\n", ""); len(v.Failures) != 0 {
		t.Errorf("the line a landing rule starts: %q", v.Failures)
	}
	if v := run(two, two, line, ""); len(v.Failures) != 1 || !strings.Contains(v.Failures[0], "gives room for 3, more than the 2") {
		t.Errorf("a first line above what the merge base measures: %q", v.Failures)
	}
	// Once the line is on the merge base, a raise fails as every count's does.
	if v := run(two, two, line, "contracts contract-bare-id 2\n"); len(v.Failures) != 1 {
		t.Errorf("a raised line: %q", v.Failures)
	}
	// Fields that take a named type lower the count, and the baseline notices.
	if v := run(wire("awire", named), two, "contracts contract-bare-id 2\n", "contracts contract-bare-id 2\n"); len(v.Failures) != 0 ||
		len(v.Notices) == 0 || !strings.Contains(v.Notices[0], "contracts contract-bare-id is 0, under its baseline 2") {
		t.Errorf("a falling count: failures %q notices %q", v.Failures, v.Notices)
	}
}
