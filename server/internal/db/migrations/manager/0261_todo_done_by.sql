-- A task opened from an assistant conversation is marked done by the hub when its judge model finds the work
-- finished (docs/assistant/todos.md "Automatic completion", #1092). done_by says who did it ('' = the user, 'auto' = the hub) and done_reason
-- why; reopened_at is when the user last reopened it, so the same stretch of work does not close it again.
ALTER TABLE todos
    ADD COLUMN IF NOT EXISTS done_by text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS done_reason text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reopened_at timestamptz;
