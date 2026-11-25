package externalsourcestore

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

// PutExternalSourcePolicy replaces a node's overrides. An empty policy restores inheritance.
func (s *Store) PutExternalSourcePolicy(ctx context.Context, p assistantwire.ExternalScopedPolicy) error {
	return s.database.Transaction(ctx, func(tx pgx.Tx) error {
		if p.Policy == (assistantwire.ExternalPolicy{}) {
			_, err := tx.Exec(ctx, `DELETE FROM assistant_external_source_policies WHERE external_ref = $1`, p.ExternalRef)
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO assistant_external_source_policies (external_ref, policy) VALUES ($1, $2)
			ON CONFLICT (external_ref) DO UPDATE SET policy = EXCLUDED.policy`, p.ExternalRef, p.Policy)
		return err
	})
}

// ExternalSourcePolicies reads the sparse overrides for the requested path and, optionally, a selected binding.
func (s *Store) ExternalSourcePolicies(ctx context.Context, path []assistantwire.ExternalRef, id assistantwire.ExternalBindingID) ([]assistantwire.ExternalScopedPolicy, *assistantwire.ExternalBinding, error) {
	list := []assistantwire.ExternalScopedPolicy{}
	var binding *assistantwire.ExternalBinding
	err := db.ReadSnapshot(ctx, s.database, func(tx pgx.Tx) error {
		if id != "" {
			row := tx.QueryRow(ctx, `SELECT id, external_ref, target_kind, target_id, policy FROM assistant_external_bindings WHERE id = $1`, id)
			b, err := scanBinding(row)
			if err != nil {
				return bindingError(err)
			}
			binding = &b
		}
		rows, err := tx.Query(ctx, `
			SELECT external_ref, policy FROM assistant_external_source_policies
			WHERE external_ref IN (SELECT value FROM jsonb_array_elements($1::jsonb))`, path)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p assistantwire.ExternalScopedPolicy
			if err := rows.Scan(&p.ExternalRef, &p.Policy); err != nil {
				return err
			}
			list = append(list, p)
		}
		return rows.Err()
	})
	return list, binding, err
}
