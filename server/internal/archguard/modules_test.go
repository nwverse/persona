package archguard

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The guards of Persona Core and its feature modules (docs/architecture/core-and-modules.md "Guards"). They read the
// import lines of every .go file, whatever its build constraint or GOOS: a file only a persona_core_only build, or
// only Windows, compiles is checked as well as any other.

const (
	modulesDir = "internal/modules"
	modulesAll = "internal/modules/all"
	modulesDoc = `see docs/architecture/core-and-modules.md "Guards"`
	// repoPath prefixes every Go module of this repository (the server's, the SDK's); an import outside it is external.
	repoPath = "github.com/nwverse/persona/"
)

// goFile is one .go file's package (its directory under the server module), whether it is a test, and its imports.
type goFile struct {
	pkg, name string
	test      bool
	imports   []string
}

// scanGoFiles reads the imports of every .go file under root (the server module's root), testdata, vendor and hidden
// directories left out.
func scanGoFiles(t *testing.T, root string) []goFile {
	t.Helper()
	var out []goFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		dir, _ := filepath.Rel(root, filepath.Dir(path))
		g := goFile{pkg: filepath.ToSlash(dir), name: name, test: strings.HasSuffix(name, "_test.go")}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			g.imports = append(g.imports, p)
		}
		out = append(out, g)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// coreModuleImports are the core's imports of a module, as "<package> <module package>" (a test file's as
// "<package> (test) <module package>"), and the roots that import internal/modules/all. A root's code may import
// internal/modules/all; so may any core package's tests, to run as the manager does; nothing else outside
// internal/modules imports a module.
func coreModuleImports(files []goFile, roots []string) (violations []string, rootsUsed map[string]bool) {
	rootsUsed = map[string]bool{}
	for _, f := range files {
		if under(f.pkg, modulesDir) {
			continue
		}
		for _, imp := range f.imports {
			rel, ok := strings.CutPrefix(imp, modulePath)
			if !ok || !under(rel, modulesDir) {
				continue
			}
			switch {
			case rel == modulesAll && f.test:
			case rel == modulesAll && slices.Contains(roots, f.pkg):
				rootsUsed[f.pkg] = true
			case f.test:
				violations = append(violations, f.pkg+" (test) "+rel)
			default:
				violations = append(violations, f.pkg+" "+rel)
			}
		}
	}
	slices.Sort(violations)
	return slices.Compact(violations), rootsUsed
}

// No package of the core imports a module: only the modules themselves and the composition roots (testdata/
// module_roots.txt) import internal/modules/…, and a root imports only internal/modules/all. The core reaches a
// module through the kernel (internal/assistant/kernel), so a module can be left out (build tags) or switched off.
func TestCoreImportsNoModule(t *testing.T) {
	roots := readList(t, "module_roots.txt")
	baseline := readList(t, "module_imports_baseline.txt")
	violations, used := coreModuleImports(scanGoFiles(t, filepath.Join("..", "..")), roots)
	for _, v := range violations {
		if !slices.Contains(baseline, v) {
			t.Errorf("%s: the core never imports a module; ask the kernel (internal/assistant/kernel) for what the module "+
				"added through modapi, or, for a binary's composition root, import %s and list the package in "+
				"testdata/module_roots.txt; %s", v, modulesAll, modulesDoc)
		}
	}
	for _, b := range baseline {
		if !slices.Contains(violations, b) {
			t.Errorf("testdata/module_imports_baseline.txt lists %q, which no longer happens: delete the line", b)
		}
	}
	for _, r := range roots {
		if !used[r] {
			t.Errorf("testdata/module_roots.txt lists %s, which does not import %s: delete the line", r, modulesAll)
		}
	}
}

// moduleManifest is what the guard reads of a module's module.json (schema/module.schema.json).
type moduleManifest struct {
	ID    string   `json:"id"`
	Needs []string `json:"needs"`
}

// moduleImportProblems checks the modules under root/internal/modules: each directory but all has a module.json whose
// id is its name, and every file in it (tests and every build constraint included) imports only the standard library,
// external modules (a library outside this repository cannot import the core), internal/modapi and the other contract
// packages, the core libraries (testdata/module_libraries.txt, leaves: TestModuleLibrariesAreLeaves), its own packages,
// and the modules its manifest needs. A module it only wants may be left out of the build (persona_without_<id>), so it
// is reached through the kernel, never imported.
func moduleImportProblems(t *testing.T, root string, files []goFile, contracts, libraries []string) (problems []string, checked int) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, modulesDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "all" {
			continue
		}
		id := e.Name()
		raw, err := os.ReadFile(filepath.Join(root, modulesDir, id, "module.json"))
		if err != nil {
			problems = append(problems, "internal/modules/"+id+" has no module.json: every module declares itself (schema/module.schema.json)")
			continue
		}
		var m moduleManifest
		if err := json.Unmarshal(raw, &m); err != nil || m.ID != id {
			problems = append(problems, "internal/modules/"+id+"/module.json: id "+strconv.Quote(m.ID)+", want the directory's name")
			continue
		}
		own := modulesDir + "/" + id
		allowed := []string{own}
		for _, need := range m.Needs {
			allowed = append(allowed, modulesDir+"/"+need)
		}
		for _, f := range files {
			if !under(f.pkg, own) {
				continue
			}
			for _, imp := range f.imports {
				rel, inServer := strings.CutPrefix(imp, modulePath)
				switch {
				case !inServer && !strings.Contains(strings.SplitN(imp, "/", 2)[0], "."): // the standard library
				case !inServer && slices.Contains(contracts, imp):
				case !inServer && !strings.HasPrefix(imp, repoPath): // an external module
				case !inServer:
					problems = append(problems, "module "+id+" ("+f.pkg+"/"+f.name+") imports "+imp+", another module of this "+
						"repository: a module uses only modapi, the contract packages, the core libraries and the modules it needs")
				case slices.Contains(contracts, rel), slices.Contains(libraries, rel),
					slices.ContainsFunc(allowed, func(a string) bool { return under(rel, a) }):
				default:
					problems = append(problems, "module "+id+" ("+f.pkg+"/"+f.name+") imports "+rel+": a module reaches the core only "+
						"through internal/modapi (add what it needs to modapi.Host) and imports another module only when its "+
						"module.json needs it (a module it wants is reached through the kernel)")
				}
			}
		}
		checked++
	}
	return problems, checked
}

// A module uses only the module API (moduleImportProblems).
func TestModulesUseOnlyTheSPI(t *testing.T) {
	root := filepath.Join("..", "..")
	problems, checked := moduleImportProblems(t, root, scanGoFiles(t, root), readList(t, "contracts.txt"),
		readList(t, "module_libraries.txt"))
	for _, p := range problems {
		t.Errorf("%s; %s", p, modulesDoc)
	}
	if checked == 0 {
		t.Error("no module under internal/modules: the guard checks nothing")
	}
}

// The core libraries modules may import (testdata/module_libraries.txt) are leaves: each depends, transitively, only on
// the standard library, external modules, the contract packages and other listed libraries, so importing one brings
// no state or part of the core into a module. None is a module or under internal/modules.
func TestModuleLibrariesAreLeaves(t *testing.T) {
	contracts := readList(t, "contracts.txt")
	libraries := readList(t, "module_libraries.txt")
	if len(libraries) == 0 {
		t.Fatal("testdata/module_libraries.txt lists nothing")
	}
	for _, lib := range libraries {
		if under(lib, modulesDir) || slices.Contains(contracts, lib) {
			t.Errorf("testdata/module_libraries.txt lists %s, a module or a contract package: list only core libraries; %s", lib, modulesDoc)
			continue
		}
		for _, dep := range goList(t, "{{if not .Standard}}{{.ImportPath}}{{end}}", true, lib) {
			rel, ok := strings.CutPrefix(dep, modulePath)
			switch {
			case !ok && !strings.HasPrefix(dep, repoPath): // an external module
			case !ok && slices.Contains(contracts, dep):
			case !ok:
				t.Errorf("core library %s depends on %s, another module of this repository: a library modules may import "+
					"depends only on contracts and other libraries; %s", lib, dep, modulesDoc)
			case rel == lib, slices.Contains(contracts, rel), slices.Contains(libraries, rel):
			default:
				t.Errorf("core library %s depends on %s, which is neither a contract nor a listed library: a module importing "+
					"it would reach the core; take it off testdata/module_libraries.txt or cut the dependency; %s", lib, rel, modulesDoc)
			}
		}
	}
}

// The guards see files no default build compiles: a core file only persona_core_only builds that imports a module,
// a Windows-only module file that imports the core, a module that imports one it only wants, and one that imports
// another module of this repository are each caught; an external module and a listed core library are not.
func TestModuleGuardsSeeEveryBuild(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	imp := func(p string) string { return strconv.Quote(modulePath + p) }
	write("internal/assistant/core_only.go", "//go:build persona_core_only\n\npackage assistant\n\nimport _ "+imp("internal/modules/briefing")+"\n")
	write("internal/assistant/x_test.go", "package assistant\n\nimport _ "+imp("internal/modules/all")+"\n")
	write("internal/manager/modules.go", "package manager\n\nimport _ "+imp("internal/modules/all")+"\n")
	write("internal/modules/briefing/module.json", `{"id":"briefing","api":1,"origin":"builtin","wants":["voice"]}`)
	write("internal/modules/briefing/b_windows.go", "package briefing\n\nimport _ "+imp("internal/assistant")+"\n")
	write("internal/modules/briefing/b.go", "package briefing\n\nimport (\n\t_ \"encoding/json\"\n\t_ "+imp("internal/modapi")+
		"\n\t_ "+imp("internal/modules/voice")+"\n)\n")
	write("internal/modules/voice/module.json", `{"id":"voice","api":1,"origin":"builtin"}`)
	write("internal/modules/voice/v.go", "//go:build !persona_without_voice\n\npackage voice\n\nimport (\n\t_ \"example.com/imap\"\n\t_ "+
		imp("internal/i18n")+"\n\t_ \"github.com/nwverse/persona/other\"\n)\n")
	files := scanGoFiles(t, root)

	violations, used := coreModuleImports(files, []string{"internal/manager"})
	if want := []string{"internal/assistant internal/modules/briefing"}; !slices.Equal(violations, want) || !used["internal/manager"] {
		t.Fatalf("core imports %v (roots %v), want %v", violations, used, want)
	}
	problems, checked := moduleImportProblems(t, root, files, []string{"internal/modapi"}, []string{"internal/i18n"})
	all := strings.Join(problems, "\n")
	if checked != 2 || len(problems) != 3 || !strings.Contains(all, "imports internal/assistant") ||
		!strings.Contains(all, "imports internal/modules/voice") || !strings.Contains(all, "imports github.com/nwverse/persona/other") {
		t.Fatalf("checked %d modules, problems %q", checked, problems)
	}
}
