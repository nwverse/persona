package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/update"
)

type updateLoopTransport func(*http.Request) (*http.Response, error)

func (f updateLoopTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateCheckLoopPreservesInstall(t *testing.T) {
	for _, state := range []string{"waiting_idle", "confirming"} {
		for _, reply := range []string{"newer", "current", "failed"} {
			t.Run(state+"/"+reply, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					h := newTestHub(t)
					h.build = update.Make("v1-202611021500", update.Release, "202611021500", "https://hub.example.com/dl")
					h.version = h.build.Version
					h.build.Commit = strings.Repeat("a", 40)
					u := h.upd
					u.services = serviceFinder{}
					u.target = func() (string, error) { return h.cfg.dir + "/personad", nil }
					u.self = u.target
					u.state, u.progress, u.errText = state, "hub", "existing update detail"
					u.running, u.confirmed = state == "waiting_idle", state != "confirming"
					u.busy = []busyItem{{MachineID: "box", SessionID: "session", Title: "Working"}}
					u.lastCheck = time.Now().Add(-time.Hour)
					u.release = &update.Published{Manifest: &update.Manifest{Version: "previous", Build: 202611021400}}
					if state == "confirming" {
						m := newMachine(h, MachineConfig{ID: "box", Name: "Box"})
						h.machines, h.byID[m.cfg.ID] = []*Machine{m}, m
						// Keep confirmation pending across both checks while the machine is offline.
						u.confirmIn = 2 * time.Hour
						go u.confirm(ctx, update.Pending{Online: []string{"box"}, ToBuild: h.build.Number})
						synctest.Wait()
					}
					before := u.view()
					app := appClient(h)
					manifest := update.Manifest{Version: "v2-202611021700", Build: 202611021700, Commit: h.build.Commit,
						Files: map[string]update.File{"linux-amd64": {Name: "personad-linux-amd64.gz"}}}
					if reply == "current" {
						manifest.Version, manifest.Build = h.version, h.build.Number
					}
					data, err := json.Marshal(manifest)
					if err != nil {
						t.Fatal(err)
					}
					var calls atomic.Int32
					u.client = &http.Client{Transport: updateLoopTransport(func(r *http.Request) (*http.Response, error) {
						calls.Add(1)
						if r.URL.Path != "/dl/latest.json" {
							t.Errorf("check started another install: %s", r.URL.Path)
						}
						status := http.StatusOK
						if reply == "failed" {
							status = http.StatusServiceUnavailable
						}
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
					})}
					go u.loop(ctx)
					synctest.Wait()
					for i, wait := range []time.Duration{updateFirstCheck, updateCheckEvery} {
						time.Sleep(wait - time.Nanosecond)
						synctest.Wait()
						if got := calls.Load(); got != int32(i) {
							t.Fatalf("check %d ran early: %d requests", i+1, got)
						}
						time.Sleep(time.Nanosecond)
						synctest.Wait()
						if got := calls.Load(); got != int32(i+1) {
							t.Fatalf("check %d: got %d requests", i+1, got)
						}
						v := u.view()
						assertUpdateInstallView(t, v, before)
						if v.LastCheck == nil || !v.LastCheck.Equal(time.Now()) {
							t.Fatalf("check %d did not refresh last_check: %v", i+1, v.LastCheck)
						}
						wantBuild := manifest.Build
						if reply == "failed" {
							wantBuild = before.Latest.Build
						}
						if v.Latest == nil || v.Latest.Build != wantBuild {
							t.Fatalf("check %d latest: %+v, want build %d", i+1, v.Latest, wantBuild)
						}
						if u.running != (state == "waiting_idle") || len(u.settings.History) != 0 {
							t.Fatal("check started or recorded another install")
						}
						var last *updateView
						for len(app.out) > 0 {
							var event struct {
								Type   string     `json:"type"`
								Update updateView `json:"update"`
							}
							if err := json.Unmarshal(<-app.out, &event); err != nil {
								t.Fatal(err)
							}
							if event.Type == "hub_update" {
								assertUpdateInstallView(t, event.Update, before)
								last = &event.Update
							}
						}
						if last == nil || last.LastCheck == nil || !last.LastCheck.Equal(*v.LastCheck) || last.Latest == nil || last.Latest.Build != wantBuild {
							t.Fatalf("check %d did not broadcast refreshed metadata: %+v", i+1, last)
						}
					}
					cancel()
					synctest.Wait()
				})
			})
		}
	}
}
