package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const candidatesRoute = "/api/v1/assistant/dispatch/candidates"

// Read the wire response as its caller would. Comparing maps also catches new fields that a typed decoder would ignore.
func readCandidateRows(t *testing.T, s *Service, route, envelope, machine string) []map[string]any {
	t.Helper()
	rec := request(s, http.MethodGet, route+"?machine="+url.QueryEscape(machine), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s for %q: %d %s", route, machine, rec.Code, rec.Body.String())
	}
	var body map[string][]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body) != 1 || body[envelope] == nil {
		t.Fatalf("expected one %q array, got %s (decode: %v)", envelope, rec.Body.String(), err)
	}
	return body[envelope]
}

func TestDispatchCandidatesMatchWorkerPresets(t *testing.T) {
	s, f := defaultsHub(t)
	ctx := context.Background()
	external := proto.ExternalLauncherName("sample", "remote")
	const privateMarker = "private-inventory-fixture"
	f.mu.Lock()
	for i := range f.devices {
		if f.devices[i].ID == "box" {
			f.devices[i].Name, f.devices[i].SSHHost = "Lab workbench", "lab-alias"
			f.devices[i].ExternalInventory = true
		}
	}
	f.zshrc["box"] = []proto.Launcher{
		{Name: "gateway", Source: "zshrc", BaseURL: "https://" + privateMarker + ".invalid", Model: privateMarker},
		{Name: "hidden", Source: "zshrc"},
		{Name: external, Source: proto.ExternalLauncherSource},
	}
	f.mu.Unlock()
	saveTiers(t, s, map[string]ModelTier{
		"busy":     {Launcher: "gateway", Model: "small", Effort: "high", Speed: "fast", Description: "Test suite"},
		"basic":    {Launcher: "claude"},
		"retired":  {Launcher: "hidden"},
		"missing":  {Launcher: "other"},
		"external": {Launcher: external},
	})
	if err := s.store().PutSetting(ctx, launchersHiddenKey, []string{"hidden"}); err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour)
	for _, name := range []string{"gateway", "hidden"} {
		s.recordHealth(ctx, "box", name, healthResult{
			err: privateMarker, kind: errKindUsageLimit, source: healthSourceSession, resetsAt: &reset,
		})
	}
	want := []map[string]any{
		{"name": "basic", "launcher": "claude"},
		{"name": "busy", "launcher": "gateway", "model": "small", "effort": "high", "speed": "fast",
			"description": "Test suite", "unavailable": true},
	}
	for _, machine := range []string{"box", "LAB WORKBENCH", "lab-ALIAS"} {
		t.Run(machine, func(t *testing.T) {
			presets := readCandidateRows(t, s, "/api/v1/assistant/worker-presets", "presets", machine)
			got := readCandidateRows(t, s, candidatesRoute, "candidates", machine)
			if !reflect.DeepEqual(got, presets) || !reflect.DeepEqual(got, want) {
				t.Fatalf("candidates = %v, worker presets = %v, want %v", got, presets, want)
			}
		})
	}
}

func TestDispatchCandidatesErrorsAndEmptyInventory(t *testing.T) {
	s, f := defaultsHub(t)
	for _, path := range []string{candidatesRoute, candidatesRoute + "?machine=", candidatesRoute + "?machine=absent"} {
		rec := request(s, http.MethodGet, path, "")
		var body struct{ Error string }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusNotFound || body.Error != "unknown machine" {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	store := s.store().(*profileTaskStore)
	store.failKey(modelProfilesKey, errors.New("profile read failed"))
	if rows := readCandidateRows(t, s, candidatesRoute, "candidates", "box"); len(rows) != 0 {
		t.Fatal(rows)
	}
	store.failKey(modelProfilesKey, nil)
	saveTiers(t, s, map[string]ModelTier{"elsewhere": {Launcher: "other"}})
	if rows := readCandidateRows(t, s, candidatesRoute, "candidates", "box"); len(rows) != 0 {
		t.Fatal(rows)
	}
	if err := store.PutSetting(context.Background(), modelProfileStateKey, modelProfileState{}); err != nil {
		t.Fatal(err)
	}
	if rows := readCandidateRows(t, s, candidatesRoute, "candidates", "box"); len(rows) != 0 {
		t.Fatal(rows)
	}
	const internalDetail = "internal-inventory-error-fixture"
	f.answerNode(DeviceLaunchersList, func(string, json.RawMessage) (any, error) {
		return nil, errors.New(internalDetail)
	})
	for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		w := &recorder{header: http.Header{}, lang: func() i18n.Lang { return lang }}
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, candidatesRoute+"?machine=box", nil))
		var public struct{ Error string }
		if err := json.Unmarshal(w.body.Bytes(), &public); err != nil || w.status != http.StatusBadGateway ||
			public.Error != "launcher inventory unavailable" || strings.Contains(w.body.String(), internalDetail) {
			t.Fatalf("candidate inventory error in %s: %d %s", lang, w.status, w.body.String())
		}
	}
}

func TestDispatchCandidatesReadOnly(t *testing.T) {
	s, f := defaultsHub(t)
	store := s.store().(*profileTaskStore)
	store.mu.Lock()
	writes := store.puts
	store.mu.Unlock()
	f.mu.Lock()
	from, appCalls, events := len(f.calls), len(f.appCalls), len(f.events)
	f.mu.Unlock()
	s.admission.mu.Lock()
	beforeAdmission := s.admission.at
	s.admission.mu.Unlock()
	head := request(s, http.MethodHead, candidatesRoute+"?machine=box", "")
	if head.Code != http.StatusMethodNotAllowed || head.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("HEAD candidates: %d, Allow %q", head.Code, head.Header().Get("Allow"))
	}
	for range 2 {
		readCandidateRows(t, s, candidatesRoute, "candidates", "box")
	}
	store.mu.Lock()
	afterWrites := store.puts
	store.mu.Unlock()
	store.planMu.Lock()
	plans := len(store.plans)
	store.planMu.Unlock()
	if afterWrites != writes || plans != 0 {
		t.Fatalf("read changed settings or plans: writes %d -> %d, plans %d", writes, afterWrites, plans)
	}
	s.admission.mu.Lock()
	accessedAdmission := s.admission.at != beforeAdmission || len(s.admission.machineClaims) > 0 ||
		len(s.admission.directionClaims) > 0 || len(s.admission.groupClaims) > 0
	s.admission.mu.Unlock()
	if accessedAdmission {
		t.Fatal("candidate reads entered the admission gate")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.appCalls) != appCalls || len(f.events) != events || len(f.starts) != 0 || len(f.asked) != 0 {
		t.Fatal("candidate reads triggered application calls, events, Agent starts or model calls")
	}
	for _, call := range f.calls[from:] {
		switch call.Method {
		case DeviceDevicesList:
		case DeviceLaunchersList:
			var params map[string]any
			if err := json.Unmarshal(call.Params, &params); err != nil || params["refresh"] != nil {
				t.Fatalf("unexpected launcher inventory parameters: %s", call.Params)
			}
		default:
			t.Errorf("unexpected device call %s", call.Method)
		}
	}
}
