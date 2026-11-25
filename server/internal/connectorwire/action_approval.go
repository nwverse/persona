package connectorwire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// ActionApprovalRegistrar records that the connector gate already let one
// action through. The random action id, not a static extension credential, is
// the capability the Agent API presents when it consumes the exact write.
type ActionApprovalRegistrar func(ctx context.Context, id string, expires time.Time) error

type actionApprovalRegistrarKey struct{}

// WithActionApprovalRegistrar puts the gate's one-action approval registrar on
// the write's context. It is process-local and never serializes into a request.
func WithActionApprovalRegistrar(ctx context.Context, register ActionApprovalRegistrar) context.Context {
	return context.WithValue(ctx, actionApprovalRegistrarKey{}, register)
}

// RegisterActionApproval records one action after its gate let it through. A
// connector without a registrar keeps its existing behavior.
func RegisterActionApproval(ctx context.Context, id string, expires time.Time) error {
	register, _ := ctx.Value(actionApprovalRegistrarKey{}).(ActionApprovalRegistrar)
	if register == nil {
		return nil
	}
	if id == "" || expires.IsZero() {
		return errors.New("an action approval needs an id and expiry")
	}
	return register(ctx, id, expires)
}

// ActionApprovalID derives the capability id for an action or reply. The seed
// already contains a random 128-bit request id in production; hashing keeps the
// two protocol paths in one fixed, bounded id shape.
func ActionApprovalID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "odc_act" + hex.EncodeToString(sum[:16])
}
