package assistant

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// readonlyTaskSafeBash reports whether a Bash permission request from an active read-only task is a
// strictly read-only inspection command (for example git show/diff/log, gh pr view/diff, rg/grep/cat
// within the task workspace). It fails closed on any unparseable syntax, command substitution,
// output redirection, inline environment override, mutating subcommand, dangerous flag, or credential path.
func readonlyTaskSafeBash(raw json.RawMessage, cwd string) bool {
	if len(raw) == 0 {
		return false
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return false
	}
	cmd := strings.TrimSpace(in.Command)
	if cmd == "" {
		return false
	}
	segs, ok := shellcmd.Split(cmd)
	if !ok || len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		if len(seg.Words) == 0 || seg.Subst || seg.ArgSubst || len(seg.Nested) > 0 {
			return false
		}
		if !safeReadonlyRedirect(seg.Redirect) {
			return false
		}
		// Reject inline VAR=val overrides (GIT_PAGER, GIT_EXTERNAL_DIFF, etc.) and wrappers (env/exec/command).
		if shellcmd.CommandIndex(seg.Words, map[string]string{}) != 0 {
			return false
		}
		if !safeReadonlySegment(seg.Words, cwd) {
			return false
		}
	}
	return true
}

func safeReadonlyRedirect(redirect string) bool {
	if strings.TrimSpace(redirect) == "" {
		return true
	}
	for _, tok := range strings.Fields(redirect) {
		if tok != "2>&1" && tok != "2>/dev/null" {
			return false
		}
	}
	return true
}

func safeReadonlySegment(words []string, cwd string) bool {
	switch words[0] {
	case "git":
		return safeReadonlyGit(words[1:], cwd)
	case "gh":
		return safeReadonlyGH(words[1:])
	case "rg":
		return safeReadonlySearch(words[1:], cwd, true)
	case "grep":
		return safeReadonlySearch(words[1:], cwd, false)
	case "cat":
		return safeReadonlyFileTool(words[1:], cwd, false, false, nil)
	case "head":
		return safeReadonlyFileTool(words[1:], cwd, false, false, map[string]bool{
			"-n": true, "-c": true, "--lines": true, "--bytes": true,
		})
	case "tail":
		return safeReadonlyFileTool(words[1:], cwd, true, false, map[string]bool{
			"-n": true, "-c": true, "--lines": true, "--bytes": true,
		})
	case "ls":
		return safeReadonlyFileTool(words[1:], cwd, false, false, map[string]bool{
			"-I": true, "--ignore": true, "--color": true, "--time-style": true, "--sort": true, "--format": true, "-w": true, "--width": true,
		})
	case "wc":
		return safeReadonlyFileTool(words[1:], cwd, false, true, nil)
	case "pwd":
		for _, a := range words[1:] {
			if a != "-L" && a != "-P" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func safeReadonlyGit(args []string, cwd string) bool {
	if readonlyGuardedDir(cwd) {
		return false
	}
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--no-pager" {
			i++
			continue
		}
		if a == "-C" {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || !safeReadonlyPath(args[i+1], cwd) {
				return false
			}
			i += 2
			continue
		}
		if strings.HasPrefix(a, "-") {
			// Reject -c, --config-env, --exec-path, --git-dir, --work-tree, --paginate, etc.
			return false
		}
		break
	}
	if i >= len(args) {
		return false
	}
	sub := args[i]
	switch sub {
	case "show", "diff", "log", "status", "rev-parse", "merge-base", "ls-files", "ls-tree", "cat-file", "grep", "blame", "describe", "shortlog":
	default:
		return false
	}
	for _, a := range args[i+1:] {
		if a == "--" {
			break
		}
		switch {
		case a == "--ext-diff", strings.HasPrefix(a, "--ext-diff="),
			a == "--textconv",
			a == "--no-index",
			a == "--output", strings.HasPrefix(a, "--output="), a == "-o",
			a == "--exec", strings.HasPrefix(a, "--exec="), a == "-x",
			a == "--paginate",
			a == "--batch-command",
			a == "--write-commit-graph":
			return false
		case sub == "grep" && (a == "-f" || a == "--file" || strings.HasPrefix(a, "--file=")):
			return false
		}
	}
	return true
}

func safeReadonlyGH(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "pr":
		switch args[1] {
		case "view", "diff", "list", "status", "checks":
		default:
			return false
		}
	case "issue":
		switch args[1] {
		case "view", "list", "status":
		default:
			return false
		}
	case "repo":
		if args[1] != "view" {
			return false
		}
	case "run":
		switch args[1] {
		case "view", "list":
		default:
			return false
		}
	default:
		return false
	}
	for _, a := range args[2:] {
		if a == "-w" || a == "--web" || a == "-o" || a == "--output" || strings.HasPrefix(a, "--output=") {
			return false
		}
	}
	return true
}

func safeReadonlySearch(args []string, cwd string, isRG bool) bool {
	if readonlyGuardedDir(cwd) {
		return false
	}
	valueFlags := map[string]bool{
		"-e": true, "--regexp": true,
		"-A": true, "--after-context": true,
		"-B": true, "--before-context": true,
		"-C": true, "--context": true,
		"-m": true, "--max-count": true,
		"--color": true, "--label": true, "--binary-files": true,
		"-d": true, "--directories": true,
	}
	if isRG {
		for _, f := range []string{
			"-g", "--glob", "--iglob", "-t", "--type", "-T", "--type-not",
			"--max-depth", "--max-columns", "--max-filesize", "--sort", "--sortr",
			"-j", "--threads", "--engine", "-E", "--encoding",
		} {
			valueFlags[f] = true
		}
	} else {
		for _, f := range []string{"--include", "--exclude", "--exclude-dir"} {
			valueFlags[f] = true
		}
	}
	hasPattern := false
	endOfFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !endOfFlags {
			if a == "--" {
				endOfFlags = true
				continue
			}
			if strings.HasPrefix(a, "--") {
				flag, _, hasVal := strings.Cut(a, "=")
				switch flag {
				case "--file", "--pre", "--pre-glob", "--search-zip", "--ignore-file", "--ignore-file-case-insensitive":
					return false
				case "--regexp":
					hasPattern = true
					if !hasVal {
						if i+1 >= len(args) {
							return false
						}
						i++
					}
					continue
				}
				if valueFlags[flag] && !hasVal {
					if i+1 >= len(args) {
						return false
					}
					i++
				}
				continue
			}
			if strings.HasPrefix(a, "-") && len(a) > 1 {
				if a == "-e" {
					if i+1 >= len(args) {
						return false
					}
					hasPattern = true
					i++
					continue
				}
				if valueFlags[a] {
					if i+1 >= len(args) {
						return false
					}
					i++
					continue
				}
				// Short flag bundle (e.g. -ni, -nE); reject -f and (for rg) -z anywhere before -e.
				bundle := a[1:]
				for j := 0; j < len(bundle); j++ {
					ch := bundle[j]
					if ch == 'f' || (isRG && ch == 'z') {
						return false
					}
					if ch == 'e' {
						hasPattern = true
						if j == len(bundle)-1 {
							if i+1 >= len(args) {
								return false
							}
							i++
						}
						break
					}
				}
				continue
			}
		}
		if !hasPattern {
			hasPattern = true
			continue
		}
		if !safeReadonlyPath(a, cwd) {
			return false
		}
	}
	return hasPattern
}

func safeReadonlyFileTool(args []string, cwd string, noFollow, rejectFiles0 bool, valueFlags map[string]bool) bool {
	if readonlyGuardedDir(cwd) {
		return false
	}
	endOfFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !endOfFlags {
			if a == "--" {
				endOfFlags = true
				continue
			}
			if strings.HasPrefix(a, "--") {
				flag, _, hasVal := strings.Cut(a, "=")
				if noFollow && (flag == "--follow" || flag == "--pid") {
					return false
				}
				if rejectFiles0 && flag == "--files0-from" {
					return false
				}
				if valueFlags[flag] && !hasVal {
					if i+1 >= len(args) {
						return false
					}
					i++
				}
				continue
			}
			if strings.HasPrefix(a, "-") && len(a) > 1 {
				if noFollow && strings.ContainsAny(a[1:], "fF") {
					return false
				}
				if valueFlags[a] {
					if i+1 >= len(args) {
						return false
					}
					i++
				}
				continue
			}
		}
		if !safeReadonlyPath(a, cwd) {
			return false
		}
	}
	return true
}

func safeReadonlyPath(arg, cwd string) bool {
	if arg == "" || strings.ContainsRune(arg, '\x00') {
		return false
	}
	if arg == "~" || strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, `~\`) {
		return false
	}
	if readonlyGuardedDir(cwd) {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(arg), "/") {
		if seg == ".." || readonlyCredentialSegment(seg) {
			return false
		}
	}
	if filepath.IsAbs(arg) {
		if cwd == "" || !filepath.IsAbs(cwd) {
			return false
		}
		cleanCwd := filepath.Clean(cwd)
		cleanArg := filepath.Clean(arg)
		rel, err := filepath.Rel(cleanCwd, cleanArg)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return false
		}
		for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
			if seg == ".." || readonlyCredentialSegment(seg) {
				return false
			}
		}
	}
	return true
}

func readonlyGuardedDir(dir string) bool {
	if dir == "" {
		return false
	}
	slash := strings.ToLower(filepath.ToSlash(filepath.Clean(dir)))
	if strings.HasSuffix(slash, "/.config/gh") || strings.Contains(slash, "/.config/gh/") ||
		strings.HasSuffix(slash, "/.config/gcloud") || strings.Contains(slash, "/.config/gcloud/") {
		return true
	}
	for _, seg := range strings.Split(slash, "/") {
		if readonlyCredentialSegment(seg) {
			return true
		}
	}
	return false
}

func readonlyCredentialSegment(seg string) bool {
	n := strings.ToLower(seg)
	switch {
	case n == "", n == ".":
		return false
	case n == ".env", strings.HasPrefix(n, ".env."), strings.HasSuffix(n, ".env"),
		strings.HasSuffix(n, ".cred"), strings.HasSuffix(n, ".creds"),
		strings.HasSuffix(n, ".pem"), strings.HasSuffix(n, ".key"),
		strings.HasSuffix(n, ".p12"), strings.HasSuffix(n, ".pfx"),
		strings.HasSuffix(n, ".keychain"), strings.HasSuffix(n, ".keychain-db"),
		strings.HasPrefix(n, "id_rsa"), strings.HasPrefix(n, "id_ed25519"), strings.HasPrefix(n, "id_ecdsa"),
		n == "hub.json", n == "credentials.json", n == "auth.json", n == "secrets.json", n == "admin.cred",
		n == ".netrc", n == ".npmrc", n == ".pypirc", n == ".git-credentials", n == "hosts.yml",
		n == ".ssh", n == ".aws", n == ".gnupg", n == ".docker", n == ".kube",
		n == ".persona", n == compat.StateDir, n == ".claude", n == ".codex", n == ".claude.json",
		strings.Contains(n, "launcher-settings"):
		return true
	}
	return false
}
