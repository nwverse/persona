package node

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/identity"
)

// healthDenyRules are the permission rules that keep an assistant conversation outside the health space off the
// health space's files (docs/assistant/health.md "How directories and memory are isolated", #3282): its directory (the assistant directory's sibling,
// and the data directory's in case they differ) and its Claude project directories (auto memory, transcripts). Deny
// rules hold over any allow rule: "Always allow in this session" and bypassPermissions included. A health conversation
// gets the other managers' health spaces only (health, health-<id>), never its own. Bash is not covered: a command can
// name a path any way it likes (the issue says what would).
func (n *Node) healthDenyRules(cwd string) []string {
	if cwd == "" {
		return nil
	}
	// A health conversation has its own space: every other manager's is denied to it (its own is not).
	own := ""
	if identity.SpaceOf(cwd) == identity.SpaceHealth {
		own = filepath.Base(filepath.Clean(cwd))
	}
	var dirs []string
	add := func(d string) {
		if d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	// Every manager's health space: the built-in's health, another's health-<id> (identity.HealthDirName), next to
	// the conversation's directory and in the data directory.
	roots := []string{filepath.Dir(cwd)}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Dir(identity.HealthDir(home)))
	}
	var candidates []string
	for _, root := range roots {
		if own != identity.SpaceHealth {
			candidates = append(candidates, filepath.Join(root, identity.SpaceHealth))
		}
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if e.IsDir() && e.Name() != identity.SpaceHealth && e.Name() != own && identity.IsManagerDirName(e.Name(), identity.SpaceHealth) {
				candidates = append(candidates, filepath.Join(root, e.Name()))
			}
		}
	}
	if own != "" { // the same space reached another way (a symlinked home) stays its own
		candidates = slices.DeleteFunc(candidates, func(d string) bool { return sameDir(d, cwd) })
	}
	for _, d := range candidates {
		add(filepath.Clean(d))
		if real, err := filepath.EvalSymlinks(d); err == nil {
			add(real) // Claude names a project after the directory as it resolved it
		}
	}
	paths := slices.Clone(dirs)
	for _, root := range n.projectDirs() {
		for _, d := range dirs {
			paths = append(paths, filepath.Join(root, nonAlnum.ReplaceAllString(d, "-")))
		}
	}
	var rules []string
	for _, p := range paths {
		p = filepath.ToSlash(p)
		if p[0] != '/' {
			p = "/" + p // a Windows drive path
		}
		for _, tool := range []string{"Read", "Edit"} { // Read covers the reading tools, Edit the writing ones
			rules = append(rules, tool+"(/"+p+"/**)")
		}
	}
	return rules
}

// healthReportRules let a health conversation write the records of the reports uploaded to it (reports/, docs/assistant/health.md
// "checkup reports") without the phone asking each time; nil outside the health space, and for a path the comma-joined
// --allowedTools could not carry.
func healthReportRules(cwd string) []string {
	if cwd == "" || identity.SpaceOf(cwd) != identity.SpaceHealth || strings.Contains(cwd, ",") {
		return nil
	}
	// Edit(path) covers Write too: Claude Code matches path rules on Read and Edit only.
	return []string{"Edit(/" + filepath.Join(cwd, "reports") + "/**)"}
}

// sameDir: a and b are the same directory (as they resolve), or the same path when either cannot be read.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}
