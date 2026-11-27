// Package seam is a planted application seam (TestDomainTypedPlanted): forwarding a type parameter to another
// exported generic in the same seam passes, an unexported generic helper passes, and a concrete non-contract type
// argument fails.
//
//persona:layer application seam
package seam

import (
	"context"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

type box[T any] struct{ v T }

// Box is an exported generic seam type.
type Box[T any] struct{ v T }

// Entry is a planted seam registration value.
type Entry struct{ keep box[func()] }

// Register is an exported generic seam function.
func Register[P, R any](fn func(context.Context, P) (R, error)) Entry {
	_ = fn
	return Entry{keep: box[func()]{}}
}

// Wrap forwards its own exported type parameters to Register, which callers are checked on.
func Wrap[P, R any](fn func(context.Context, P) (R, error)) Entry {
	return Register(fn)
}

func unexportedWrap[P, R any](fn func(context.Context, P) (R, error)) Entry {
	return Register(fn) // planted: typed-seam-contract
}

// Bad instantiates Register with a concrete non-contract type inside the seam itself.
func Bad() Entry {
	_ = unexportedWrap(func(_ context.Context, _ map[string]any) (assistantwire.InboxAck, error) {
		return assistantwire.InboxAck{}, nil
	})
	return Register(func(_ context.Context, _ map[string]any) (assistantwire.InboxAck, error) { // planted: typed-seam-contract
		return assistantwire.InboxAck{}, nil
	})
}
