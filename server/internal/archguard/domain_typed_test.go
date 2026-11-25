package archguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/archguard/domainrules"
)

// The type-aware domain rules (docs/server/architecture.md "Domain model", "The rules and their checks"): what the
// syntax-only rules of domainrules cannot see, because it takes a value's type to see it. TestDomainTyped runs them
// over the cores that predate no rule (every core but the legacy ones the baseline holds) and the packages that import
// one, type-checked from source against the gc export data of what they import (go list -export -deps; go/types and
// go/importer, the standard library only).

// typedRule is what a typed finding says besides what was found, as domainrules' rules do.
type typedRule struct{ why, fix, good string }

var typedRules = map[string]typedRule{
	"typed-visitor-impl": {
		"a visitor is how a core's data leaves it, and only an adapter turns that data into anything else",
		"move the type into the core's adapter and call behaviour from the application",
		"server/internal/assistant/fleetcount/fleetcountadapt/render.go",
	},
	"typed-discard": {
		"an outcome or a failure dropped is a failure nobody handles",
		"unpack it (outcome.Unpack) and act on the failure, or log it through the adapter's Log",
		"server/internal/assistant/fleet_live.go (fill)",
	},
	"typed-codec": {
		"fmt, log, slog, json and templates read a value's unexported fields through reflection, a getter by another name",
		"hand the value to its adapter, which writes its wire form or its Log line",
		"server/internal/agentref/agentrefadapt/agentrefadapt.go (Log)",
	},
	"typed-map-range": {
		"a map's order changes from run to run, so a core that ranges over one is not a function of its arguments",
		"range over slices.Sorted(maps.Keys(m)), or keep the entries in a slice",
		"server/internal/assistant/fleetcount/tally.go",
	},
	"typed-fail-compare": {
		"a failure's code cannot be compared, so == between Fail values panics at run time",
		"visit the failure (fail.FailVisitor), or compare it with nil only",
		"server/internal/domainkit/domaintest/domaintest.go (Describe)",
	},
}

func typedMessage(id, what string) string {
	r := typedRules[id]
	return "RULE " + id + " " + what + ". WHY " + r.why + ". FIX " + r.fix + ". GOOD " + r.good +
		`. see docs/server/architecture.md "Domain model"`
}

const domainKit = serverModule + "/internal/domainkit"

// typedUnit is one package to check: its import path, layer and files (the ones the build takes).
type typedUnit struct {
	path, layer string
	files       []string
}

// typedFinding is one breach: its rule and position (for the planted cases) and its message.
type typedFinding struct {
	rule string
	pos  token.Position
	msg  string
}

func TestDomainTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks packages from source")
	}
	start := time.Now()
	pkgs, err := domainrules.Load(serverDir, serverModule)
	if err != nil {
		t.Fatal(err)
	}
	strict := map[string]bool{} // import paths of the cores the rules hold in full
	for _, p := range pkgs {
		if (p.Layer == "core" || p.Layer == "core shared") && !domainrules.LegacyCore(p.Rel) {
			strict[p.Path] = true
		}
	}
	layers := map[string]string{}
	var targets []string
	for _, p := range pkgs {
		layers[p.Path] = p.Layer
		imports := strict[p.Path]
		for _, f := range p.Files {
			for _, imp := range f.Imports {
				imports = imports || !f.Test && strict[imp.Path]
			}
		}
		if imports && p.Name != "" {
			targets = append(targets, p.Path)
		}
	}
	listed, err := goListExport(serverDir, targets)
	if err != nil {
		t.Fatal(err)
	}
	var units []typedUnit
	for _, path := range targets {
		l := listed[path]
		var files []string
		for _, f := range l.GoFiles {
			files = append(files, filepath.Join(l.Dir, f))
		}
		units = append(units, typedUnit{path: path, layer: layers[path], files: files})
	}
	findings, checked, err := typedCheck(units, strict, listed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("domain typed: %d packages (%d strict cores), %d files type-checked in %s", len(units), len(strict), checked,
		time.Since(start).Round(time.Millisecond))
	if len(units) == 0 || checked == 0 {
		t.Fatalf("the typed domain rules inspected nothing: %d cores, %d packages", len(strict), len(units))
	}
	for _, f := range findings {
		t.Error(f.pos.String() + ": " + f.msg)
	}
}

// Every typed rule fails on its planted case and passes its compliant twin (testdata/domaintyped, one package per
// layer, each case marked with a "planted: <rule>" comment on its line; a twin names none).
func TestDomainTypedPlanted(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks packages from source")
	}
	planted := filepath.Join("testdata", "domaintyped")
	units := []typedUnit{
		{path: "planted/app", layer: "application"},
		{path: "planted/core", layer: "core"},
	}
	want := map[string]bool{}
	for i := range units {
		dir := filepath.Join(planted, filepath.Base(units[i].path))
		names, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(names) == 0 {
			t.Fatalf("no planted files in %s: %v", dir, err)
		}
		for _, name := range names {
			units[i].files = append(units[i].files, name)
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			for n, line := range strings.Split(string(b), "\n") {
				if _, rule, ok := strings.Cut(line, "// planted: "); ok {
					want[rule+" "+filepath.Base(name)+":"+fmt.Sprint(n+1)] = true
				}
			}
		}
	}
	strict := map[string]bool{serverModule + "/internal/assistant/fleetcount": true, serverModule + "/internal/agentref": true,
		"planted/core": true}
	listed, err := goListExport(serverDir, []string{serverModule + "/internal/assistant/fleetcount/fleetcountadapt",
		serverModule + "/internal/agentref/agentrefadapt", "fmt", "maps", "slices", "encoding/json", "log/slog"})
	if err != nil {
		t.Fatal(err)
	}
	findings, _, err := typedCheck(units, strict, listed)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range findings {
		got[f.rule+" "+filepath.Base(f.pos.Filename)+":"+fmt.Sprint(f.pos.Line)] = true
		if !strings.Contains(f.msg, ". WHY ") || !strings.Contains(f.msg, ". GOOD ") {
			t.Errorf("message without WHY/GOOD: %s", f.msg)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("planted %s: not found", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("found %s, which no planted comment names", k)
		}
	}
	for id := range typedRules {
		planted := false
		for k := range want {
			planted = planted || strings.HasPrefix(k, id+" ")
		}
		if !planted {
			t.Errorf("rule %s has no planted case", id)
		}
		if _, err := os.Stat(filepath.Join(serverDir, "..", strings.Fields(typedRules[id].good)[0])); err != nil {
			t.Errorf("rule %s's GOOD path: %v", id, err)
		}
	}
}

// listedPkg is what go list says of a package.
type listedPkg struct {
	ImportPath, Dir, Export string
	GoFiles                 []string
}

// goListExport is go list -export -deps over pkgs, from dir: every package they reach, with its export data file.
func goListExport(dir string, pkgs []string) (map[string]listedPkg, error) {
	cmd := exec.Command("go", append([]string{"list", "-export", "-deps", "-json=ImportPath,Dir,Export,GoFiles"}, pkgs...)...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -export: %v: %s", err, stderr.String())
	}
	listed := map[string]listedPkg{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		listed[p.ImportPath] = p
	}
	return listed, nil
}

// typedCheck type-checks each unit from source, against the export data listed of what it imports, and runs the
// rules over it; it returns the findings and the number of files checked.
func typedCheck(units []typedUnit, strict map[string]bool, listed map[string]listedPkg) ([]typedFinding, int, error) {
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		p, ok := listed[path]
		if !ok || p.Export == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(p.Export)
	})
	var findings []typedFinding
	files := 0
	for _, u := range units {
		var parsed []*ast.File
		for _, name := range u.files {
			src, err := os.ReadFile(name)
			if err != nil {
				return nil, 0, err
			}
			f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution|parser.ParseComments)
			if err != nil {
				return nil, 0, err
			}
			parsed = append(parsed, f)
		}
		info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{},
			Uses: map[*ast.Ident]types.Object{}}
		conf := types.Config{Importer: imp}
		pkg, err := conf.Check(u.path, fset, parsed, info)
		if err != nil {
			return nil, 0, fmt.Errorf("type-checking %s: %v", u.path, err)
		}
		files += len(parsed)
		c := typedChecks{fset: fset, unit: u, pkg: pkg, info: info, strict: strict}
		c.fail = c.failInterface()
		for _, f := range parsed {
			c.file(f)
		}
		c.visitors()
		findings = append(findings, c.findings...)
	}
	slices.SortFunc(findings, func(a, b typedFinding) int {
		return strings.Compare(a.pos.String()+a.rule, b.pos.String()+b.rule)
	})
	return findings, files, nil
}

// typedChecks is the rules over one type-checked unit.
type typedChecks struct {
	fset     *token.FileSet
	unit     typedUnit
	pkg      *types.Package
	info     *types.Info
	strict   map[string]bool
	fail     *types.Interface // the kit's fail.Fail, nil when the unit reaches none
	findings []typedFinding
}

func (c *typedChecks) add(pos token.Pos, rule, what string) {
	c.findings = append(c.findings, typedFinding{rule: rule, pos: c.fset.Position(pos), msg: typedMessage(rule, what)})
}

// core says whether the unit is a core, which the syntax rules hold to discarding nothing already.
func (c *typedChecks) core() bool { return c.unit.layer == "core" || c.unit.layer == "core shared" }

func (c *typedChecks) file(f *ast.File) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ExprStmt:
			if call, ok := ast.Unparen(x.X).(*ast.CallExpr); ok && !c.core() {
				if t := c.info.Types[call].Type; t != nil && c.mustUse(t) {
					c.add(call.Pos(), "typed-discard", types.ExprString(call.Fun)+" discards "+c.typeName(t))
				}
			}
		case *ast.AssignStmt:
			if !c.core() {
				c.blanks(x)
			}
		case *ast.CallExpr:
			c.codec(x)
		case *ast.RangeStmt:
			if c.core() {
				if t := c.info.Types[x.X].Type; t != nil {
					if _, ok := t.Underlying().(*types.Map); ok {
						c.add(x.Pos(), "typed-map-range", c.unit.path+" ranges over "+types.ExprString(x.X)+", a map")
					}
				}
			}
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				l, r := c.info.Types[x.X], c.info.Types[x.Y]
				if !l.IsNil() && !r.IsNil() && c.isFail(l.Type) && c.isFail(r.Type) {
					c.add(x.Pos(), "typed-fail-compare", types.ExprString(x)+" compares two failures")
				}
			}
		}
		return true
	})
}

// blanks is typed-discard on an assignment's blank identifiers: an outcome or a failure assigned to _ is dropped.
func (c *typedChecks) blanks(a *ast.AssignStmt) {
	var results []types.Type
	if len(a.Rhs) == 1 && len(a.Lhs) > 1 {
		if tuple, ok := c.info.Types[a.Rhs[0]].Type.(*types.Tuple); ok {
			for i := range tuple.Len() {
				results = append(results, tuple.At(i).Type())
			}
		}
	} else {
		for _, r := range a.Rhs {
			results = append(results, c.info.Types[r].Type)
		}
	}
	for i, l := range a.Lhs {
		if id, ok := l.(*ast.Ident); ok && id.Name == "_" && i < len(results) && results[i] != nil && c.mustUse(results[i]) {
			c.add(l.Pos(), "typed-discard", "an assignment to _ discards "+c.typeName(results[i]))
		}
	}
}

// codecs are the functions and methods that read a value's fields through reflection, by package and name ("" for
// any name in the package).
var codecs = map[string][]string{
	"fmt":           {""},
	"log":           {""},
	"log/slog":      {""},
	"encoding/json": {"Marshal", "MarshalIndent", "Encode"},
	"text/template": {"Execute", "ExecuteTemplate"},
	"html/template": {"Execute", "ExecuteTemplate"},
}

// codec is typed-codec on one call: an argument that is, or holds, a strict core's value.
func (c *typedChecks) codec(call *ast.CallExpr) {
	var obj types.Object
	switch fn := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		obj = c.info.Uses[fn]
	case *ast.SelectorExpr:
		obj = c.info.Uses[fn.Sel]
	}
	if obj == nil || obj.Pkg() == nil {
		return
	}
	names, ok := codecs[obj.Pkg().Path()]
	if !ok || !slices.Contains(names, "") && !slices.Contains(names, obj.Name()) {
		return
	}
	for _, arg := range call.Args {
		if t := c.info.Types[arg].Type; t != nil {
			if core := c.holdsCore(t, map[types.Type]bool{}); core != "" {
				c.add(arg.Pos(), "typed-codec", types.ExprString(arg)+" holds "+core+", passed to "+obj.Pkg().Path()+"."+obj.Name())
			}
		}
	}
}

// holdsCore is the strict core type a value of type t is or holds (through pointers, slices, arrays, maps and struct
// fields), "" for none.
func (c *typedChecks) holdsCore(t types.Type, seen map[types.Type]bool) string {
	if seen[t] {
		return ""
	}
	seen[t] = true
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil && c.strict[n.Obj().Pkg().Path()] {
		return n.Obj().Pkg().Name() + "." + n.Obj().Name()
	}
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		return c.holdsCore(u.Elem(), seen)
	case *types.Slice:
		return c.holdsCore(u.Elem(), seen)
	case *types.Array:
		return c.holdsCore(u.Elem(), seen)
	case *types.Map:
		if k := c.holdsCore(u.Key(), seen); k != "" {
			return k
		}
		return c.holdsCore(u.Elem(), seen)
	case *types.Struct:
		for i := range u.NumFields() {
			if k := c.holdsCore(u.Field(i).Type(), seen); k != "" {
				return k
			}
		}
	}
	return ""
}

// mustUse says whether a value of type t may not be dropped: an outcome.Of, or a failure.
func (c *typedChecks) mustUse(t types.Type) bool {
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == domainKit+"/outcome" &&
		n.Obj().Name() == "Of" {
		return true
	}
	return c.isFail(t)
}

// isFail says whether t is a failure: fail.Fail, a family that embeds it, or a type that implements it.
func (c *typedChecks) isFail(t types.Type) bool {
	if c.fail == nil || t == nil {
		return false
	}
	if _, basic := t.(*types.Basic); basic {
		return false
	}
	return types.Implements(t, c.fail)
}

// failInterface is the kit's fail.Fail as the unit sees it, nil when the unit reaches no failure at all.
func (c *typedChecks) failInterface() *types.Interface {
	var found *types.Interface
	var walk func(p *types.Package, seen map[*types.Package]bool)
	walk = func(p *types.Package, seen map[*types.Package]bool) {
		if found != nil || seen[p] {
			return
		}
		seen[p] = true
		if p.Path() == domainKit+"/fail" {
			if obj := p.Scope().Lookup("Fail"); obj != nil {
				found, _ = obj.Type().Underlying().(*types.Interface)
			}
			return
		}
		for _, i := range p.Imports() {
			walk(i, seen)
		}
	}
	walk(c.pkg, map[*types.Package]bool{})
	return found
}

// typeName is t as a message names it.
func (c *typedChecks) typeName(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

// visitors is typed-visitor-impl over the unit's types: one that implements a strict core's *Visitor interface,
// outside adapters, testkit and cores.
func (c *typedChecks) visitors() {
	switch c.unit.layer {
	case "adapter", "testkit", "core", "core shared":
		return
	}
	var ifaces []*types.TypeName
	for _, p := range c.pkg.Imports() {
		if !c.strict[p.Path()] {
			continue
		}
		for _, name := range p.Scope().Names() {
			obj, ok := p.Scope().Lookup(name).(*types.TypeName)
			if !ok || !obj.Exported() || !strings.HasSuffix(name, "Visitor") {
				continue
			}
			if iface, ok := obj.Type().Underlying().(*types.Interface); ok && iface.NumMethods() > 0 {
				ifaces = append(ifaces, obj)
			}
		}
	}
	for id, obj := range c.info.Defs {
		tn, ok := obj.(*types.TypeName)
		if !ok || id.Name == "_" {
			continue
		}
		for _, v := range ifaces {
			iface := v.Type().Underlying().(*types.Interface)
			if types.Implements(tn.Type(), iface) || types.Implements(types.NewPointer(tn.Type()), iface) {
				c.add(id.Pos(), "typed-visitor-impl", tn.Name()+" implements "+v.Pkg().Name()+"."+v.Name()+" outside an adapter")
			}
		}
	}
}
