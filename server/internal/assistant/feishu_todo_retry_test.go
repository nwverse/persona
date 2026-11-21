package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/feishubot"
)

// All calls terminate at fakeHub. Accepted cards are indexed by the transport UUID, independently of whether the
// caller receives their receipt, to model Feishu's deduplication of an ambiguous send.
type todoRetryPosts struct {
	mu       sync.Mutex
	posts    []map[string]any
	accepted map[string]string
}

func (p *todoRetryPosts) record(raw json.RawMessage, accept bool) (int, string) {
	var post map[string]any
	_ = json.Unmarshal(raw, &post)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.posts = append(p.posts, post)
	n := len(p.posts)
	key, _ := post["uuid"].(string)
	if key == "" {
		key = fmt.Sprintf("unkeyed-attempt-%d", n)
	}
	if p.accepted == nil {
		p.accepted = map[string]string{}
	}
	mid := p.accepted[key]
	if accept && mid == "" {
		mid = fmt.Sprintf("om_todo_retry_%d", len(p.accepted)+1)
		p.accepted[key] = mid
	}
	return n, mid
}

func (p *todoRetryPosts) snapshot() ([]map[string]any, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.posts...), len(p.accepted)
}

func installRetryTodo(s *Service, todo Todo) {
	s.todos.mu.Lock()
	defer s.todos.mu.Unlock()
	s.todos.todos = map[string]*Todo{todo.ID: &todo}
	s.todos.loaded = true
}

func TestFeishuTodoReminderRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		postErr                    error
		accepted, always, health   bool
		initialStatus, retryStatus bool
		disabled, unpaired         bool
		wantPosts, wantAccepted    int
	}{
		{name: "normal", wantPosts: 1, wantAccepted: 1},
		{name: "temporary post", postErr: errors.New("HTTP 503"), wantPosts: 2, wantAccepted: 1},
		{name: "rate limited", postErr: &feishubot.APIError{Code: 230429}, wantPosts: 2, wantAccepted: 1},
		{name: "accepted receipt lost", postErr: context.DeadlineExceeded, accepted: true, wantPosts: 2, wantAccepted: 1},
		{name: "initial status unknown", initialStatus: true, wantPosts: 1, wantAccepted: 1},
		{name: "retry status unknown", postErr: errors.New("HTTP 503"), retryStatus: true, wantPosts: 2, wantAccepted: 1},
		{name: "status budget", initialStatus: true, always: true},
		{name: "post budget", postErr: errors.New("HTTP 503"), always: true, wantPosts: 1 + feishuMirrorRetries},
		{name: "permanent refusal", postErr: &feishubot.APIError{Code: 230002}, always: true, wantPosts: 1},
		{name: "pushes disabled", postErr: errors.New("HTTP 503"), disabled: true, wantPosts: 1},
		{name: "unpaired", postErr: errors.New("HTTP 503"), unpaired: true, wantPosts: 1},
		{name: "health recovery", postErr: context.DeadlineExceeded, accepted: true, health: true, wantPosts: 2, wantAccepted: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, s, _ := todoReminderHub(t)
			synctest.Test(t, func(t *testing.T) {
				var capture todoRetryPosts
				statusCalls := 0
				f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
					statusCalls++
					if tc.initialStatus && (tc.always || statusCalls == 1) || tc.retryStatus && statusCalls == 2 {
						return nil, errors.New("temporary status transport failure")
					}
					return FeishuStatus{Ready: !(tc.unpaired && statusCalls > 1), Owner: "ou_a", PairedAt: time.Unix(100, 0),
						PublicURL: "https://hub.example.com", Features: FeishuFeatures{Pushes: !(tc.disabled && statusCalls > 1)}}, nil
				})
				f.answerApp(AppFeishu, FeishuPost, func(raw json.RawMessage) (any, error) {
					posts, _ := capture.snapshot()
					fail := tc.postErr != nil && (tc.always || len(posts) == 0)
					_, mid := capture.record(raw, !fail || tc.accepted)
					if fail {
						return nil, tc.postErr
					}
					return map[string]any{"message_id": mid}, nil
				})
				now := time.Now()
				at := now.Add(-time.Minute)
				todo := Todo{ID: "todo_retry", Title: "Synthetic reminder", Notes: "Synthetic notes", RemindAt: &at}
				if tc.health {
					todo.Origin = "health1"
				}
				installRetryTodo(s, todo)
				s.remindDue(context.Background(), now)
				synctest.Wait()
				// Only the virtual clock advances; no real service or wall-clock retry wait is used.
				time.Sleep(time.Duration(feishuMirrorRetries+2) * feishuMirrorRetry) // sleep-ok: exhaust bounded synctest retry timers
				synctest.Wait()
				s.remindDue(context.Background(), time.Now())
				synctest.Wait()
				posts, accepted := capture.snapshot()
				if len(posts) != tc.wantPosts || accepted != tc.wantAccepted {
					t.Fatalf("posts=%d accepted=%d, want %d/%d", len(posts), accepted, tc.wantPosts, tc.wantAccepted)
				}
				if statusCalls > 1+feishuMirrorRetries {
					t.Fatalf("status calls=%d exceed original retry budget", statusCalls)
				}
				if tc.initialStatus && tc.always && statusCalls != 1+feishuMirrorRetries {
					t.Fatalf("status attempts=%d, want %d", statusCalls, 1+feishuMirrorRetries)
				}
				if got := len(f.pushed()); got != 1 {
					t.Fatalf("phone notifications=%d, want exactly one", got)
				}
				for i, p := range posts {
					key, _ := p["uuid"].(string)
					if key == "" || key != posts[0]["uuid"] || len(key) > 50 {
						t.Fatalf("post %d lost stable bounded key: %q", i, key)
					}
					card := p["card"].(string)
					if tc.health && (strings.Contains(card, todo.Title) || strings.Contains(card, todo.Notes)) {
						t.Fatal("health content escaped on initial send or retry")
					}
				}
				if accepted > 0 && (tc.postErr == nil || !tc.always) {
					target := s.botTargetFor("", "om_todo_retry_1")
					wantTodo := todo.ID
					if tc.health {
						wantTodo = ""
					}
					if target.SessionID != "m1" || target.MachineID != "server" || target.TodoID != wantTodo {
						t.Fatalf("reply route after recovery: %+v", target)
					}
				}
			})
		})
	}
}

func TestFeishuTodoReminderMultipartRecovery(t *testing.T) {
	f, s, _ := todoReminderHub(t)
	synctest.Test(t, func(t *testing.T) {
		var capture todoRetryPosts
		var notes strings.Builder
		for i := 0; i < 700; i++ {
			fmt.Fprintf(&notes, "Segment%04d: %s\n\n", i, strings.Repeat("sample ", 2))
		}
		failed := false
		f.answerApp(AppFeishu, FeishuPost, func(raw json.RawMessage) (any, error) {
			n, mid := capture.record(raw, true)
			if n == 2 && !failed {
				failed = true
				return nil, context.DeadlineExceeded // second card accepted; its receipt is lost
			}
			return map[string]any{"message_id": mid}, nil
		})
		at := time.Now().Add(-time.Minute)
		todo := Todo{ID: "todo_multipart_retry", Title: "Long reminder", Notes: notes.String(), RemindAt: &at}
		installRetryTodo(s, todo)
		s.remindDue(context.Background(), time.Now())
		synctest.Wait()
		time.Sleep(time.Duration(feishuMirrorRetries+2) * feishuMirrorRetry) // sleep-ok: advance only synctest timers
		synctest.Wait()
		posts, accepted := capture.snapshot()
		if accepted < 2 || len(posts) != 2*accepted {
			t.Fatalf("multipart posts=%d accepted=%d, want one retry and no duplicate deliveries", len(posts), accepted)
		}
		for i := 0; i < accepted; i++ {
			if posts[i]["uuid"] == "" || posts[i]["uuid"] != posts[i+accepted]["uuid"] || posts[i]["card"] != posts[i+accepted]["card"] {
				t.Fatalf("card %d changed its key or payload on retry", i)
			}
			mid := fmt.Sprintf("om_todo_retry_%d", i+1)
			if target := s.botTargetFor("", mid); target.TodoID != todo.ID || target.SessionID != "m1" {
				t.Fatalf("card %d lost reply routing: %+v", i, target)
			}
		}
		bodies := postedCardBodies(posts[:accepted])
		for i := 0; i < 700; i++ {
			if marker := fmt.Sprintf("Segment%04d:", i); strings.Count(bodies, marker) != 1 {
				t.Fatalf("missing or duplicated notes segment %s", marker)
			}
		}
		if last := posts[accepted-1]["card"].(string); !strings.Contains(last, "https://hub.example.com/todos/"+todo.ID) {
			t.Fatal("multipart reminder lost the to-do detail link")
		}
		if got := len(f.pushed()); got != 1 {
			t.Fatalf("phone notifications=%d, want 1", got)
		}
	})
}

func TestFeishuTodoReminderOccurrenceKeys(t *testing.T) {
	_, s, posts := todoReminderHub(t)
	at := time.Date(2026, 11, 21, 10, 0, 0, 123, time.UTC)
	todo := Todo{ID: "todo_identity", Title: "Reminder", RemindAt: &at}
	send := func(todo Todo) string {
		t.Helper()
		if err := s.feishuTodoPush(context.Background(), "server", "m1", todo.Title, "Notes", todo); err != nil {
			t.Fatal(err)
		}
		key, _ := (*posts)[len(*posts)-1]["uuid"].(string)
		return key
	}
	first := send(todo)
	zone := at.In(time.FixedZone("offset", 8*60*60))
	todo.RemindAt = &zone
	if first == "" || send(todo) != first {
		t.Fatal("same reminder occurrence lost its key across time zones")
	}
	later := at.Add(time.Nanosecond)
	todo.RemindAt = &later
	if send(todo) == first {
		t.Fatal("different reminder occurrence reused its predecessor's key")
	}
	todo.RemindAt, todo.ID = &at, "todo_other"
	if send(todo) == first {
		t.Fatal("different to-do reused the first to-do's key")
	}
}

func TestFeishuTodoReminderStopsObsoleteOccurrence(t *testing.T) {
	for _, change := range []string{"done", "deleted", "rearmed", "removed"} {
		t.Run(change, func(t *testing.T) {
			f, s, _ := todoReminderHub(t)
			synctest.Test(t, func(t *testing.T) {
				at := time.Now().Add(-time.Minute)
				todo := Todo{ID: "todo_obsolete", Title: "Reminder", RemindAt: &at}
				installRetryTodo(s, todo)
				var capture todoRetryPosts
				f.answerApp(AppFeishu, FeishuPost, func(raw json.RawMessage) (any, error) {
					capture.record(raw, false)
					s.todos.mu.Lock()
					defer s.todos.mu.Unlock()
					cur := s.todos.todos[todo.ID]
					switch change {
					case "done":
						now := time.Now()
						cur.DoneAt = &now
					case "deleted":
						delete(s.todos.todos, todo.ID)
					case "rearmed":
						later := at.Add(time.Hour)
						cur.RemindAt = &later
					case "removed":
						cur.RemindAt = nil
					}
					return nil, errors.New("temporary send failure")
				})
				s.feishuTodoReminder("server", "m1", todo.Title, "Notes", todo)
				time.Sleep(time.Duration(feishuMirrorRetries+2) * feishuMirrorRetry) // sleep-ok: exhaust only virtual retry timers
				synctest.Wait()
				if posts, _ := capture.snapshot(); len(posts) != 1 {
					t.Fatalf("obsolete reminder sent %d attempts", len(posts))
				}
			})
		})
	}
}
