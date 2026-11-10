-- One center hub per mesh group (hub/mesh_group.go, docs/server/mesh.md "Primary"): every hub keeps which hub it takes for the
-- center and the term that made it so (a larger term wins: changing the center adds one). Keyed by the hub's own
-- hub_id: a database restored on, or shared with, another hub does not make that one a member.
CREATE TABLE IF NOT EXISTS mesh_group (
    hub_id     text PRIMARY KEY,
    center     text NOT NULL,
    term       bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- Computers of kind service (hub/service.go, docs/archive/operations/plan-hub-center.md 4): the hub dials the connector on the computer.
-- What pairing left: the connector's TLS key pin and the credential the hub made up for it, sealed with the hub's
-- ~/.persona/secret.key (never in the clear; the connector keeps only its hash).
CREATE TABLE IF NOT EXISTS connector_credentials (
    machine_id text PRIMARY KEY,
    fp         text NOT NULL,
    token      text NOT NULL,
    version    text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
