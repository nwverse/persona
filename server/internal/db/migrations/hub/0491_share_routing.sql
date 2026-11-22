-- Routing handles are roster identities, not bus credentials. Removed handles
-- remain retired permanently so recreating an ID cannot revive old capabilities.
CREATE TABLE share_manager_routes (
 manager_id text NOT NULL,
 routing_handle bytea PRIMARY KEY CHECK(octet_length(routing_handle)=16),
 retired_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX share_manager_active_route ON share_manager_routes(manager_id) WHERE retired_at IS NULL;
CREATE TABLE share_machine_authorities (
 issuer text NOT NULL,
 machine_id text NOT NULL,
 manager_id text NOT NULL,
 generation text NOT NULL,
 retired_at timestamptz,
 PRIMARY KEY(issuer,machine_id)
);
CREATE FUNCTION retire_share_manager_route() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE share_manager_routes SET retired_at=now() WHERE manager_id=OLD.id AND retired_at IS NULL;
 UPDATE share_machine_authorities SET retired_at=now() WHERE manager_id=OLD.id AND retired_at IS NULL;
 RETURN OLD;
END $$;
CREATE TRIGGER share_manager_removed AFTER DELETE ON bus_identities FOR EACH ROW EXECUTE FUNCTION retire_share_manager_route();
