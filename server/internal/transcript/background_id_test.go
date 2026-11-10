package transcript

import "testing"

func TestLaunchedBackgroundID(t *testing.T) {
	for _, c := range []struct{ result, want string }{
		{"Command running in background with ID: b85polnoj. Output is being written to: /tmp/tasks/b85polnoj.output.", "b85polnoj"},
		{"Command running in background with ID: bash_12", "bash_12"},
		{"Async agent launched successfully.\nagentId: a1b2c3 (use it to check on the agent)", "a1b2c3"},
		{"Workflow launched in background. Task ID: w5k2nd1ap\nSummary: build", "w5k2nd1ap"},
		{"Monitor started (task bqke1prjm, expires in 30m unless stopped)", "bqke1prjm"},
		{"Command timed out and was moved to the background (ID: bmv1). Output is being written to: /tmp/x", "bmv1"},
		{"Command was manually backgrounded by user with ID: bmk9rnfdz. Output is being written to: /tmp/y", "bmk9rnfdz"},
		{"<task_id>bx1</task_id><status>running</status>", ""},
		{"Exit code 0", ""},
	} {
		if got := LaunchedBackgroundID(c.result); got != c.want {
			t.Errorf("%q: %q, want %q", c.result, got, c.want)
		}
	}
}
