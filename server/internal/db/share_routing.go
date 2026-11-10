package db

import (
	"context"
	"crypto/rand"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/devicewire"
)

type ShareRoute struct {
	Manager   string
	Handle    []byte
	Authority devicewire.ShareAuthority
}

// ShareRouting admits only a live authoritative roster identity. The caller has
// resolved this machine from the hub's roster, never from manager-reported state.
func (d *DB) ShareRouting(ctx context.Context, issuer, machine, manager string) (ShareRoute, error) {
	var route ShareRoute
	route.Manager = manager
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `SELECT id FROM bus_identities WHERE id=$1 AND role='manager' FOR SHARE`, manager).Scan(&id); err != nil {
			return err
		}
		handle := make([]byte, 16)
		if _, err := rand.Read(handle); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO share_manager_routes(manager_id,routing_handle) VALUES($1,$2) ON CONFLICT(manager_id) WHERE retired_at IS NULL DO NOTHING`, manager, handle); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT routing_handle FROM share_manager_routes WHERE manager_id=$1 AND retired_at IS NULL`, manager).Scan(&route.Handle); err != nil {
			return err
		}
		generation := rand.Text()
		if _, err := tx.Exec(ctx, `INSERT INTO share_machine_authorities(issuer,machine_id,manager_id,generation) VALUES($1,$2,$3,$4)
   ON CONFLICT(issuer,machine_id) DO UPDATE SET manager_id=EXCLUDED.manager_id,generation=EXCLUDED.generation,retired_at=NULL
   WHERE share_machine_authorities.manager_id<>EXCLUDED.manager_id OR share_machine_authorities.retired_at IS NOT NULL`, issuer, machine, manager, generation); err != nil {
			return err
		}
		route.Authority.Issuer, route.Authority.Machine = issuer, machine
		return tx.QueryRow(ctx, `SELECT generation FROM share_machine_authorities WHERE issuer=$1 AND machine_id=$2 AND manager_id=$3 AND retired_at IS NULL`, issuer, machine, manager).Scan(&route.Authority.Generation)
	})
	return route, err
}
func (d *DB) RetireShareMachine(ctx context.Context, issuer, machine string) error {
	_, err := d.pool.Exec(ctx, `UPDATE share_machine_authorities SET retired_at=now() WHERE issuer=$1 AND machine_id=$2 AND retired_at IS NULL`, issuer, machine)
	return err
}
func (d *DB) ShareManagerByHandle(ctx context.Context, handle []byte) (string, error) {
	var id string
	err := d.pool.QueryRow(ctx, `SELECT r.manager_id FROM share_manager_routes r JOIN bus_identities b ON b.id=r.manager_id WHERE r.routing_handle=$1 AND r.retired_at IS NULL AND b.role='manager'`, handle).Scan(&id)
	return id, err
}
func (d *DB) VerifyShareAuthority(ctx context.Context, manager string, authority devicewire.ShareAuthority) error {
	var ok bool
	err := d.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM share_machine_authorities a JOIN bus_identities b ON b.id=a.manager_id
  WHERE a.issuer=$1 AND a.machine_id=$2 AND a.manager_id=$3 AND a.generation=$4 AND a.retired_at IS NULL AND b.role='manager')`, authority.Issuer, authority.Machine, manager, authority.Generation).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("sharing authority unavailable")
	}
	return nil
}
