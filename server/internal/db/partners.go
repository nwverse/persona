package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// PartnerHub is an independent hub this one is paired with as a partner (migration 20261122193642, hub/partner_store.go):
// not a mesh peer.
type PartnerHub struct {
	HubID     string
	Name      string
	FP        string   // the partner's TLS key pin; "" when it has no TLS listener (it dials this hub, never the reverse)
	Endpoints []string // its https candidates
	// Token is the credential the partner issued to this hub, sealed by the hub (never stored in the clear).
	Token string
	// CredentialID is the device token this hub issued to the partner.
	CredentialID string
	Dial         string // "auto" (this hub dials it) or "never" (it waits for the partner to)
	// CreatedAt is when this pairing was made (the managers' PairedAt): a pairing is never replaced, so a hub paired
	// again after an unpair has a later one. Saved as given (zero: now), to the microsecond Postgres keeps.
	CreatedAt time.Time
	UpdatedAt time.Time
	SeenAt    *time.Time // when a link with it was last up
}

const partnerHubColumns = `hub_id, name, fp, endpoints, token, credential_id, dial, created_at, updated_at, seen_at`

func (d *DB) PartnerHubs(ctx context.Context) ([]PartnerHub, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+partnerHubColumns+` FROM partner_hubs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PartnerHub, error) {
		var p PartnerHub
		var eps []byte
		err := r.Scan(&p.HubID, &p.Name, &p.FP, &eps, &p.Token, &p.CredentialID, &p.Dial, &p.CreatedAt, &p.UpdatedAt, &p.SeenAt)
		if err == nil {
			err = json.Unmarshal(eps, &p.Endpoints)
		}
		return p, err
	})
}

// ErrPartnerPaired: that hub is a partner already. A pairing never replaces another (whoever holds a code names its
// own hub_id, so it must not take over a partner's place): the owner unpairs first.
var ErrPartnerPaired = errors.New("that hub is a partner already")

// insertPartnerHub is a new pairing's row; nothing (no row affected) when the hub has one.
const insertPartnerHub = `INSERT INTO partner_hubs (hub_id, name, fp, endpoints, token, credential_id, dial, created_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8) ON CONFLICT (hub_id) DO NOTHING`

// SavePartnerHub adds a new partner (the pasting side's save); ErrPartnerPaired when that hub is one already.
func (d *DB) SavePartnerHub(ctx context.Context, p PartnerHub) error {
	tag, err := d.pool.Exec(ctx, insertPartnerHub, partnerHubArgs(p)...)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrPartnerPaired
	}
	return err
}

func partnerHubArgs(p PartnerHub) []any {
	eps, _ := json.Marshal(nonNil(p.Endpoints))
	dial := p.Dial
	if dial == "" {
		dial = "auto"
	}
	created := p.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	return []any{p.HubID, p.Name, p.FP, eps, p.Token, p.CredentialID, dial, created.UTC().Truncate(time.Microsecond)}
}

// SetPartnerDial sets whether this hub dials a partner; it reports whether there was one.
func (d *DB) SetPartnerDial(ctx context.Context, hubID, dial string) (bool, error) {
	tag, err := d.pool.Exec(ctx, `UPDATE partner_hubs SET dial = $2, updated_at = now() WHERE hub_id = $1`, hubID, dial)
	return err == nil && tag.RowsAffected() > 0, err
}

// UpdatePartnerSeen keeps what a live partner says about itself (p's name, pin and addresses) and that it was seen
// now, for the pairing p is (its credential): a pairing made again meanwhile is not overwritten with the older one's.
func (d *DB) UpdatePartnerSeen(ctx context.Context, p PartnerHub) error {
	eps, _ := json.Marshal(nonNil(p.Endpoints))
	_, err := d.pool.Exec(ctx, `UPDATE partner_hubs SET name = $2, fp = $3, endpoints = $4, seen_at = now(), updated_at = now()
		WHERE hub_id = $1 AND credential_id = $5`, p.HubID, p.Name, p.FP, eps, p.CredentialID)
	return err
}

// DeletePartnerHub forgets a partner; it reports whether there was one.
func (d *DB) DeletePartnerHub(ctx context.Context, hubID string) (bool, error) {
	tag, err := d.pool.Exec(ctx, `DELETE FROM partner_hubs WHERE hub_id = $1`, hubID)
	return err == nil && tag.RowsAffected() > 0, err
}

func (d *DB) CreatePartnerCode(ctx context.Context, hash string, expiresAt time.Time) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO partner_codes (code_hash, expires_at) VALUES ($1, $2)`, hash, expiresAt)
	return err
}

// PartnerCodeValid: the code exists, was not used and has not expired (a pairing request may go ahead).
func (d *DB) PartnerCodeValid(ctx context.Context, hash string) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM partner_codes WHERE code_hash = $1 AND used_at IS NULL
		AND expires_at > clock_timestamp())`, hash).Scan(&ok)
	return ok, err
}

// RedeemPartnerCode pairs with the hub that brought a code, in one transaction: the code is used up, the partner
// saved, any partner credential issued to that hub before (one left over: unpairing revokes them) revoked and the new
// one created (its hash). False, and nothing changed, when the code is unknown, used or expired; ErrPartnerPaired, and
// nothing changed (the code still good), when that hub is a partner already.
func (d *DB) RedeemPartnerCode(ctx context.Context, codeHash string, p PartnerHub, cred DeviceToken, credHash string) (bool, error) {
	var redeemed bool
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE partner_codes SET used_at = clock_timestamp(), hub_id = $2
			WHERE code_hash = $1 AND used_at IS NULL AND expires_at > clock_timestamp()`, codeHash, p.HubID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if tag, err := tx.Exec(ctx, insertPartnerHub, partnerHubArgs(p)...); err != nil {
			return err
		} else if tag.RowsAffected() == 0 {
			return ErrPartnerPaired
		}
		if _, err := tx.Exec(ctx, `UPDATE device_tokens SET revoked_at = now() WHERE kind = 'partner' AND machine_id = $1 AND revoked_at IS NULL`,
			p.HubID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO device_tokens (id, name, kind, role, token_hash, machine_id) VALUES ($1, $2, $3, $4, $5, $6)`,
			cred.ID, cred.Name, cred.Kind, cred.Role, credHash, cred.MachineID); err != nil {
			return err
		}
		redeemed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return redeemed, nil
}

// PrunePartnerCodes drops the codes that expired before before (used or not).
func (d *DB) PrunePartnerCodes(ctx context.Context, before time.Time) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM partner_codes WHERE expires_at < $1`, before)
	return err
}
