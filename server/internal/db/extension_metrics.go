package db

import (
	"context"
	"encoding/json"
	"time"
)

// ---- extension_metrics (docs/extensions/extensions.md "Runtime status") ----

// ExtensionMetricsRow is one extension's counts over one 5-minute bucket.
type ExtensionMetricsRow struct {
	Extension string
	Start     time.Time
	Data      json.RawMessage
}

// PutExtensionMetrics writes a bucket's counts, replacing what was there.
func (d *DB) PutExtensionMetrics(ctx context.Context, extension string, start time.Time, data json.RawMessage) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO extension_metrics (extension, bucket_start, data) VALUES ($1, $2, $3)
		ON CONFLICT (extension, bucket_start) DO UPDATE SET data = EXCLUDED.data`, extension, start, []byte(data))
	return err
}

// ExtensionMetrics are an extension's buckets from since on, oldest first.
func (d *DB) ExtensionMetrics(ctx context.Context, extension string, since time.Time) ([]ExtensionMetricsRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT extension, bucket_start, data FROM extension_metrics
		WHERE extension = $1 AND bucket_start >= $2 ORDER BY bucket_start`, extension, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExtensionMetricsRow
	for rows.Next() {
		var r ExtensionMetricsRow
		var data []byte
		if err := rows.Scan(&r.Extension, &r.Start, &data); err != nil {
			return nil, err
		}
		r.Data = data
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteExtensionMetricsBefore drops the buckets older than before.
func (d *DB) DeleteExtensionMetricsBefore(ctx context.Context, before time.Time) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM extension_metrics WHERE bucket_start < $1`, before)
	return err
}
