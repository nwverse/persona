package assistant

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func feishuModelCommand(t *testing.T, s *Service, f *fakeHub) func(string) string {
	t.Helper()
	var note string
	f.answerApp(AppFeishu, FeishuText, func(raw json.RawMessage) (any, error) {
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		note = p.Text
		return map[string]any{}, nil
	})
	s.setBotCurrent(botTarget{Kind: targetMain})
	return func(text string) string {
		t.Helper()
		note = ""
		if handled, rest := s.botCommand(t.Context(), FeishuFeatures{Main: true}, "synthetic-message", text, "", ""); !handled || rest != "" {
			t.Fatalf("%s: handled=%v rest=%q", text, handled, rest)
		}
		return note
	}
}

func TestFeishuModelsRecoverRemovedExternalModel(t *testing.T) {
	s, f, l := externalPickFixture(t)
	old := pickExternal(t, s, l, "vendor/model-one")
	l.External.Models = l.External.Models[1:]
	setExternalAdvertisement(f, l)
	command := feishuModelCommand(t, s, f)
	err := s.sendModel(t.Context(), "server", "main", map[string]any{}, i18n.En)
	requireExternalError(t, err, "model_not_found")
	before, puts := profileStoreSnapshot(s.store().(*memStore))
	note := command("/modellist")
	if !strings.Contains(note, "• vendor/model-two") {
		t.Errorf("list omitted the usable replacement: %q", note)
	}
	requireProfileStoreUnchanged(t, s.store().(*memStore), before, puts)
	if kept, _ := s.modelPickOf(t.Context(), "server", "main"); kept != old {
		t.Fatalf("listing changed the selection: %+v", kept)
	}
	for _, model := range []string{"vendor/model-one", "absent"} {
		if note := command("/modelchange " + model); note != i18n.T(s.botLang(t.Context()), "assistant-feishu_cards.model_change_unknown", i18n.Args{"model": model}) {
			t.Errorf("invalid replacement: %q", note)
		}
		requireProfileStoreUnchanged(t, s.store().(*memStore), before, puts)
	}
	note = command("/modelchange vendor/model-two")
	want := i18n.T(s.botLang(t.Context()), "assistant-feishu_cards.model_change_done", i18n.Args{"model": "vendor/model-two"})
	if note != want {
		t.Errorf("replacement confirmation: %q, want %q", note, want)
	}
	kept, ok := s.modelPickOf(t.Context(), "server", "main")
	if !ok || kept.Model != "vendor/model-two" || kept.Launcher != old.Launcher {
		t.Fatalf("replacement was not kept on the same connection: %+v", kept)
	}
	params, err := send(t, s, "main", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	externalExecutionOf(t, params, l)
	if params["model"] != "vendor/model-two" || params["launcher"] != l.Name {
		t.Fatalf("next send disagreed with confirmation: %v", params)
	}
}

func TestFeishuModelsExternalConnectionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Service, *fakeHub, *proto.Launcher)
	}{
		{"missing credentials", func(_ *Service, _ *fakeHub, l *proto.Launcher) { l.External.CredentialState = "missing" }},
		{"unavailable target", func(_ *Service, _ *fakeHub, l *proto.Launcher) {
			l.External.UnavailableCode = "adapter_config_conflict"
		}},
		{"missing target", func(_ *Service, _ *fakeHub, l *proto.Launcher) { l.External.ID = "another-target" }},
		{"unsupported context", func(_ *Service, _ *fakeHub, l *proto.Launcher) { l.External.Capabilities.Contexts = []string{"coding"} }},
		{"unsupported engine", func(_ *Service, _ *fakeHub, l *proto.Launcher) { l.External.Engine = "another-engine" }},
		{"outside main scope", func(s *Service, _ *fakeHub, _ *proto.Launcher) {
			d := newFleetDevs()
			d.add("server", "Computer")
			d.setSessions("server", []proto.Session{{SessionID: "main", Assistant: true, PaneID: "coding-pane"}})
			s.devs = d
		}},
		{"incompatible conversation", func(_ *Service, f *fakeHub, _ *proto.Launcher) {
			f.setSessions("server", proto.Session{SessionID: "main", Assistant: true, Launcher: "cx", Agent: "codex"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, l := externalPickFixture(t)
			old := pickExternal(t, s, l, "vendor/model-one")
			tc.change(s, f, &l)
			setExternalAdvertisement(f, l)
			command := feishuModelCommand(t, s, f)
			before, puts := profileStoreSnapshot(s.store().(*memStore))
			err := s.sendModel(t.Context(), "server", "main", map[string]any{}, s.botLang(t.Context()))
			if err == nil {
				t.Fatal("fixture did not refuse the connection")
			}
			want := i18n.T(s.botLang(t.Context()), "assistant-feishu_cards.model_error", i18n.Args{"error": err.Error()})
			for _, text := range []string{"/modellist", "/modelchange vendor/model-two"} {
				if note := command(text); note != want {
					t.Errorf("%s: refusal %q, want %q", text, note, want)
				}
				requireProfileStoreUnchanged(t, s.store().(*memStore), before, puts)
				if kept, _ := s.modelPickOf(t.Context(), "server", "main"); kept != old {
					t.Fatalf("connection failure overwrote selection: %+v", kept)
				}
			}
		})
	}
}

func TestFeishuModelsExistingExternalSelection(t *testing.T) {
	s, f, l := externalPickFixture(t)
	old := pickExternal(t, s, l, "vendor/model-one")
	old.Effort = "high"
	if err := s.setModelPick(t.Context(), "main", &old); err != nil {
		t.Fatal(err)
	}
	command := feishuModelCommand(t, s, f)
	before, puts := profileStoreSnapshot(s.store().(*memStore))
	note := command("/modellist")
	for _, part := range []string{"• vendor/model-one", "• vendor/model-two", i18n.T(s.botLang(t.Context()), "assistant-feishu_cards.model_list_current", i18n.Args{"model": old.Model})} {
		if !strings.Contains(note, part) {
			t.Fatalf("list omitted %q: %q", part, note)
		}
	}
	requireProfileStoreUnchanged(t, s.store().(*memStore), before, puts)
	command("/modelchange vendor/model-two")
	kept, _ := s.modelPickOf(t.Context(), "server", "main")
	if kept.Launcher != old.Launcher || kept.Model != "vendor/model-two" || kept.Effort != "high" {
		t.Fatalf("switch lost connection or effort: %+v", kept)
	}
}

func TestFeishuModelsReplacementSaveFailure(t *testing.T) {
	s, f, l := externalPickFixture(t)
	old := pickExternal(t, s, l, "vendor/model-one")
	l.External.Models = l.External.Models[1:]
	setExternalAdvertisement(f, l)
	command := feishuModelCommand(t, s, f)
	st := s.store().(*memStore)
	st.setFailPut(errors.New("example settings unavailable"))
	before, puts := profileStoreSnapshot(st)
	note := command("/modelchange vendor/model-two")
	want := i18n.T(s.botLang(t.Context()), "assistant-feishu_cards.model_error", i18n.Args{"error": "example settings unavailable"})
	if note != want {
		t.Fatalf("failed save was not reported: %q, want %q", note, want)
	}
	requireProfileStoreUnchanged(t, st, before, puts)
	if kept, _ := s.modelPickOf(t.Context(), "server", "main"); kept != old {
		t.Fatalf("failed save replaced old selection: %+v", kept)
	}
}

func TestFeishuModelsUnreadableSelection(t *testing.T) {
	s, f, l := externalPickFixture(t)
	pickExternal(t, s, l, "vendor/model-one")
	command := feishuModelCommand(t, s, f)
	st := s.store().(*memStore)
	st.failKey(modelPicksKey, errors.New("example selection unavailable"))
	s.modelPicks.mu.Lock()
	s.modelPicks.loaded = false
	s.modelPicks.mu.Unlock()
	before, puts := profileStoreSnapshot(st)
	for _, text := range []string{"/modellist", "/modelchange vendor/model-two"} {
		want := i18n.T(s.botLang(t.Context()), "assistant-feishu_cards.model_error", i18n.Args{"error": i18n.Err("assistant-model_pick.err_unreadable").Error()})
		if note := command(text); note != want {
			t.Errorf("unreadable selection fell back: %q, want %q", note, want)
		}
		requireProfileStoreUnchanged(t, st, before, puts)
	}
}
