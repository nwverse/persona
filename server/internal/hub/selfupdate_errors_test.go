package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/update"
)

type updateSettingsErrorStore struct {
	settingsStore
	readErr  error
	writeErr error
	reads    int
}

func (s *updateSettingsErrorStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	s.reads++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s.readErr != nil {
		// A decoding failure can leave the destination partially populated.
		out.(*updateSettings).Bad = []int64{999}
		return false, s.readErr
	}
	return s.settingsStore.Setting(ctx, key, out)
}

func (s *updateSettingsErrorStore) PutSetting(ctx context.Context, key string, value any) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.settingsStore.PutSetting(ctx, key, value)
}

func updaterWithStoredSettings(t *testing.T) (*updater, *updateSettingsErrorStore, updateSettings) {
	t.Helper()
	h := newTestHub(t)
	off := false
	settings := updateSettings{Auto: &off, Bad: []int64{202611021700}, History: []updateRecord{{ToBuild: 202611021700, Result: "rolled_back"}}}
	st := &updateSettingsErrorStore{settingsStore: settingsStore{vals: map[string][]byte{}}}
	if err := st.PutSetting(context.Background(), updateSettingsKey, settings); err != nil {
		t.Fatal(err)
	}
	h.store = st
	return h.upd, st, settings
}

func TestUpdateSettingsReadFailureIsRetried(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "database", true: "cancelled"}[cancelled], func(t *testing.T) {
			u, st, want := updaterWithStoredSettings(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			} else {
				st.readErr = errors.New("database unavailable")
			}
			if err := u.load(ctx); err == nil || u.loaded {
				t.Fatalf("load = %v, loaded = %v", err, u.loaded)
			}
			if !reflect.DeepEqual(u.settings, updateSettings{}) {
				t.Fatalf("failed read published partial settings: %+v", u.settings)
			}
			st.readErr = nil
			if err := u.load(context.Background()); err != nil || !u.loaded || !reflect.DeepEqual(u.settings, want) {
				t.Fatalf("retry: err = %v, loaded = %v, settings = %+v", err, u.loaded, u.settings)
			}
			if st.reads != 2 {
				t.Fatalf("reads = %d, want 2", st.reads)
			}
		})
	}
}

func TestUpdateSettingsMissingRowIsLoaded(t *testing.T) {
	u, st, _ := updaterWithStoredSettings(t)
	delete(st.vals, updateSettingsKey)
	if err := u.load(context.Background()); err != nil || !u.loaded || !u.settings.auto() {
		t.Fatalf("missing row: err = %v, loaded = %v, settings = %+v", err, u.loaded, u.settings)
	}
	st.readErr = errors.New("database unavailable")
	if err := u.load(context.Background()); err != nil || st.reads != 1 {
		t.Fatalf("cached empty settings: err = %v, reads = %d", err, st.reads)
	}
}

func TestUpdateRoutesRefuseUnreadableSettings(t *testing.T) {
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/hub/update", ""},
		{http.MethodGet, "/api/v1/hub/update/history", ""},
		{http.MethodPost, "/api/v1/hub/update/check", ""},
		{http.MethodPost, "/api/v1/hub/update", `{"now":true}`},
		{http.MethodPut, "/api/v1/hub/update/settings", `{"auto":true}`},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			u, st, _ := updaterWithStoredSettings(t)
			st.readErr = errors.New("database unavailable")
			mux := http.NewServeMux()
			u.routes(mux)
			handler := logRequests(u.h, mux)
			for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
				req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				req.Header.Set("Accept-Language", string(lang))
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusServiceUnavailable || body["error"] != i18n.T(lang, "http-status.unavailable") || body["code"] != "internal" {
					t.Fatalf("%s: status = %d, body = %s", lang, w.Code, w.Body.String())
				}
			}
			if st.puts != 1 || u.loaded || len(u.settings.History) != 0 || u.running {
				t.Fatalf("failed load changed settings or started an update: puts = %d, updater = %+v", st.puts, u)
			}
		})
	}
}

func TestUpdateSettingsWriteAfterRecoveryPreservesHistory(t *testing.T) {
	u, st, want := updaterWithStoredSettings(t)
	st.readErr = errors.New("database unavailable")
	mux := http.NewServeMux()
	u.routes(mux)
	put := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/hub/update/settings", strings.NewReader(`{"auto":true}`)))
		return w
	}
	if w := put(); w.Code != http.StatusServiceUnavailable || st.puts != 1 {
		t.Fatalf("failed read: status = %d, puts = %d", w.Code, st.puts)
	}
	st.readErr = nil
	if w := put(); w.Code != http.StatusOK {
		t.Fatalf("retry: status = %d, body = %s", w.Code, w.Body.String())
	}
	var got updateSettings
	if _, err := st.Setting(context.Background(), updateSettingsKey, &got); err != nil {
		t.Fatal(err)
	}
	if !got.auto() || !got.AutoNoticeSeen || !reflect.DeepEqual(got.History, want.History) || !slices.Equal(got.Bad, want.Bad) {
		t.Fatalf("saved settings lost history or bad builds: %+v", got)
	}
}

// A choice the database did not save is refused and not kept in memory either, where an update waiting for idle and
// the view broadcast to the apps would act on it until the next read.
func TestUpdateSettingsFailedWriteKeepsStoredSettings(t *testing.T) {
	u, st, want := updaterWithStoredSettings(t)
	u.h.build = update.Make("v1-202611021500", "release", "202611021500", "https://example.com/dl")
	st.writeErr = errors.New("database unavailable")
	mux := http.NewServeMux()
	u.routes(mux)
	w := httptest.NewRecorder()
	logRequests(u.h, mux).ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/hub/update/settings", strings.NewReader(`{"auto":true}`)))
	if w.Code != http.StatusServiceUnavailable || st.puts != 1 {
		t.Fatalf("failed write: status = %d, puts = %d, body = %s", w.Code, st.puts, w.Body.String())
	}
	if !reflect.DeepEqual(u.settings, want) {
		t.Fatalf("unsaved settings kept in memory: %+v", u.settings)
	}
	if v := u.view(); v.Auto || !v.AutoNotice {
		t.Fatalf("view shows the unsaved choice: auto = %v, auto_notice = %v", v.Auto, v.AutoNotice)
	}
}

func TestUpdateRecordWaitsForSettingsRecovery(t *testing.T) {
	u, st, want := updaterWithStoredSettings(t)
	st.readErr = errors.New("database unavailable")
	r := updateRecord{ToBuild: 202611021800, Result: "rolled_back"}
	if err := u.record(context.Background(), r); err == nil || st.puts != 1 || len(u.settings.History) != 0 {
		t.Fatalf("record after failed read: err = %v, puts = %d, settings = %+v", err, st.puts, u.settings)
	}
	st.readErr = nil
	if err := u.record(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(u.settings.History, append(want.History, r)) || !slices.Equal(u.settings.Bad, append(want.Bad, r.ToBuild)) {
		t.Fatalf("record lost existing history or bad builds: %+v", u.settings)
	}
}

func TestUpdateRollbackMarkerSurvivesSettingsFailure(t *testing.T) {
	for _, failure := range []string{"read", "write"} {
		t.Run(failure, func(t *testing.T) {
			u, st, want := updaterWithStoredSettings(t)
			path := update.RolledBackPath(u.dir)
			rb := update.RolledBack{ToBuild: 202611021800, ToVersion: "v2", FromVersion: "v1"}
			if err := update.Save(path, rb); err != nil {
				t.Fatal(err)
			}
			if failure == "read" {
				st.readErr = errors.New("database unavailable")
			} else {
				st.writeErr = errors.New("database unavailable")
			}
			u.recordRolledBack(context.Background())
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("unsaved rollback marker was removed: %v", err)
			}
			st.readErr, st.writeErr = nil, nil
			u.recordRolledBack(context.Background())
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("saved rollback marker remains: %v", err)
			}
			if len(u.settings.History) != len(want.History)+1 || !slices.Equal(u.settings.Bad, append(want.Bad, rb.ToBuild)) {
				t.Fatalf("rollback recovery lost or duplicated history: %+v", u.settings)
			}
		})
	}
}

func TestUpdateAutomaticCheckWaitsForSettings(t *testing.T) {
	u, st, _ := updaterWithStoredSettings(t)
	p := newPublisher(t, "v2-202611021700", 202611021700)
	var reads atomic.Int32
	p.asked = func(string) { reads.Add(1) }
	u.h.build = update.Make("v1-202611021500", "release", "202611021500", p.srv.URL+"/dl")
	u.client = p.srv.Client()
	u.release = &update.Published{Manifest: &update.Manifest{Build: 202611021700}}
	st.readErr = errors.New("database unavailable")
	if _, err := u.check(context.Background()); err == nil || reads.Load() != 0 || u.wantsAuto() {
		t.Fatalf("checked or offered an automatic update before loading settings: err = %v, reads = %d", err, reads.Load())
	}
	st.readErr = nil
	if _, err := u.check(context.Background()); err != nil || reads.Load() == 0 || u.wantsAuto() {
		t.Fatalf("recovery ignored stored update policy: err = %v, reads = %d", err, reads.Load())
	}
}

func TestUpdateHTTPCheckRecoversRollbackBeforeAutomaticUpdate(t *testing.T) {
	p := newPublisherWith(t, "v2-202611021800", 202611021800, func(m *update.Manifest) {
		m.Commit = gitB
		m.Ancestors, _ = update.EncodeAncestors([]string{gitB, gitA})
	})
	h, _, _, execs := updatingHub(t, p, "release")
	h.build.Commit = gitA
	u := h.upd
	st := &updateSettingsErrorStore{settingsStore: settingsStore{vals: map[string][]byte{}}}
	h.store = st
	if err := st.PutSetting(context.Background(), updateSettingsKey, updateSettings{}); err != nil {
		t.Fatal(err)
	}
	path := update.RolledBackPath(u.dir)
	rb := update.RolledBack{ToBuild: 202611021800, ToVersion: "v2-202611021800", FromVersion: h.version}
	if err := update.Save(path, rb); err != nil {
		t.Fatal(err)
	}
	st.readErr = errors.New("database unavailable")
	u.recordRolledBack(context.Background())
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed startup recording lost rollback marker: %v", err)
	}

	// An app can request a check before the periodic update loop retries the failed startup read.
	st.readErr = nil
	mux := http.NewServeMux()
	u.routes(mux)
	w := httptest.NewRecorder()
	logRequests(h, mux).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/hub/update/check", nil))
	var view updateView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || view.Latest == nil || !view.Latest.Bad || u.wantsAuto() || len(execs()) != 0 {
		t.Fatalf("HTTP check offered the rolled-back build again: status = %d, body = %s", w.Code, w.Body.String())
	}
	var saved updateSettings
	if _, err := st.Setting(context.Background(), updateSettingsKey, &saved); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(saved.Bad, rb.ToBuild) || len(saved.History) != 1 || saved.History[0].Result != "rolled_back" {
		t.Fatalf("rollback was not persisted before checking: %+v", saved)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("persisted rollback marker remains: %v", err)
	}
}
