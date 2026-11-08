-- Server-owned registration/liveness timestamps; existing rows get a delivery
-- observation grace so lack of historical delivery telemetry is not inactivity.
ALTER TABLE devices ADD COLUMN registered_at timestamptz;
UPDATE devices SET registered_at = updated_at;
ALTER TABLE devices ALTER COLUMN registered_at SET NOT NULL;
ALTER TABLE devices ALTER COLUMN registered_at SET DEFAULT now();
ALTER TABLE devices ADD COLUMN last_push_at timestamptz;
UPDATE devices SET last_push_at = now();

CREATE TABLE push_device_retirements (
    device_id text PRIMARY KEY,
    canonical_id text NOT NULL,
    credential_id text NOT NULL,
    retired_at timestamptz NOT NULL DEFAULT now()
);

-- Fence old hub slots too: a late save must not recreate a superseded identity.
CREATE FUNCTION reject_retired_push_device() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Serialize old-version INSERT ... ON CONFLICT writers too, before checking
    -- the tombstone. Otherwise a writer waiting behind retirement could revive it.
    PERFORM pg_advisory_xact_lock(x'706572736f6e61'::bigint);
    IF EXISTS (SELECT 1 FROM push_device_retirements WHERE device_id = NEW.device_id) THEN
        RAISE EXCEPTION 'push registration was superseded' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER devices_retirement_fence BEFORE INSERT OR UPDATE ON devices
FOR EACH ROW EXECUTE FUNCTION reject_retired_push_device();
CREATE TRIGGER live_activities_retirement_fence BEFORE INSERT OR UPDATE ON live_activities
FOR EACH ROW EXECUTE FUNCTION reject_retired_push_device();
