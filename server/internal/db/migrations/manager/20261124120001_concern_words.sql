-- The owner's words a concern (a matter) holds (docs/assistant/todos.md "Matters"): every message the owner writes in
-- the main conversation is filed under one concern (the main conversation files it with concern_file, or the owner
-- moves it in the app). One row per message: moving it to another concern rewrites todo_id. Deleting the concern
-- deletes its rows.
CREATE TABLE IF NOT EXISTS concern_words (
    conversation text NOT NULL,
    message_id   text NOT NULL,
    todo_id      text NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
    text         text NOT NULL,
    said_at      timestamptz NOT NULL,
    filed_at     timestamptz NOT NULL,
    filed_by     text NOT NULL DEFAULT 'assistant',
    PRIMARY KEY (conversation, message_id)
);
CREATE INDEX IF NOT EXISTS concern_words_todo ON concern_words (todo_id);

-- What a concern is about and where it stands, as the main conversation last said it (concern_file): about (what it
-- is, why, what counts as done), now_line (what is being done or waited for), and ask with options (the decision the
-- owner must make; '' / '{}' none).
CREATE TABLE IF NOT EXISTS concern_state (
    todo_id    text PRIMARY KEY REFERENCES todos(id) ON DELETE CASCADE,
    about      text NOT NULL DEFAULT '',
    now_line   text NOT NULL DEFAULT '',
    ask        text NOT NULL DEFAULT '',
    options    text[] NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL
);
