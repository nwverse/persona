package assistant

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// botModels uses the main conversation's persistent model pick, shared with the apps.
func (s *Service) botModels(ctx context.Context, f FeishuFeatures, messageID, cmd, model, root, parent string) {
	ctx = withLauncherPurpose(ctx, "assistant")
	l := s.botLang(ctx)
	note := func(key string, args ...i18n.Args) {
		s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards."+key, args...))
	}
	if !f.Main {
		note("main_off")
		return
	}
	cfg := s.settings(ctx)
	t := s.botTargetFor(root, parent)
	if t.Kind != targetMain && !(t.Kind == targetPersona && cfg.Main != "" && t.SessionID == cfg.Main && (t.MachineID == "" || t.MachineID == cfg.mainRouteMachine())) {
		note("model_main_only")
		return
	}
	if cfg.Main == "" {
		note("model_no_main")
		return
	}
	if cmd == "/modelchange" && model == "" {
		note("model_change_usage")
		return
	}
	machine := cfg.mainRouteMachine()
	params := map[string]any{}
	if err := s.botModelParams(ctx, machine, cfg.Main, params, l); err != nil {
		note("model_error", i18n.Args{"error": err.Error()})
		return
	}
	launcher, _ := params["launcher"].(string)
	current, _ := params["model"].(string)
	effort, _ := params["effort"].(string)
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := s.askLauncherModels(listCtx, machine, launcher, false)
	if err != nil {
		note("model_error", i18n.Args{"error": err.Error()})
		return
	}
	res = s.withCustomModels(ctx, machine, launcher, res)
	if cmd == "/modelchange" {
		if !listsModel(res, model) {
			note("model_change_unknown", i18n.Args{"model": model})
			return
		}
		_, err = s.pickModel(ctx, cfg.Main, machine, proto.ModelPick{Launcher: launcher, Model: model, Effort: effort}, l)
		if err != nil {
			note("model_error", i18n.Args{"error": err.Error()})
			return
		}
		note("model_change_done", i18n.Args{"model": model})
		return
	}
	var b strings.Builder
	b.WriteString(i18n.T(l, "assistant-feishu_cards.model_list_title"))
	fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_launcher", i18n.Args{"launcher": launcher}))
	if current != "" {
		fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_current", i18n.Args{"model": current}))
	}
	if len(res.Models) == 0 {
		fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_empty"))
	} else {
		for _, m := range res.Models {
			if m.ID == "" {
				continue
			}
			line := "\n• " + m.ID
			if b.Len()+len(line) > 16000 {
				fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_truncated"))
				break
			}
			b.WriteString(line)
		}
	}
	fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_hint"))
	s.botNote(ctx, messageID, b.String())
}

// botModelParams resolves the command's connection without requiring its old external
// model to remain runnable: listing and an explicit replacement are how the owner recovers
// when that model is removed. The connection and conversation still have to be usable.
func (s *Service) botModelParams(ctx context.Context, machine, sid string, params map[string]any, lang i18n.Lang) error {
	pick, ok, err := s.readModelPick(ctx, machine, sid)
	if err != nil {
		return err
	}
	if !ok || !proto.IsExternalLauncher(pick.Launcher) {
		return s.sendModel(ctx, machine, sid, params, lang)
	}
	if !s.externalMainScope(ctx, machine, sid) && !s.externalMainStartScope(ctx, machine, sid, pick) {
		return i18n.Err("external-adapter.scope_required")
	}
	if err := s.modelPickRefusal(ctx, machine, pick.Launcher, lang, true); err != nil {
		return err
	}
	if err := s.otherKindPick(ctx, machine, sid, pick.Launcher); err != nil {
		return err
	}
	params["launcher"], params["model"], params["effort"] = pick.Launcher, pick.Model, pick.Effort
	return nil
}
