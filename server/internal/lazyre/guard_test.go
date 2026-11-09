package lazyre

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// No package-level regexp.MustCompile in what personad and persona-node are made of (internal/ and cmd/, tests aside):
// every program that links the package would compile it when it starts, `personad hook` too, which Claude Code waits
// for on each hook event (#1308). Use lazyre.New, which takes the same pattern and has the same methods. The
// extension programs under plugins/ are programs of their own and are not looked at.
func TestNoPackageLevelMustCompile(t *testing.T) {
	var found []string
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			ast.Inspect(g, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false // runs when it is called, not at start
				}
				if c, ok := n.(*ast.CallExpr); ok {
					if s, ok := c.Fun.(*ast.SelectorExpr); ok && (s.Sel.Name == "MustCompile" || s.Sel.Name == "MustCompilePOSIX") {
						if x, ok := s.X.(*ast.Ident); ok && x.Name == "regexp" {
							found = append(found, fset.Position(c.Pos()).String())
						}
					}
				}
				return true
			})
		}
		return nil
	}
	for _, dir := range []string{"internal", "cmd"} {
		if err := filepath.WalkDir(filepath.Join("..", "..", dir), walk); err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Fatalf("package-level regexp.MustCompile, compiled whenever personad starts (a hook too); use lazyre.New "+
			"(in a contract package, which may use the standard library only: sync.OnceValue):\n%s",
			strings.Join(found, "\n"))
	}
}

func TestLazyMatchesLikeRegexp(t *testing.T) {
	r := New(`^a(b+)c$`)
	if !r.MatchString("abbc") || r.MatchString("ac") {
		t.Fatal("MatchString")
	}
	if got := r.FindStringSubmatch("abbbc"); len(got) != 2 || got[1] != "bbb" {
		t.Fatalf("FindStringSubmatch: %q", got)
	}
	if r.String() != `^a(b+)c$` || r.Regexp().String() != `^a(b+)c$` {
		t.Fatal("String")
	}
}

func TestCompiledAtOnceInTests(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a bad pattern did not panic in New in a test binary")
		}
	}()
	New(`(`)
}
