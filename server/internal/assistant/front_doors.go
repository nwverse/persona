package assistant

import (
	"context"
	"slices"

	"github.com/nwverse/persona/server/internal/assistant/kernel/seam"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// The front doors: a domain in package assistant registers its routes and its loops from an init() in its own file,
// never with a line in New or in Service.Run (docs/server/architecture.md "Registering a domain's entries"):
//
//	func init() {
//		registerRoutes(seam.NewModuleName("x"), (*Service).xRoutes)
//		registerLoop(seam.NewLoopName("x"), (*Service).xLoop)
//	}
//
// New hands what the doors took to the Service's registry (kernel/seam) before the modules register: each door's
// routes as the module its name names, so their Service.handle calls register through that module's Kernel, and the
// loops as the module "assistant-loops". Service.Run starts the registry's loops after the loops it lists and before
// scheduleLoop. The route calls already in New and the loops already in Run stay where they are.

// doors are the routes and loops the domains' init()s registered. Only init() adds to them, so they are complete before
// any New reads them.
var doors frontDoors

// registerRoutes has New register routes, a domain's route function, as the module name. A zero name, a name another
// registerRoutes took, or a nil routes is a defect, raised at the init() that called it.
func registerRoutes(name seam.ModuleName, routes func(*Service)) {
	doors.addRoutes(name, routes)
}

// registerLoop has Service.Run start loop, a domain's loop, as the loop name. A zero name, a name another registerLoop
// took, or a nil loop is a defect, raised at the init() that called it.
func registerLoop(name seam.LoopName, loop func(*Service, context.Context)) {
	doors.addLoop(name, loop)
}

// frontDoors are the routes and loops registered through the front doors, each kind in registration order. The zero
// frontDoors has none.
type frontDoors struct {
	routes []routesDoor
	loops  []loopDoor
}

// routesDoor is one domain's route function and the module name it registers as.
type routesDoor struct {
	name   seam.ModuleName
	routes func(*Service)
}

// loopDoor is one domain's loop and its name.
type loopDoor struct {
	name seam.LoopName
	loop func(*Service, context.Context)
}

// addRoutes is registerRoutes on d.
func (d *frontDoors) addRoutes(name seam.ModuleName, routes func(*Service)) {
	const op = "assistant.registerRoutes"
	switch {
	case name == seam.ModuleName{}:
		defect.Raise(op, "a ModuleName comes from seam.NewModuleName")
	case routes == nil:
		defect.Raise(op, "the route function is nil")
	case slices.ContainsFunc(d.routes, func(r routesDoor) bool { return r.name == name }):
		defect.Raise(op, "the module name is registered twice: give each domain's routes a name of its own")
	}
	d.routes = append(d.routes, routesDoor{name: name, routes: routes})
}

// addLoop is registerLoop on d.
func (d *frontDoors) addLoop(name seam.LoopName, loop func(*Service, context.Context)) {
	const op = "assistant.registerLoop"
	switch {
	case name == seam.LoopName{}:
		defect.Raise(op, "a LoopName comes from seam.NewLoopName")
	case loop == nil:
		defect.Raise(op, "the loop is nil")
	case slices.ContainsFunc(d.loops, func(l loopDoor) bool { return l.name == name }):
		defect.Raise(op, "the loop name is registered twice: give each loop a name of its own")
	}
	d.loops = append(d.loops, loopDoor{name: name, loop: loop})
}

// register hands what d took to reg for s: each routes door as a module of its own, then the loops as one module. A
// door's module name that another module takes later, or took before, is a defect where reg registers the second.
func (d frontDoors) register(s *Service, reg *seam.Registry) {
	for _, door := range d.routes {
		reg.Register(routesModule{door: door, s: s})
	}
	reg.Register(loopsModule{loops: d.loops, s: s})
}

// routesModule is one routes door as a module of the registry: its routes register through its Kernel.
type routesModule struct {
	door routesDoor
	s    *Service
}

// Name is the name the door was registered with.
func (m routesModule) Name() seam.ModuleName { return m.door.name }

// Register runs the door's route function with each Service.handle in it registering through k (routesThrough).
func (m routesModule) Register(k seam.Kernel) { m.s.routesThrough(k, func() { m.door.routes(m.s) }) }

// loopsModule is the loops the doors took, as one module of the registry.
type loopsModule struct {
	loops []loopDoor
	s     *Service
}

// Name is the loops' module name.
func (loopsModule) Name() seam.ModuleName { return seam.NewModuleName("assistant-loops") }

// Register registers each loop through k, in the order the doors took them.
func (m loopsModule) Register(k seam.Kernel) {
	for _, l := range m.loops {
		k.Loop(l.name, func(ctx context.Context) { l.loop(m.s, ctx) })
	}
}
