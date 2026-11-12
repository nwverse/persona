package node

import (
	"io"
	"log"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// assistantWindowsLaunchCase is a native Windows path set the assistant's launch command quotes for Git Bash (#5487).
type assistantWindowsLaunchCase struct {
	name, launcher, profile, bin string
	managed                      bool
}

var assistantWindowsLaunchCases = []assistantWindowsLaunchCase{
	{"profile", "claude", `C:\Users\Example\.persona\profiles\claude`, `C:\Persona\personad.exe`, false},
	{"profile with a space", "claude", `C:\Users\Example User\.persona\profiles\claude`, `C:\Persona\personad.exe`, false},
	{"managed launcher", "work", `C:\Users\Example User\.persona\profiles\work`, `C:\Program Files\Persona\personad.exe`, true},
}

// script is the -ic script assistantCLICommand builds for c on goos, launched with shell.
func (c assistantWindowsLaunchCase) script(t *testing.T, goos, shell string) string {
	t.Helper()
	old := assistantCLIGOOS
	assistantCLIGOOS = goos
	t.Cleanup(func() { assistantCLIGOOS = old })
	n := &Node{bin: c.bin, home: t.TempDir(), dir: t.TempDir(), logger: log.New(io.Discard, "", 0)}
	if c.managed {
		n.managed.byID = map[string]proto.ManagedLauncher{c.launcher: {Name: c.launcher}}
	}
	cmd := n.assistantCLICommand(shell, c.launcher, n.dir, []string{"-p"}, []string{"CLAUDE_CONFIG_DIR=" + c.profile})
	i := slices.Index(cmd.Args, "-ic")
	if i < 0 || i+2 >= len(cmd.Args) || cmd.Args[i+2] != "persona" {
		t.Fatalf("not a -ic launch: %q", cmd.Args)
	}
	return cmd.Args[i+1]
}

// words are the words the launch script must run: the profile directory and the command, forward-slashed.
func (c assistantWindowsLaunchCase) words() []string {
	words := []string{"CLAUDE_CONFIG_DIR=" + strings.ReplaceAll(c.profile, `\`, "/"), c.launcher}
	if c.managed {
		words = []string{words[0], strings.ReplaceAll(c.bin, `\`, "/"), "launch", c.launcher}
	}
	return words
}

// wordsProbe prefixes a launch script with a function printing each of its words, so a shell reports how it parsed the
// script instead of running the launcher.
const wordsProbe = `words() { printf '%s\n' "$@"; }; words `

// On Windows the assistant's launch command crosses the native process → Git Bash argument boundary, which eats the
// backslashes shellQuote escapes and leaves an unmatched quote (#5487). Its paths go in with forward slashes, and a
// POSIX shell reads them back as the intended paths: sh here, Git Bash started natively on Windows (shellRun).
func TestAssistantCLICommandWindowsPathsUseForwardSlashes(t *testing.T) {
	for _, c := range assistantWindowsLaunchCases {
		t.Run(c.name, func(t *testing.T) {
			script := c.script(t, "windows", "bash")
			if strings.Contains(script, `\`) {
				t.Fatalf("backslash in the Git Bash script: %s", script)
			}
			out, err := shellRun(t, wordsProbe+script)
			if err != nil {
				t.Fatalf("the shell rejected %s: %v\n%s", script, err, out)
			}
			if got, want := out, strings.Join(c.words(), "\n")+"\n"; got != want {
				t.Fatalf("the shell read %q, want %q (script %s)", got, want, script)
			}
		})
	}
}

// Other platforms keep their paths exactly as shellQuote has them, backslashes included.
func TestAssistantCLICommandNonWindowsPathsUnchanged(t *testing.T) {
	c := assistantWindowsLaunchCase{launcher: "work", profile: `/home/a\b/profile`, bin: `/opt/a\b/personad`, managed: true}
	want := "CLAUDE_CONFIG_DIR=" + shellQuote(c.profile) + " " + shellQuote(c.bin) + ` launch work "$@"`
	if got := c.script(t, "linux", "/bin/sh"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
