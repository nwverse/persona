-- Market signatures (docs/extensions/extensions.md "Signing", #1848): the key the hub trusts for a market (the first it saw, or the
-- one its owner accepted; '' while the market has never been signed), and the state of the index last read.
ALTER TABLE plugin_markets ADD COLUMN IF NOT EXISTS signing_key text NOT NULL DEFAULT '';
ALTER TABLE plugin_markets ADD COLUMN IF NOT EXISTS signature jsonb;
