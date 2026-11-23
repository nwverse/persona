-- Partner hubs (hub/partner_*.go, docs/server/partner-hubs.md): independent hubs this one is paired with so their
-- assistants can message each other, and the one-time codes that pair them. Not the mesh (peers): no group, no
-- records. Each side keeps the credential the other issued to it (sealed with the hub's ~/.persona/secret.key) and the
-- id of the one it issued in return, a device token of kind and role partner whose machine_id is the partner's hub_id.
-- dial is whether this hub dials the partner (auto) or waits for it (never). The hubs keep no message here: the
-- managers own those (partner_messages).
CREATE TABLE IF NOT EXISTS partner_hubs (
    hub_id        text PRIMARY KEY,
    name          text NOT NULL DEFAULT '',
    fp            text NOT NULL DEFAULT '',
    endpoints     jsonb NOT NULL DEFAULT '[]',
    token         text NOT NULL,
    credential_id text NOT NULL,
    dial          text NOT NULL DEFAULT 'auto',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    seen_at       timestamptz
);
CREATE TABLE IF NOT EXISTS partner_codes (
    code_hash  text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    hub_id     text NOT NULL DEFAULT ''
);
