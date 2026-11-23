-- Partner hubs (docs/server/partner-hubs.md): the envelopes this hub's assistant sent to and received from paired hubs,
-- each one once by (direction, peer_hub, message_id), with where it stands; and, per paired hub, whether a message to
-- it waits for the owner's OK first (no row: it does). paired_at is the pairing an envelope was written for, or a
-- setting made for (the hub's PartnerHub.PairedAt, from the hub's clock; NULL: not known): a hub paired again under the
-- same hub_id gets nothing of the earlier pairing and does not inherit its setting.
CREATE TABLE IF NOT EXISTS partner_messages (
    id text PRIMARY KEY,
    direction text NOT NULL CHECK (direction IN ('out', 'in')),
    peer_hub text NOT NULL,
    peer_name text NOT NULL DEFAULT '',
    message_id text NOT NULL,
    kind text NOT NULL,
    reply_to text NOT NULL DEFAULT '',
    hop int NOT NULL DEFAULT 1,
    machine_id text NOT NULL DEFAULT '',
    session_id text NOT NULL DEFAULT '',
    title text NOT NULL DEFAULT '',
    text text NOT NULL DEFAULT '',
    state text NOT NULL,
    code text NOT NULL DEFAULT '',
    card_id text NOT NULL DEFAULT '',
    attempts int NOT NULL DEFAULT 0,
    next_try timestamptz,
    paired_at timestamptz,
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (direction, peer_hub, message_id)
);

CREATE INDEX IF NOT EXISTS partner_messages_due ON partner_messages (next_try) WHERE next_try IS NOT NULL;
CREATE INDEX IF NOT EXISTS partner_messages_recent ON partner_messages (peer_hub, created_at DESC);
CREATE INDEX IF NOT EXISTS partner_messages_state ON partner_messages (direction, state);

CREATE TABLE IF NOT EXISTS partner_prefs (
    hub_id text PRIMARY KEY,
    confirm boolean NOT NULL DEFAULT true,
    paired_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
