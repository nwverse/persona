-- When the owner confirmed (Got it) a concern that was closed with concern_report, or a finished task of it
-- (docs/assistant/todos.md "Matters"). Only the owner's press writes it: opening a to-do (seen_at) does not. A concern
-- counts as confirmed only when confirmed_at >= done_at of its latest report. NULL when never confirmed. Not activity:
-- updated_at stays.
ALTER TABLE todos ADD COLUMN IF NOT EXISTS confirmed_at timestamptz;
