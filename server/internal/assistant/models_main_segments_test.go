package assistant

import (
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"strings"
	"testing"
)

func TestMainOverrideDoesNotRebindAnExplicitLogicalSegment(t *testing.T) {
	s, _, d, _ := logicalPair(t, "codex")
	setSettings(t, s, func(cfg *Settings) {
		cfg.Main = "side-chat"
		cfg.MainMachineID = "server"
		cfg.Leaders = []string{"side-chat"}
	})
	shared := ModelBinding{Provider: "launcher.claude", Model: "shared-default", Effort: "low"}
	setModel(t, s, "assistant", shared)
	setModel(t, s, purposeMain, ModelBinding{Provider: "launcher.claude", Model: "main-default", Effort: "high"})
	c := activatePair(t, d)
	params := map[string]any{}
	if err := s.sendModel(t.Context(), "server", "side-chat", params, s.lang(t.Context())); err != nil {
		t.Fatal(err)
	}
	if params["launcher"] != "codex" || params["model"] != "target-model" || params["effort"] != "medium" {
		t.Fatalf("main setting replaced explicit segment choice: %v", params)
	}
	after, err := d.LogicalConversation(t.Context(), "server", "side-chat")
	if err != nil || after.Epoch != c.Epoch || after.SessionID != c.SessionID {
		t.Fatalf("main settings changed logical identity: %+v %v", after, err)
	}
	if cfg := s.settings(t.Context()); cfg.Main != "side-chat" || len(cfg.Leaders) != 1 || cfg.Leaders[0] != "side-chat" {
		t.Fatalf("main settings changed role: %+v", cfg)
	}
	if got, err := s.resolveModel(t.Context(), "assistant"); err != nil || got != shared {
		t.Fatalf("main settings changed side defaults: %+v %v", got, err)
	}
}

func TestMainDefaultWaitsForExplicitSwitchBeforeCrossingExecutionPaths(t *testing.T) {
	for _, tc := range []struct {
		name, launcher, model string
	}{
		{"another engine", "cx", "gpt-5"},
		{"same engine in another runtime", "hosted-claude", "opus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := pickFixture(t)
			f.setSessions("server", proto.Session{SessionID: "main", Agent: "claude", Assistant: true, Launcher: "gw"})
			f.mu.Lock()
			f.launcherList = append(f.launcherList, proto.Launcher{Name: "hosted-claude", Source: "runtime-other", Agent: "claude", Assistant: true})
			f.mu.Unlock()
			f.answerNode("assistant.segment.check", func(string, json.RawMessage) (any, error) {
				return segmentTarget{Registered: true, Prepare: true, Engine: "claude|runtime:runtime-other"}, nil
			})
			setModel(t, s, purposeMain, ModelBinding{Provider: "launcher." + tc.launcher, Model: tc.model})
			p := map[string]any{}
			err := s.sendModel(t.Context(), "server", "main", p, s.lang(t.Context()))
			var refusal *rpc.Error
			if !errors.As(err, &refusal) || refusal.Code == assistantwire.CodeLauncherOtherKind || !strings.Contains(string(refusal.Data), `"delivered":"none"`) {
				t.Fatalf("default implicitly changed execution: %v %v", p, err)
			}
			if _, kept := s.modelPickOf(t.Context(), "server", "main"); kept || s.settings(t.Context()).Main != "main" {
				t.Fatal("a default save changed conversation ownership or its explicit choice")
			}
		})
	}
}
