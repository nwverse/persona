-- Hub-owned push delivery budget, independent of client activity/token registration.
-- A claim is spent before APNs dispatch, even if delivery fails or the hub exits.
CREATE TABLE IF NOT EXISTS live_activity_start_budget (
    device_id text PRIMARY KEY REFERENCES devices(device_id) ON DELETE CASCADE,
    claimed_at timestamptz NOT NULL,
    next_start_at timestamptz NOT NULL
);
