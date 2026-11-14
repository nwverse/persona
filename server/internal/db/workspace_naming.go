package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/workspacenaming"
)

type WorkspaceRulesRecord struct {
	TodoID   string                `json:"todo_id"`
	Revision int64                 `json:"revision"`
	Rules    workspacenaming.Rules `json:"workspace_rules"`
}

func readWorkspaceRules(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (WorkspaceRulesRecord, error) {
	out := WorkspaceRulesRecord{TodoID: id}
	var raw []byte
	err := q.QueryRow(ctx, `SELECT COALESCE(r.revision,0),r.rules FROM todos t LEFT JOIN todo_workspace_rules r ON r.todo_id=t.id WHERE t.id=$1`, id).Scan(&out.Revision, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrTodoNotFound
	}
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, &out.Rules)
	}
	return out, err
}
func (d *DB) TodoWorkspaceRules(ctx context.Context, id string) (WorkspaceRulesRecord, error) {
	return readWorkspaceRules(ctx, d.pool, id)
}

// ReadWorkspaceRulesTx shares the Todo lock with T1 and preparation consumers.
// The lock protects snapshot capture, not future edits of business metadata.
func ReadWorkspaceRulesTx(ctx context.Context, tx pgx.Tx, id string) (WorkspaceRulesRecord, error) {
	if _, err := ReadTodoRepositoriesTx(ctx, tx, id, nil); err != nil {
		return WorkspaceRulesRecord{}, err
	}
	return readWorkspaceRules(ctx, tx, id)
}
func (d *DB) SetTodoWorkspaceRules(ctx context.Context, id string, expected int64, patch json.RawMessage) (WorkspaceRulesRecord, error) {
	var out WorkspaceRulesRecord
	if expected < 0 || expected >= repository.MaxRevision {
		return out, repository.Problem("invalid")
	}
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		cur, err := ReadWorkspaceRulesTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if cur.Revision != expected {
			return repository.Problem("revision_conflict")
		}
		rules, err := workspacenaming.Patch(cur.Rules, patch)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(rules)
		if err != nil {
			return err
		}
		cur.Rules = rules
		cur.Revision++
		out = cur
		_, err = tx.Exec(ctx, `INSERT INTO todo_workspace_rules(todo_id,revision,rules) VALUES($1,$2,$3) ON CONFLICT(todo_id) DO UPDATE SET revision=$2,rules=$3`, id, cur.Revision, raw)
		return err
	})
	return out, err
}
func (d *DB) WorkspaceNamingPlan(ctx context.Context, id, operation string) (*workspacenaming.Plan, error) {
	var raw []byte
	err := d.pool.QueryRow(ctx, `SELECT plan FROM workspace_naming_plans WHERE todo_id=$1 AND operation_id=$2`, id, operation).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plan workspacenaming.Plan
	err = json.Unmarshal(raw, &plan)
	if err == nil {
		err = workspacenaming.CheckPlan(plan)
	}
	return &plan, err
}

// ReadWorkspaceNamingPlanTx lets preparation adopt this immutable snapshot in its
// own operation transaction. It never binds or permanently freezes the Todo.
func ReadWorkspaceNamingPlanTx(ctx context.Context, tx pgx.Tx, id, operation string) (workspacenaming.Plan, error) {
	var plan workspacenaming.Plan
	if _, err := ReadTodoRepositoriesTx(ctx, tx, id, nil); err != nil {
		return plan, err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT plan FROM workspace_naming_plans WHERE todo_id=$1 AND operation_id=$2`, id, operation).Scan(&raw); err != nil {
		return plan, err
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		return plan, err
	}
	return plan, workspacenaming.CheckPlan(plan)
}

func (d *DB) SaveWorkspaceNamingPlan(ctx context.Context, id, operation string, revision int64, plan workspacenaming.Plan) (*workspacenaming.Plan, error) {
	if !workspacenaming.ValidOperationID(operation) || plan.Source.TodoID != id {
		return nil, repository.Problem("invalid")
	}
	if err := workspacenaming.CheckPlan(plan); err != nil {
		return nil, err
	}
	var out *workspacenaming.Plan
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		cur, err := ReadWorkspaceRulesTx(ctx, tx, id)
		if err != nil {
			return err
		}
		var prior []byte
		err = tx.QueryRow(ctx, `SELECT plan FROM workspace_naming_plans WHERE operation_id=$1`, operation).Scan(&prior)
		if err == nil {
			var p workspacenaming.Plan
			if err = json.Unmarshal(prior, &p); err != nil {
				return err
			}
			if p.Source.TodoID != id || p.Source.MachineID != plan.Source.MachineID || p.Source.Layout != plan.Source.Layout || p.Source.Rules != plan.Source.Rules || p.Source.Repositories.Digest != plan.Source.Repositories.Digest {
				return repository.Problem("revision_conflict")
			}
			out = &p
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if cur.Revision != revision {
			return repository.Problem("revision_conflict")
		}
		set, err := ReadTodoRepositoriesTx(ctx, tx, id, nil)
		if err != nil {
			return err
		}
		ids := []string{}
		for _, repo := range plan.Source.Repositories.Repositories {
			ids = append(ids, repo.RepoID)
		}
		snapshot, err := repository.Snapshot(plan.Source.Manager, id, set.Revision, set.Repositories, ids)
		if err != nil {
			return err
		}
		if snapshot.Digest != plan.Source.Repositories.Digest {
			return repository.Problem("revision_conflict")
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO workspace_naming_plans(operation_id,todo_id,plan) VALUES($1,$2,$3)`, operation, id, raw)
		out = &plan
		return err
	})
	return out, err
}
