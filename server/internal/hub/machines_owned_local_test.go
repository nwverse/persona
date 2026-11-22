package hub

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
)

// devicesConfig is what the hub hands the manager now.
func (h *Hub) devicesConfig() devicewire.Config { return h.devicesConfigFor(devicesOwner) }

type localOwnersStore struct {
	*ownersStore
	failWrite   bool
	pending     string
	beforeWrite func(map[string]string)
}

// Pending installs have a code but no bus identity until the code is redeemed.
func (s *localOwnersStore) BusIdentityByID(ctx context.Context, id string) (*db.BusIdentity, error) {
	if id == s.pending {
		return nil, nil
	}
	return s.ownersStore.BusIdentityByID(ctx, id)
}

func (s *localOwnersStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == machineOwnersKey {
		if s.failWrite {
			return errors.New("owner write unavailable")
		}
		if s.beforeWrite != nil {
			s.beforeWrite(value.(map[string]string))
		}
	}
	return s.ownersStore.PutSetting(ctx, key, value)
}
func localMachineHub(t *testing.T) (*Hub, *localOwnersStore) {
	t.Helper()
	h := newTestHub(t)
	h.cfg.Machines = []MachineConfig{{ID: "primary-computer", Kind: "local"}, {ID: "other", Kind: "link"}}
	h.devices.entries, h.devices.started = append([]MachineConfig(nil), h.cfg.Machines...), true
	st := &localOwnersStore{ownersStore: &ownersStore{kept: map[string]json.RawMessage{machineOwnersKey: json.RawMessage(`{"other":"m-abcdef"}`)}}}
	h.bus.idents = st
	raw, err := json.Marshal(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.cfg.dir, "hub.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return h, st
}
func TestOwnedLocalMachineFirstPublicationAndRestart(t *testing.T) {
	h, st := localMachineHub(t)
	ctx := context.Background()
	st.beforeWrite = func(next map[string]string) {
		if next["other"] != "m-abcdef" || next["work-local"] != "m-0b0b0b" {
			t.Fatalf("lost owner: %v", next)
		}
		if h.cfgEntry("work-local") != nil {
			t.Fatal("published before durable owner")
		}
		if len(h.devicesConfigFor("m-0b0b0b").Machines) != 0 {
			t.Fatal("owner got entry before persistence")
		}
	}
	mc := &MachineConfig{ID: "work-local", Kind: "local", CodexHomes: []string{"/isolated/codex"}}
	if err := h.addOwnedLocalMachine(ctx, mc, "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	if !mc.OwnerRequired {
		t.Fatal("missing durable owner requirement")
	}
	if got := h.devicesConfigFor("m-0b0b0b"); len(got.Machines) != 1 {
		t.Fatalf("work config: %+v", got)
	}
	if h.entryOwner("primary-computer") != devicesOwner || h.entryOwner("other") != "m-abcdef" {
		t.Fatal("existing owners changed")
	}
	for _, raw := range h.devicesConfig().Machines {
		if machineEntryID(raw) == mc.ID {
			t.Fatal("carrier was given dedicated entry")
		}
	}
	st.beforeWrite = nil
	disk, err := h.diskConfig()
	if err != nil {
		t.Fatal(err)
	}
	restarted := newTestHub(t)
	restarted.cfg.Machines, restarted.bus.idents = disk.Machines, st
	if err := restarted.loadMachineOwners(ctx); err != nil {
		t.Fatal(err)
	}
	if restarted.entryOwner(mc.ID) != "m-0b0b0b" {
		t.Fatal("restart lost owner")
	}
	st.mu.Lock()
	st.kept[machineOwnersKey] = json.RawMessage(`{}`)
	st.mu.Unlock()
	if err := restarted.loadMachineOwners(ctx); err != nil {
		t.Fatal(err)
	}
	if got := restarted.entryOwner(mc.ID); got != unknownMachineOwner {
		t.Fatalf("missing owner fallback: %s", got)
	}
	for _, raw := range restarted.devicesConfig().Machines {
		if machineEntryID(raw) == mc.ID {
			t.Fatal("missing owner sent to carrier")
		}
	}
	if restarted.entryOwner("primary-computer") != devicesOwner {
		t.Fatal("legacy owner changed")
	}
}
func TestOwnedLocalMachinePersistenceFailureDoesNotPublish(t *testing.T) {
	for _, diskFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "owner_write", true: "config_write"}[diskFailure], func(t *testing.T) {
			h, st := localMachineHub(t)
			if diskFailure {
				if err := os.WriteFile(filepath.Join(h.cfg.dir, "hub.json"), []byte(`[1]`), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				st.failWrite = true
			}
			if err := h.addOwnedLocalMachine(context.Background(), &MachineConfig{ID: "work", Kind: "local"}, "m-0b0b0b"); err == nil {
				t.Fatal("failed persistence succeeded")
			}
			if h.devices.due {
				t.Fatal("failed persistence scheduled device config notification")
			}
			if h.cfgEntry("work") != nil || len(h.devicesConfigFor("m-0b0b0b").Machines) != 0 {
				t.Fatal("failed creation published")
			}
			if got := st.owners(t); len(got) != 1 || got["other"] != "m-abcdef" {
				t.Fatalf("existing owners changed: %v", got)
			}
		})
	}
}
func TestOwnedLocalMachineRejectsInvalidAndExistingTargets(t *testing.T) {
	for _, tc := range []struct{ id, owner string }{{"primary-computer", "m-0b0b0b"}, {"work", ""}, {"work", devicesOwner}, {"work", "unknown"}, {"work", "m-111111"}} {
		h, st := localMachineHub(t)
		st.pending = "m-111111"
		if err := h.addOwnedLocalMachine(context.Background(), &MachineConfig{ID: tc.id, Kind: "local"}, tc.owner); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
		if got := st.owners(t); len(got) != 1 || got["other"] != "m-abcdef" {
			t.Fatalf("existing owners changed: %v", got)
		}
	}
}

func TestOwnedLocalMachineRollbackFailureCannotAssignOrdinaryMachine(t *testing.T) {
	h, st := localMachineHub(t)
	path := filepath.Join(h.cfg.dir, "hub.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[1]`), 0600); err != nil {
		t.Fatal(err)
	}
	// Let the reservation persist, then fail both the config write and rollback.
	st.beforeWrite = func(map[string]string) { st.failWrite = true }
	if err := h.addOwnedLocalMachine(context.Background(), &MachineConfig{ID: "work", Kind: "local"}, "m-0b0b0b"); err == nil {
		t.Fatal("failed config and rollback succeeded")
	}
	if got := st.owners(t); got["work"] != "m-0b0b0b" || got["other"] != "m-abcdef" {
		t.Fatalf("missing reservation or existing owner: %v", got)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	err = h.addMachine(&MachineConfig{ID: "work", Kind: "link"}, "admin")
	var refused *addMachineError
	if !errors.As(err, &refused) || refused.code != "ownership_reserved" {
		t.Fatalf("ordinary machine accepted reservation: %v", err)
	}
	if h.cfgEntry("work") != nil || len(h.devicesConfigFor("m-0b0b0b").Machines) != 0 {
		t.Fatal("ordinary machine was published to the reserved manager")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("refused creation changed hub.json")
	}
}
func TestMachineOwnersReadFailureWithholdsDedicatedMachineOnly(t *testing.T) {
	h, st := localMachineHub(t)
	ctx := context.Background()
	if err := h.loadMachineOwners(ctx); err != nil {
		t.Fatal(err)
	}
	h.devices.entries = append(h.devices.entries, MachineConfig{ID: "dedicated", Kind: "local", OwnerRequired: true})
	h.devices.assign["dedicated"] = "m-0b0b0b"
	if err := st.PutSetting(ctx, machineOwnersKey, map[string]string{"dedicated": "m-0b0b0b", "other": "m-abcdef"}); err != nil {
		t.Fatal(err)
	}
	st.readFails = 1
	if err := h.loadMachineOwners(ctx); err == nil {
		t.Fatal("read should fail")
	}
	if h.entryOwner("dedicated") != unknownMachineOwner || len(h.devicesConfigFor("m-0b0b0b").Machines) != 0 {
		t.Fatal("unread dedicated owner was sent to a manager")
	}
	if h.entryOwner("primary-computer") != devicesOwner || h.entryOwner("other") != "m-abcdef" {
		t.Fatal("legacy ownership changed on failed read")
	}
	if len(h.devicesConfig().Machines) != 1 || len(h.devicesConfigFor("m-abcdef").Machines) != 1 {
		t.Fatal("legacy config changed on failed read")
	}
	if err := h.readOwnersMoving(ctx); err != nil {
		t.Fatal(err)
	}
	if h.entryOwner("primary-computer") != devicesOwner || h.entryOwner("other") != "m-abcdef" {
		t.Fatal("retry did not preserve owners")
	}
	if h.entryOwner("dedicated") != "m-0b0b0b" || len(h.devicesConfigFor("m-0b0b0b").Machines) != 1 {
		t.Fatal("successful ownership retry did not restore the dedicated machine")
	}
}

func TestAdminCreatesOwnedLocalMachine(t *testing.T) {
	h, _, mux := adminTestHub(t)
	st := &localOwnersStore{ownersStore: &ownersStore{kept: map[string]json.RawMessage{}}}
	h.bus.idents = st
	h.devices.started = true
	c := signedIn(t, mux, ip4(192, 168, 1, 20)+":1")
	code, out, _ := c.do("POST", "/api/v1/admin/machines", `{"kind":"local","id":"work-local","name":"Work","manager":"m-0b0b0b","config":{"codex_discovery":false,"codex_homes":["/isolated/codex"],"botmux":false}}`)
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	mc := h.cfgEntry("work-local")
	if mc == nil || !mc.OwnerRequired || len(mc.CodexHomes) != 1 || mc.CodexHomes[0] != "/isolated/codex" || mc.Botmux == nil || *mc.Botmux {
		t.Fatalf("config not preserved: %+v", mc)
	}
	if len(h.devicesConfig().Machines) != 0 || len(h.devicesConfigFor("m-0b0b0b").Machines) != 1 {
		t.Fatal("first config went to wrong manager")
	}
	if _, err := h.moveMachine(context.Background(), "work-local", devicesOwner); err == nil {
		t.Fatal("dedicated computer moved to carrier")
	}
	code, out, _ = c.do("POST", "/api/v1/admin/machines", `{"kind":"link","id":"ordinary","manager":"m-0b0b0b"}`)
	if code != 400 || out["code"] != "invalid_config" {
		t.Fatalf("nonlocal owner accepted: %d %v", code, out)
	}
}

func TestOwnedLocalMachineCrashBeforeEntryIsHarmless(t *testing.T) {
	h, st := localMachineHub(t)
	st.mu.Lock()
	st.kept[machineOwnersKey] = json.RawMessage(`{"other":"m-abcdef","work-not-published":"m-0b0b0b"}`)
	st.mu.Unlock()
	if err := h.loadMachineOwners(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.cfgEntry("work-not-published") != nil || len(h.devicesConfigFor("m-0b0b0b").Machines) != 0 {
		t.Fatal("orphan reservation started a node")
	}
	if got := st.owners(t); len(got) != 1 || got["other"] != "m-abcdef" {
		t.Fatalf("crash cleanup lost existing owners: %v", got)
	}
}

func TestRemovedManagerCannotRegainDedicatedMachine(t *testing.T) {
	h, st := localMachineHub(t)
	ctx := context.Background()
	if err := h.addOwnedLocalMachine(ctx, &MachineConfig{ID: "work-local", Kind: "local"}, "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	h.releaseMachines(ctx, "m-0b0b0b")
	if h.entryOwner("work-local") != unknownMachineOwner || len(h.devicesConfigFor("m-0b0b0b").Machines) != 0 {
		t.Fatal("removed manager kept dedicated owner")
	}
	if got := st.owners(t); len(got) != 1 || got["other"] != "m-abcdef" {
		t.Fatalf("owner map after removal: %v", got)
	}
	// The mock roster still recognizes the same ID, as a re-registration would.
	if !h.knownManager(ctx, "m-0b0b0b") || len(h.devicesConfigFor("m-0b0b0b").Machines) != 0 {
		t.Fatal("same manager ID recovered ownership")
	}
	for _, raw := range h.devicesConfig().Machines {
		if machineEntryID(raw) == "work-local" {
			t.Fatal("removed manager's machine given to carrier")
		}
	}
}

func TestDedicatedOwnerReleaseMustPersistBeforeManagerRevocation(t *testing.T) {
	h, st := localMachineHub(t)
	ctx := context.Background()
	if err := h.addOwnedLocalMachine(ctx, &MachineConfig{ID: "work-local", Kind: "local"}, "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	st.failWrite = true
	revoked := false
	err := h.removingManager(func() error {
		if err := h.releaseManagerDedicatedMachines(ctx, "m-0b0b0b"); err != nil {
			return err
		}
		revoked = true
		return nil
	})
	if err == nil || revoked {
		t.Fatal("manager removal ignored owner persistence failure")
	}
	if h.entryOwner("work-local") != "m-0b0b0b" {
		t.Fatal("failed removal altered assignment")
	}
	st.failWrite = false
	if err := h.releaseManagerDedicatedMachines(ctx, "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	if h.entryOwner("work-local") != unknownMachineOwner || len(h.devicesConfigFor("m-0b0b0b").Machines) != 0 {
		t.Fatal("successful removal left reusable owner")
	}
}
