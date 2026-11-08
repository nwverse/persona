package hub

import (
	"context"
	"time"
)

const (
	pushDeviceMaxIdle     = 90 * 24 * time.Hour
	pushDeviceMaintenance = 5 * time.Minute
)

// maintenanceLoop is tied to hub shutdown. Acceptance timestamps are coalesced;
// registration ownership and inactivity remain in Postgres across hub versions.
func (p *Pusher) maintenanceLoop(ctx context.Context) {
	if p.store == nil {
		return
	}
	tick := time.NewTicker(pushDeviceMaintenance)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := p.reconcileDevices(ctx); err != nil {
				p.logf("push registration maintenance: %v", err)
			}
		}
	}
}

func (p *Pusher) reconcileDevices(parent context.Context) error {
	if p.store == nil {
		return nil
	}
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	p.mu.Lock()
	dirty := map[string]time.Time{}
	for id := range p.acceptedDirty {
		dirty[id] = p.devices[id].LastPushAt
	}
	p.mu.Unlock()
	for id, at := range dirty {
		if err := p.saveLocked(id); err != nil {
			// A superseded row is not recreated. Reconciliation below removes it
			// from this process as well; other errors leave the dirty bit for retry.
			p.logf("push acceptance flush registration=%s: %v", trafficIdentity(id), err)
			continue
		}
		p.mu.Lock()
		if p.devices[id].LastPushAt.Equal(at) {
			delete(p.acceptedDirty, id)
		}
		p.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(parent, deviceStoreTimeout)
	defer cancel()
	retired, err := p.store.ReconcilePushDevices(ctx, p.now().UTC(), pushDeviceMaxIdle)
	if err != nil {
		return err
	}
	rows, err := p.store.Devices(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current := map[string]Device{}
	for _, d := range rows {
		// A push can be accepted during the read. Keep its newer local timestamp
		// and dirty bit so the next pass persists it instead of losing liveness.
		if old := p.devices[d.DeviceID]; old.LastPushAt.After(d.LastPushAt) {
			d.LastPushAt = old.LastPushAt
		}
		current[d.DeviceID] = d
	}
	for id := range p.devices {
		if _, ok := current[id]; !ok {
			delete(p.failed, id)
			delete(p.replaced, id)
			delete(p.acceptedDirty, id)
		}
	}
	p.devices = current
	for _, r := range retired {
		p.logf("push registration retired registration=%s canonical=%s reason=%s", trafficIdentity(r.DeviceID), trafficIdentity(r.CanonicalID), r.Reason)
	}
	return nil
}
