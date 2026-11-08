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
    reason text NOT NULL,
    push_key_hash text NOT NULL,
    target_hash text NOT NULL,
    topic text NOT NULL,
    apns_env text NOT NULL,
    retired_at timestamptz NOT NULL DEFAULT now()
);

-- Fence old hub slots too, including their unconditional ownership upserts.
CREATE FUNCTION reject_retired_push_device() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    retired_reason text;
    retired_owner text;
    new_owner text;
    current_owner text;
    retired_key text;
    retired_target text;
    retired_topic text;
    retired_env text;
    retired_time timestamptz;
BEGIN
    PERFORM pg_advisory_xact_lock(x'706572736f6e61'::bigint);
    SELECT reason, credential_id, push_key_hash, target_hash, topic, apns_env, retired_at
        INTO retired_reason, retired_owner, retired_key, retired_target, retired_topic, retired_env, retired_time
        FROM push_device_retirements WHERE device_id = NEW.device_id;
    IF TG_TABLE_NAME = 'devices' THEN
        new_owner := COALESCE(NULLIF(NEW.credential_id, ''), NEW.credential);
        SELECT COALESCE(NULLIF(credential_id, ''), credential) INTO current_owner
            FROM devices WHERE device_id = NEW.device_id;
        IF TG_OP = 'UPDATE' AND current_setting('persona.push_writer', true) IS DISTINCT FROM NEW.device_id
            AND (NEW.apns_token IS DISTINCT FROM OLD.apns_token OR NEW.relay_url IS DISTINCT FROM OLD.relay_url
                OR NEW.topic IS DISTINCT FROM OLD.topic OR NEW.apns_env IS DISTINCT FROM OLD.apns_env
                OR NEW.push_key IS DISTINCT FROM OLD.push_key) THEN
            RAISE EXCEPTION 'push registration ownership changed' USING ERRCODE = '23514';
        END IF;
        -- INSERT ... ON CONFLICT prefs writes may present another caller's
        -- credential but retain the existing owner in the actual UPDATE row.
        IF (TG_OP = 'UPDATE' AND current_owner <> '' AND current_owner <> new_owner)
            OR ((TG_OP = 'UPDATE' OR current_owner IS NULL)
                AND EXISTS (SELECT 1 FROM device_tokens WHERE id = new_owner AND revoked_at IS NOT NULL)) THEN
            RAISE EXCEPTION 'push registration ownership changed' USING ERRCODE = '23514';
        END IF;
    END IF;
    IF retired_reason IS NOT NULL THEN
        IF TG_TABLE_NAME <> 'devices' THEN
            RAISE EXCEPTION 'push registration was superseded' USING ERRCODE = '23514';
        END IF;
        -- Authenticated enrollment can restore an inactive/invalid same-owner ID.
        -- Re-pairing needs a different live owner and scoped installation proof.
        -- A stale slot or background save never may restore a retirement.
        IF TG_TABLE_NAME = 'devices'
            AND current_setting('persona.push_enrollment', true) = NEW.device_id
            AND (
                (retired_reason = 'superseded' AND retired_owner <> '' AND retired_owner = new_owner
                    AND NEW.registered_at > retired_time
                    AND EXISTS (SELECT 1 FROM devices candidate
                        WHERE COALESCE(NULLIF(candidate.credential_id, ''), candidate.credential) = new_owner
                            AND candidate.topic <> '' AND candidate.topic = NEW.topic AND candidate.apns_env = NEW.apns_env
                            AND ((candidate.push_key <> '' AND candidate.push_key = NEW.push_key)
                                OR (candidate.apns_token <> '' AND candidate.apns_token = NEW.apns_token))))
                OR (retired_reason IN ('inactive', 'invalid_token') AND (retired_owner = '' OR retired_owner = new_owner))
                OR (retired_reason IN ('inactive', 'invalid_token', 'revoked') AND new_owner <> retired_owner
                    AND retired_topic <> '' AND retired_topic = NEW.topic AND retired_env = NEW.apns_env
                    AND ((retired_key <> '' AND retired_key = encode(sha256(convert_to(NEW.push_key, 'UTF8')), 'hex'))
                        OR (retired_target <> '' AND retired_target = encode(sha256(convert_to(NEW.apns_token, 'UTF8')), 'hex'))))
            ) THEN
            DELETE FROM push_device_retirements WHERE device_id = NEW.device_id;
        ELSE
            RAISE EXCEPTION 'push registration was superseded' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER devices_retirement_fence BEFORE INSERT OR UPDATE ON devices
FOR EACH ROW EXECUTE FUNCTION reject_retired_push_device();
CREATE TRIGGER live_activities_retirement_fence BEFORE INSERT OR UPDATE ON live_activities
FOR EACH ROW EXECUTE FUNCTION reject_retired_push_device();

-- An old slot's token-only memory cannot authorize deletion of a newer row.
-- Credential revocation remains safe for old slots because the owner is revoked.
CREATE FUNCTION guard_push_device_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('persona.push_retire', true) IS DISTINCT FROM OLD.device_id
        AND NOT EXISTS (SELECT 1 FROM device_tokens
            WHERE id = COALESCE(NULLIF(OLD.credential_id, ''), OLD.credential) AND revoked_at IS NOT NULL) THEN
        RAISE EXCEPTION 'push retirement requires a guarded writer' USING ERRCODE = '23514';
    END IF;
    RETURN OLD;
END;
$$;
CREATE TRIGGER devices_guarded_delete BEFORE DELETE ON devices
FOR EACH ROW EXECUTE FUNCTION guard_push_device_delete();
