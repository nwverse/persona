package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/update"
)

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
					response = update.DevelopmentCandidate{Branch: "main", Commit: strings.Repeat("b", 40)}
				case "build":
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
			if u.view().State != "updated" {
				t.Fatal(u.view().State)
			}
		})
	}
}
