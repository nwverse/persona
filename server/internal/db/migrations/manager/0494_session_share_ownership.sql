-- Session business ownership lives in the manager's database, independently of
-- the ordinary 60-day node-state loading window.
ALTER TABLE node_sessions ADD COLUMN creation_id text NOT NULL DEFAULT '';
ALTER TABLE node_sessions ADD COLUMN incarnation text NOT NULL DEFAULT '';
CREATE TABLE session_creation_intents (
 creation_id text PRIMARY KEY,
 machine_id text NOT NULL,
 issuer text NOT NULL,
 owner_subject uuid NOT NULL,
 assignment_generation text NOT NULL,
 incarnation text NOT NULL UNIQUE,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE session_creation_receipts (
 machine_id text NOT NULL,
 session_id text NOT NULL,
 creation_id text NOT NULL,
 incarnation text NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(machine_id,session_id),
 UNIQUE(machine_id,creation_id)
);
CREATE TABLE session_share_ownership (
 machine_id text NOT NULL,
 session_id text NOT NULL,
 issuer text NOT NULL,
 owner_subject uuid NOT NULL,
 incarnation text NOT NULL,
 assignment_generation text NOT NULL,
 owner_revision bigint NOT NULL DEFAULT 1,
 creation_id text,
 audit_id uuid NOT NULL DEFAULT gen_random_uuid(),
 attributed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(machine_id,session_id)
);
CREATE TABLE share_submission_receipts (
 submission_id text PRIMARY KEY,
 machine_id text NOT NULL,
 session_id text NOT NULL,
 incarnation text NOT NULL,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 content_digest text NOT NULL,
 sanitized_text text NOT NULL CHECK(octet_length(sanitized_text)<=32768),
 delivery_state text NOT NULL CHECK(delivery_state IN ('pending','delivered','failed','unknown')),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX share_submission_session_sequence ON share_submission_receipts(machine_id,session_id,incarnation,sequence);

-- A durable state write is the creation acknowledgement. The event's arrival
-- order relative to the node's start reply cannot establish ownership by itself.
CREATE FUNCTION reconcile_session_creation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.creation_id <> '' AND NEW.incarnation <> '' THEN
  INSERT INTO session_creation_receipts(machine_id,session_id,creation_id,incarnation)
  VALUES(NEW.machine_id,NEW.session_id,NEW.creation_id,NEW.incarnation)
  ON CONFLICT(machine_id,session_id) DO NOTHING;
  INSERT INTO session_share_ownership(machine_id,session_id,issuer,owner_subject,incarnation,assignment_generation,creation_id)
  SELECT NEW.machine_id,NEW.session_id,i.issuer,i.owner_subject,i.incarnation,i.assignment_generation,i.creation_id
  FROM session_creation_intents i JOIN session_creation_receipts r
   ON r.machine_id=i.machine_id AND r.creation_id=i.creation_id AND r.incarnation=i.incarnation
  WHERE i.machine_id=NEW.machine_id AND i.creation_id=NEW.creation_id AND i.incarnation=NEW.incarnation AND r.session_id=NEW.session_id
  ON CONFLICT(machine_id,session_id) DO NOTHING;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER node_session_creation AFTER INSERT OR UPDATE ON node_sessions
FOR EACH ROW EXECUTE FUNCTION reconcile_session_creation();
