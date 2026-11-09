package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// DeleteDeviceIfCurrent acts on an APNs gone response only while its exact
// registration is still persisted. Process-local memory can lag another hub slot.
func (d *DB) DeleteDeviceIfCurrent(ctx context.Context, expected Device) (bool, error) {
	deleted := false
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := lockPushRegistrations(ctx, tx); err != nil {
			return err
		}
		var current Device
		err := tx.QueryRow(ctx, `SELECT apns_token,relay_url,topic,apns_env,push_key,registered_at FROM devices WHERE device_id=$1 FOR UPDATE`, expected.DeviceID).Scan(&current.APNSToken, &current.RelayURL, &current.Topic, &current.APNSEnv, &current.PushKey, &current.RegisteredAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.APNSToken != expected.APNSToken || current.RelayURL != expected.RelayURL || current.Topic != expected.Topic || current.APNSEnv != expected.APNSEnv || current.PushKey != expected.PushKey || (!expected.RegisteredAt.IsZero() && !current.RegisteredAt.Equal(expected.RegisteredAt.UTC().Truncate(time.Microsecond))) {
			return nil
		}
		// Keep the owner in the retirement fence without selecting any push secret.
		current.DeviceID = expected.DeviceID
		if err := tx.QueryRow(ctx, `SELECT credential_id,credential FROM devices WHERE device_id=$1`, expected.DeviceID).Scan(&current.CredentialID, &current.Credential); err != nil {
			return err
		}
		if err := retirePushDevice(ctx, tx, current, "", "invalid_token"); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}
