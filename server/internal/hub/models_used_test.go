package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
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
