package proto

import (
	"path/filepath"
	"strings"
)

// ExpandHome makes a leading "~" in p the given machine's home: "~" is home itself, "~/x" (or `~\x` on Windows) is x under home. Anything
// else, and every p when home is unknown (""), comes back unchanged. Only the user's own home is meant: "~user" stays.
func ExpandHome(p, home string) string {
	if home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	if filepath.Separator == '\\' {
		if rest, ok := strings.CutPrefix(p, `~\`); ok {
			return filepath.Join(home, rest)
		}
	}
	return p
}
