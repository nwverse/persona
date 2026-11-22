package hub

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestClientMapMutationsHoldLock(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, pos := range unlockedClientMapMutations(f) {
			t.Errorf("%s: use addTestClient/removeTestClient, or hold the hub's mu before mutating clients", fset.Position(pos))
		}
	}
}

// Recognize explicit locks in surrounding blocks, not arbitrary control flow or aliases.
// Inspect closures independently: a caller's lock does not protect a new goroutine.
func unlockedClientMapMutations(f *ast.File) []token.Pos {
	var found []token.Pos
	var inspect func(ast.Node, map[string]bool)
	inspect = func(root ast.Node, held map[string]bool) {
		check := func(expr ast.Expr) {
			if owner := clientMapFieldOwner(expr, "clients"); owner != "" && !held[owner] {
				found = append(found, expr.Pos())
			}
		}
		ast.Inspect(root, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CaseClause:
				inspect(&ast.BlockStmt{List: n.Body}, held)
				return false
			case *ast.CommClause:
				inspect(&ast.BlockStmt{List: n.Body}, held)
				return false
			case *ast.FuncLit:
				inspect(n.Body, nil)
				return false
			case *ast.BlockStmt:
				locked := map[string]bool{}
				for owner, value := range held {
					locked[owner] = value
				}
				for _, stmt := range n.List {
					if expr, ok := stmt.(*ast.ExprStmt); ok {
						if call, ok := expr.X.(*ast.CallExpr); ok {
							if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
								if owner := clientMapFieldOwner(sel.X, "mu"); owner != "" {
									switch sel.Sel.Name {
									case "Lock":
										locked[owner] = true
									case "Unlock":
										locked[owner] = false
									}
								}
							}
						}
					}
					inspect(stmt, locked)
				}
				return false
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if index, ok := lhs.(*ast.IndexExpr); ok {
						check(index.X)
					}
				}
			case *ast.CallExpr:
				if fn, ok := n.Fun.(*ast.Ident); ok && (fn.Name == "delete" || fn.Name == "clear") && len(n.Args) > 0 {
					check(n.Args[0])
				}
			}
			return true
		})
	}
	inspect(f, nil)
	return found
}

func clientMapFieldOwner(expr ast.Expr, field string) string {
	sel, ok := ast.Unparen(expr).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != field {
		return ""
	}
	var buf bytes.Buffer
	_ = format.Node(&buf, token.NewFileSet(), ast.Unparen(sel.X))
	return buf.String()
}

func TestClientMapMutationGuard(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"add", `h.clients[c] = struct{}{}`, 1},
		{"other receiver", `hub.clients[c] = struct{}{}`, 1},
		{"multiple add", `h.clients[a], h.clients[b] = struct{}{}, struct{}{}`, 2},
		{"delete", `delete(h.clients, c)`, 1},
		{"clear", `clear(h.clients)`, 1},
		{"locked", `h.mu.Lock(); h.clients[c] = struct{}{}; delete(h.clients, c); h.mu.Unlock()`, 0},
		{"deferred unlock", `hub.mu.Lock(); defer hub.mu.Unlock(); hub.clients[c] = struct{}{}`, 0},
		{"wrong lock", `other.mu.Lock(); h.clients[c] = struct{}{}`, 1},
		{"read lock", `h.mu.RLock(); delete(h.clients, c)`, 1},
		{"after unlock", `h.mu.Lock(); h.mu.Unlock(); delete(h.clients, c)`, 1},
		{"closure", `h.mu.Lock(); go func() { h.clients[c] = struct{}{} }()`, 1},
		{"locked loop", `h.mu.Lock(); defer h.mu.Unlock(); for _, c := range clients { h.clients[c] = struct{}{} }`, 0},
		{"locked case", `switch { case true: h.mu.Lock(); delete(h.clients, c); h.mu.Unlock() }`, 0},
		{"unlocked case", `switch { case true: h.mu.Lock(); h.mu.Unlock(); default: delete(h.clients, c) }`, 1},
		{"locked select", `select { default: h.mu.Lock(); delete(h.clients, c); h.mu.Unlock() }`, 0},
		{"nested receiver", `fixture.hub.mu.Lock(); fixture.hub.clients[c] = struct{}{}`, 0},
		{"read", `_, ok := h.clients[c]; _ = ok`, 0},
		{"helper", `addTestClient(h, c); removeTestClient(h, c)`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), "fixture_test.go", "package hub; func fixture() {"+tc.body+"}", 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(unlockedClientMapMutations(f)); got != tc.want {
				t.Fatalf("unlocked mutations = %d, want %d", got, tc.want)
			}
		})
	}
}
