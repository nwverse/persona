package seam

import (
	"context"
	"strconv"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// The loops: what a module runs on its own for as long as the assistant runs. They start where Service.Run started
// the loops it lists, after them and before its schedule loop (Registry.StartLoops). Goroutine start order means
// nothing there, so a loop registered here runs as a listed one does. Nothing recovers a panic in one, as nothing
// recovers one in a listed loop: a panic that ends the manager is the same panic wherever its loop was started.

// LoopName names one loop in a registry: lower-case letters, digits and "-". The zero LoopName names no loop; only
// NewLoopName makes one.
type LoopName struct{ name string }

// NewLoopName is the loop name name. An empty name, or one with a character outside [a-z0-9-], is a defect.
func NewLoopName(name string) LoopName {
	checkName("seam.NewLoopName", "a loop name", name)
	return LoopName{name: name}
}

// registeredLoop is a loop and the module that registered it.
type registeredLoop struct {
	module ModuleName
	name   LoopName
	run    func(ctx context.Context)
}

// Loop has run run from Service.Run until its context ends, on a goroutine of its own (Registry.StartLoops). A zero
// name, a name another loop took, a nil run, or a call after Seal is a defect.
func (k Kernel) Loop(name LoopName, run func(ctx context.Context)) {
	const op = "seam.Kernel.Loop"
	reg := k.registry(op)
	reg.open(op)
	if name.name == "" {
		defect.Raise(op, "a LoopName comes from NewLoopName")
	}
	if run == nil {
		defect.Raise(op, "loop "+strconv.Quote(name.name)+" has no function")
	}
	for _, l := range reg.loops {
		if l.name == name {
			defect.Raise(op, "loop "+strconv.Quote(name.name)+" is registered twice, by "+strconv.Quote(l.module.name)+
				" and "+strconv.Quote(k.module.name))
		}
	}
	reg.loops = append(reg.loops, registeredLoop{module: k.module, name: name, run: run})
}

// StartLoops starts each registered loop, in registration order, on a goroutine of its own, and returns: the loops run
// until ctx ends. Service.Run calls it once, after the loops it lists and before its schedule loop.
func (r *Registry) StartLoops(ctx context.Context) {
	r.made("seam.Registry.StartLoops")
	for _, l := range r.loops {
		go l.run(ctx)
	}
}
