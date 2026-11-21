package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestProtocolConformance(t *testing.T) {
	// The standard SDK checks handshake/error contracts and all declared UI families.
	// No Hub credential is supplied: this verifies the honest unavailable view too.
	exttest.Check(t, manifest, exttest.Serve(t, program()))
}

func TestPermissionsAndManifest(t *testing.T) {
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.Permissions == nil || !slices.Equal(m.Permissions.Hub, []string{"agents.read", "machines.read"}) {
		t.Fatal("unexpected Hub permissions")
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(manifest, &raw)
	var permissions map[string]json.RawMessage
	_ = json.Unmarshal(raw["permissions"], &permissions)
	if len(permissions) != 1 {
		t.Fatal("unexpected additional permission")
	}
	if len(m.Contributes.UI) != 2 {
		t.Fatal("unexpected contributions")
	}
	var widget struct {
		Contributes struct {
			UI []struct {
				ID, Placement  string
				Families       []string
				RefreshMinutes int `json:"refresh_minutes"`
			}
		}
	}
	_ = json.Unmarshal(manifest, &widget)
	for _, ui := range widget.Contributes.UI {
		if ui.Placement == "widget" && (ui.ID != widgetID || ui.RefreshMinutes != 15 || !slices.Equal(ui.Families, []string{"accessory_circular", "accessory_rectangular", "large"})) {
			t.Fatal("unexpected widget contract")
		}
	}
}

func TestProtocolActualReadsAndLocalizedCounts(t *testing.T) {
	var sessionCount atomic.Int32
	sessionCount.Store(2)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-only-token" {
			t.Errorf("unexpected request method or auth")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/v1/machines":
			fmt.Fprint(w, `{"machines":[{"id":"one","name":"private host"}]}`)
		case "/api/v1/sessions":
			rows := make([]map[string]string, sessionCount.Load())
			for i := range rows {
				rows[i] = map[string]string{"title": "private conversation", "last_text": "private text"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": rows})
		case "/api/v1/interactions":
			fmt.Fprint(w, `{"interactions":[{"id":"q","summary":"private request"}]}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	run := exttest.Serve(t, program())
	encoder, decoder := json.NewEncoder(run.ToProgram), json.NewDecoder(run.FromProgram)
	call := func(method string, params any) json.RawMessage {
		t.Helper()
		if err := encoder.Encode(map[string]any{"id": "test", "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		var response struct {
			Result json.RawMessage
			Error  json.RawMessage
		}
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if len(response.Error) != 0 {
			t.Fatalf("unexpected protocol error: %s", response.Error)
		}
		return response.Result
	}
	call("initialize", map[string]any{"protocol": 1, "hub": map[string]any{"url": server.URL, "token": "test-only-token", "scopes": []string{"agents.read", "machines.read"}}})
	for _, locale := range []string{"en", "zh-Hans", "ja"} {
		for _, family := range []string{"accessory_circular", "accessory_rectangular", "large"} {
			raw := call("ui.render", map[string]any{"contribution": widgetID, "placement": "widget", "size": family, "ui_version": 3, "locale": locale})
			var view wire.View
			if err := json.Unmarshal(raw, &view); err != nil {
				t.Fatal(err)
			}
			if view.Title != localized(locale).Title {
				t.Fatalf("wrong localized title for %s", locale)
			}
			if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "test-only-token") || strings.Contains(string(raw), server.URL) {
				t.Fatal("private source content leaked into view")
			}
			items := view.Sections[0].Items
			switch family {
			case "accessory_circular":
				if items[0].Value != "1" {
					t.Fatal("pending count not rendered")
				}
			case "accessory_rectangular":
				if !strings.Contains(items[0].Text, localized(locale).Sessions+" 2") {
					t.Fatal("session count not rendered")
				}
			case "large":
				if len(items) != 3 || items[0].Value != "1" || items[1].Value != "2" || items[2].Value != "1" {
					t.Fatal("incorrect aggregate counts")
				}
			}
		}
	}
	sessionCount.Store(3)
	raw := call("ui.render", map[string]any{"contribution": widgetID, "placement": "widget", "size": "large", "ui_version": 3, "locale": "en"})
	var changed wire.View
	_ = json.Unmarshal(raw, &changed)
	if changed.Sections[0].Items[1].Value != "3" {
		t.Fatal("fresh source data was not read")
	}
	if requests.Load() != 30 {
		t.Fatalf("expected three GETs per render, got %d", requests.Load())
	}
	_ = run.ToProgram.Close()
}

func TestFailureDoesNotBecomeZero(t *testing.T) {
	for _, body := range []string{`{}`, `{"machines":"no"}`, `{"machines":[4]}`, strings.Repeat("x", bodyLimit+1)} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			a := &app{client: server.Client(), base: server.URL, token: "test-only-token"}
			view, err := a.render(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{Size: "large", Locale: "en"}})
			if err != nil || len(view.Sections[0].Items) != 1 || view.Sections[0].Items[0].Text != "Hub unavailable" {
				t.Fatal("failed read must show unavailable")
			}
		})
	}
}

func TestPresentEmptyLists(t *testing.T) {
	for _, empty := range []string{"[]", "null"} {
		t.Run(empty, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := strings.TrimPrefix(r.URL.Path, "/api/v1/")
				fmt.Fprintf(w, `{"%s":%s}`, key, empty)
			}))
			defer server.Close()
			a := &app{client: server.Client(), base: server.URL, token: "test-only-token"}
			got, err := a.read(context.Background())
			if err != nil || got != (counts{}) {
				t.Fatal("present empty lists must be zero counts")
			}
		})
	}
}

func TestRedirectDoesNotForwardCredential(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1); fmt.Fprint(w, `{"machines":[]}`) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer origin.Close()
	a := &app{client: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, base: origin.URL, token: "test-only-token"}
	if _, err := a.count(context.Background(), "/api/v1/machines", "machines"); err == nil || redirected.Load() != 0 {
		t.Fatal("redirect followed or accepted")
	}
}

func TestMissingScopesDoNotReadHub(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reads.Add(1) }))
	defer server.Close()
	a := &app{client: server.Client()}
	_, _ = a.init(context.Background(), &ext.Init{InitializeParams: wire.InitializeParams{Hub: wire.HubInfo{URL: server.URL, Token: "test-only-token", Scopes: []string{"agents.read"}}}}, nil)
	if _, err := a.read(context.Background()); err == nil || reads.Load() != 0 {
		t.Fatal("missing permission must not access the Hub")
	}
}
