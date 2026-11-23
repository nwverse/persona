package archguard

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The guards of Persona Core and its feature modules (docs/architecture/core-and-modules.md "Guards").

const (
	modulesDir = "internal/modules"
	modulesAll = "internal/modules/all"
	modulesDoc = `see docs/architecture/core-and-modules.md "Guards"`
)

// goListLines runs go list with a template over pattern (relative to the server's module root) and returns its lines.
func goListLines(t *testing.T, format, pattern string) []string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool:", err)
	}
	cmd := exec.Command(goBin, "list", "-f", format, pattern)
	cmd.Dir = filepath.Join("..", "..") // the server's module root
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = string(ee.Stderr)
		}
		t.Fatalf("go list %s: %v %s", pattern, err, msg)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines
}

// goPackages lists the module's packages (its own code, tests left out) and their direct imports.
func goPackages(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, line := range goListLines(t, `{{.ImportPath}}{{range .Imports}} {{.}}{{end}}`, "./...") {
		f := strings.Fields(line)
		rel, ok := strings.CutPrefix(f[0], modulePath)
		if !ok {
			continue
		}
		out[rel] = f[1:]
	}
	return out
}

// No package of the core imports a module: only the modules themselves and the composition roots (testdata/
// module_roots.txt) import internal/modules/…, and a root imports only internal/modules/all. The core reaches a
// module through the kernel (internal/assistant/kernel), so a module can be left out (build tags) or switched off.
// Tests are not read: a core package's test may link internal/modules/all to run as the manager does.
func TestCoreImportsNoModule(t *testing.T) {
	roots := readList(t, "module_roots.txt")
	baseline := readList(t, "module_imports_baseline.txt")
	used := map[string]bool{}
	pkgs := goPackages(t)
	for _, r := range roots {
		if _, ok := pkgs[r]; !ok {
			t.Errorf("testdata/module_roots.txt lists %s, which is no package: delete the line", r)
		}
	}
	for pkg, imports := range pkgs {
		if under(pkg, modulesDir) {
			continue
		}
		for _, imp := range imports {
			rel, ok := strings.CutPrefix(imp, modulePath)
			if !ok || !under(rel, modulesDir) {
				continue
			}
			if rel == modulesAll && slices.Contains(roots, pkg) {
				used["root "+pkg] = true
				continue
			}
			if entry := pkg + " " + rel; slices.Contains(baseline, entry) {
				used[entry] = true
				continue
			}
			t.Errorf("%s imports %s: the core never imports a module; ask the kernel (internal/assistant/kernel) for what "+
				"the module added through modapi, or, for a binary's composition root, import %s and list the package in "+
				"testdata/module_roots.txt; %s", pkg, rel, modulesAll, modulesDoc)
		}
	}
	for _, b := range baseline {
		if !used[b] {
			t.Errorf("testdata/module_imports_baseline.txt lists %q, which no longer happens: delete the line", b)
		}
	}
	for _, r := range roots {
		if !used["root "+r] {
			t.Errorf("testdata/module_roots.txt lists %s, which does not import %s: delete the line", r, modulesAll)
		}
	}
}

// moduleManifest is what the guard reads of a module's module.json (schema/module.schema.json).
type moduleManifest struct {
	ID    string   `json:"id"`
	Needs []string `json:"needs"`
	Wants []string `json:"wants"`
}

// A module uses only the module API: everything it depends on (its whole import graph, tests left out) is the
// standard library, internal/modapi and the other contract packages (testdata/contracts.txt), its own packages, and
// the modules its manifest needs or wants. Each module directory has a module.json whose id is the directory's name.
func TestModulesUseOnlyTheSPI(t *testing.T) {
	contracts := readList(t, "contracts.txt")
	root := filepath.Join("..", "modules")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "all" {
			continue
		}
		id := e.Name()
		raw, err := os.ReadFile(filepath.Join(root, id, "module.json"))
		if err != nil {
			t.Errorf("internal/modules/%s has no module.json: every module declares itself (schema/module.schema.json); %s", id, modulesDoc)
			continue
		}
		var m moduleManifest
		if err := json.Unmarshal(raw, &m); err != nil || m.ID != id {
			t.Errorf("internal/modules/%s/module.json: id %q (%v), want the directory's name", id, m.ID, err)
			continue
		}
		own := modulesDir + "/" + id
		allowed := []string{own}
		for _, other := range append(slices.Clone(m.Needs), m.Wants...) {
			allowed = append(allowed, modulesDir+"/"+other)
		}
		for _, dep := range goList(t, "{{if not .Standard}}{{.ImportPath}}{{end}}", true, own+"/...") {
			rel, ok := strings.CutPrefix(dep, modulePath)
			switch {
			case !ok && slices.Contains(contracts, dep):
			case !ok:
				t.Errorf("module %s depends on %s, outside the standard library: a module uses only modapi, the contract "+
					"packages and the modules it needs; %s", id, dep, modulesDoc)
			case slices.Contains(contracts, rel), slices.ContainsFunc(allowed, func(a string) bool { return under(rel, a) }):
			default:
				t.Errorf("module %s depends on %s: a module reaches the core only through internal/modapi (add what it "+
					"needs to modapi.Host) and other modules only when its module.json needs or wants them; %s", id, rel, modulesDoc)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Error("no module under internal/modules: the guard checks nothing")
	}
}
