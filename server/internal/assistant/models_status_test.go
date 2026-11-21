package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

func TestModelPurposeStatusCodeAcrossListAndDetail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config ai.Config
		code   ModelPurposeStatusCode
		key    string
	}{
		{"no API", ai.Config{}, modelStatusNoAPI, "status_no_api"},
		{"no model", ai.Config{BaseURL: "https://api.example.com"}, modelStatusNoModel, "status_no_model"},
		{"no credentials", ai.Config{BaseURL: "https://api.example.com", Model: "m1"}, modelStatusNoCredentials, "status_no_credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, _ := modelService(t)
			f.mu.Lock()
			f.hello.Models.AI = tc.config
			f.mu.Unlock()
			for _, lang := range []i18n.Lang{i18n.En, i18n.Ja} {
				get := func(path string) []byte {
					t.Helper()
					res, err := serveHTTP(context.Background(), s, assistantwire.HTTPRequest{
						Method: http.MethodGet, Path: path, Header: map[string][]string{"Accept-Language": {string(lang)}},
					}, s.lang)
					if err != nil || res.Status != http.StatusOK {
						t.Fatalf("GET %s in %s: %d %s (%v)", path, lang, res.Status, res.Body, err)
					}
					return res.Body
				}
				var list struct {
					Purposes []ModelPurpose `json:"purposes"`
				}
				if err := json.Unmarshal(get("/api/v1/model-config"), &list); err != nil {
					t.Fatal(err)
				}
				expectations := map[string]struct {
					code ModelPurposeStatusCode
					text string
				}{
					"agent.new": {modelStatusOK, modelUnverified},
					"kb":        {tc.code, mc(lang, tc.key)},
				}
				for _, p := range list.Purposes {
					want, ok := expectations[p.ID]
					if !ok {
						continue
					}
					var detail struct {
						Purpose ModelPurpose `json:"purpose"`
					}
					if err := json.Unmarshal(get("/api/v1/model-config/purposes/"+p.ID), &detail); err != nil {
						t.Fatal(err)
					}
					for _, got := range []ModelPurpose{p, detail.Purpose} {
						if got.StatusCode != want.code || got.Status != want.text {
							t.Fatalf("%s in %s: want %s / %q, got %s / %q", p.ID, lang, want.code, want.text, got.StatusCode, got.Status)
						}
					}
					delete(expectations, p.ID)
				}
				if len(expectations) > 0 {
					t.Fatalf("missing purposes: %v", expectations)
				}
			}
		})
	}
}

func TestModelPurposeOpaquePluginStatusCode(t *testing.T) {
	s, f, _ := modelService(t)
	f.mu.Lock()
	// Plugin status text is opaque, even when it happens to match the manager's legacy sentinel.
	f.voice = []assistantwire.VoicePlugin{{ID: "voice", Name: "Voice", Error: modelUnverified}}
	f.mu.Unlock()
	if p := purposeOf(t, s, "plugin.voice"); p.Status != modelUnverified || p.StatusCode != modelStatusOther {
		t.Fatalf("status %s / %q, want other / %q", p.StatusCode, p.Status, modelUnverified)
	}
}

func TestModelPurposeMachineStatusCodes(t *testing.T) {
	for _, tc := range []struct {
		online bool
		code   ModelPurposeStatusCode
		key    string
	}{
		{false, modelStatusMachineOffline, "status_machine_offline"},
		{true, modelStatusNodeOutdated, "status_node_outdated"},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			s, f, _ := modelService(t)
			f.mu.Lock()
			f.devices = []Device{{ID: "server", Name: "Server", Online: tc.online, Reachable: tc.online, Version: "v1"}}
			f.mu.Unlock()
			want := mc(s.requestCtxLang(context.Background()), tc.key)
			if p := purposeOf(t, s, "assistant"); p.Status != want || p.StatusCode != tc.code {
				t.Fatalf("status %s / %q, want %s / %q", p.StatusCode, p.Status, tc.code, want)
			}
		})
	}
}
