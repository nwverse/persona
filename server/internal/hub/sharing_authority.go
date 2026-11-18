package hub

import (
	"context"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
)

type shareRoutingStore interface {
	ShareRouting(context.Context, string, string, string) (db.ShareRoute, error)
	RetireShareMachine(context.Context, string, string) error
	ShareManagerByHandle(context.Context, []byte) (string, error)
	VerifyShareAuthority(context.Context, string, devicewire.ShareAuthority) error
}

func (h *Hub) shareRoutingStore() shareRoutingStore { s, _ := h.store.(shareRoutingStore); return s }
func (h *Hub) retireShareMachine(ctx context.Context, id string) error {
	if s := h.shareRoutingStore(); s != nil {
		return s.RetireShareMachine(ctx, h.hubID(), id)
	}
	return nil
}
