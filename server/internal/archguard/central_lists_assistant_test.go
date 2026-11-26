package archguard

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"
)

// The freezes of the assistant's two longest central lists: the top-level s.X() calls in New
// (internal/assistant/service.go) and the go statements of Service.Run (internal/assistant/schedule.go). A domain in
// package assistant registers its routes and loops from an init() in its own file through registerRoutes and
// registerLoop (internal/assistant/front_doors.go), and a module in a subpackage registers through reg.Register, so
// neither list takes a new name. The calls and loops already listed stay where they are.

// The rows are appended from this file, as a domain's entries are, so that a PR adding another list's row to the
// literal in central_lists_test.go never meets them in one hunk.
func init() {
	centralLists = append(centralLists,
		centralList{
			id:     "assistant_new_calls",
			source: "internal/assistant/service.go",
			what:   "top-level s.X() call in the assistant's New",
			names:  newCalls,
			seam: "register a domain's routes from an init() in its own file, func init() { registerRoutes(" +
				"seam.NewModuleName(\"x\"), (*Service).xRoutes) } (assistant/front_doors.go); a module in a subpackage " +
				"registers through reg.Register",
		},
		centralList{
			id:     "assistant_run_loops",
			source: "internal/assistant/schedule.go",
			what:   "loop Service.Run starts",
			names:  runLoops,
			seam: "start a domain's loop from an init() in its own file, func init() { registerLoop(" +
				"seam.NewLoopName(\"x\"), (*Service).xLoop) } (assistant/front_doors.go); a module in a subpackage " +
				"registers it through Kernel.Loop",
		},
	)
}

// newCalls reads the statements of the function New that call a method of s, each as s.X ("s.archiveRoutes"): only
// those at the top level of its body, where the route calls are listed.
func newCalls(f *ast.File) []centralName {
	var out []centralName
	for _, stmt := range funcBody(f, "", "New") {
		call, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		if c, ok := call.X.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "s" {
					out = append(out, centralName{types.ExprString(sel), sel})
				}
			}
		}
	}
	return out
}

// runLoops reads the go statements at the top level of Service.Run's body, each by what it calls ("s.a2aLoop"), so a
// go statement of any form is a name.
func runLoops(f *ast.File) []centralName {
	var out []centralName
	for _, stmt := range funcBody(f, "Service", "Run") {
		if g, ok := stmt.(*ast.GoStmt); ok {
			out = append(out, centralName{types.ExprString(g.Call.Fun), g.Call.Fun})
		}
	}
	return out
}

// funcBody is the top-level statements of f's function name, or of its method name on recv when recv is not empty.
func funcBody(f *ast.File, recv, name string) []ast.Stmt {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Body == nil {
			continue
		}
		switch {
		case recv == "" && fn.Recv == nil:
		case recv != "" && fn.Recv != nil && len(fn.Recv.List) == 1 && receiverType(fn.Recv.List[0].Type) == recv:
		default:
			continue
		}
		return fn.Body.List
	}
	return nil
}

// The two freezes on planted sources: a new top-level s.X() call in New, and a new go statement in Run, of a method or
// a function literal, fail and name the front door and the doc; a call that is no s.X() (reg.Register, a nested one)
// and a statement that is no go statement are no names; a pending name passes once its PR merges.
func TestAssistantCentralListsFreeze(t *testing.T) {
	const service = `package assistant

func New(o Options) *Service {
	s := &Service{}
	s.archiveRoutes()
	s.kbInit(o.KB) // a comment
	reg := s.newRegistry()
	defer reg.Seal()
	reg.Register(saved.NewModule())
	s.todosV2BootEpoch.Store(nil)
	if o.KB != nil {
		s.nestedRoutes()
	}
	return s
}

func (s *Service) handle() { s.elsewhereRoutes() }
`
	const schedule = `package assistant

func (s *Service) Run(ctx context.Context) {
	s.runModuleJobs(ctx)
	go s.a2aLoop(ctx)
	go s.kbLoop(ctx) // a comment
	s.kernel.StartLoops(ctx)
	s.scheduleLoop(ctx)
}

func (s *Service) Other(ctx context.Context) { go s.otherLoop(ctx) }

func Run() { go elsewhere() }
`
	for _, c := range []struct {
		id, src, list string
		at, planted   string // the planted statement goes after this text of src
		name, door    string // what the failure names
	}{
		{"assistant_new_calls", service, "s.archiveRoutes\ns.kbInit\n", "\ts.archiveRoutes()\n", "\ts.plantedRoutes()\n",
			"s.plantedRoutes", "registerRoutes("},
		{"assistant_run_loops", schedule, "s.a2aLoop\ns.kbLoop\n", "\tgo s.a2aLoop(ctx)\n", "\tgo s.plantedLoop(ctx)\n",
			"s.plantedLoop", "registerLoop("},
		{"assistant_run_loops", schedule, "s.a2aLoop\ns.kbLoop\n", "\tgo s.a2aLoop(ctx)\n", "\tgo func() {}()\n",
			"(func() literal)", "registerLoop("},
	} {
		l := centralListByID(t, c.id)
		check := func(src, list string) ([]string, []string) {
			t.Helper()
			failures, notices, err := checkCentralList(l, []byte(src), list)
			if err != nil {
				t.Fatal(err)
			}
			return failures, notices
		}
		if failures, notices := check(c.src, c.list); len(failures)+len(notices) != 0 {
			t.Errorf("%s: unplanted source reports %q, %q", c.id, failures, notices)
		}
		planted := strings.Replace(c.src, c.at, c.at+c.planted, 1)
		failures, _ := check(planted, c.list)
		if len(failures) != 1 || !strings.Contains(failures[0], " "+c.name+" is a new ") ||
			!strings.Contains(failures[0], c.door) || !strings.Contains(failures[0], centralListsDoc) {
			t.Errorf("%s: planting %s reports %q, want one failure naming it, %s and the doc", c.id, c.name, failures, c.door)
		}
		if failures, _ := check(planted, c.list+c.name+" #4512\n"); len(failures) != 0 {
			t.Errorf("%s: %s, pending with its PR, still fails once its PR merges: %q", c.id, c.name, failures)
		}
	}
}
