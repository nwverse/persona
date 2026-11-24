-- Sorting to-dos into knowledge-base projects in one go (#13026, docs/assistant/todos.md "Sorting into projects").
-- temporary marks a one-off to-do that belongs to no project; filing it under a project clears it.
ALTER TABLE todos ADD COLUMN IF NOT EXISTS temporary boolean NOT NULL DEFAULT false;

-- One batch of to-do moves the owner confirmed (the sort sheet, a move from the chat), so it can be undone as one:
-- items is a JSON array of {todo, before, after}, each place {project, temporary}. undone_at is set once, by the undo
-- that claims it; outcomes is what that undo did with each item, in the items' order.
CREATE TABLE IF NOT EXISTS todo_sort_batches (
    id text PRIMARY KEY,
    at timestamptz NOT NULL,
    by_whom text NOT NULL DEFAULT '',
    kind text NOT NULL DEFAULT '',
    summary text NOT NULL DEFAULT '',
    items jsonb NOT NULL DEFAULT '[]',
    undone_at timestamptz,
    outcomes jsonb
);

CREATE INDEX IF NOT EXISTS todo_sort_batches_at ON todo_sort_batches (at DESC);
