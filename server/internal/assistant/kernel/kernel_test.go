package kernel

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/modapi"
)

type fake struct {
	info modapi.Info
	reg  func(h modapi.Host) error
}

func (f fake) Info() modapi.Info { return f.info }
func (f fake) Register(h modapi.Host) error {
	if f.reg == nil {
		return nil
	}
	return f.reg(h)
}

type rules struct{ modapi.WaitRules }

func setting(key string) modapi.Setting {
	return modapi.Setting{Key: key, Value: func(json.RawMessage) json.RawMessage { return nil },
		Check: func(json.RawMessage) (json.RawMessage, error) { return nil, nil }, Describe: func(json.RawMessage) string { return "" }}
}

func info(id string, settings ...string) modapi.Info {
	return modapi.Info{ID: id, API: modapi.API, Origin: modapi.OriginBuiltin, Settings: settings, Schedules: []string{modapi.ScheduleFactsName}}
}

func full(id string) fake {
	return fake{info: info(id, "x."+id), reg: func(h modapi.Host) error {
		if err := h.AddSetting(setting("x." + id)); err != nil {
			return err
		}
		return h.AddScheduleFacts(modapi.ScheduleFacts{Setting: "x." + id, Waits: func(json.RawMessage) modapi.WaitRules { return rules{} }})
	}}
}

func TestLoadsAndServes(t *testing.T) {
	k := New([]modapi.Module{full("a")}, nil, t.Logf)
	if !k.Loaded("a") || len(k.Modules()) != 1 {
		t.Fatalf("not loaded: %+v", k.Modules())
	}
	if _, ok := k.Setting("x.a"); !ok {
		t.Fatal("setting not served")
	}
	if f, ok := k.ScheduleFacts(); !ok || f.Setting != "x.a" {
		t.Fatalf("facts not served: %+v", f)
	}
}

// A module switched off, or one that cannot load, adds nothing: what it added before failing is dropped too.
func TestAbsentModulesAddNothing(t *testing.T) {
	failing := full("b")
	inner := failing.reg
	failing.reg = func(h modapi.Host) error {
		_ = inner(h)
		return errors.New("boom")
	}
	undeclared := fake{info: info("c"), reg: func(h modapi.Host) error { return h.AddSetting(setting("x.other")) }}
	unserved := full("d")
	unserved.info.HubRoutes = []string{"GET /api/v1/d"}
	oldAPI := full("e")
	oldAPI.info.API = modapi.API + 1
	needy := full("f")
	needy.info.Needs = []string{"a"}
	ext := full("g")
	ext.info.Origin = modapi.OriginExtension
	k := New([]modapi.Module{full("a"), failing, undeclared, unserved, oldAPI, needy, ext}, func(id string) bool { return id == "a" }, t.Logf)
	if len(k.Modules()) != 0 {
		t.Fatalf("loaded: %+v", k.Modules())
	}
	if _, ok := k.Setting("x.a"); ok {
		t.Fatal("a switched-off module's setting is served")
	}
	if _, ok := k.Setting("x.b"); ok {
		t.Fatal("a failed module's setting is served")
	}
	if _, ok := k.ScheduleFacts(); ok {
		t.Fatal("schedule facts served with no module loaded")
	}
	for id, want := range map[string]string{"a": "switched off", "b": "register: boom", "c": "not in the manifest", "d": "hub_routes",
		"e": "module API", "f": "needs a", "g": "only built-in"} {
		if got := k.Skipped(id); !strings.Contains(got, want) {
			t.Errorf("skipped %s = %q, want %q", id, got, want)
		}
	}
}

func TestTwoModulesCannotShareASlot(t *testing.T) {
	second := full("b")
	second.info.Settings = []string{"x.a"}
	second.reg = full("a").reg
	k := New([]modapi.Module{full("a"), second}, nil, t.Logf)
	if !k.Loaded("a") || k.Loaded("b") {
		t.Fatalf("loaded %+v", k.Modules())
	}
}

func TestNilKernelHasNothing(t *testing.T) {
	var k *Kernel
	if k.Loaded("a") || k.Modules() != nil || k.Skipped("a") != "" {
		t.Fatal("a nil kernel answered")
	}
	if _, ok := k.Setting("x"); ok {
		t.Fatal("a nil kernel served a setting")
	}
	if _, ok := k.ScheduleFacts(); ok {
		t.Fatal("a nil kernel served facts")
	}
}
