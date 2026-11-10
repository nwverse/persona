package update

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// There is one personad: no file of persona's builds differently for the published binaries (the public tag of
// server/build-tags.txt is only the privacy scan's marker; its ts_omit_ tags only leave tailscale.com features out),
// so every hub can update to the release, ours included (docs/operations/hub-update.md "Taking the skill out of the binary").
func TestOneBuild(t *testing.T) {
	root := filepath.Join("..", "..") // server/
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
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
			if strings.HasPrefix(line, "//go:build") && strings.Contains(line, "public") {
				t.Errorf("%s builds differently with -tags public: %s", p, line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
