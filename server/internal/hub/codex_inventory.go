package hub

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/nwverse/persona/server/internal/proto"
)

func codexInventoryKey(machine string) string { return "codex_inventory/" + machine }

func (m *Machine) codexConfig(ctx context.Context) proto.CodexConfig {
	var cfg proto.CodexConfig
	if m.hub.store != nil {
		_, _ = m.hub.store.Setting(ctx, codexInventoryKey(m.cfg.ID), &cfg)
	}
	if m.cfg.CodexDiscovery != nil {
		cfg.Discovery = m.cfg.CodexDiscovery
	}
	for _, h := range m.cfg.CodexHomes {
		if !slices.Contains(cfg.Homes, h) {
			cfg.Homes = append(cfg.Homes, h)
		}
	}
	for _, s := range m.cfg.CodexServers {
		i := slices.IndexFunc(cfg.Servers, func(v proto.CodexServer) bool { return v.ID == s.ID })
		if i >= 0 {
			cfg.Servers[i] = s
		} else {
			cfg.Servers = append(cfg.Servers, s)
		}
	}
	return cfg
}

// codexInventoryHanded: the node's current connection, as its manager reports it, is the one the hub handed the kept
// inventory to (devices.connected, machineConnected's codex). A node's registry starts empty on every connection and
// gets the kept inventory only then; a report from before (a node before this check waited for it, node
// codex_registry.go rememberCodex) would store that empty registry over what the hub kept: the threads the user joined,
// the other homes and the app-servers. A machine the hub connects itself (no manager reports it) is taken as handed.
func (m *Machine) codexInventoryHanded() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.managed || m.connected && !m.hookedAt.IsZero() && m.hookedAt.Equal(m.connectedAt)
}

func (m *Machine) rememberCodexInventory(raw json.RawMessage) {
	if m.hub.store == nil {
		return
	}
	var cfg proto.CodexConfig
	if json.Unmarshal(raw, &cfg) != nil {
		return
	}
	if !m.hub.quiesce.beginOrDefer(func() {
		m.hub.codexPendingMu.Lock()
		if m.hub.codexPending == nil {
			m.hub.codexPending = map[string]proto.CodexConfig{}
		}
		m.hub.codexPending[m.cfg.ID] = cfg
		m.hub.codexPendingMu.Unlock()
	}) {
		return
	}
	defer m.hub.quiesce.end()
	ctx, cancel := context.WithTimeout(context.Background(), 5e9)
	defer cancel()
	// Updates come from this authenticated node, not public endpoint URLs. The node never reports credentials.
	if err := m.hub.store.PutSetting(ctx, codexInventoryKey(m.cfg.ID), cfg); err != nil {
		m.hub.logf("[%s] remember Codex inventory: %v", m.cfg.ID, err)
	}
}

func (h *Hub) resumeCodexInventory() {
	if h.store == nil {
		return
	}
	h.codexPendingMu.Lock()
	pending := h.codexPending
	h.codexPending = nil
	h.codexPendingMu.Unlock()
	if len(pending) == 0 {
		return
	}
	h.nodeWorkers.Add(1)
	go func() {
		defer h.nodeWorkers.Done()
		for mid, cfg := range pending {
			if !h.quiesce.beginOrDefer(func() {
				h.codexPendingMu.Lock()
				if h.codexPending == nil {
					h.codexPending = map[string]proto.CodexConfig{}
				}
				if _, exists := h.codexPending[mid]; !exists {
					h.codexPending[mid] = cfg
				}
				h.codexPendingMu.Unlock()
			}) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5e9)
			if err := h.store.PutSetting(ctx, codexInventoryKey(mid), cfg); err != nil {
				h.logf("[%s] remember Codex inventory: %v", mid, err)
			}
			cancel()
			h.quiesce.end()
		}
	}()
}
