package assistant

import (
	"context"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/channel"
)

type channelAdmissionKey struct {
	channel, thread string
	generation      uint64
}

type channelAdmissionSlot struct {
	key  channelAdmissionKey
	prev chan struct{}
}

type channelAdmission struct {
	slots [2]channelAdmissionSlot
	count int
	done  chan struct{}
}

// admitChannelMessage claims the message, captures its binding generation and reserves its position atomically.
// No node or channel RPC runs under the book lock. Reserve before persisting the claim so a later command cannot
// overtake a durable receipt. A failed write rolls both changes back for redelivery.
func (s *Service) admitChannelMessage(ctx context.Context, id string, m channel.Message) (*channelAdmission, error) {
	v2 := s.integrationsV2On(ctx)
	var v channel.View
	if v2 {
		var err error
		v, _, err = s.pluginChannelNow(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	s.loadChannelStateLocked(ctx)
	if !c.stLoaded {
		return nil, errChannelsUnread
	}
	seen, hadSeen := c.st.Seen[id]
	if m.ID == "" || slices.Contains(seen, m.ID) {
		return nil, nil
	}
	thread := strings.TrimSpace(m.Thread)
	if v2 {
		s.loadChannelsLocked(ctx)
		if !c.loaded {
			return nil, errChannelsUnread
		}
		thread = channelRouteKey(s.channelPrefLocked(id).ThreadMode.Effective(v.Capabilities.Threads), m)
	}
	keys := channelThreadAliases(thread, m)
	a := &channelAdmission{done: make(chan struct{})}
	// An empty thread is this channel's ordinary route. Its replies fold into the latest
	// receipt too, even when the earlier message's send RPC takes longer.
	if c.receiving == nil {
		c.receiving = map[channelAdmissionKey]chan struct{}{}
	}
	// A dual-key topic keeps its place in either routing mode and behind messages that supplied just one key.
	for i, thread := range [...]string{keys.primary, keys.alternate} {
		if i == 1 && thread == "" {
			break
		}
		key := channelAdmissionKey{channel: id, thread: thread, generation: c.generation[id]}
		a.slots[i] = channelAdmissionSlot{key: key, prev: c.receiving[key]}
		a.count++
		c.receiving[key] = a.done
	}
	next := append(seen, m.ID)
	if len(next) > channelSeenKeep {
		next = next[len(next)-channelSeenKeep:]
	}
	c.st.Seen[id] = next
	if err := s.saveChannelStateLocked(); err != nil {
		if hadSeen {
			c.st.Seen[id] = seen
		} else {
			delete(c.st.Seen, id)
		}
		for _, slot := range a.slots[:a.count] {
			if slot.prev == nil {
				delete(c.receiving, slot.key)
			} else {
				c.receiving[slot.key] = slot.prev
			}
		}
		return nil, err
	}
	return a, nil
}

// run keeps one binding generation's ordinary route or thread ordered. A new binding never waits for an old generation's RPC.
// The routing code still gates destinations on the channel's threads capability.
func (a *channelAdmission) run(s *Service, m channel.Message, action string) {
	defer func() {
		c := &s.chans
		c.mu.Lock()
		for _, slot := range a.slots[:a.count] {
			if c.receiving[slot.key] == a.done {
				delete(c.receiving, slot.key)
			}
		}
		close(a.done)
		c.mu.Unlock()
	}()
	for _, slot := range a.slots[:a.count] {
		if slot.prev != nil {
			<-slot.prev
		}
	}
	key := a.slots[0].key
	s.channelReceiveInGeneration(key.channel, m, action, key.generation)
}

func (s *Service) channelGeneration(id string) uint64 {
	s.chans.mu.Lock()
	defer s.chans.mu.Unlock()
	return s.chans.generation[id]
}

func (s *Service) channelGenerationCurrent(id string, generation uint64) bool {
	return s.channelGeneration(id) == generation
}
