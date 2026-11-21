package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// The bot's menu (docs/extensions/bots.md "The menu"): entries set up in the developer console, each with an event key
// that names one of the bot's commands. A press is answered in the owner's private chat as if the owner had sent the
// command there; the hub passed on only the owner's presses.

// botMenuCommands are the event keys the menu may use and the commands they run.
var botMenuCommands = map[string]string{
	"home": "/home", "today": "/today", "inbox": "/inbox", "tasks": "/tasks", "agents": "/agents", "start": "/start",
	"list": "/list", "new": "/new", "main": "/main", "status": "/status", "help": "/help",
}

// botMenuWorkbench are the keys only the workbench answers.
var botMenuWorkbench = map[string]bool{"home": true, "today": true, "inbox": true}

// FeishuMenu answers a press on the bot's menu. It is taken at once and answered in the background: nobody waits for
// a menu's answer, and the hub would send a press again that took longer than its call.
func (s *Service) FeishuMenu(_ context.Context, p FeishuMenu) error {
	cmd, ok := botMenuCommands[p.Key]
	if !ok {
		s.logf("bots: a menu entry with an unknown key %q", p.Key)
		return nil
	}
	go func() {
		ctx, cancel := context.WithTimeout(feishuSendAs(context.Background(), "menu", "", ""), time.Minute)
		defer cancel()
		f, ready := s.feishuFeatures(ctx)
		if !ready {
			return
		}
		if botMenuWorkbench[p.Key] && !f.Workbench {
			s.botSay(ctx, i18n.T(s.botLang(ctx), "assistant-feishu_cards.menu_needs_workbench"))
			return
		}
		if handled, _ := s.botCommand(ctx, f, "", cmd, "", ""); !handled {
			s.logf("bots: the menu's %s was not answered", cmd)
		}
	}()
	return nil
}
