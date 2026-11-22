-- The assistant's spaces (#13104, docs/assistant/health.md): named places the assistant's conversations run in, each
-- with a directory of its own. The main space ('') is not a row. The health space is the built-in row; its directory
-- name is the manager's (identity.HealthDirName), which the manager supplies, so dir_name here is informational for it.
-- How a space's information may be used (the lock screen, handing it out, extensions) is decided by the manager per
-- use, not stored here.
--   template: the instructions a space's conversations get: 'space' (the generic base) or 'health'.
CREATE TABLE IF NOT EXISTS assistant_spaces (
    slug             text PRIMARY KEY CHECK (slug ~ '^[a-z][a-z0-9-]{1,31}$'),
    title            text NOT NULL DEFAULT '',
    icon             text NOT NULL DEFAULT '',
    description      text NOT NULL DEFAULT '' CHECK (char_length(description) <= 120),
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

INSERT INTO assistant_spaces (slug, template, dir_name, builtin)
VALUES ('health', 'health', 'health', true)
ON CONFLICT (slug) DO NOTHING;
