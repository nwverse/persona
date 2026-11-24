package domainrules

import (
	"go/ast"
	"go/token"
	"strings"
)

// outsideChecks is one pass of the rules on code that uses a core, the kit or an adapter.
type outsideChecks struct {
	findings
	p   *Pkg
	idx Index
}

// Outside is the rules on p's parsed files as users of cores, the kit and adapters (out-*), p's own rules as an
// adapter (adapter-*), and its imports as a part that takes no core (path-no-core). Files Full or the prefilter did
// not parse are left alone: they use no domain package.
func Outside(p *Pkg, idx Index) []Finding {
	c := &outsideChecks{p: p, idx: idx}
	c.pathNoCore()
	if p.Layer == layerAdapter {
		c.adapterImports()
		if p.decls != nil {
			c.adapterDecls()
		}
	}
	for _, f := range p.Files {
		if f.ast != nil {
			c.file(f)
		}
	}
	return c.list
}

// ref is a domain package a file imports: the name it uses, the import and the package.
type ref struct {
	name string
	imp  Import
	pkg  *Pkg
}

// refs is the domain packages (cores, shared kernels, kit) a file imports, by name. A file's own package is left
// out, except for an external test file (package <name>_test), which is outside it.
func (c *outsideChecks) refs(f *File) map[string]ref {
	out := map[string]ref{}
	for name, imp := range c.idx.imports(f) {
		if p := c.idx.pkg(imp.Path); p != nil && c.foreign(f, p) && c.idx.domain(imp.Path) {
			out[name] = ref{name, imp, p}
		}
	}
	return out
}

// foreign says whether p is outside the code of f: another package, or f's own when f is an external test.
func (c *outsideChecks) foreign(f *File, p *Pkg) bool { return p != c.p || f.External }

// kitDir says whether the file is in a kit package's directory (its own tests included).
func (c *outsideChecks) kitDir() bool { return c.p.Layer == layerKit }

func (c *outsideChecks) file(f *File) {
	layer := c.idx.layer(c.p.Path)
	refs := c.refs(f)
	adapter, testkit := layer == layerAdapter, layer == layerTestkit
	inCore := core(layer) || layer == layerKit
	// Dot imports and aliases hide literals and conversions.
	for _, imp := range f.Imports {
		if imp.Name == "." && c.idx.domain(imp.Path) && c.foreign(f, c.idx.pkg(imp.Path)) {
			c.add(imp.Pos, "out-dot-import", c.idx.rel(imp.Path), f.Rel+" dot-imports "+c.idx.rel(imp.Path))
		}
	}
	key := func(r ref) string { return r.pkg.Rel }
	// The first domain package the file imports, which file-wide findings count against.
	firstKey := ""
	for _, imp := range f.Imports {
		if c.idx.domain(imp.Path) && c.foreign(f, c.idx.pkg(imp.Path)) {
			firstKey = c.idx.rel(imp.Path)
			break
		}
	}
	if firstKey != "" {
		for _, imp := range f.Imports {
			if imp.Path == "unsafe" {
				c.add(imp.Pos, "out-unsafe", firstKey, f.Rel+" uses unsafe")
			}
		}
	}
	reflectName := c.idx.stdName(f, "reflect")
	imports := c.idx.imports(f)
	adapters := map[string]bool{}
	for name, imp := range imports {
		if c.idx.layer(imp.Path) == layerAdapter && c.idx.pkg(imp.Path) != c.p {
			adapters[name] = true
		}
	}
	var stack []ast.Node
	ast.Inspect(f.ast, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		parent := ast.Node(nil)
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		stack = append(stack, n)
		switch x := n.(type) {
		case *ast.TypeSpec:
			if x.Assign != 0 {
				if r, t, ok := c.domainType(refs, x.Type); ok {
					c.at(f, x, "out-alias", key(r), f.Rel+" aliases "+r.name+"."+t)
				}
			}
		case *ast.CompositeLit:
			if r, t, ok := c.domainType(refs, x.Type); ok {
				c.at(f, x, "out-literal", key(r), f.Rel+" builds "+r.name+"."+t+" by literal")
			} else if elem := elementType(x.Type); elem != nil {
				// []core.T{{...}}: the elided element literals are literals of core.T.
				if r, t, ok := c.domainType(refs, elem); ok {
					for _, e := range x.Elts {
						if kv, ok := e.(*ast.KeyValueExpr); ok {
							e = kv.Value
						}
						if lit, ok := e.(*ast.CompositeLit); ok && lit.Type == nil {
							c.at(f, lit, "out-literal", key(r), f.Rel+" builds "+r.name+"."+t+" by literal")
						}
					}
				}
			}
		case *ast.CallExpr:
			if len(x.Args) == 1 && x.Ellipsis == 0 {
				if r, t, ok := c.domainType(refs, x.Fun); ok {
					c.at(f, x, "out-conversion", key(r), f.Rel+" converts to "+r.name+"."+t)
				}
			}
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Log" && !adapter {
				if id, ok := sel.X.(*ast.Ident); ok && adapters[id.Name] && !logArgument(parent, x) {
					c.at(f, x, "out-log-use", c.p.Rel, f.Rel+" keeps "+id.Name+".Log(...)")
				}
			}
		case *ast.SelectorExpr:
			id, isIdent := x.X.(*ast.Ident)
			_, isImport := imports[identName(id)]
			if x.Sel.Name == "Wire" && !(isIdent && isImport) && !adapter && !testkit && !(c.kitDir() && f.Test) {
				c.at(f, x, "out-wire-ref", c.p.Rel, f.Rel+" reads "+exprString(x.X)+".Wire")
			}
			if !isIdent {
				break
			}
			if id.Name == reflectName && reflectName != "" && x.Sel.Name != "DeepEqual" && firstKey != "" {
				c.at(f, x, "out-unsafe", firstKey, f.Rel+" uses reflect."+x.Sel.Name)
			}
			r, ok := refs[id.Name]
			if !ok {
				break
			}
			rel := r.pkg.Rel
			switch {
			case core(c.idx.layer(r.imp.Path)) && (wordPrefix(x.Sel.Name, "Parse") || wordPrefix(x.Sel.Name, "Restore")) &&
				!adapter && !testkit && !f.Test && c.declares(r.pkg, x.Sel.Name):
				c.at(f, x, "out-parse-call", rel, f.Rel+" calls "+id.Name+"."+x.Sel.Name)
			case rel == "internal/domainkit/instant" && x.Sel.Name == "ReadClock" &&
				!under(c.p.Rel, "internal/domainkit/clock") && !(c.kitDir() && f.Test):
				c.at(f, x, "out-clock-read", rel, f.Rel+" reads the clock")
			case rel == "internal/domainkit/slot" && x.Sel.Name == "NewSlot" && !adapter && !testkit && !(c.kitDir() && f.Test):
				c.at(f, x, "out-slot-mint", rel, f.Rel+" mints a Slot")
			case rel == "internal/domainkit/fail" && (x.Sel.Name == "NewCode" || x.Sel.Name == "NewPart") &&
				!inCore && !testkit:
				c.at(f, x, "out-code-mint", rel, f.Rel+" makes a failure code")
			}
		case *ast.FuncDecl:
			c.visitorImpl(f, x, refs, adapter || testkit || inCore && !f.Test)
		}
		return true
	})
}

// identName is an identifier's name, "" for none.
func identName(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	return id.Name
}

// declares says whether p declares a top-level function name.
func (c *outsideChecks) declares(p *Pkg, name string) bool {
	if p.decls == nil {
		return true
	}
	for _, fd := range p.decls.funcs {
		if fd.decl.Name.Name == name {
			return true
		}
	}
	return false
}

// visitorImpl is out-visitor-impl on one method: a Visit* method whose parameters name a type of an imported core
// or the kit implements a visitor, which only adapters, testkit and a core's own tests do. The finding counts against
// the first core the parameters name (the kit when they name no core).
func (c *outsideChecks) visitorImpl(f *File, fd *ast.FuncDecl, refs map[string]ref, allowed bool) {
	if allowed || fd.Recv == nil || !strings.HasPrefix(fd.Name.Name, "Visit") {
		return
	}
	var named []ref
	for _, p := range params(fd.Type.Params) {
		pkg, _ := baseType(unwrapParam(p))
		if r, ok := refs[pkg]; ok {
			named = append(named, r)
		}
	}
	if len(named) == 0 {
		return
	}
	pick := named[0]
	for _, r := range named {
		if f.Test && r.pkg == c.p {
			return // the core's own black-box tests visiting it
		}
		if core(r.pkg.Layer) && !core(pick.pkg.Layer) {
			pick = r
		}
	}
	recv, _ := receiverName(fd.Recv.List[0].Type)
	c.at(f, fd.Name, "out-visitor-impl", pick.pkg.Rel, recv+"."+fd.Name.Name+" implements "+pick.name+"'s visitor")
}

// domainType is the domain package and type a type expression names (core.T, core.T[X], (core.T)), when it names a
// type a core, shared kernel or the kit declares.
func (c *outsideChecks) domainType(refs map[string]ref, e ast.Expr) (ref, string, bool) {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
			continue
		case *ast.IndexExpr:
			e = x.X
			continue
		case *ast.IndexListExpr:
			e = x.X
			continue
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return ref{}, "", false
			}
			r, ok := refs[id.Name]
			if !ok || r.pkg.decls == nil || r.pkg.decls.types[x.Sel.Name] == nil {
				return ref{}, "", false
			}
			return r, x.Sel.Name, true
		}
		return ref{}, "", false
	}
}

// elementType is a slice, array or map literal type's element type, nil for another type.
func elementType(e ast.Expr) ast.Expr {
	switch x := e.(type) {
	case *ast.ArrayType:
		return x.Elt
	case *ast.MapType:
		return x.Value
	}
	return nil
}

// logArgument says whether call is an argument of parent, a call to a logging function.
func logArgument(parent ast.Node, call *ast.CallExpr) bool {
	p, ok := parent.(*ast.CallExpr)
	if !ok {
		return false
	}
	isArg := false
	for _, a := range p.Args {
		if a == ast.Expr(call) {
			isArg = true
		}
	}
	if !isArg {
		return false
	}
	name := ""
	switch fn := p.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		name = fn.Sel.Name
	}
	for _, s := range []string{"logf", "Logf", "Printf", "Info", "Warn", "Error", "Debug"} {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

func (c *outsideChecks) at(f *File, n ast.Node, id, key, what string) { c.add(f.pos(n), id, key, what) }

// pathNoCore is path-no-core: the hub, hubedge, db, prompts and cmd/persona-hub import no core, kit or adapter.
func (c *outsideChecks) pathNoCore() {
	inPart := false
	for _, part := range noCore {
		if under(c.p.Rel, part) {
			inPart = true
		}
	}
	if !inPart {
		return
	}
	for _, f := range c.p.Files {
		for _, imp := range f.Imports {
			l := c.idx.layer(imp.Path)
			if !c.idx.domain(imp.Path) && l != layerAdapter {
				continue
			}
			from := display(c.p)
			if f.Test {
				from += " (test)"
			}
			rel := c.idx.rel(imp.Path)
			c.edge(imp.Pos, "path-no-core", rel, from+" imports "+rel, from+" imports "+rel)
		}
	}
}

// adapterImports is adapter-no-application and adapter-import on an adapter's non-test imports.
func (c *outsideChecks) adapterImports() {
	for _, f := range c.p.Files {
		if f.Test {
			continue
		}
		for _, imp := range f.Imports {
			dep := c.idx.pkg(imp.Path)
			if dep == nil || dep == c.p {
				continue
			}
			switch l := c.idx.layer(imp.Path); l {
			case layerApp, layerSeam, undeclared:
				c.add(imp.Pos, "adapter-no-application", c.p.Rel, "adapter "+display(c.p)+" imports application "+dep.Rel)
			case layerAdapter:
				// Allowed only for a shared kernel's adapter: one under a core shared package.
				owner := c.idx.pkg(dep.Path[:strings.LastIndex(dep.Path, "/")])
				if owner == nil || owner.Layer != layerShared {
					c.add(imp.Pos, "adapter-import", c.p.Rel, "adapter "+display(c.p)+" imports "+dep.Rel)
				}
			}
		}
	}
}

// adapterDecls is adapter-name and adapter-exported-result on an adapter's declarations: what its exported functions
// and methods hand out in their results and through their parameters, and its exported structs' exported fields.
func (c *outsideChecks) adapterDecls() {
	d := c.p.decls
	coreTypes := map[string]string{} // exported type name -> the core declaring it
	for _, f := range c.p.Files {
		if f.Test {
			continue
		}
		for _, r := range c.refs(f) {
			if r.pkg.decls == nil || !core(r.pkg.Layer) {
				continue
			}
			for name := range r.pkg.decls.types {
				if exported(name) {
					coreTypes[name] = r.name
				}
			}
		}
	}
	for _, t := range d.order {
		if core, ok := coreTypes[t.name]; ok && exported(t.name) {
			c.at(t.file, t.spec.Name, "adapter-name", c.p.Rel, c.p.Name+"."+t.name+" shadows "+core+"."+t.name)
		}
		c.exportedFields(t)
	}
	for _, v := range d.values {
		if v.tok == token.VAR && exported(v.name.Name) {
			c.at(v.file, v.name, "adapter-exported-result", c.p.Rel, c.p.Name+"."+v.name.Name+" is an exported variable")
		}
	}
	check := func(fd *funcDecl) {
		if !exported(fd.decl.Name.Name) || fd.recv != "" && !exported(fd.recv) {
			return
		}
		for _, r := range params(fd.decl.Type.Results) {
			if !c.adapterResult(fd, r) {
				c.at(fd.file, fd.decl.Name, "adapter-exported-result", c.p.Rel, fd.name()+" returns "+exprString(r))
			}
		}
		tps := typeParams(fd.decl)
		for _, p := range params(fd.decl.Type.Params) {
			for _, out := range c.handedBack(fd.file, p, tps) {
				switch {
				case out == nil:
					c.at(fd.file, fd.decl.Name, "adapter-exported-result", c.p.Rel,
						fd.name()+" can hand out any value through "+exprString(p))
				case out == p && !c.handsOutOnly(fd.file, out):
					c.at(fd.file, fd.decl.Name, "adapter-exported-result", c.p.Rel,
						fd.name()+" can hand back what it writes into "+exprString(p)+", a type of another package")
				case !c.handsOutOnly(fd.file, out):
					c.at(fd.file, fd.decl.Name, "adapter-exported-result", c.p.Rel,
						fd.name()+" hands out "+exprString(out)+" through "+exprString(p))
				}
			}
		}
	}
	for _, fd := range d.funcs {
		check(fd)
	}
	for _, t := range d.order {
		for _, fd := range d.methods[t.name] {
			check(fd)
		}
	}
}

// exportedFields is adapter-exported-result on an adapter's exported struct other than its Record: an exported field
// hands what it holds to whoever has the struct, as a getter would.
func (c *outsideChecks) exportedFields(t *typeDecl) {
	if !exported(t.name) || t.name == "Record" || t.strct == nil {
		return
	}
	for _, fl := range t.strct.Fields.List {
		if len(fl.Names) == 0 {
			if _, name := baseType(fl.Type); exported(name) {
				c.at(t.file, fl, "adapter-exported-result", c.p.Rel, c.p.Name+"."+t.name+"."+name+" is an exported field")
			}
		}
		for _, n := range fl.Names {
			if exported(n.Name) {
				c.at(t.file, n, "adapter-exported-result", c.p.Rel, c.p.Name+"."+t.name+"."+n.Name+" is an exported field")
			}
		}
	}
}

// sinks are the interfaces an adapter writes an answer to, by import path and name: what goes into one is not read by
// the rule and is a review's to check (docs/server/architecture.md "Data objects and adapters"). They are the only
// types of another package an adapter takes that the rule does not read as a way back to its caller.
var sinks = map[string]bool{"io.Writer": true, "net/http.ResponseWriter": true}

// handedBack is what an adapter can hand its caller through a parameter of type p of f: what a pointer, slice, map
// or channel it can send on holds, written into it (a map's keys included); what a callback takes; and what the
// methods of an interface take, called on it. A type the adapter declares counts as the type it is declared as. A
// type of another package is itself what goes back: a named map or slice (http.Header, url.Values), a struct with a
// map or pointer inside, or an interface whose methods take what the adapter passes them all reach the caller, and
// the rule cannot see which of them a type is. So it passes only as a type an adapter may hand out (a core, shared
// kernel, kit, contract, db or prompts type) or as one of the sinks. A nil entry stands for any value at all: a
// parameter of type any, interface{} or a type parameter (tps) can carry a pointer to anything, which the adapter
// can assert and write through. A value of a basic type, error or a struct the adapter declares hands nothing back.
func (c *outsideChecks) handedBack(f *File, p ast.Expr, tps map[string]bool) []ast.Expr {
	seen := map[string]bool{}
	var back func(p ast.Expr) []ast.Expr
	back = func(p ast.Expr) []ast.Expr {
		switch x := p.(type) {
		case *ast.ParenExpr:
			return back(x.X)
		case *ast.SelectorExpr, *ast.IndexExpr, *ast.IndexListExpr:
			pkg, name := baseType(x)
			if pkg == "" {
				if t := c.p.decls.types[name]; t != nil && t.strct == nil && !seen[name] {
					seen[name] = true
					return back(t.spec.Type) // a generic type the adapter declares
				}
				return nil
			}
			if imp, ok := c.idx.imports(f)[pkg]; ok && sinks[imp.Path+"."+name] {
				return nil
			}
			return []ast.Expr{x}
		case *ast.StarExpr:
			return []ast.Expr{x.X}
		case *ast.ArrayType:
			if x.Len == nil {
				return []ast.Expr{x.Elt}
			}
			return back(x.Elt) // an array is copied, but not what its pointers reach
		case *ast.Ellipsis:
			return []ast.Expr{x.Elt}
		case *ast.MapType:
			return []ast.Expr{x.Key, x.Value}
		case *ast.ChanType:
			if x.Dir != ast.RECV {
				return []ast.Expr{x.Value}
			}
		case *ast.FuncType:
			return params(x.Params)
		case *ast.InterfaceType:
			if len(x.Methods.List) == 0 {
				return []ast.Expr{nil}
			}
			var out []ast.Expr
			for _, m := range x.Methods.List {
				if ft, ok := m.Type.(*ast.FuncType); ok {
					out = append(out, params(ft.Params)...)
				} else {
					out = append(out, back(m.Type)...) // an embedded interface: any, the adapter's own or another package's
				}
			}
			return out
		case *ast.Ident:
			if x.Name == "any" || tps[x.Name] {
				return []ast.Expr{nil}
			}
			if t := c.p.decls.types[x.Name]; t != nil && t.strct == nil && !seen[x.Name] {
				seen[x.Name] = true
				return back(t.spec.Type)
			}
		}
		return nil
	}
	return back(p)
}

// adapterResult says whether an adapter may return a type: an outcome, a value of a core, shared kernel or the kit,
// a contract, a db row or prompt input, its own Record, an error, or Log's string; or a pointer, slice, array, map or
// channel of these, a map's keys included.
func (c *outsideChecks) adapterResult(fd *funcDecl, r ast.Expr) bool {
	if exprString(r) == "string" {
		return fd.recv == "" && fd.decl.Name.Name == "Log" && len(params(fd.decl.Type.Results)) == 1
	}
	return c.handsOutOnly(fd.file, r)
}

// handsOutOnly says whether every type a value of type t holds (handedParts) is one an adapter may hand out: an error,
// or a type handedOut allows. A map hands out its keys as well as its values.
func (c *outsideChecks) handsOutOnly(f *File, t ast.Expr) bool {
	for _, part := range handedParts(t) {
		if exprString(part) != "error" && !c.handedOut(f, part) {
			return false
		}
	}
	return true
}

// handedParts is the types a value of type t gives whoever holds it: t itself, or what a pointer, slice, array, map
// (its keys and its values) or channel holds, all the way down.
func handedParts(t ast.Expr) []ast.Expr {
	switch x := t.(type) {
	case *ast.ParenExpr:
		return handedParts(x.X)
	case *ast.StarExpr:
		return handedParts(x.X)
	case *ast.ArrayType:
		return handedParts(x.Elt)
	case *ast.Ellipsis:
		return handedParts(x.Elt)
	case *ast.MapType:
		return append(handedParts(x.Key), handedParts(x.Value)...)
	case *ast.ChanType:
		return handedParts(x.Value)
	}
	return []ast.Expr{t}
}

// handedOut says whether an adapter may hand a type out, in a result or as a kit type's argument: its own Record, or
// a type of a core, shared kernel, contract, db or prompts, or of the kit when every type argument is one of these
// too, all the way down (handedParts). So outcome.Of[agentref.Ref, agentref.Fail] and opt.Option[[]fleetcount.Agent]
// pass, and outcome.Of[string, fail.Fail], opt.Option[[]string] and opt.Option[map[string]fleetcount.Agent] are raw
// values one layer out.
func (c *outsideChecks) handedOut(f *File, t ast.Expr) bool {
	for _, part := range handedParts(t) {
		if !c.handedOutType(f, part) {
			return false
		}
	}
	return true
}

// handedOutType is handedOut on one type that holds no other (handedParts).
func (c *outsideChecks) handedOutType(f *File, t ast.Expr) bool {
	if exprString(t) == "Record" {
		return true
	}
	pkg, _ := baseType(t)
	if pkg == "" {
		return false
	}
	imp, ok := c.idx.imports(f)[pkg]
	if !ok {
		return false
	}
	switch c.idx.layer(imp.Path) {
	case layerKit:
		for _, a := range typeArgs(t) {
			if !c.handedOut(f, a) {
				return false
			}
		}
		return true
	case layerCore, layerShared, layerContract:
		return true
	}
	rel := c.idx.rel(imp.Path)
	return under(rel, "internal/db") || under(rel, "internal/prompts")
}
