package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/assistant/repository"
)

func decodeTodoRepositories(id string, revision int64, raw []byte) (repository.TaskSet, error) {
	out := repository.TaskSet{SchemaVersion: 1, TodoID: id, Revision: revision, Repositories: []repository.TaskRepository{}}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.Repositories); err != nil {
			return out, err
		}
		if out.Repositories == nil {
			return out, repository.Problem("store_unavailable")
		}
	}
	return out, nil
}

func (d *DB) TodoRepositories(ctx context.Context, id string) (repository.TaskSet, error) {
	var revision int64
	var raw []byte
	err := d.pool.QueryRow(ctx, `SELECT COALESCE(r.revision,0), r.repositories FROM todos t LEFT JOIN todo_repository_sets r ON r.todo_id=t.id WHERE t.id=$1`, id).Scan(&revision, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.TaskSet{}, ErrTodoNotFound
	}
	if err != nil {
		return repository.TaskSet{}, err
	}
	return decodeTodoRepositories(id, revision, raw)
}

// ReadTodoRepositoriesTx locks the Todo, including an empty association set. Preparation
// consumers can read this value and persist their own selected snapshot in the same
// transaction. It never seals business associations or changes legacy execution paths.
func ReadTodoRepositoriesTx(ctx context.Context, tx pgx.Tx, id string, expected *int64) (repository.TaskSet, error) {
	var todo string
	if err := tx.QueryRow(ctx, `SELECT id FROM todos WHERE id=$1 FOR UPDATE`, id).Scan(&todo); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.TaskSet{}, ErrTodoNotFound
		}
		return repository.TaskSet{}, err
	}
	var revision int64
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT revision,repositories FROM todo_repository_sets WHERE todo_id=$1`, id).Scan(&revision, &raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return repository.TaskSet{}, err
	}
	if expected != nil && revision != *expected {
		return repository.TaskSet{}, repository.Problem("revision_conflict")
	}
	return decodeTodoRepositories(id, revision, raw)
}

// ReadRepositoryCatalogTx holds a shared lock through association validation and commit.
// Existing catalog CAS updates take the conflicting row lock, so identity cannot change
// between validation and save. No network call occurs while holding these locks.
func ReadRepositoryCatalogTx(ctx context.Context, tx pgx.Tx) (repository.Catalog, error) {
	var raw []byte
	var catalog repository.Catalog
	err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1 FOR SHARE`, repository.CatalogKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog, nil
	}
	if err != nil {
		return catalog, err
	}
	err = json.Unmarshal(raw, &catalog)
	return catalog, err
}

func writeTodoRepositoriesTx(ctx context.Context, tx pgx.Tx, set repository.TaskSet) error {
	raw, err := json.Marshal(set.Repositories)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO todo_repository_sets(todo_id,revision,repositories) VALUES($1,$2,$3) ON CONFLICT(todo_id) DO UPDATE SET revision=$2,repositories=$3`, set.TodoID, set.Revision, raw)
	return err
}

func (d *DB) CreateTodoWithRepositories(ctx context.Context, t TodoRow, in repository.CreateBindings) error {
	if !repository.ValidRevision(in.CatalogRevision) || in.Repositories == nil {
		return repository.Problem("invalid")
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := createTodo(ctx, tx, t); err != nil {
			return err
		}
		catalog, err := ReadRepositoryCatalogTx(ctx, tx)
		if err != nil {
			return err
		}
		if catalog.Revision != *in.CatalogRevision {
			return repository.Problem("catalog_conflict")
		}
		rows, err := repository.Bind(nil, in.Repositories, catalog)
		if err != nil {
			return err
		}
		return writeTodoRepositoriesTx(ctx, tx, repository.TaskSet{TodoID: t.ID, Revision: 1, Repositories: rows})
	})
}

func (d *DB) SetTodoRepositories(ctx context.Context, id string, in repository.SetInput) (repository.TaskSet, error) {
	var out repository.TaskSet
	if !repository.ValidRevision(in.ExpectedRevision) || !repository.ValidRevision(in.ExpectedCatalogRevision) || in.Repositories == nil {
		return out, repository.Problem("invalid")
	}
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		cur, err := ReadTodoRepositoriesTx(ctx, tx, id, in.ExpectedRevision)
		if err != nil {
			return err
		}
		catalog, err := ReadRepositoryCatalogTx(ctx, tx)
		if err != nil {
			return err
		}
		if catalog.Revision != *in.ExpectedCatalogRevision {
			return repository.Problem("catalog_conflict")
		}
		rows, err := repository.Bind(cur.Repositories, in.Repositories, catalog)
		if err != nil {
			return err
		}
		cur.Repositories = rows
		cur.Revision++
		out = cur
		return writeTodoRepositoriesTx(ctx, tx, cur)
	})
	return out, err
}

func (d *DB) SetTodoRepositoryLinks(ctx context.Context, id, repoID string, in repository.LinksInput) (repository.TaskSet, error) {
	var out repository.TaskSet
	if !repository.ValidRevision(in.ExpectedRevision) || in.Links == nil {
		return out, repository.Problem("invalid")
	}
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		cur, err := ReadTodoRepositoriesTx(ctx, tx, id, in.ExpectedRevision)
		if err != nil {
			return err
		}
		for index, row := range cur.Repositories {
			if row.RepoID == repoID {
				links, err := repository.NormalizeLinks(row.SourceSnapshot.Identity, in.Links)
				if err != nil {
					return err
				}
				cur.Repositories[index].Links = links
				cur.Revision++
				out = cur
				return writeTodoRepositoriesTx(ctx, tx, cur)
			}
		}
		return repository.Problem("not_found")
	})
	return out, err
}
