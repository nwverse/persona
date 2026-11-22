package db

import (
	"context"
	"time"
)

// The assistant's tracks (#13104, migrations/manager/20261123010000_assistant_tracks.sql): the registry the manager
// reads to tell a track's kind, directory, template and default categories. The main space ('') is not a row.

// Track is one row of assistant_tracks.
type Track struct {
	Slug        string
	Kind        string
	Title       string
	Icon        string
	Description string
	// DefaultCategories are the information categories everything produced in the track carries at least.
	DefaultCategories []string
	Template          string
	// ConfigOverride is what the track sets apart from its kind's defaults (a JSON object).
	ConfigOverride  []byte
	DirName         string
	MachineID       string
	DefaultLauncher string
	DefaultModel    string
	Builtin         bool
	CreatedAt       time.Time
	ArchivedAt      *time.Time
	Revision        int64
}

// Tracks are every registered track, archived ones included, by slug.
func (d *DB) Tracks(ctx context.Context) ([]Track, error) {
	rows, err := d.pool.Query(ctx, `SELECT slug, kind, title, icon, description, default_categories, template, config_override, dir_name, machine_id,
		default_launcher, default_model, builtin, created_at, archived_at, revision
		FROM assistant_tracks ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Track
	for rows.Next() {
		var s Track
		if err := rows.Scan(&s.Slug, &s.Kind, &s.Title, &s.Icon, &s.Description, &s.DefaultCategories, &s.Template, &s.ConfigOverride, &s.DirName, &s.MachineID,
			&s.DefaultLauncher, &s.DefaultModel, &s.Builtin, &s.CreatedAt, &s.ArchivedAt, &s.Revision); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
