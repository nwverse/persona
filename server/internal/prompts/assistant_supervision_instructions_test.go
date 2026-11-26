package prompts

import (
	"strings"
	"testing"
)

func TestSupervisionRoleInstructionsPreserveTheFullPolicy(t *testing.T) {
	roles := []SupervisionRole{SupervisionRoleMain, SupervisionRoleLeader, SupervisionRoleBackground, SupervisionRoleSide}
	rendered := make(map[SupervisionRole]string, len(roles))
	for _, role := range roles {
		text, err := RenderSupervisionInstructionsForRole(role)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if text == "" {
			t.Fatalf("%s: empty policy", role)
		}
		rendered[role] = text
	}
	full := RenderSupervisionInstructions()
	fullLines := strings.Split(full, "\n")
	for role, text := range rendered {
		at := 0
		for _, line := range strings.Split(text, "\n") {
			if line == "" {
				continue
			}
			for at < len(fullLines) && fullLines[at] != line {
				at++
			}
			if at == len(fullLines) {
				t.Fatalf("%s contains new, changed, or reordered policy line: %q", role, line)
			}
			at++
		}
	}
	leaderOnly := []string{
		"- A direction (leader conversation) supervises",
		"- Report upward only results",
		"- For each reached item and each reporting cycle",
	}
	backgroundOnly := []string{
		"- Background supervises for main",
		"- Background's initial role notice",
	}
	private := func(line string) bool {
		for _, prefixes := range [][]string{leaderOnly, backgroundOnly} {
			for _, prefix := range prefixes {
				if strings.HasPrefix(line, prefix) {
					return true
				}
			}
		}
		return false
	}
	for role, text := range rendered {
		for _, prefix := range leaderOnly {
			if strings.Contains(text, prefix) != (role == SupervisionRoleLeader) {
				t.Errorf("%s: leader-only rule %q leaked or disappeared", role, prefix)
			}
		}
		for _, prefix := range backgroundOnly {
			if strings.Contains(text, prefix) != (role == SupervisionRoleBackground) {
				t.Errorf("%s: background-only rule %q leaked or disappeared", role, prefix)
			}
		}
		for _, line := range fullLines {
			if line != "" && !private(line) && !strings.Contains(text, line) {
				t.Errorf("%s lost common policy line: %q", role, line)
			}
		}
	}
	// Together the role outputs cover every full-union line; no rule exists only in the compatibility renderer.
	for _, line := range fullLines {
		if line == "" {
			continue
		}
		covered := false
		for _, text := range rendered {
			if strings.Contains(text, line) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("full policy is not reconstructible from its roles; missing %q", line)
		}
	}
	if rendered[SupervisionRoleMain] != rendered[SupervisionRoleSide] {
		t.Fatal("main and side common policies differ without a role-specific reason")
	}
}

func TestSupervisionRoleInstructionsRejectUnknownRoles(t *testing.T) {
	for _, role := range []SupervisionRole{"", "worker", "MAIN"} {
		if text, err := RenderSupervisionInstructionsForRole(role); err == nil || text != "" {
			t.Fatalf("role %q: got %d bytes, %v; want fail-closed error", role, len(text), err)
		}
	}
}
