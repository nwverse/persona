package node

import "testing"

// The App's command line loses the login shell around it as a rollout's argv does; anything else is kept whole.
func TestCodexShellArgv(t *testing.T) {
	for in, want := range map[string]string{
		`/bin/bash -lc 'git status'`:         `git status`,
		`/bin/zsh -lc 'echo '\''hi'\'''`:     `echo 'hi'`,
		`bash -c "ls -la"`:                   `ls -la`,
		`git status`:                         `git status`,
		`/usr/bin/env python3 -c 'print(1)'`: `/usr/bin/env python3 -c 'print(1)'`,
		`/bin/bash -lc 'unterminated`:        `'unterminated`,
	} {
		argv := codexShellArgv(in)
		if got := argv[len(argv)-1]; got != want {
			t.Errorf("codexShellArgv(%q) = %q, want inner %q", in, argv, want)
		}
	}
}
