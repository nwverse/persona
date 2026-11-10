package update

import (
	"bufio"
	"go/build/constraint"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// There is one personad: no file of persona's builds differently for the published binaries, so every hub can update
// to the release, ours included (docs/operations/hub-update.md "Taking the skill out of the binary"). The release is
// built with the tags of server/build-tags.txt (the public marker for the privacy scan and ts_omit_ tags that leave
// tailscale.com features out), while tests and plain builds use none: a persona file whose build constraint names
// any of them would compile one way in the release and another everywhere else.
func TestOneBuild(t *testing.T) {
	root := filepath.Join("..", "..") // server/
	b, err := os.ReadFile(filepath.Join(root, "build-tags.txt"))
	if err != nil {
		t.Fatal(err)
	}
	release := map[string]bool{}
	for _, tag := range strings.Split(strings.TrimSpace(string(b)), ",") {
		release[tag] = true
	}
	if !release["public"] {
		t.Fatalf("server/build-tags.txt has no public tag: %q", b)
	}
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "package ") {
				break
			}
			if !constraint.IsGoBuild(line) && !constraint.IsPlusBuild(line) {
				continue
			}
			expr, err := constraint.Parse(line)
			if err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
			// Eval visits every tag of the expression, both sides of && and || included.
			expr.Eval(func(tag string) bool {
				if release[tag] {
					t.Errorf("%s builds differently with the release tags (server/build-tags.txt), by %s: %s", p, tag, line)
				}
				return false
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
