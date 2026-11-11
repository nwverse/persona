-- How the extensions ran (docs/extensions/extensions.md "Runtime status", #1857): one row per extension and 5-minute bucket, the
-- counts as JSON (calls, errors and latency by method, runs, exit reasons, counters). The hub writes the closed buckets
-- once an hour and drops what is older than 7 days.
CREATE TABLE IF NOT EXISTS extension_metrics (
	extension    text        NOT NULL,
	bucket_start timestamptz NOT NULL,
	data         jsonb       NOT NULL,
	PRIMARY KEY (extension, bucket_start)
);
CREATE INDEX IF NOT EXISTS extension_metrics_bucket_start ON extension_metrics (bucket_start);
