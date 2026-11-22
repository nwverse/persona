-- Task briefs (#3822, docs/assistant/unified-entry.md "Briefs"): one record per piece of work, made when a task is
-- handed out. brief is what was asked, frozen: the owner's message that led to it (or who started it and why), the goal,
-- done_when, expect_minutes and what is out of bounds; nothing rewrites it. state is what happens to it afterwards:
-- the log of scope changes, the overdue mark, the per-criterion result. Kept apart from todos: several to-dos (a
-- take-over, a rework round, a retry on another computer) share one brief.
CREATE TABLE IF NOT EXISTS task_briefs (
    id         text PRIMARY KEY,
    brief      jsonb NOT NULL,
    state      jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Which brief a to-do works for, and the to-do it continues (with why) when it took the work over from another.
-- A to-do has one brief; a brief's rows in linked_at order are its lineage.
CREATE TABLE IF NOT EXISTS task_brief_todos (
    todo_id   text PRIMARY KEY REFERENCES todos (id) ON DELETE CASCADE,
    brief_id  text NOT NULL REFERENCES task_briefs (id) ON DELETE CASCADE,
    continues text NOT NULL DEFAULT '',
    reason    text NOT NULL DEFAULT '',
    linked_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS task_brief_todos_brief ON task_brief_todos (brief_id, linked_at);
