package assistant

import (
	"strings"

	"github.com/nwverse/persona/server/internal/channel"
)

// queueChannelReceive reserves the message's place before its receipt returns. A thread's commands, route lookup,
// start/send and binding finish before its next message begins; other threads and unthreaded messages stay independent.
// The routing code still decides whether the channel supports threads. An older channel that sends an unused thread
// key keeps its existing destination.
func (s *Service) queueChannelReceive(id string, m channel.Message, action string) {
	thread := strings.TrimSpace(m.Thread)
	if thread == "" {
		go s.channelReceive(id, m, action)
		return
	}
	key := [2]string{id, thread}
	c := &s.chans
	c.mu.Lock()
	if c.receiving == nil {
		c.receiving = map[[2]string]chan struct{}{}
	}
	generation := c.generation[id]
	prev := c.receiving[key]
	done := make(chan struct{})
	c.receiving[key] = done
	c.mu.Unlock()
	go func() {
		if prev != nil {
			<-prev
		}
		defer func() {
			c.mu.Lock()
			if c.receiving[key] == done {
				delete(c.receiving, key)
			}
			close(done)
			c.mu.Unlock()
		}()
		c.mu.Lock()
		current := c.generation[id] == generation
		c.mu.Unlock()
		if current {
			s.channelReceive(id, m, action)
		}
	}()
}
