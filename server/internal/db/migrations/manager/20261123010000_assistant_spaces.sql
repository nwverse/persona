-- The assistant's spaces (#13104, docs/assistant/health.md): named places the assistant's conversations run in, each
-- with a directory of its own. The main space ('') is not a row, and neither is the built-in health space: the manager
-- supplies it (health template, default categories {health}, its own health directory), so a new manager database
-- stays empty for the move of a manager's data into its own database (db.CopyTables refuses a target with data).
-- A space organizes (a directory, conversations, rules); it holds no privacy level. How information may be used is
-- decided per category and use by the manager.
--   default_categories: the information categories everything produced in the space carries at least (health,
--   finance, relationships, emotion, legal_id): the tagging default, not a privacy level. The health space's is
--   {health}.
--   template: the instructions a space's conversations get: 'space' (the generic base) or 'health'.
CREATE TABLE IF NOT EXISTS assistant_spaces (
    slug             text PRIMARY KEY CHECK (slug ~ '^[a-z][a-z0-9-]{1,31}$'),
    title            text NOT NULL DEFAULT '',
    icon             text NOT NULL DEFAULT '',
    description      text NOT NULL DEFAULT '' CHECK (char_length(description) <= 120),
    default_categories text[] NOT NULL DEFAULT '{}' CHECK (default_categories <@ ARRAY['health', 'finance', 'relationships', 'emotion', 'legal_id']::text[]),
    template         text NOT NULL DEFAULT 'space' CHECK (template IN ('space', 'health')),
    dir_name         text NOT NULL DEFAULT '',
    machine_id       text NOT NULL DEFAULT '',
    default_launcher text NOT NULL DEFAULT '',
    default_model    text NOT NULL DEFAULT '',
    builtin          boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    archived_at      timestamptz,
    revision         bigint NOT NULL DEFAULT 1
);
