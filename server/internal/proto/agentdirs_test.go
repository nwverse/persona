package proto

import (
	"slices"
	"testing"
)

// Hidden folders use the blocked folders' syntax: the same cleaning, validity and matcher.
func TestAgentDirPatterns(t *testing.T) {
	for in, want := range map[string]string{" ~/work/ ": "~/work", "/opt/b//": "/opt/b", "*multiremi*": "*multiremi*"} {
		if got, ok := CleanDirPattern(in); !ok || got != want {
			t.Errorf("clean %q = %q %v", in, got, ok)
		}
	}
	for _, everything := range []string{"", "~", "~/", "/", "//", "~/.."} {
		if _, ok := CleanDirPattern(everything); ok {
			t.Errorf("%q covers everything", everything)
		}
	}
	rules := []HiddenAgentDir{{Pattern: "/work", Enabled: true}, {Pattern: "/off", Enabled: false}, {Pattern: "*multiremi*", Enabled: true}}
	patterns := HiddenAgentDirPatterns(rules)
	if !slices.Equal(patterns, []string{"/work", "*multiremi*"}) {
		t.Fatalf("enabled patterns %v", patterns)
	}
	for dir, want := range map[string]string{
		"/work/a/src": "/work", "/home/me/.remi/multiremi/workspaces/x": "*multiremi*", "/off/a": "", "/workshop": "",
	} {
		if got := DirPatternMatchedBy(dir, "/home/me", patterns); got != want {
			t.Errorf("%q matched by %q, want %q", dir, got, want)
		}
	}
	if got := ClaudeDirBlockedBy("/work/a", "", []string{"/work"}); got != DirPatternMatchedBy("/work/a", "", []string{"/work"}) {
		t.Fatal("the two lists match differently")
	}
}

// A home reached through a link: either name of it is the same folder, in the dir and in the pattern.
func TestDirPatternMatchedOnLinkedHome(t *testing.T) {
	const home, real = "/home/me", "/data/home/me"
	for _, c := range []struct {
		dir, pattern, real string
		want               bool
	}{
		{"/data/home/me/.remi/multiremi/workspaces/x", "~/.remi/multiremi/*", real, true},
		{"/data/home/me/.remi/multiremi", "~/.remi/multiremi", real, true},
		{"/home/me/.remi/multiremi/w", "/data/home/me/.remi/multiremi", real, true},
		{"/data/home/me/.remi/multiremi/workspaces/x", "~/.remi/multiremi/*", "", false}, // the node never said
		{"/data/home/meow/.remi/multiremi/x", "~/.remi/multiremi/*", real, false},
		{"/data/home/me/code", "~/.remi/multiremi/*", real, false},
	} {
		if got := DirPatternMatchedOn(c.dir, home, c.real, []string{c.pattern}) != ""; got != c.want {
			t.Errorf("%q by %q (real %q) = %v", c.dir, c.pattern, c.real, got)
		}
	}
	for dir, want := range map[string]string{"/data/home/me": home, "/data/home/me/x/": "/home/me/x", "/data/home/meow": "/data/home/meow", "/opt": "/opt"} {
		if got := HomeNamed(dir, home, real); got != want {
			t.Errorf("HomeNamed(%q) = %q", dir, got)
		}
	}
}
