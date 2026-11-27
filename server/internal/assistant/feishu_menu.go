package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
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

// FeishuMenu answers a press on the bot's menu. It takes its place in the chat's line as a message does
// (FeishuMessage), so a /new pressed and a message sent right after it are answered in that order, and is answered in
// the background once its turn comes: the hub's call does not wait for the command. A press with an event id already
// taken (Feishu delivering it again, the hub's handover made again) is answered once.
func (s *Service) FeishuMenu(ctx context.Context, p FeishuMenu) error {
	cmd, ok := botMenuCommands[p.Key]
	if !ok {
		s.logf("bots: a menu entry with an unknown key %q", p.Key)
		return nil
	}
	turn := s.takeFeishuTurn()
	queued := false
	defer func() {
		if !queued {
			turn.pass()
		}
	}()
	if _, err := s.feishuStatus(ctx); err != nil { // as a message's: the chat's state as it is now, before the claim
		return &rpc.Error{Code: CodeTryAgain, Message: "the bot's status: " + err.Error()}
	}
	if p.EventID != "" {
		first, err := s.claimBotMessage("menu:" + p.EventID)
		if err != nil {
			return &rpc.Error{Code: CodeTryAgain, Message: err.Error()}
		}
		if !first {
			return nil
		}
	}
	queued = true
	go func() {
		defer turn.pass()
		turn.waitTurn()
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
