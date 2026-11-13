-- Plugin markets (docs/extensions/extensions.md "Extension marketplace", #1303): the markets this hub reads plugins from, the token of a private
-- one sealed with the hub's secret.key, and the index last read (so the catalog shows without waiting on the network).
CREATE TABLE IF NOT EXISTS plugin_markets (
	id text PRIMARY KEY,
	spec text NOT NULL UNIQUE,
	name text NOT NULL DEFAULT '',
	token_sealed text NOT NULL DEFAULT '',
	index_json jsonb,
	warnings jsonb NOT NULL DEFAULT '[]',
	error text NOT NULL DEFAULT '',
	fetched_at timestamptz,
	added_at timestamptz NOT NULL DEFAULT now()
);
