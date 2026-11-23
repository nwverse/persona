package hub

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// partnerStore keeps the partner hubs and their pairing codes (internal/db/partners.go on Postgres; the mesh tests'
// meshStore in memory). Optional like peerStore: a hub whose store lacks it has no partner hubs.
type partnerStore interface {
	PartnerHubs(ctx context.Context) ([]db.PartnerHub, error)
	SavePartnerHub(ctx context.Context, p db.PartnerHub) error
	SetPartnerDial(ctx context.Context, hubID, dial string) (bool, error)
	UpdatePartnerSeen(ctx context.Context, p db.PartnerHub) error
	DeletePartnerHub(ctx context.Context, hubID string) (bool, error)
	CreatePartnerCode(ctx context.Context, hash string, expiresAt time.Time) error
	PartnerCodeValid(ctx context.Context, hash string) (bool, error)
	RedeemPartnerCode(ctx context.Context, codeHash string, p db.PartnerHub, cred db.DeviceToken, credHash string) (bool, error)
	PrunePartnerCodes(ctx context.Context, before time.Time) error
}

// Asserted here because partnerStore() finds out at run time: a method gone astray would turn partner hubs off.
var _ partnerStore = (*db.DB)(nil)

func (h *Hub) partnerStore() partnerStore {
	ps, _ := h.store.(partnerStore)
	return ps
}
