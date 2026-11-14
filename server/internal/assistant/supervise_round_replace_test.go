package assistant

import (
	"context"
	"testing"
)

// When the background supervisor is replaced (#7315, supervise_round.go backgroundOutgrown).

// drainingRound has the relay book say a queued message is going into conversation id (drainRelays marks it before
// deliverRelay sends, while the session list may still say Idle), or no longer.
func drainingRound(s *Service, id string, going bool) {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	s.relayLocked(context.Background())
	if going {
		b.draining[id] = true
	} else {
		delete(b.draining, id)
	}
}

// A background supervisor that is to be replaced is not while a message is going into it, though listed Idle: the
// round going in would otherwise run in an archived conversation that is no longer the background supervisor, and
// its task_review, task_scope and agent_send would be refused. Once that is over, it is replaced.
func TestBackgroundNotReplacedWhileAMessageGoesIn(t *testing.T) {
	s, f, _, _ := newRoundService(t)
	ctx := context.Background()
	bg := withBackgroundBG1(t, s, f)
	s.superviseRoundAnswered(bg, superviseRoundReply{Context: "kept", Why: "no_anchor"})

	drainingRound(s, bg, true)
	if id, gone := s.ensureBackground(ctx); id != bg || gone != "" {
		t.Fatalf("replaced while a message goes in: %q %q", id, gone)
	}
	if s.isArchived(bg) || len(conversationStarts(f)) != 0 || s.settings(ctx).BackgroundSupervisor != bg {
		t.Fatalf("put away while a message goes in: archived %v, %d starts", s.isArchived(bg), len(conversationStarts(f)))
	}
	if why := outgrownOf(s, bg); why != "no_anchor" {
		t.Fatalf("forgot why it is to be replaced: %q", why)
	}

	drainingRound(s, bg, false)
	if id, gone := s.ensureBackground(ctx); id == bg || id == "" || gone != bg || !s.isArchived(bg) {
		t.Fatalf("not replaced once the message went in: %q %q, archived %v", id, gone, s.isArchived(bg))
	}
}

// The size limit counts only for a round that restarted the process (fresh, or start_failed resumed as before):
// one that keeps its context for good (unsupported: an older Claude Code, another runtime) or was busy is neither
// restarted per round nor spared idle compaction, so a large transcript is no reason to throw its context away.
func TestBackgroundOversizedOnlyWhereRoundsRestart(t *testing.T) {
	s, f, _, _ := newRoundService(t)
	ctx := context.Background()
	bg := withBackgroundBG1(t, s, f)
	big := backgroundTranscriptLimit + 1
	for _, res := range []superviseRoundReply{
		{Context: "kept", Why: "unsupported", TranscriptBytes: big},
		{Context: "kept", Why: "busy", TranscriptBytes: big},
	} {
		s.superviseRoundAnswered(bg, res)
		if why := outgrownOf(s, bg); why != "" {
			t.Fatalf("%+v: to be replaced (%s)", res, why)
		}
	}
	if id, gone := s.ensureBackground(ctx); id != bg || gone != "" || len(conversationStarts(f)) != 0 {
		t.Fatalf("replaced: %q %q", id, gone)
	}
	s.superviseRoundAnswered(bg, superviseRoundReply{Context: "kept", Why: "start_failed", TranscriptBytes: big})
	if why := outgrownOf(s, bg); why != "oversized" {
		t.Fatalf("a round started again as before, over the limit: %q", why)
	}
}
