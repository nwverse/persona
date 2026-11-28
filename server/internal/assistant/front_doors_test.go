package assistant

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/kernel/seam"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// The front doors (front_doors.go). Each test registers on a frontDoors of its own, never on the package's doors, so the
// tests run in parallel and the doors' init()s are never undone.

// raisesDefect runs f and fails unless it raises a defect.Defect whose text contains want.
func raisesDefect(t *testing.T, want string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		d, isDefect := recover().(defect.Defect)
		switch {
		case !isDefect:
			t.Errorf("want a defect.Defect containing %q, got none", want)
		case !strings.Contains(d.Error(), want):
			t.Errorf("defect %q does not say %q", d.Error(), want)
		}
	}()
	f()
}

// A door refuses a name it took already, a nil function and a zero name; an empty name is refused where it is made.
func TestFrontDoorsRefuseRegistrationMistakes(t *testing.T) {
	t.Parallel()
	var d frontDoors
	routes := func(*Service) {}
	loop := func(*Service, context.Context) {}
	d.addRoutes(seam.NewModuleName("x"), routes)
	d.addLoop(seam.NewLoopName("x"), loop) // a loop and a module of one name are two names
	raisesDefect(t, "assistant.registerRoutes: the module name is registered twice", func() {
		d.addRoutes(seam.NewModuleName("x"), routes)
	})
	raisesDefect(t, "assistant.registerLoop: the loop name is registered twice", func() {
		d.addLoop(seam.NewLoopName("x"), loop)
	})
	raisesDefect(t, "assistant.registerRoutes: the route function is nil", func() { d.addRoutes(seam.NewModuleName("y"), nil) })
	raisesDefect(t, "assistant.registerLoop: the loop is nil", func() { d.addLoop(seam.NewLoopName("y"), nil) })
	raisesDefect(t, "assistant.registerRoutes: a ModuleName comes from seam.NewModuleName", func() {
		d.addRoutes(seam.ModuleName{}, routes)
	})
	raisesDefect(t, "assistant.registerLoop: a LoopName comes from seam.NewLoopName", func() {
		d.addLoop(seam.LoopName{}, loop)
	})
	raisesDefect(t, "seam.NewModuleName: a module name is not empty", func() { d.addRoutes(seam.NewModuleName(""), routes) })
	raisesDefect(t, "seam.NewLoopName: a loop name is not empty", func() { d.addLoop(seam.NewLoopName(""), loop) })
	if len(d.routes) != 1 || len(d.loops) != 1 {
		t.Errorf("the refused registrations were kept: %d routes doors, %d loops", len(d.routes), len(d.loops))
	}
}

// What the doors took reaches the registry: each door's routes through Service.handle as its own module, and the
// loops, which StartLoops starts with the Service they belong to.
func TestFrontDoorsRegisterThroughTheKernel(t *testing.T) {
	t.Parallel()
	const savedList, savedAdd = "GET /api/v1/assistant/saved-prompts", "POST /api/v1/assistant/saved-prompts"
	serve := func(http.ResponseWriter, *http.Request) {}
	type started struct {
		name string
		s    *Service
	}
	ran := make(chan started, 2)
	var d frontDoors
	d.addRoutes(seam.NewModuleName("prompts"), func(s *Service) { s.handle(savedList, serve) })
	d.addRoutes(seam.NewModuleName("prompt-edits"), func(s *Service) { s.handle(savedAdd, serve) })
	for _, name := range []string{"one", "two"} {
		d.addLoop(seam.NewLoopName(name), func(s *Service, ctx context.Context) {
			ran <- started{name, s}
			<-ctx.Done()
		})
	}
	s := &Service{}
	p := &kernelPorts{}
	reg := seam.NewRegistry(p, p, p, p)
	d.register(s, reg)
	reg.Seal()
	if want := []string{savedList, savedAdd}; !slices.Equal(p.routes, want) {
		t.Errorf("the doors' routes reached the registry as %v, want %v", p.routes, want)
	}
	if s.routing != nil {
		t.Error("routesThrough left the Service routing through a Kernel")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.StartLoops(ctx)
	var names []string
	for range 2 {
		select {
		case l := <-ran:
			names = append(names, l.name)
			if l.s != s {
				t.Errorf("loop %s runs with another Service", l.name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("after %v, no loop started within 5s", names)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"one", "two"}) {
		t.Errorf("started %v, want one and two", names)
	}
}

// New keeps one duplicate check across the doors and the modules: a door's module name another module takes is a
// defect where the second registers, and so is a route pattern both serve.
func TestFrontDoorsShareTheRegistrysNames(t *testing.T) {
	t.Parallel()
	const savedList = "GET /api/v1/assistant/saved-prompts"
	serve := func(http.ResponseWriter, *http.Request) {}
	p := &kernelPorts{}
	var d frontDoors
	d.addRoutes(seam.NewModuleName("inbox"), func(*Service) {})
	d.addRoutes(seam.NewModuleName("prompts"), func(s *Service) { s.handle(savedList, serve) })
	s := &Service{}
	reg := seam.NewRegistry(p, p, p, p)
	d.register(s, reg)
	raisesDefect(t, `seam.Registry.Register: module "inbox" is registered twice`, func() { reg.Register(newInboxModule(s)) })
	var none frontDoors // its loops' module is the one d registered: "assistant-loops" is no door's name
	raisesDefect(t, `seam.Registry.Register: module "assistant-loops" is registered twice`, func() { none.register(s, reg) })

	var other frontDoors
	other.addRoutes(seam.NewModuleName("prompts-again"), func(s *Service) { s.handle(savedList, serve) })
	raisesDefect(t, `seam.Kernel.Route: "GET /api/v1/assistant/saved-prompts" is registered twice, by "prompts" and "prompts-again"`,
		func() { other.register(s, reg) })
}
