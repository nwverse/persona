-- When a todo is due, when to remind the user of it and how much it matters (#1683, docs/assistant/todos.md "Due date,
-- reminder, priority"). A due date is a moment (due_at) or a whole day in the user's time zone (due_date), never both.
-- remind_at is when the phone is pushed; reminded_at the moment it was (claimed before the push, so a restart or a
-- second manager does not push it again), cleared whenever remind_at changes. priority is '' (normal), 'high' or 'low'.
ALTER TABLE todos
    ADD COLUMN IF NOT EXISTS due_at      timestamptz,
    ADD COLUMN IF NOT EXISTS due_date    date CONSTRAINT todos_due_one CHECK (due_date IS NULL OR due_at IS NULL),
    ADD COLUMN IF NOT EXISTS remind_at   timestamptz,
    ADD COLUMN IF NOT EXISTS reminded_at timestamptz,
    ADD COLUMN IF NOT EXISTS priority    text NOT NULL DEFAULT '' CONSTRAINT todos_priority_known CHECK (priority IN ('', 'high', 'low'));
