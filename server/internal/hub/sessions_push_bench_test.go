package hub

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// BenchmarkBroadcastSessions is one session of a 150-session machine changing, pushed to two older apps and three
// delta apps (docs/server/server.md "What gets pushed to apps"): each change encodes the whole list again.
func BenchmarkBroadcastSessions(b *testing.B) { benchmarkBroadcastSessions(b, 2) }

// BenchmarkBroadcastSessionsDeltaOnly is the same with five delta apps and no older one: no whole-list message.
func BenchmarkBroadcastSessionsDeltaOnly(b *testing.B) { benchmarkBroadcastSessions(b, 0) }

// benchmarkBroadcastSessions: five apps, the first older of them older apps (whole lists), the rest delta apps.
func benchmarkBroadcastSessions(b *testing.B, older int) {
	h := newTestHub(&testing.T{})
	m := newMachine(h, MachineConfig{ID: "box", Name: "Box", Kind: "local"})
	h.machines, h.byID["box"] = append(h.machines, m), m
	for i := range 150 {
		m.sessions = append(m.sessions, proto.Session{SessionID: fmt.Sprintf("s%03d", i), Status: proto.StatusIdle, Title: "title",
			LastText: strings.Repeat("x", 600), Cwd: "/Users/me/projects/app", UpdatedAt: time.Unix(int64(i), 0)})
	}
	for i := range 5 {
		c := &client{out: make(chan []byte, 4), subs: map[[2]string]bool{}, role: roleFull}
		if i >= older {
			c.delta, c.have = true, map[string]map[string][]byte{}
		}
		h.clients[c] = struct{}{}
	}
	n := 0
	for b.Loop() {
		n++
		m.sessions[n%len(m.sessions)].LastText = fmt.Sprintf("%600d", n)
		h.sessionsChanged(m) // what a new list from the manager ends in (takeFleetSessions)
		for c := range h.clients {
			for len(c.out) > 0 {
				<-c.out
			}
		}
	}
}
