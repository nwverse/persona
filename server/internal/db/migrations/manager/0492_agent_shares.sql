-- Manager-owned sealed projections, previews, revision cursors and publications.
-- No raw capabilities or original submitted text are retained.
CREATE TABLE agent_share_objects (
 id text PRIMARY KEY,
 kind text NOT NULL,
 actor text NOT NULL,
 expires_at timestamptz NOT NULL,
 body jsonb NOT NULL CHECK(octet_length(body::text)<=33554432)
);
CREATE INDEX agent_share_objects_expiry ON agent_share_objects(expires_at);
CREATE TABLE agent_shares (
 id text PRIMARY KEY,
 issuer text NOT NULL,
 subject uuid NOT NULL,
 machine text NOT NULL,
 session_id text NOT NULL,
 token_hash text NOT NULL UNIQUE CHECK(length(token_hash)=64),
 expires_at timestamptz NOT NULL,
 live boolean NOT NULL,
 active boolean NOT NULL DEFAULT false,
 header jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(octet_length(header::text)<=32768),
 body jsonb NOT NULL CHECK(octet_length(body::text)<=33554432)
);
CREATE INDEX agent_shares_owner ON agent_shares(issuer,subject);
CREATE INDEX agent_shares_live ON agent_shares(expires_at) WHERE live;
CREATE TABLE agent_share_creations (
 actor text NOT NULL,
 request_id uuid NOT NULL,
 digest text NOT NULL,
 share_id text NOT NULL REFERENCES agent_shares(id),
 PRIMARY KEY(actor,request_id)
);
CREATE TABLE session_attribution_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 machine text NOT NULL,
 session_id text NOT NULL,
 issuer text NOT NULL,
 subject uuid NOT NULL,
 administrator text NOT NULL,
 reason text NOT NULL,
 checkpoint_digest text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
