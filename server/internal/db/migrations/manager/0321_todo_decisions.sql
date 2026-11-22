-- The calls the user made on a todo (#1618, docs/assistant/todos.md "Decision records"): what was asked, what was chosen and why, where
-- it came from. A decision is written by hand in the todo, or by the hub when the user answers a question card of an
-- agent working on it (source 'card': the machine, session and interaction it came from). A later decision may
-- supersede an earlier one of the same todo: the old one is kept, and only those no other decision supersedes are in
-- effect.
CREATE TABLE IF NOT EXISTS todo_decisions (
    id             text PRIMARY KEY,
    todo_id        text NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
    question       text NOT NULL,
    choice         text NOT NULL,
    reason         text NOT NULL DEFAULT '',
    source         text NOT NULL DEFAULT 'manual',
    source_machine text NOT NULL DEFAULT '',
    source_session text NOT NULL DEFAULT '',
    source_message text NOT NULL DEFAULT '',
    supersedes_id  text REFERENCES todo_decisions(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS todo_decisions_todo ON todo_decisions (todo_id, created_at);
CREATE INDEX IF NOT EXISTS todo_decisions_supersedes ON todo_decisions (supersedes_id) WHERE supersedes_id IS NOT NULL;
-- A card's answer is written once per question, however often the resolution is reported.
CREATE UNIQUE INDEX IF NOT EXISTS todo_decisions_card ON todo_decisions (todo_id, source_message, question) WHERE source_message <> '';
