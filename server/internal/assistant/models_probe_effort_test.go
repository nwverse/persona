package assistant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestProviderTestUsesDraftModelAndEffortWithoutSaving(t *testing.T) {
	for _, tc := range []struct {
		api, model, effort, field string
		want                      any
	}{
		{"responses", "draft-model", "max", "reasoning", map[string]any{"effort": "max"}},
		{"responses", "draft-model", "off", "reasoning", map[string]any{"effort": "none"}},
		{"messages", "draft-model", "high", "output_config", map[string]any{"effort": "high"}},
		{"messages", "draft-model", "off", "thinking", map[string]any{"type": "disabled"}},
		{"messages", "claude-sonnet-5-5", "off", "thinking", map[string]any{"type": "between_tools"}},
		{"messages", "claude-opus-5-5", "off", "output_config", map[string]any{"effort": "low"}},
	} {
		t.Run(tc.api+"/"+tc.model+"/"+tc.effort, func(t *testing.T) {
			_, mux := launcherTestService(t)
			var body map[string]any
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&body)
				if tc.api == "responses" {
					_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
				} else {
					_, _ = w.Write([]byte(`{"type":"message","content":[]}`))
				}
			}))
			defer gateway.Close()
			saved, _ := json.Marshal(map[string]any{"name": "gateway", "kind": "api", "api": tc.api, "model": "saved-model", "base_url": gateway.URL, "api_key": fakeKey})
			if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers", string(saved)); code != http.StatusOK {
				t.Fatal(raw)
			}
			_, before := launcherCall(t, mux, "GET", "/api/v1/model-config", "")
			draft, _ := json.Marshal(map[string]any{"name": "gateway", "model": tc.model, "effort": tc.effort})
			result := probeResult(t, mux, "/api/v1/launchers/test", string(draft))
			if result["ok"] != true || result["model"] != tc.model || result["effort"] != tc.effort || result["ms"] == nil {
				t.Fatalf("draft combination was not verified: %v", result)
			}
			if body["model"] != tc.model || !reflect.DeepEqual(body[tc.field], tc.want) {
				t.Fatalf("draft not sent to provider: model=%v %s=%v", body["model"], tc.field, body[tc.field])
			}
			_, after := launcherCall(t, mux, "GET", "/api/v1/model-config", "")
			if before != after {
				t.Fatal("a draft connection test changed model configuration")
			}
			stored := probeResult(t, mux, "/api/v1/launchers/test", `{"name":"gateway"}`)
			if stored["model"] != "saved-model" || stored["effort"] != "" {
				t.Fatalf("draft test changed provider defaults: %v", stored)
			}
		})
	}
}

func TestProviderTestCannotVerifyUnsupportedEffort(t *testing.T) {
	_, mux := launcherTestService(t)
	calls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer gateway.Close()
	for _, api := range []string{"chat", "messages"} {
		effort := "high"
		if api == "messages" {
			effort = "not-an-effort"
		}
		draft, _ := json.Marshal(map[string]any{"api": api, "base_url": gateway.URL, "api_key": fakeKey, "model": "m", "effort": effort})
		result := probeResult(t, mux, "/api/v1/launchers/test", string(draft))
		if result["ok"] != false || result["code"] != "unsupported_effort" || result["effort"] != nil {
			t.Fatalf("unsupported combination marked verified: %v", result)
		}
	}
	if calls != 0 {
		t.Fatalf("unsupported effort reached the provider: %d requests", calls)
	}
}
