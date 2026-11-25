package hub

import (
	"context"
	"sync"
	"time"
)

// pushGradingEffectiveCache keeps one consistent base/layer snapshot and
// resolves it independently for each manager. A failed reload retains the last
// good snapshot until another TTL interval passes.
type pushGradingEffectiveCache struct {
	mu       sync.Mutex
	now      func() time.Time
	ttl      time.Duration
	snapshot *pushGradingSnapshot
	expires  time.Time
	loading  chan struct{}
	loadErr  error
}

func newPushGradingEffectiveCache(ttl time.Duration) *pushGradingEffectiveCache {
	if ttl <= 0 {
		ttl = gradingConfigTTL
	}
	return &pushGradingEffectiveCache{now: time.Now, ttl: ttl}
}

// effectiveForManager resolves a manager-specific effective configuration. The
// cached extension layer is applied only when its owner matches the manager.
func (s pushGradingSnapshot) effectiveForManager(manager string) pushGradingEffective {
	if s.Layer != nil && normalizeGradingManager(s.Layer.Manager) == normalizeGradingManager(manager) {
		return resolvePushGrading(s.Base, &s.Layer.Config, s.Layer.Source)
	}
	return resolvePushGrading(s.Base, nil, "")
}

func (c *pushGradingEffectiveCache) effective(ctx context.Context, store Store, manager string) (pushGradingEffective, error) {
	for {
		c.mu.Lock()
		if c.snapshot != nil && c.now().Before(c.expires) {
			snapshot := *c.snapshot
			c.mu.Unlock()
			return snapshot.effectiveForManager(manager), nil
		}
		if c.loading != nil {
			done := c.loading
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return pushGradingEffective{}, ctx.Err()
			case <-done:
			}
			c.mu.Lock()
			snapshot, err := c.snapshot, c.loadErr
			c.mu.Unlock()
			if snapshot != nil {
				return snapshot.effectiveForManager(manager), nil
			}
			return pushGradingEffective{}, err
		}
		done := make(chan struct{})
		c.loading = done
		c.mu.Unlock()

		snapshot, err := readPushGradingSnapshot(ctx, store)
		c.mu.Lock()
		if err == nil {
			c.snapshot = &snapshot
		}
		c.loadErr = err
		if c.snapshot != nil {
			c.expires = c.now().Add(c.ttl)
		} else {
			c.expires = time.Time{}
		}
		c.loading = nil
		close(done)
		if c.snapshot != nil {
			result := c.snapshot.effectiveForManager(manager)
			c.mu.Unlock()
			return result, nil
		}
		c.mu.Unlock()
		return pushGradingEffective{}, err
	}
}

func (c *pushGradingEffectiveCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot = nil
	c.expires = time.Time{}
}
