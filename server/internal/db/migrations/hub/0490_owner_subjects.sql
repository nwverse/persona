-- Trusted authentication subjects are scoped to their enrolling hub authority.
CREATE TABLE owner_subjects (
    issuer text NOT NULL,
    subject uuid NOT NULL DEFAULT gen_random_uuid(),
    label text NOT NULL CHECK (length(label) BETWEEN 1 AND 120),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, subject)
);
CREATE TABLE owner_bindings (
    credential_id text PRIMARY KEY,
    issuer text NOT NULL,
    subject uuid NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    retired_at timestamptz,
    FOREIGN KEY (issuer, subject) REFERENCES owner_subjects(issuer, subject)
);
CREATE TABLE owner_enrollment_audit (
    audit_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer text NOT NULL,
    subject uuid NOT NULL,
    credential_id text,
    revision bigint NOT NULL,
    action text NOT NULL CHECK (action IN ('subject_created', 'enrolled', 'enrollment_confirmed', 'credential_revoked')),
    administrator text NOT NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- Every existing SQL revocation path, including credential replacement, retires the binding.
-- The device row is also the cross-process sharing admission fence.
CREATE FUNCTION retire_owner_binding() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL THEN
        WITH retired AS (
            UPDATE owner_bindings SET retired_at = NEW.revoked_at, revision = revision + 1
            WHERE credential_id = NEW.id AND retired_at IS NULL
            RETURNING issuer, subject, credential_id, revision
        ) INSERT INTO owner_enrollment_audit (issuer, subject, credential_id, revision, action, administrator, reason)
          SELECT issuer, subject, credential_id, revision, 'credential_revoked', 'system', 'Credential revoked'
          FROM retired;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER device_owner_revoked AFTER UPDATE OF revoked_at ON device_tokens
FOR EACH ROW EXECUTE FUNCTION retire_owner_binding();
