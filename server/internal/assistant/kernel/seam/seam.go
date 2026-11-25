package seam

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// RouteTable is where the assistant serves the routes the hub relays to it (package assistant's Service.handle).
type RouteTable interface {
	// Install serves r. The kernel has checked it and wrapped its handler in the catch site.
	Install(r Route)
}

// Access says what a request may do.
type Access interface {
	// Full says whether r came from a device that may do everything.
	Full(r *http.Request) bool
}

// BodyErrors answers a request body that could not be read.
type BodyErrors interface {
	// Write answers err: 413 for a body over the limit, else 400 with bad, the caller's own message.
	Write(w http.ResponseWriter, err, bad error)
}

// Defects hears of a defect a registered route raised, before the kernel answers the request with 500.
type Defects interface {
	// Report records d, raised while serving r.
	Report(r *http.Request, d defect.Defect)
}

// Module is a part of the assistant that registers through the kernel. It takes its own dependencies through its own
// constructor, never from the kernel.
type Module interface {
	// Name is the module's name, once per registry.
	Name() ModuleName
	// Register adds the module's entries through k, once, while assistant.New runs.
	Register(k Kernel)
}

// ModuleName names one module in a registry: lower-case letters, digits and "-". The zero ModuleName names no module;
// only NewModuleName makes one.
type ModuleName struct{ name string }

// NewModuleName is the module name name. An empty name, or one with a character outside [a-z0-9-], is a defect.
func NewModuleName(name string) ModuleName {
	if name == "" {
		defect.Raise("seam.NewModuleName", "a module name is not empty")
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			defect.Raise("seam.NewModuleName", strconv.Quote(name)+" has a character outside [a-z0-9-]")
		}
	}
	return ModuleName{name: name}
}

// Route is one route the hub relays and the handler that serves it. The zero Route is no route; only NewRoute makes
// one.
type Route struct {
	pattern string
	serve   http.HandlerFunc
}

// NewRoute is pattern served by serve. A pattern the hub does not relay (in neither assistantwire.Routes nor
// assistantwire.InboxRoutes), or a nil serve, is a defect.
func NewRoute(pattern string, serve http.HandlerFunc) Route {
	if !slices.Contains(assistantwire.Routes, pattern) && !slices.Contains(assistantwire.InboxRoutes, pattern) {
		defect.Raise("seam.NewRoute", strconv.Quote(pattern)+" is no route the hub relays: list it in "+
			"assistantwire.Routes from its domain's own file")
	}
	if serve == nil {
		defect.Raise("seam.NewRoute", strconv.Quote(pattern)+" has no handler")
	}
	return Route{pattern: pattern, serve: serve}
}

// RouteVisitor reads a Route: how a RouteTable installs one.
type RouteVisitor interface {
	VisitRoute(pattern string, serve http.HandlerFunc)
}

// Accept hands the route's pattern and handler to v. A zero Route is a defect.
func (r Route) Accept(v RouteVisitor) {
	r.made("seam.Route.Accept")
	v.VisitRoute(r.pattern, r.serve)
}

func (r Route) made(op string) {
	if r.serve == nil {
		defect.Raise(op, "a Route comes from NewRoute")
	}
}

// Registry is one assistant Service's registrations: its ports, the modules and routes registered so far, and
// whether New has sealed it. Only NewRegistry makes one.
type Registry struct {
	routes   RouteTable
	access   Access
	bodies   BodyErrors
	defects  Defects
	modules  []ModuleName
	patterns map[string]ModuleName
	sealed   bool
}

// NewRegistry is an open registry over package assistant's ports. A nil port is a defect.
func NewRegistry(routes RouteTable, access Access, bodies BodyErrors, defects Defects) *Registry {
	const op = "seam.NewRegistry"
	switch {
	case routes == nil:
		defect.Raise(op, "the RouteTable port is nil")
	case access == nil:
		defect.Raise(op, "the Access port is nil")
	case bodies == nil:
		defect.Raise(op, "the BodyErrors port is nil")
	case defects == nil:
		defect.Raise(op, "the Defects port is nil")
	}
	return &Registry{routes: routes, access: access, bodies: bodies, defects: defects, patterns: map[string]ModuleName{}}
}

// Register has m register through a Kernel of its own. A zero name, a name another module took, or a call after Seal
// is a defect.
func (r *Registry) Register(m Module) {
	const op = "seam.Registry.Register"
	r.open(op)
	if m == nil {
		defect.Raise(op, "the module is nil")
	}
	name := m.Name()
	if name.name == "" {
		defect.Raise(op, "a ModuleName comes from NewModuleName")
	}
	if slices.Contains(r.modules, name) {
		defect.Raise(op, "module "+strconv.Quote(name.name)+" is registered twice")
	}
	r.modules = append(r.modules, name)
	m.Register(Kernel{reg: r, module: name})
}

// Seal closes the registry: assistant.New seals it before it returns. Registering after it, or sealing again, is a
// defect.
func (r *Registry) Seal() {
	r.open("seam.Registry.Seal")
	r.sealed = true
}

// open raises a defect unless r is a registry NewRegistry made that is not sealed yet.
func (r *Registry) open(op string) {
	if r == nil || r.routes == nil {
		defect.Raise(op, "a Registry comes from NewRegistry")
	}
	if r.sealed {
		defect.Raise(op, "the registry is sealed: modules register while assistant.New runs")
	}
}

// Kernel is what one module registers through. The zero Kernel belongs to no registry; only Registry.Register makes
// one, for the module it registers.
type Kernel struct {
	reg    *Registry
	module ModuleName
}

// Route serves r through the registry's RouteTable, under the kernel's catch site (dispatch.go). A zero Route, a
// pattern already registered, or a call after Seal is a defect.
func (k Kernel) Route(r Route) {
	const op = "seam.Kernel.Route"
	reg := k.registry(op)
	reg.open(op)
	r.made(op)
	if owner, taken := reg.patterns[r.pattern]; taken {
		defect.Raise(op, strconv.Quote(r.pattern)+" is registered twice, by "+strconv.Quote(owner.name)+" and "+
			strconv.Quote(k.module.name))
	}
	reg.patterns[r.pattern] = k.module
	reg.routes.Install(Route{pattern: r.pattern, serve: reg.dispatch(r.serve)})
}

// Full says whether r came from a device that may do everything.
func (k Kernel) Full(r *http.Request) bool { return k.registry("seam.Kernel.Full").access.Full(r) }

// BodyError answers err, a request body that could not be read: 413 for one over the limit, else 400 with bad.
func (k Kernel) BodyError(w http.ResponseWriter, err, bad error) {
	k.registry("seam.Kernel.BodyError").bodies.Write(w, err, bad)
}

func (k Kernel) registry(op string) *Registry {
	if k.reg == nil {
		defect.Raise(op, "a Kernel comes from Registry.Register")
	}
	return k.reg
}
