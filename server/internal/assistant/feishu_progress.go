package assistant

// The Feishu progress card's content and the folded process panel, which stay in the core while the core still draws
// them for the lark module (larkHost.ProgressCard, ProcessPanel) and for its own cards (answerCards): what a run's
// card shows is read from the channels' state (chanLive), which is the core's.

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// botTargetOf is what a run's conversation is to the chat: replies to the bot's messages about it go there.
func botTargetOf(r *channel.Run) botTarget {
	kind := r.TargetKind
	if kind == "" || kind == targetMain {
		kind = targetPersona
	}
	return botTarget{Kind: kind, MachineID: r.MachineID, SessionID: r.SessionID}
}

// liveView is what the progress card shows besides the steps (a copy of chanLive's notes) and the time now.
type liveView struct {
	open                []openCall
	calls, read, edited int
	subs                []subtask
	subsMore            int
	now                 time.Time
}

// runLiveView is run id's liveView now.
func (s *Service) runLiveView(id string) liveView {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	v := liveView{now: c.clock()}
	if p := c.live[id]; p != nil {
		v.open, v.subs = slices.Clone(p.open), slices.Clone(p.subs)
		v.calls, v.read, v.edited, v.subsMore = p.calls, len(p.read), len(p.edited), p.subsMore
	}
	return v
}

// runWaiting says why a run's message is not found in its conversation yet: queued behind a turn that works
// ("queued"), or not in yet ("preparing"); "" once it is.
func (s *Service) runWaiting(r *channel.Run) string {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	switch p := c.live[r.ID]; {
	case p == nil || p.from != 0:
		return ""
	case r.Seen:
		return "queued"
	}
	return "preparing"
}

