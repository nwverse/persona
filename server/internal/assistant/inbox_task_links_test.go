package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// Exercise the production pass: matching must not depend on triage, the twin, or a model response.
func TestInboxTaskURLRouting(t *testing.T) {
	const pr = "https://github.com/example/project/pull/123"
	now := time.Now()
	for _, c := range []struct {
		name, field, link                                       string
		enabled, noMatch, done, ambiguous, existing, signalsOff bool
	}{
		{name: "PR URL with twin off", field: "pr_url", link: pr + "#issuecomment-456"},
		{name: "title", field: "title", link: pr + "?notification_referrer_id=1#discussion_r456"},
		{name: "notes", field: "notes", link: pr + "/"},
		{name: "issue", field: "issue", link: "https://github.com/example/project/issues/456#issuecomment-789"},
		{name: "context", field: "context", link: pr},
		{name: "preview", field: "preview", link: pr},
		{name: "item title", field: "item title", link: pr},
		{name: "markdown punctuation", field: "notes", link: pr + "#discussion_r456)."},
		{name: "twin on skips judge", field: "pr_url", link: pr, enabled: true},
		{name: "completed", field: "pr_url", link: pr, done: true, noMatch: true},
		{name: "different repository", field: "pr_url", link: "https://github.com/other/project/pull/123", noMatch: true},
		{name: "different host", field: "pr_url", link: "https://git.example.com/example/project/pull/123", noMatch: true},
		{name: "issue and PR differ", field: "pr_url", link: "https://github.com/example/project/issues/123", noMatch: true},
		{name: "number boundary", field: "pr_url", link: pr + "4", noMatch: true},
		{name: "bare number", field: "pr_url", link: "#123", noMatch: true},
		{name: "ambiguous tasks", field: "pr_url", link: pr, ambiguous: true, noMatch: true},
		{name: "forwarding off still links", field: "pr_url", link: pr, signalsOff: true},
		{name: "existing link receives new content", field: "pr_url", link: pr, existing: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			s, f := newQuietService(t)
			s.superviseTimes.gather = time.Hour
			in := newFakeInbox()
			f.inbox, in.svc = in, s
			h := &inboxHub{Service: s, f: f, in: in}
			if err := s.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.Agent.Enabled = c.enabled })
			if !c.signalsOff {
				turnInboxSignalsOn(t, s)
			}
			todo := supervisedTodo(t, s, "Review the change")
			s.todos.mu.Lock()
			task := s.todos.todos[todo.ID]
			switch c.field {
			case "title":
				task.Title = "Review " + pr
			case "notes":
				task.Notes = "See [change](" + pr + ")."
			case "issue":
				task.Notes = "https://github.com/example/project/issues/456"
			default:
				task.PRURL = pr
			}
			if c.done {
				task.DoneAt = &now
			}
			s.todos.mu.Unlock()
			if c.ambiguous {
				other := supervisedTodo(t, s, "Another task")
				s.todos.mu.Lock()
				s.todos.todos[other.ID].PRURL = pr
				s.todos.mu.Unlock()
			}
			model, calls := captureModel(t, `{"task_id":"wrong-task"}`)
			in.setModel(model)
			it := &InboxItem{ID: "github:comment", Source: "github", Kind: KindMention, Title: "New review comment", Last: "comment-456", UpdatedAt: now,
				Context: "A review comment arrived", Connector: &InboxConnector{URL: c.link}}
			switch c.field {
			case "context":
				it.Connector = nil
				it.Context = "Review [comment](" + c.link + ")"
			case "preview":
				it.Connector = nil
				it.Preview = c.link
			case "item title":
				it.Connector = nil
				it.Title = c.link
			}
			if c.enabled {
				it.Tier, it.TriagedAt = TierAction, it.Last
			}
			if c.existing {
				it.Agent = &InboxAgent{TodoID: todo.ID}
			}
			s.inbox.mu.Lock()
			s.inbox.items[it.ID] = it
			s.inbox.mu.Unlock()
			s.handPass(ctx)
			s.inbox.mu.Lock()
			linked := it.Agent != nil && it.Agent.TodoID == todo.ID
			s.inbox.mu.Unlock()
			if linked == c.noMatch {
				t.Fatalf("linked=%v, want %v; agent=%+v", linked, !c.noMatch, it.Agent)
			}
			if got := calls(); len(got) != 0 {
				t.Fatalf("deterministic pass called the judge %d times", len(got))
			}
			g, raised := pendingInbox(s, todo.ID)
			if raised != (!c.noMatch && !c.signalsOff) {
				t.Fatalf("supervisor signal=%v, want %v: %+v", raised, !c.noMatch && !c.signalsOff, g)
			}
			if raised && g.Inbox.Item != it.ID {
				t.Fatalf("signal item=%q", g.Inbox.Item)
			}
			if !c.enabled && it.Handling != nil {
				t.Fatalf("routing started handling with twin off: %+v", it.Handling)
			}
		})
	}
}

// The event entry point must schedule a routing pass even when no twin work is queued.
func TestInboxTaskURLRoutingStartsWithTwinOff(t *testing.T) {
	ctx := context.Background()
	s, f := newQuietService(t)
	turnInboxSignalsOn(t, s)
	in := newFakeInbox()
	f.inbox, in.svc = in, s
	if err := s.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	todo := supervisedTodo(t, s, "Review the change")
	s.todos.mu.Lock()
	s.todos.todos[todo.ID].PRURL = "https://github.com/example/project/pull/123"
	s.todos.mu.Unlock()
	it := &InboxItem{ID: "github:real-comment", Source: "github", Kind: KindMention, Title: "Review comment", Sender: "Reviewer", Last: "1", UpdatedAt: time.Now(),
		Connector: &InboxConnector{URL: "https://github.com/example/project/pull/123#issuecomment-456"}}
	s.inbox.mu.Lock()
	s.inbox.items[it.ID] = it
	s.inbox.mu.Unlock()
	s.handInbox(ctx)
	waitFor(t, "the task supervisor receives the comment", func() bool {
		for _, notice := range f.notices() {
			if strings.Contains(notice, it.ID) {
				return true
			}
		}
		return false
	})
	s.handInbox(ctx)
	s.inbox.handing.Lock()
	s.inbox.handing.Unlock()
	if len(f.notices()) != 1 {
		t.Fatalf("repeated pass duplicated supervisor notice: %v", f.notices())
	}
}

// With no unique URL match, the original judge still gets the event and can link it.
func TestInboxTaskURLJudgeFallback(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "no URL", true: "ambiguous URL"}[ambiguous], func(t *testing.T) {
			ctx := context.Background()
			h := agentHub(t)
			todo, err := h.createTodo(ctx, "Review the change", "", "conv1", "")
			if err != nil {
				t.Fatal(err)
			}
			it := actionItem("github:fallback", time.Now())
			if ambiguous {
				other, err := h.createTodo(ctx, "Another change", "", "conv1", "")
				if err != nil {
					t.Fatal(err)
				}
				h.todos.mu.Lock()
				for _, id := range []string{todo.ID, other.ID} {
					h.todos.todos[id].PRURL = "https://github.com/example/project/pull/123"
				}
				h.todos.mu.Unlock()
				it.Connector = &InboxConnector{URL: "https://github.com/example/project/pull/123#discussion_r456"}
			}
			h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.Agent.Enabled = true })
			model, calls := captureModel(t, `{"task_id":"`+todo.ID+`"}`)
			h.in.setModel(model)
			h.inbox.mu.Lock()
			h.inbox.items[it.ID] = it
			h.inbox.mu.Unlock()
			h.handPass(ctx)
			if it.Agent == nil || it.Agent.TodoID != todo.ID {
				t.Fatalf("judge did not link: %+v", it.Agent)
			}
			if got := calls(); len(got) != 1 {
				t.Fatalf("judge calls=%d, want 1", len(got))
			}
		})
	}
}

// Two different items on one task must count only new messages across repeated passes.
func TestInboxTaskURLRepeatedPassCountsOnce(t *testing.T) {
	ctx := context.Background()
	h := agentHub(t)
	turnInboxSignalsOn(t, h.Service)
	h.superviseTimes.gather = time.Hour
	todo := supervisedTodo(t, h.Service, "Review the change")
	h.todos.mu.Lock()
	h.todos.todos[todo.ID].PRURL = "https://github.com/example/project/pull/123"
	h.todos.mu.Unlock()
	h.inbox.mu.Lock()
	for _, id := range []string{"github:first", "github:second"} {
		h.inbox.items[id] = &InboxItem{ID: id, Kind: KindMention, Last: "1", Connector: &InboxConnector{URL: "https://github.com/example/project/pull/123"}}
	}
	h.inbox.mu.Unlock()
	h.handPass(ctx)
	if g, ok := pendingInbox(h.Service, todo.ID); !ok || g.Inbox.Count != 2 {
		t.Fatalf("first pass: %+v, %v", g.Inbox, ok)
	}
	h.handPass(ctx)
	if g, ok := pendingInbox(h.Service, todo.ID); !ok || g.Inbox.Count != 2 {
		t.Fatalf("repeat pass: %+v, %v", g.Inbox, ok)
	}
	h.inbox.mu.Lock()
	h.inbox.items["github:first"].Last = "2"
	h.inbox.mu.Unlock()
	h.handPass(ctx)
	if g, ok := pendingInbox(h.Service, todo.ID); !ok || g.Inbox.Count != 3 {
		t.Fatalf("new comment: %+v, %v", g.Inbox, ok)
	}
}
