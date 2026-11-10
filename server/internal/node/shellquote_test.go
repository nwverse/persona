package node

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// shellQuoteTexts are the texts the shellQuote tests read back: a backslash before a quote, at the end, doubled, quotes,
// line breaks and what the shells would expand outside quotes.
var shellQuoteTexts = []string{
	`a\';touch PWNED;#`,
	`trailing\`,
	`\\server\share`,
	`it's`,
	`\'`,
	`'\''`,
	"two\nlines",
	"$HOME $(touch PWNED) `touch PWNED`",
	`"double" and !bang`,
	"",
}

// shellQuote writes no backslash inside its single quotes: fish still takes \' and \\ as escapes there (#2867), so a
// backslash goes outside them, escaped, where the sh family and fish read it alike. A POSIX reading gives the text back
// as one word.
func TestShellQuoteKeepsBackslashesOutOfQuotes(t *testing.T) {
	for _, s := range shellQuoteTexts {
		q := shellQuote(s)
		in := false
		for i := 0; i < len(q); i++ {
			switch {
			case q[i] == '\'':
				in = !in
			case q[i] == '\\' && in:
				t.Errorf("shellQuote(%q) = %s: a backslash inside the quotes at byte %d", s, q, i)
			case q[i] == '\\':
				i++ // escaped outside the quotes
			}
		}
		if got := shellWords(q); !reflect.DeepEqual(got, []string{s}) {
			t.Errorf("shellQuote(%q) = %s reads back as %q", s, q, got)
		}
	}
}

// shellQuote's word is the text itself in every shell a pane may run, fish included: before #2867, fish read the POSIX
// quoting of a\';touch PWNED;# as a short word, then ran touch. A shell that is not installed is skipped.
func TestShellQuoteReadsBackInRealShells(t *testing.T) {
	for _, name := range []string{"sh", "bash", "zsh", "dash", "fish"} {
		t.Run(name, func(t *testing.T) {
			bin, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("%s is not installed", name)
			}
			dir := t.TempDir()
			for _, s := range shellQuoteTexts {
				out, err := realShell(t, bin, dir, "printf '<%s>' "+shellQuote(s)).Output()
				if err != nil || string(out) != "<"+s+">" {
					t.Errorf("%s read %q back as %q (%v)", name, s, out, shellErr(err))
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "PWNED")); err == nil {
				t.Errorf("%s ran text that was meant to stay inside its quotes", name)
			}
		})
	}
}

// In a fish pane the start file runs through `source` (startLine), so fish parses the compatible CLI's command: its
// first message stays one literal argument there too, a backslash before a quote included (#2867).
func TestOtherAgentStartFileInFish(t *testing.T) {
	bin, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	home := t.TempDir()
	for _, prompt := range shellQuoteTexts[:6] {
		file, err := writeStartScript(filepath.Join(home, "node"), otherAgentCommand("wrapper", "", prompt))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(filepath.Join(home, "args"))
		script := `function wrapper; printf '%s\n' (count $argv) $argv >"$HOME/args"; end; ` + startLine(file, startID, bin)
		if out, err := realShell(t, bin, home, script).CombinedOutput(); err != nil {
			t.Fatalf("%q: %v: %s", prompt, err, out)
		}
		if got, _ := os.ReadFile(filepath.Join(home, "args")); string(got) != "1\n"+prompt+"\n" {
			t.Errorf("first message %q reached the CLI as %q", prompt, got)
		}
		if _, err := os.Stat(filepath.Join(home, "PWNED")); err == nil {
			t.Fatalf("first message %q ran a command in fish", prompt)
		}
	}
}

// realShell runs script with the shell bin in dir, which is also its home: no user configuration (fish's --no-config).
// The deadline is only a guard against a hang.
func realShell(t *testing.T, bin, dir, script string) *exec.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	args := []string{"-c", script}
	if filepath.Base(bin) == "fish" {
		args = append([]string{"--no-config"}, args...)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "XDG_DATA_HOME=" + dir, "PATH=/usr/bin:/bin"}
	return cmd
}

// shellErr is err with what the shell wrote to stderr.
func shellErr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return errors.New(err.Error() + ": " + string(ee.Stderr))
	}
	return err
}
