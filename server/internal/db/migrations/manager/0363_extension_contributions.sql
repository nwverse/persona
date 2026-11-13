-- The contributions of an extension the user switched off (docs/extensions/extensions.md "Turning contributions on and off", #1854): "<point>/<id>", the
-- rest of the extension runs. A connector's switch stays the connector's own (settings, connectorSettingsKey).
ALTER TABLE plugins ADD COLUMN IF NOT EXISTS disabled_contributions jsonb NOT NULL DEFAULT '[]';
