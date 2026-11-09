package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProgramBeside(t *testing.T) {
	if got := ProgramBeside("/a/bin/personad", "persona-hub"); got != filepath.Join("/a/bin", "persona-hub") {
		t.Fatal(got)
	}
	if got := ProgramBeside(`C:\p\personad.exe`, "persona-hub"); !strings.HasSuffix(got, "persona-hub.exe") {
		t.Fatal(got)
	}
}

// SplitBeside takes the program only when it is there and says what bin says to `version -v`; otherwise it says why
// one there was passed over, and nothing when there is none.
func TestSplitBeside(t *testing.T) {
	dir := t.TempDir()
	bin, hub := filepath.Join(dir, "personad"), filepath.Join(dir, "persona-hub")
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	builds := map[string]string{bin: "0.1.0\nchannel release, build 7"}
	fail := map[string]error{}
	version := func(_ context.Context, p string) (string, error) { return builds[p], fail[p] }
	ctx := context.Background()

	if p, note := SplitBeside(ctx, bin, "persona-hub", version); p != "" || note != "" {
		t.Fatalf("none there: %q %q", p, note)
	}
	if err := os.Mkdir(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, note := SplitBeside(ctx, bin, "persona-hub", version); p != "" || note != "" {
		t.Fatalf("a directory: %q %q", p, note)
	}
	if err := os.Remove(hub); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hub, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	builds[hub] = builds[bin]
	if p, note := SplitBeside(ctx, bin, "persona-hub", version); p != hub || note != "" {
		t.Fatalf("same build: %q %q", p, note)
	}
	for _, c := range []struct {
		name, theirs string
		theirsErr    error
		mineErr      error
		want         string
	}{
		{name: "another build", theirs: "0.0.9\nchannel release, build 6", want: `persona-hub is another build ("0.0.9", personad "0.1.0")`},
		{name: "says nothing", theirs: "", want: "is another build"},
		{name: "does not run", theirsErr: errors.New("exec format error"), want: "persona-hub does not run (exec format error)"},
		{name: "personad does not answer", theirs: builds[bin], mineErr: errors.New("killed"), want: "personad version -v failed (killed)"},
	} {
		builds[hub], fail[hub], fail[bin] = c.theirs, c.theirsErr, c.mineErr
		if p, note := SplitBeside(ctx, bin, "persona-hub", version); p != "" || !strings.Contains(note, c.want) {
			t.Errorf("%s: %q %q", c.name, p, note)
		}
	}
}
