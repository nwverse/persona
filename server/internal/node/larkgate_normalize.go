package node

import (
	"bytes"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// normalizeLarkCommand removes inert here-documents and resolves immediately preceding
// literal command assignments solely for classification. It never executes the input.
func normalizeLarkCommand(cmd string) string {
	tree, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return cmd
	}
	// Restrict these transformations to a straight sequence: an assignment in a
	// conditional, loop or background job may never have happened.
	for _, stmt := range tree.Stmts {
		if _, ok := stmt.Cmd.(*syntax.CallExpr); !ok || stmt.Background || stmt.Negated || stmt.Coprocess {
			return cmd
		}
	}
	vars := map[string]string{}
	changed := false
	for _, stmt := range tree.Stmts {
		call := stmt.Cmd.(*syntax.CallExpr)
		if len(call.Args) == 0 {
			for _, a := range call.Assigns {
				if a.Name == nil {
					continue
				}
				delete(vars, a.Name.Value)
				if a.Append || a.Index != nil || a.Array != nil || a.Value == nil {
					continue
				}
				value, dynamic := executionWord(a.Value)
				if !dynamic && safeLiteralCommand(value) {
					vars[a.Name.Value] = value
				}
			}
			continue
		}
		words := make([]string, len(call.Args))
		for i, word := range call.Args {
			words[i], _ = executionWord(word)
		}
		suffix, ok := unwrapCommand(words)
		if ok && len(suffix) > 0 {
			index := len(words) - len(suffix)
			word := call.Args[index]
			if len(word.Parts) == 1 {
				if p, ok := word.Parts[0].(*syntax.ParamExp); ok && p.Param != nil && !p.Excl && !p.Length && !p.Width && p.Index == nil && p.Slice == nil && p.Repl == nil && p.Exp == nil && p.Names == 0 {
					if value, ok := vars[p.Param.Value]; ok {
						word.Parts = []syntax.WordPart{&syntax.Lit{Value: value}}
						changed = true
					}
				}
			}
		}
		// Preserve assignments only across simple commands known not to change
		// shell state. Expansions can themselves assign variables.
		name := words[0]
		unchanged := map[string]bool{"echo": true, ":": true, "true": true, "false": true, "ls": true, "git": true, "go": true, "pwd": true, "date": true, "sleep": true, "cat": true, "head": true, "grep": true, "jq": true}[name]
		for _, arg := range call.Args {
			_, dynamic := executionWord(arg)
			if dynamic {
				unchanged = false
			}
		}
		if len(call.Assigns) != 0 || len(stmt.Redirs) != 0 || !unchanged {
			clear(vars)
		}
	}

	// A heredoc fed only to a file writer is data, except when a later command
	// could execute it or its unquoted body contains an executable substitution.
	hasHeredoc, safe := false, true
	for _, stmt := range tree.Stmts {
		call := stmt.Cmd.(*syntax.CallExpr)
		if len(call.Args) == 0 {
			continue
		}
		name, dynamic := executionWord(call.Args[0])
		base := filepath.Base(name)
		if dynamic || dynamicCommandName(name) || indirectLarkCommand(name) {
			safe = false
		}
		for _, redir := range stmt.Redirs {
			if redir.Hdoc == nil {
				continue
			}
			hasHeredoc = true
			if base != "cat" && base != "tee" {
				safe = false
				continue
			}
			if base == "cat" {
				output := false
				for _, other := range stmt.Redirs {
					if (other.Op == syntax.RdrOut || other.Op == syntax.AppOut) && other.Word != nil && other.Word.Lit() != "" {
						output = true
					}
				}
				if !output || len(call.Args) != 1 {
					safe = false
				}
			} else {
				if len(call.Args) < 2 {
					safe = false
				}
				for _, arg := range call.Args[1:] {
					if arg.Lit() == "" || strings.HasPrefix(arg.Lit(), "-") {
						safe = false
					}
				}
			}
			syntax.Walk(redir.Hdoc, func(n syntax.Node) bool {
				switch n.(type) {
				case *syntax.CmdSubst, *syntax.ProcSubst:
					safe = false
				}
				return true
			})
		}
	}
	if hasHeredoc && safe {
		for _, stmt := range tree.Stmts {
			redirs := stmt.Redirs[:0]
			for _, r := range stmt.Redirs {
				if r.Hdoc == nil {
					redirs = append(redirs, r)
				}
			}
			stmt.Redirs = redirs
		}
		changed = true
	}
	if !changed {
		return cmd
	}
	var out bytes.Buffer
	if err := syntax.NewPrinter().Print(&out, tree); err != nil {
		return cmd
	}
	return out.String()
}

func safeLiteralCommand(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/._+-", c) {
			continue
		}
		return false
	}
	return true
}
