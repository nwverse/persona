package node

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A session of Claude's desktop app that nothing runs goes back to the app, not into a terminal. The app opens a session
// at its own link, claude://claude.ai/epitaxy/<the session's local id> (the index's sessionId, claudedesktop.go). Opened
// in the background, the app starts that session's claude, with its peer inbox, without coming to the front or changing
// the session its window shows: about 3 seconds on a Mac, measured 2026-11-11. The message then goes to the inbox as to
// any session the app runs (claudebg.go sendOutside). The session stays the app's: its sidebar and window show the turn,
// and no second claude writes the transcript. Before, the node resumed it with `claude --resume` in a terminal tab, so the
// session ran outside the app and was refused on a machine without a terminal engine.
//
// Only the index's sessions (macOS: the index is read there alone) and only messages: a slash command, which the inbox
// does not run, goes the old way.

// desktopOpenWait bounds the wait for the app to start the session's claude and its inbox.
var desktopOpenWait = 30 * time.Second

// openDesktopLink opens link with its handler, in the background (`open -g`; a variable: tests record the link).
var openDesktopLink = func(ctx context.Context, link string) error {
	return exec.CommandContext(ctx, "open", "-g", link).Run()
}

const (
	desktopOpenFailed     = "desktop_open_failed"
	desktopOpenFailedMsg  = "没能让电脑上的 Claude 桌面版打开这个会话：在电脑上打开它再发。这次没有发送。"
	desktopOpenTimeoutMsg = "Claude 桌面版 30 秒内没有把这个会话打开（它可能没在运行、还在启动，或电脑在锁屏）：在电脑上打开它再发。这次没有发送。"
)

// sendThroughDesktopApp sends text to session id through the desktop app when the app's sidebar lists it and no claude
// runs it: the app opens it, then the message goes to its inbox. handled false for any other session or a command.
func (n *Node) sendThroughDesktopApp(ctx context.Context, id, text string, images []proto.Image) (handled bool, err error) {
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		return false, nil
	}
	local := readClaudeDesktopIndex(n.home).about[id].local
	if local == "" {
		return false, nil
	}
	if err := openDesktopLink(ctx, "claude://claude.ai/epitaxy/"+local); err != nil {
		log.Printf("send %s: opening it in the desktop app: %v", id, err)
		return true, refusal(desktopOpenFailed, desktopOpenFailedMsg, nil)
	}
	deadline := time.Now().Add(desktopOpenWait)
	for !n.desktopClaudeReady(id) {
		if time.Now().After(deadline) {
			log.Printf("send %s: the desktop app did not start it within %s", id, desktopOpenWait)
			return true, refusal(desktopOpenFailed, desktopOpenTimeoutMsg, nil)
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	log.Printf("send %s: opened in the desktop app", id)
	if handled, err := n.sendOutside(ctx, id, text, images); handled {
		return true, err
	}
	// Its claude has no inbox (an older Claude Code): it is open in the app, where the message can be typed.
	return true, refusal(peerUnsupported, peerNoInboxMsg, nil)
}

// desktopClaudeReady: a claude outside Persona's panes runs session id and its inbox is listening, or it has none (then
// nothing is worth waiting for).
func (n *Node) desktopClaudeReady(id string) bool {
	c, ok := n.outsideClaude(id)
	if !ok {
		return false
	}
	p, has := peerOf(c)
	if !has {
		return true
	}
	_, err := os.Stat(p.socket)
	return err == nil
}
