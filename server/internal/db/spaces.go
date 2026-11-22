package db

import (
	"context"
	"time"
)

// The assistant's spaces (#13104, migrations/manager/20261123010000_assistant_spaces.sql): the registry the manager
// reads to tell a space's directory and template. The main space ('') is not a row.

// Space is one row of assistant_spaces.
type Space struct {
	Slug            string
	Title           string
	Icon            string
	Description     string
	Template        string
	DirName         string
	MachineID       string
	DefaultLauncher string
	DefaultModel    string
	Builtin         bool
	CreatedAt       time.Time
	ArchivedAt      *time.Time
	Revision        int64
}

// Spaces are every registered space, archived ones included, by slug.
func (d *DB) Spaces(ctx context.Context) ([]Space, error) {
	rows, err := d.pool.Query(ctx, `SELECT slug, title, icon, description, template, dir_name, machine_id,
		default_launcher, default_model, builtin, created_at, archived_at, revision
		FROM assistant_spaces ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Space
	for rows.Next() {
		var s Space
		if err := rows.Scan(&s.Slug, &s.Title, &s.Icon, &s.Description, &s.Template, &s.DirName, &s.MachineID,
			&s.DefaultLauncher, &s.DefaultModel, &s.Builtin, &s.CreatedAt, &s.ArchivedAt, &s.Revision); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
