package assistant

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

// conversation_send with wake: false (#11614). Every turn of a supervising conversation re-reads its whole context, so
// a message that only acknowledges or informs should not start one. Such a message is kept for its receiver
// (relayState.Later) and goes in, as a context block in front of it, with the next message the manager delivers there
// for any other reason: a supervision message, another conversation's message, a scheduled follow-up, a notice. It
// never starts a turn of its own, costs no hop and no place in the relay rate, and is never guessed from its words.
// A turn the owner starts from an app goes to the node without this manager: what is kept waits for the next one the
// manager delivers, and shows above the composer meanwhile (queuedRelays).

const (
	// relayLaterMax bounds what one conversation keeps; past it the oldest goes.
	relayLaterMax = 20
	// relayLaterKeep is how long a kept message waits for a delivery to go with; a conversation that got nothing in
	// that long (deleted, abandoned) lets it go.
	relayLaterKeep = 7 * 24 * time.Hour
)

// keepRelayLater keeps msg for conversation to, answering how many wait there now; false when the relay state could not
// be read (nothing was kept).
func (s *Service) keepRelayLater(to string, msg relayMsg) (int, bool) {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return 0, false
	}
	if b.st.Later == nil {
		b.st.Later = map[string][]relayMsg{}
	}
	msg.QueuedAt = time.Now().UTC()
	q := append(b.st.Later[to], msg)
	if len(q) > relayLaterMax {
		q = slices.Clone(q[len(q)-relayLaterMax:])
	}
	b.st.Later[to] = q
	if err := s.saveRelayLocked(); err != nil {
		b.loaded = false // reload durable state before another send or retry
		return 0, false
	}
	return len(q), true
}

// relayLaterFor renders what is kept for conversation to ("" when nothing), and a done that drops exactly those once
// the message carrying them went in (taken). What came meanwhile stays for the next delivery; a send that failed or is
// in doubt keeps them all, so one is at worst read twice, never lost.
func (s *Service) relayLaterFor(ctx context.Context, to string) (string, func(taken bool)) {
	b := &s.relays
	b.mu.Lock()
	if !s.relayLocked(ctx) {
		b.mu.Unlock()
		return "", func(bool) {}
	}
	s.pruneRelayLaterLocked(time.Now())
	if len(b.st.Later[to]) == 0 {
		b.mu.Unlock()
		return "", func(bool) {}
	}
	kept := slices.Clone(b.st.Later[to])
	b.mu.Unlock()
	loc := s.location(ctx)
	items := make([]prompts.RelayLaterItem, 0, len(kept))
	for _, m := range kept {
		items = append(items, prompts.RelayLaterItem{FromID: m.From, FromTitle: m.FromTitle, At: m.At.In(loc).Format("01-02 15:04"), Text: m.Text})
	}
	text := prompts.RenderRelayLater(prompts.RelayLater{Language: answerLanguage(s.hello(ctx).Locale), Items: items})
	return text, func(taken bool) {
		if !taken {
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		q := b.st.Later[to]
		rest := slices.DeleteFunc(slices.Clone(q), func(m relayMsg) bool { return slices.Contains(kept, m) })
		if len(rest) == len(q) {
			return
		}
		if len(rest) == 0 {
			delete(b.st.Later, to)
		} else {
			b.st.Later[to] = rest
		}
		s.saveRelayLocked()
	}
}

// withRelayLater puts what is kept for the receiver of an assistant.send in front of its text (params p), answering
// what to call with whether the send was taken. A meeting's batch and a command (a text starting with "/") carry
// nothing: neither is a turn that reads messages.
func (s *Service) withRelayLater(ctx context.Context, p map[string]any) func(taken bool) {
	id, _ := p["session_id"].(string)
	text, ok := p["text"].(string)
	if id == "" || !ok || meetingSource(p) || strings.HasPrefix(strings.TrimSpace(text), "/") {
		return func(bool) {}
	}
	later, done := s.relayLaterFor(ctx, id)
	if later == "" {
		return done
	}
	p["text"] = later + "\n" + text
	return done
}

// pruneRelayLaterLocked lets go of kept messages older than relayLaterKeep; b.mu is held.
func (s *Service) pruneRelayLaterLocked(now time.Time) {
	for id, q := range s.relays.st.Later {
		q = slices.DeleteFunc(q, func(m relayMsg) bool { return now.Sub(m.QueuedAt) > relayLaterKeep })
		if len(q) == 0 {
			delete(s.relays.st.Later, id)
		} else {
			s.relays.st.Later[id] = q
		}
	}
}
