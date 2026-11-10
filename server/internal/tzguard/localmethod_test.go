package tzguard

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
)

// maskBooleanLocalCalls excludes calls whose result is known to be bool, such as a rule's network locality check.
// siblings are the other files of source's package: a receiver type declared in one file and called in another is only
// known to be bool when they are checked together. Unknown or incompletely typed calls remain visible to the
// time-zone guard.
func maskBooleanLocalCalls(source []byte, siblings ...[]byte) []byte {
	if !bytes.Contains(source, []byte(".Local()")) {
		return source
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "source.go", source, 0)
	if err != nil {
		return source
	}
	files := []*ast.File{file}
	for i, sibling := range siblings {
		if other, err := parser.ParseFile(set, fmt.Sprintf("sibling%d.go", i), sibling, 0); err == nil {
			files = append(files, other)
		}
	}
	info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)}
	cfg := types.Config{Importer: importer.Default(), Error: func(error) {}}
	_, _ = cfg.Check("tzguard/source", set, files, info)
	masked := bytes.Clone(source)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Local" || !types.Identical(info.TypeOf(call), types.Typ[types.Bool]) {
			return true
		}
		start := set.Position(sel.Sel.Pos()).Offset - 1
		if start >= 0 && bytes.HasPrefix(source[start:], []byte(".Local()")) {
			for i := start; i < start+len(".Local()"); i++ {
				masked[i] = ' '
			}
		}
		return true
	})
	return masked
}
