-- A bounded scheduler batch rotates durable jobs rather than starving every
-- capability beyond the first hundred. This timestamp is not public metadata.
ALTER TABLE agent_shares ADD COLUMN last_ingested_at timestamptz;
CREATE INDEX agent_share_ingestion_queue ON agent_shares(last_ingested_at, id) WHERE live;
