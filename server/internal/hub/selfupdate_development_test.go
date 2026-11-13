package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/update"
)

func TestDevelopmentAppliesOlderProgramBackupThroughSharedInstaller(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			p := newPublisher(t, "unused", 202611021700)
			h, machine, target, execs := updatingHub(t, p, "release")
			u := h.upd
			u.client = newUpdater(h).client
			t.Setenv("PERSONA_NODE_BINARIES", t.TempDir())
			u.loaded, u.settings.Source, u.settings.DevelopmentAuto = true, "web-dev", true
			h.build.Commit = strings.Repeat("a", 40)
			h.cfg.Update.Development = &update.DevelopmentConfig{Backup: update.DevelopmentBackupConfig{Quiesce: []string{"pause"}, Resume: []string{"resume"}}}
			data := filepath.Join(h.cfg.dir, "current-history")
			os.WriteFile(data, []byte("messages created after the backup"), 0600)
			version := update.DevelopmentVersion{ID: "snapshot-fixture", Version: "web-dev-older", Build: 202611010001, Commit: strings.Repeat("b", 40), UpdatedAt: "2026-11-01T10:00:00Z"}
			phases := make(chan string, 8)
			u.devRun = func(ctx context.Context, cfg *update.DevelopmentConfig, dir, operation string, input map[string]any, result any) error {
				phases <- operation
				var response any
				switch operation {
				case "backups":
					response = map[string]any{"backups": []update.DevelopmentVersion{version}}
				case "prepare_backup":
					if corrupt {
						return errors.New("corrupt snapshot")
					}
					if input["backup_id"] != version.ID {
						return errors.New("wrong backup")
					}
					artifacts := t.TempDir()
					script := []byte(fmt.Sprintf("#!/bin/sh\necho %s\necho 'channel release, build %d'\n", version.Version, version.Build))
					os.WriteFile(filepath.Join(artifacts, "personad-"+runtime.GOOS+"-"+runtime.GOARCH+".gz"), gz(script), 0600)
					response = map[string]any{"artifacts": artifacts}
				case "backup":
					response = update.DevelopmentBackup{ID: "snapshot-current", Retained: 3}
				default:
					return fmt.Errorf("unexpected Git/build phase: %s", operation)
				}
				encoded, _ := json.Marshal(response)
				return json.Unmarshal(encoded, result)
			}
			setSessions(machine, proto.Session{SessionID: "busy", Status: proto.StatusWorking})
			api := http.NewServeMux()
			u.routes(api)
			request := func(path string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				api.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
				return w
			}
			if w := request("/api/v1/hub/update/backups/missing/apply"); w.Code != 409 {
				t.Fatal(w.Code)
			}
			if !u.view().Auto {
				t.Fatal("missing selection changed automatic policy")
			}
			if w := request("/api/v1/hub/update/backups/" + version.ID + "/apply"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			if u.view().Auto {
				t.Fatal("backup application left automatic upgrades on")
			}
			if corrupt {
				waitFor(t, "preparation failure", func() bool { return u.view().State == "failed" })
				if len(execs()) != 0 {
					t.Fatal("restarted with corrupt backup")
				}
				old, _ := os.ReadFile(target)
				if !strings.Contains(string(old), "echo old") {
					t.Fatal("current program changed")
				}
			} else {
				waitFor(t, "idle wait", func() bool {
					v := u.view()
					if v.State == "failed" {
						t.Fatalf("preparation failed: %s", v.Error)
					}
					return v.State == "waiting_idle"
				})
				if w := request("/api/v1/hub/update/backups/" + version.ID + "/apply"); w.Code != 409 {
					t.Fatal("duplicate application accepted")
				}
				setSessions(machine)
				waitFor(t, "shared restart", func() bool { return len(execs()) == 1 })
				var pending update.Pending
				if ok, err := update.Load(update.PendingPath(u.dir), &pending); !ok || err != nil {
					t.Fatal(err)
				}
				if pending.ToBuild >= pending.FromBuild || pending.Backup == nil || pending.Development.BackupID != version.ID || pending.Auto {
					t.Fatalf("wrong restoration journal: %+v", pending)
				}
				h.version, h.build.Number, h.build.Commit = pending.ToVersion, pending.ToBuild, version.Commit
				u.confirm(context.Background(), pending)
				if u.view().State != "updated" || u.view().Development.CurrentUpdatedAt == "" {
					t.Fatal("missing successful installation time")
				}
			}
			if got, _ := os.ReadFile(data); string(got) != "messages created after the backup" {
				t.Fatal("current history overwritten")
			}
			close(phases)
			var got []string
			for phase := range phases {
				got = append(got, phase)
			}
			if slices.Contains(got, "build") || slices.Contains(got, "resolve") {
				t.Fatal(got)
			}
			if !corrupt && !slices.Contains(got, "backup") {
				t.Fatal("current state was not backed up")
			}
		})
	}
}

func TestDevelopmentPlatformsFollowRegisteredMachines(t *testing.T) {
	h := newTestHub(t)
	own := runtime.GOOS + "-" + runtime.GOARCH
	if got := h.upd.developmentPlatforms(); !slices.Equal(got, []string{own}) {
		t.Fatalf("Hub-only platforms: %v", got)
	}
	for _, c := range []struct {
		id, os, arch string
		offline      bool
		registered   bool
	}{
		{"mac", "darwin", "arm64", false, true},
		{"same-platform", "darwin", "arm64", false, true},
		{"offline", "linux", "arm64", true, true},
		{"never-seen", "", "", true, true},
		{"partial", "linux", "", true, true},
		{"other-hub", "windows", "amd64", false, false},
	} {
		mc := MachineConfig{ID: c.id, Kind: "ssh"}
		if c.registered {
			h.cfg.Machines = append(h.cfg.Machines, mc)
		}
		m := newMachine(h, mc)
		m.online = !c.offline
		if c.offline {
			m.snap = &proto.Machine{OS: c.os, Arch: c.arch}
		} else {
			m.info = proto.NodeInfo{OS: c.os, Arch: c.arch}
		}
		h.machines, h.byID[c.id] = append(h.machines, m), m
	}
	want := []string{own, "darwin-arm64", "linux-arm64"}
	slices.Sort(want)
	if got := h.upd.developmentPlatforms(); !slices.Equal(got, slices.Compact(want)) {
		t.Fatalf("platforms %v, want %v", got, want)
	}
}

func TestDevelopmentRejectsUnrebuiltPackagesThatCannotBeSetAside(t *testing.T) {
	h := newTestHub(t)
	nodes := t.TempDir()
	t.Setenv("PERSONA_NODE_BINARIES", nodes)
	stale := filepath.Join(nodes, "personad-windows-amd64")
	if err := os.WriteFile(stale, []byte("old package"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stale+".prev", 0700); err != nil {
		t.Fatal(err)
	}
	if err := h.upd.updateNodes(context.Background(), t.TempDir(), "previous-version", true); err == nil {
		t.Fatal("accepted stale packages that still claim the new version")
	}
}

func TestDevelopmentSettingsOptInAndActiveChannelGuard(t *testing.T) {
	u, _, _ := updaterWithStoredSettings(t)
	u.h.cfg.Update.Development = &update.DevelopmentConfig{}
	if w := nodePolicyRequest(t, u, http.MethodPut, `{"source":"web-dev"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if u.view().Auto || u.wantsAuto() {
		t.Fatal("inherited release automatic updates")
	}
	if v := u.view(); !v.Development.Configured || v.Development.Branch != "main" {
		t.Fatalf("unexpected development source: %+v", v.Development)
	}
	u.running = true
	if w := nodePolicyRequest(t, u, http.MethodPut, `{"source":"release"}`); w.Code != 409 {
		t.Fatal("switched during an update")
	}
	u.running = false
	if w := nodePolicyRequest(t, u, http.MethodPut, `{"auto":true}`); w.Code != 409 {
		t.Fatal("enabled without backup coordination")
	}
	u.h.cfg.Update.Development.Backup = update.DevelopmentBackupConfig{Quiesce: []string{"pause"}, Resume: []string{"resume"}}
	if w := nodePolicyRequest(t, u, http.MethodPut, `{"auto":true}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if !u.settings.DevelopmentAuto {
		t.Fatal("explicit automatic choice lost")
	}
	for _, source := range []string{"release", "web-dev"} {
		if w := nodePolicyRequest(t, u, http.MethodPut, fmt.Sprintf(`{"source":%q}`, source)); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if u.settings.DevelopmentAuto {
		t.Fatal("channel switch retained development opt-in")
	}
	fence := update.Pending{ToBuild: 202611130000, Development: &update.DevelopmentCandidate{Commit: strings.Repeat("b", 40)}}
	if err := update.BeginDevelopmentAdoption(u.dir, fence); err != nil {
		t.Fatal(err)
	}
	if w := nodePolicyRequest(t, u, http.MethodPut, `{"source":"release"}`); w.Code != 409 {
		t.Fatal("changed source across unresolved effects")
	}
}

func TestDevelopmentUsesSharedInstallAndPreservesRecovery(t *testing.T) {
	for _, failure := range []string{"", "automatic", "build", "backup", "adopt"} {
		t.Run(failure, func(t *testing.T) {
			p := newPublisher(t, "unused", 202611021700)
			h, _, target, execs := updatingHub(t, p, "release")
			u := h.upd
			u.client = newUpdater(h).client
			nodes := filepath.Join(t.TempDir(), "nodes")
			t.Setenv("PERSONA_NODE_BINARIES", nodes)
			os.MkdirAll(nodes, 0755)
			stale := filepath.Join(nodes, "personad-windows-amd64")
			os.WriteFile(stale, []byte("older unused platform"), 0600)
			u.settings.Source = "web-dev"
			u.loaded = true
			u.h.build.Commit = strings.Repeat("a", 40)
			cfg := &update.DevelopmentConfig{Backup: update.DevelopmentBackupConfig{Quiesce: []string{"fixture-pause"}, Resume: []string{"fixture-resume"}}}
			u.h.cfg.Update.Development = cfg
			web := filepath.Join(h.cfg.dir, "web")
			os.MkdirAll(web, 0700)
			os.WriteFile(filepath.Join(web, "index.html"), []byte("old web"), 0600)
			var phases []string
			u.devRun = func(ctx context.Context, c *update.DevelopmentConfig, dir, operation string, input map[string]any, result any) error {
				phases = append(phases, operation)
				if operation == failure {
					return errors.New("fixture phase failed")
				}
				var response any
				switch operation {
				case "resolve":
					response = update.DevelopmentCandidate{Branch: "main", Commit: strings.Repeat("b", 40), UpdatedAt: "2026-11-13T10:00:00Z"}
				case "build":
					if got := input["platforms"].([]string); !slices.Equal(got, []string{runtime.GOOS + "-" + runtime.GOARCH}) {
						t.Fatalf("unexpected build platforms: %v", got)
					}
					artifacts := t.TempDir()
					script := []byte(fmt.Sprintf("#!/bin/sh\necho %s\necho 'channel release, build %d'\n", input["version"], input["build"]))
					os.WriteFile(filepath.Join(artifacts, "personad-"+runtime.GOOS+"-"+runtime.GOARCH+".gz"), gz(script), 0600)
					response = map[string]any{"artifacts": artifacts, "ancestors": []string{h.build.Commit, strings.Repeat("b", 40)}}
				case "backup":
					response = update.DevelopmentBackup{ID: "snapshot-fixture", Retained: 3, Pruned: []string{"snapshot-old-fixture"}}
				}
				data, _ := json.Marshal(response)
				return json.Unmarshal(data, result)
			}
			if failure == "adopt" {
				u.handover = func(context.Context, string, int64) error { return errors.New("fixture standby failed") }
			}
			if _, err := u.check(context.Background()); err != nil {
				t.Fatal(err)
			}
			if u.view().Latest.UpdatedAt != "2026-11-13T10:00:00Z" {
				t.Fatal("latest commit date missing from the update view")
			}
			if u.wantsAuto() {
				t.Fatal("automatic adoption before opting in")
			}
			automatic := failure == "automatic"
			if automatic {
				u.settings.DevelopmentAuto = true
				if !u.wantsAuto() {
					t.Fatal("explicit automatic update not scheduled")
				}
			}
			u.install(context.Background(), automatic)
			if failure != "" && !automatic {
				if u.view().State != "failed" {
					t.Fatalf("state %+v", u.view())
				}
				data, _ := os.ReadFile(target)
				if !strings.Contains(string(data), "echo old") {
					t.Fatal("old binary lost")
				}
				data, _ = os.ReadFile(filepath.Join(web, "index.html"))
				if string(data) != "old web" {
					t.Fatal("old Web lost")
				}
				if len(execs()) != 0 {
					t.Fatal("restarted on failed preparation")
				}
				if failure == "build" && len(phases) != 2 {
					t.Fatal("backup ran after failed build")
				}
				return
			}
			if len(execs()) != 1 {
				t.Fatal("shared restart not used")
			}
			var pending update.Pending
			if ok, err := update.Load(update.PendingPath(u.dir), &pending); !ok || err != nil {
				t.Fatal(err)
			}
			if pending.Development == nil || pending.Backup == nil || pending.Nodes == "" || pending.WebPrev == "" {
				t.Fatalf("missing recovery state %+v", pending)
			}
			if pending.Auto != automatic {
				t.Fatal("manual/automatic attribution lost")
			}
			// Simulate the next process's confirmation without touching a real service.
			h.build.Number = pending.ToBuild
			h.version = pending.ToVersion
			u.confirm(context.Background(), pending)
			if _, err := os.Stat(filepath.Join(nodes, "personad-"+runtime.GOOS+"-"+runtime.GOARCH)); err != nil {
				t.Fatal("manual node package missing with automatic fleet updates off", err)
			}
			if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unrebuilt package would claim the new Hub version", err)
			}
			if data, err := os.ReadFile(stale + ".prev"); err != nil || string(data) != "older unused platform" {
				t.Fatal("unrebuilt package was not preserved", err)
			}
			if u.view().State != "updated" {
				t.Fatal(u.view().State)
			}
		})
	}
}
