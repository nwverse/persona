package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A main-chat model pick can cross runtimes (#10032). Prepare an empty replacement before committing Main,
// its pick and the bounded handoff together. No model turn is needed just to switch. The old transcript stays visible.
type mainModelResult struct {
	Session  proto.Session `json:"session"`
	Replaced string        `json:"replaced,omitempty"`
	Carried  bool          `json:"carried"`
}

func (s *Service) mainModelConflict(ctx context.Context, key string) error {
	return &rpc.Error{Code: "main_changed", Message: i18n.T(s.requestCtxLang(ctx), key)}
}

// handoffSource requires an observed, quiet source; unknown state is not proof that replacing it is safe.
func (s *Service) handoffSource(ctx context.Context, expect, machine string, quiet bool) (proto.Session, error) {
	if expect == "" || s.settings(ctx).Main != expect {
		return proto.Session{}, s.mainModelConflict(ctx, "assistant-start.main_changed")
	}
	c, ok := s.session(ctx, machine, expect)
	if !ok || !c.Assistant || s.conversationAvailable(ctx, machine, expect) != nil {
		return proto.Session{}, s.mainModelConflict(ctx, "assistant-main_model.source_unavailable")
	}
	if !quiet {
		return c, nil
	}
	if c.Status == "" || c.Status == proto.StatusUnknown {
		return proto.Session{}, s.mainModelConflict(ctx, "assistant-main_model.source_unavailable")
	}
	busy := c.Status == proto.StatusWorking || c.Status == proto.StatusBlocked || c.BackgroundWorking || c.PendingInteractions > 0 ||
		len(c.Queued) > 0 || s.queuedFor(expect) > 0 || (c.Compaction != nil && c.Compaction.State == proto.CompactionRunning)
	for _, ix := range s.pendingInteractions() {
		busy = busy || ix.SessionID == expect
	}
	if busy {
		return proto.Session{}, &rpc.Error{Code: "busy", Message: i18n.T(s.requestCtxLang(ctx), "assistant-main_model.busy")}
	}
	return c, nil
}

func (s *Service) switchMainModel(ctx context.Context, expect string, pick proto.ModelPick) (mainModelResult, error) {
	s.main.mu.Lock()
	defer s.main.mu.Unlock()
	cfg := s.settings(ctx)
	machine := cfg.mainRouteMachine()
	c, err := s.handoffSource(ctx, expect, machine, false)
	if err != nil {
		return mainModelResult{}, err
	}
	pick.Launcher, pick.Model, pick.Effort = strings.TrimSpace(pick.Launcher), strings.TrimSpace(pick.Model), strings.TrimSpace(pick.Effort)
	if pick.Launcher == "" || (pick.Effort != "" && !slices.Contains(pickEfforts, pick.Effort)) {
		return mainModelResult{}, refusePick(http.StatusBadRequest, i18n.Err("assistant-main_model.invalid_pick"))
	}
	if err := checkModelID(pick.Model); err != nil {
		return mainModelResult{}, refusePick(http.StatusBadRequest, err)
	}
	if err := s.modelPickRefusal(ctx, machine, pick.Launcher, s.requestCtxLang(ctx), false); err != nil {
		return mainModelResult{}, err
	}
	if pick.Model != "" {
		if err := s.pickedModelRefusal(ctx, machine, pick.Launcher, pick.Model); err != nil {
			return mainModelResult{}, refusePick(http.StatusBadRequest, err)
		}
	}
	from := c.Agent
	if from == "" {
		from = s.launcherAgent(ctx, machine, c.Launcher)
	}
	to := s.launcherAgent(ctx, machine, pick.Launcher)
	if from == "" || to == "" {
		return mainModelResult{}, s.mainModelConflict(ctx, "assistant-main_model.unknown_runtime")
	}
	if from == to {
		p, err := s.pickModel(ctx, expect, machine, pick, s.requestCtxLang(ctx))
		c.ModelPick = p.wire()
		return mainModelResult{Session: c}, err
	}
	// Require transaction support before starting anything. A partial settings write cannot be rolled back safely.
	atomic, ok := s.store().(interface {
		PutSettings(context.Context, map[string]any) error
	})
	if !ok {
		return mainModelResult{}, i18n.Err("assistant-main_model.atomic_store")
	}
	c, err = s.handoffSource(ctx, expect, machine, true)
	if err != nil {
		return mainModelResult{}, err
	}
	before, hadPick := s.modelPickOf(ctx, machine, expect)
	h := mainHandoff{From: expect, MachineID: machine, Until: time.Now().UTC()}
	if pending := s.loadHandoff(ctx); pending.For == expect && pending.From != "" {
		h = pending // switching again before the first message must not lose the unconsumed context
	} else {
		if err := s.call(ctx, machine, DeviceAssistantHandoff, map[string]string{"session_id": expect}, &h); err != nil {
			return mainModelResult{}, err
		}
		h.From, h.MachineID, h.Until = expect, machine, time.Now().UTC()
	}
	next, err := s.startConversationOn(ctx, StartParams{MachineID: machine, Launcher: pick.Launcher, Model: pick.Model,
		Effort: pick.Effort, PermissionMode: c.PermissionMode, PrepareOnly: true}, true)
	if err != nil {
		return mainModelResult{}, err // uncertain starts are never retried here
	}
	committed := false
	defer func() {
		if !committed && next.SessionID != "" && next.SessionID != expect {
			s.closeMain(context.WithoutCancel(ctx), machine, next)
		}
	}()
	if next.SessionID == "" || next.SessionID == expect || next.Launcher != pick.Launcher {
		return mainModelResult{}, s.mainModelConflict(ctx, "assistant-main_model.target_changed")
	}
	current, err := s.handoffSource(ctx, expect, machine, true)
	if err != nil {
		return mainModelResult{}, err
	}
	if current.Rev != c.Rev || !current.UpdatedAt.Equal(c.UpdatedAt) {
		return mainModelResult{}, s.mainModelConflict(ctx, "assistant-start.main_changed")
	}
	h.For, h.Pick = next.SessionID, nil
	if err := s.commitMainModel(ctx, atomic.PutSettings, cfg, before, hadPick, next, pick, h); err != nil {
		return mainModelResult{}, err
	}
	committed = true
	next.ModelPick, next.Agent = &pick, to
	s.settingsChanged(context.WithoutCancel(ctx))
	if from == "claude" || from == "codex" {
		s.closeMain(context.WithoutCancel(ctx), machine, c) // stop the idle native process; a runtime's close may archive its history
	}
	s.handOverMainTasks(context.WithoutCancel(ctx), expect, next.SessionID)
	return mainModelResult{Session: next, Replaced: expect, Carried: h.Summary != "" || len(h.Recent) > 0}, nil
}

// Lock both settings books across the transaction and publish their caches only after it succeeds.
func (s *Service) commitMainModel(ctx context.Context, put func(context.Context, map[string]any) error, cfg Settings,
	before modelPick, hadPick bool, next proto.Session, pick proto.ModelPick, h mainHandoff) error {
	prefs, picks := &s.prefs, &s.modelPicks
	prefs.change.Lock()
	defer prefs.change.Unlock()
	picks.write.Lock()
	defer picks.write.Unlock()
	prefs.mu.Lock()
	prefs.at = time.Time{}
	prefs.mu.Unlock()
	v, ok := s.savedSettings(ctx)
	if !ok {
		return i18n.Err("assistant-main_model.source_unavailable")
	}
	v = s.effective(ctx, v)
	if v.Main != cfg.Main || v.mainRouteMachine() != cfg.mainRouteMachine() {
		return s.mainModelConflict(ctx, "assistant-start.main_changed")
	}
	picks.mu.Lock()
	if !s.modelPicksLocked(ctx) {
		picks.mu.Unlock()
		return i18n.Err("assistant-model_pick.err_unreadable")
	}
	values := maps.Clone(picks.m)
	picks.mu.Unlock()
	if p, had := values[cfg.Main]; had != hadPick || (had && p != before) {
		return s.mainModelConflict(ctx, "assistant-start.main_changed")
	}
	values[next.SessionID] = modelPick{MachineID: next.MachineID, Launcher: pick.Launcher, Model: pick.Model, Effort: pick.Effort, At: time.Now().UTC()}
	s.evictModelPicks(ctx, values, next.SessionID)
	v.Main, v.MainMachineID = next.SessionID, next.MachineID
	v.Former = slices.DeleteFunc(slices.Clone(v.Former), func(f FormerMain) bool { return f.SessionID == cfg.Main })
	v.Former = append([]FormerMain{{MachineID: cfg.mainRouteMachine(), SessionID: cfg.Main, Until: time.Now().UTC()}}, v.Former...)
	if len(v.Former) > maxFormerMains {
		v.Former = v.Former[:maxFormerMains]
	}
	if err := put(ctx, map[string]any{settingsKey: v, modelPicksKey: values, mainHandoffKey: h}); err != nil {
		return err
	}
	prefs.mu.Lock()
	prefs.cached, prefs.at = &v, time.Now()
	prefs.gen++
	prefs.mu.Unlock()
	picks.mu.Lock()
	picks.m = values
	picks.mu.Unlock()
	return nil
}

func (s *Service) mainModelRoutes() {
	s.handle("POST /api/v1/assistant/main/model", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-model_pick.err_role"))
			return
		}
		var in struct {
			Expect string `json:"expect"`
			proto.ModelPick
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		out, err := s.switchMainModel(ctx, in.Expect, in.ModelPick)
		var pe *pickError
		if errors.As(err, &pe) {
			apierr.Write(w, pe.status, pe.err)
		} else if err != nil {
			apierr.Call(w, err)
		} else {
			apierr.WriteJSON(w, out)
		}
	})
}
