package assistant

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestDigestRecheckUsesInboxInterface(t *testing.T) {
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, "supervise_digest.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "digestEntryRecheck" {
			continue
		}
		for call := range ast.Preorder(fn.Body) {
			expr, ok := call.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := expr.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			switch sel.Sel.Name {
			case "inboxSignalsOn", "inboxSignalHolds", "acceptedFeedbackHolds", "taskInboxUnhandled":
				t.Errorf("%s: use the supervision Inbox interface for %s", fs.Position(sel.Pos()), sel.Sel.Name)
			}
		}
		return
	}
	t.Fatal("digestEntryRecheck not found")
}
