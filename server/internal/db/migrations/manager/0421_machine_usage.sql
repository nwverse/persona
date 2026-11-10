-- Token usage by machine (docs/server/machines.md「Token usage」, #4973): what the nodes meter from the transcripts
-- (node/usage.go), one row a machine, minute, model and launcher (step 60) for the last 27 hours, then one row each
-- 10 minutes (step 600), nothing older than 7 days and an hour (devices/metrics.go). The four kinds of tokens and the
-- replies are sums over the row's time.
CREATE TABLE IF NOT EXISTS machine_usage (
    machine_id  text        NOT NULL,
    ts          timestamptz NOT NULL, -- the start of the row's time
    step        smallint    NOT NULL, -- seconds it covers: 60, or 600 rolled up
    model       text        NOT NULL,
    launcher    text        NOT NULL DEFAULT '', -- '' unknown (typed in a terminal, an older node)
    input       bigint      NOT NULL DEFAULT 0,  -- not from the cache
    cache_write bigint      NOT NULL DEFAULT 0,
    cache_read  bigint      NOT NULL DEFAULT 0,
    output      bigint      NOT NULL DEFAULT 0,
    requests    integer     NOT NULL DEFAULT 0,
    PRIMARY KEY (machine_id, ts, step, model, launcher)
);

-- The rollup finds the minute rows past 27 hours, the charts read one machine's range.
CREATE INDEX IF NOT EXISTS machine_usage_minutes ON machine_usage (ts) WHERE step = 60;
