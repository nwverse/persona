-- What "Always allow in this conversation" granted on an assistant conversation's permission cards (#2202, docs/assistant/assistant.md "Always allow in this session"):
-- one row per key the node named (proto.Interaction.AlwaysKeys), by its sha256 (a key may be a whole command). A card
-- whose keys all have a row is allowed by the hub without asking; the agent's own session rule ends with its process.
CREATE TABLE IF NOT EXISTS assistant_always (
    session_id text NOT NULL,
    key_hash   text NOT NULL,
    tool_name  text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, key_hash)
);
