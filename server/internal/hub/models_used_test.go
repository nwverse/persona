package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/proto"
)

// A model id typed in an app that ran (#1644) is told to the manager, which offers it in that machine's launcher list
// from then on (custom); a start that failed is not, nor one the node never got; Forget drops it.
func TestTypedModelRememberedAfterItRan(t *testing.T) {
	h, _ := modelTestHub(t)
	modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
		switch method {
		case "launchers.models":
			return proto.LauncherModels{Agent: "claude", Models: []proto.AgentModel{{ID: "opus"}}}, nil
		case "session.start":
			if p["model"] == "gw/fails" {
				return nil, errors.New("claude did not come up")
			}
			return map[string]any{"session": proto.Session{SessionID: "s1"}}, nil
		}
		return map[string]any{}, nil
	})
	connectAssistant(t, h)
	custom := func() []string {
		w := modelsRequest(h, "GET", "/api/v1/machines/box/launchers/claude/models", "")
		if w.Code != http.StatusOK {
			t.Fatalf("models: %d %s", w.Code, w.Body)
		}
		var res proto.LauncherModels
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		var out []string
		for _, m := range res.Models {
			if m.Custom {
				out = append(out, m.ID)
			}
		}
		return out
	}

	if w := modelsRequest(h, "POST", "/api/v1/sessions", `{"machine_id":"box","cwd":"/code","launcher":"claude","model":"gw/fails"}`); w.Code == http.StatusOK {
		t.Fatalf("the failing start: %d %s", w.Code, w.Body)
	}
	if w := modelsRequest(h, "POST", "/api/v1/sessions", `{"machine_id":"box","cwd":"/code","launcher":"claude","model":"a b"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a model that is no id: %d %s", w.Code, w.Body)
	}
	if w := modelsRequest(h, "POST", "/api/v1/sessions", `{"machine_id":"box","cwd":"/code","launcher":"claude","model":"opus"}`); w.Code != http.StatusOK {
		t.Fatalf("a listed model: %d %s", w.Code, w.Body)
	}
	if w := modelsRequest(h, "POST", "/api/v1/sessions", `{"machine_id":"box","cwd":"/code","launcher":"claude","model":"gw/model-x"}`); w.Code != http.StatusOK {
		t.Fatalf("a typed model: %d %s", w.Code, w.Body)
	}
	eventually(t, 5*time.Second, "the typed model in the list", func() bool { return slices.Equal(custom(), []string{"gw/model-x"}) })

	// A running session switched to a typed model (…/modes) counts too, newest first.
	if w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", `{"model":"gw/switched"}`); w.Code != http.StatusOK {
		t.Fatalf("modes: %d %s", w.Code, w.Body)
	}
	eventually(t, 5*time.Second, "the switched model in the list", func() bool {
		return slices.Equal(custom(), []string{"gw/switched", "gw/model-x"})
	})

	if w := modelsRequest(h, "DELETE", "/api/v1/machines/box/launchers/claude/models/custom?model=gw/model-x", ""); w.Code != http.StatusOK {
		t.Fatalf("forget: %d %s", w.Code, w.Body)
	}
	if got := custom(); !slices.Equal(got, []string{"gw/switched"}) {
		t.Fatalf("after forget: %v", got)
	}
}

// A typed model that is no id is refused in the app's language (its Accept-Language goes to the manager with the
// start), not the hub's.
func TestTypedModelRefusedInTheAppsLanguage(t *testing.T) {
	h, _ := modelTestHub(t)
	modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
		return map[string]any{}, nil
	})
	connectAssistant(t, h)
	for lang, want := range map[string]string{"en": "Model ID", "ja": "モデル ID", "zh-Hans": "模型 ID"} {
		r := httptest.NewRequest("POST", "https://example.com/api/v1/sessions",
			strings.NewReader(`{"machine_id":"box","cwd":"/code","launcher":"claude","model":"a b"}`))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		r.Header.Set("Accept-Language", lang)
		w := httptest.NewRecorder()
		h.routes().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", lang, w.Code, w.Body)
		}
	}
}

// An app's New Agent starts in the background (async: the node answers before it runs, node/starting.go): the
// launcher's check and the agent coming up come after the answer, so a typed id is told to the manager only once the
// session is listed as started, not while it starts nor when its start failed.
func TestTypedModelOfAnAsyncStartRememberedOnceItStarted(t *testing.T) {
	h, _ := modelTestHub(t)
	h.listedWait = 50 * time.Millisecond
	ids := map[string]string{"gw/async-fails": "a1", "gw/async-ok": "a2"}
	modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
		switch method {
		case "launchers.models":
			return proto.LauncherModels{Agent: "claude", Models: []proto.AgentModel{{ID: "opus"}}}, nil
		case "session.start":
			model, _ := p["model"].(string)
			return map[string]any{"session": proto.Session{SessionID: ids[model], Starting: true}}, nil
		}
		return map[string]any{}, nil
	})
	connectAssistant(t, h)
	m := h.localMachine("box")
	remembered := func() []string {
		var list []struct{ Machine, Launcher, Model string }
		_, _ = h.store.Setting(context.Background(), "launchers.custom_models", &list)
		var out []string
		for _, c := range list {
			out = append(out, c.Model)
		}
		return out
	}
	start := func(model string) {
		t.Helper()
		body := `{"machine_id":"box","cwd":"/code","launcher":"claude","async":true,"model":"` + model + `"}`
		if w := modelsRequest(h, "POST", "/api/v1/sessions", body); w.Code != http.StatusOK {
			t.Fatalf("start %s: %d %s", model, w.Code, w.Body)
		}
	}
	starting := proto.Session{SessionID: "a1", Status: proto.StatusIdle, Starting: true}
	failed := proto.Session{SessionID: "a1", Status: proto.StatusOffline, StartError: "the launcher sets its own --model"}

	start("gw/async-fails")
	m.setSessionList([]proto.Session{starting})
	time.Sleep(4 * startSettlePoll)
	if got := remembered(); len(got) != 0 {
		t.Fatalf("remembered while it starts: %v", got)
	}
	m.setSessionList([]proto.Session{failed})
	time.Sleep(4 * startSettlePoll)

	start("gw/async-ok")
	m.setSessionList([]proto.Session{failed, {SessionID: "a2", Status: proto.StatusIdle, Starting: true}})
	time.Sleep(4 * startSettlePoll)
	if got := remembered(); len(got) != 0 {
		t.Fatalf("remembered before it started: %v", got)
	}
	m.setSessionList([]proto.Session{failed, {SessionID: "a2", Status: proto.StatusIdle}})
	eventually(t, 5*time.Second, "the started one's model remembered", func() bool { return slices.Equal(remembered(), []string{"gw/async-ok"}) })
	time.Sleep(4 * startSettlePoll)
	if got := remembered(); !slices.Equal(got, []string{"gw/async-ok"}) {
		t.Fatalf("the failed one's came too: %v", got)
	}
}

// A message to one of the assistant's conversations with a model typed in its chip goes on that model (the list does
// not have it), and once it went the manager remembers it for that launcher on that machine.
func TestChipPickRememberedAfterTheMessageWent(t *testing.T) {
	h, _ := modelTestHub(t)
	n := newAssistantNode(t, h)
	ctx := context.Background()
	if err := h.store.PutSetting(ctx, "assistant", assistant.Settings{MachineID: "server", Main: "main"}); err != nil {
		t.Fatal(err)
	}
	n.m.mu.Lock()
	n.m.sessions = []proto.Session{{SessionID: "main", Launcher: "conv", Assistant: true, Status: proto.StatusIdle}}
	n.m.mu.Unlock()
	connectAssistant(t, h)
	eventually(t, 5*time.Second, "the main conversation", func() bool { return h.isMainOn("server", "main") })
	if w := modelsRequest(h, "POST", "/api/v1/sessions/server/main/messages", `{"text":"hi","launcher":"side-l","model":"gw/typed"}`); w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	if p := n.lastSend(t); p["model"] != "gw/typed" || p["launcher"] != "side-l" {
		t.Fatalf("the node got %v", p)
	}
	eventually(t, 5*time.Second, "the chip's model remembered", func() bool {
		var list []struct{ Machine, Launcher, Model string }
		_, _ = h.store.Setting(ctx, "launchers.custom_models", &list)
		return len(list) == 1 && list[0].Machine == "server" && list[0].Launcher == "side-l" && list[0].Model == "gw/typed"
	})
	if w := modelsRequest(h, "POST", "/api/v1/sessions/server/main/messages", `{"text":"hi","model":"$(id)"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a model that is no id: %d %s", w.Code, w.Body)
	}
}
