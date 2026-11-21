-- When a supervised task's agent started a turn after its work was judged or reviewed (state not ''). Until the new
-- work is judged done, task_review refuses to accept the task: the supervision message being answered is about the
-- work before (#10839). NULL otherwise.
ALTER TABLE todo_supervision ADD COLUMN IF NOT EXISTS resumed_at timestamptz;
