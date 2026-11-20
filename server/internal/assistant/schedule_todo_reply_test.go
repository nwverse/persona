package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// #11553: a linked run's explanation is its own reply. Two linked patrols set up in one supervising conversation came
// due in the same minute: the first run's prompt opened a turn, the second waited in the conversation's queue and
// opened the next one. Both watchers saw the conversation settle after the second answer and read its latest turn, so
// the first to-do's no-progress decision asked the owner about the other task's progress.

const (
	backfillReply = "The backfill Agent is still reading the source tables; no batch is written yet."
	otherReply    = "The other task found 930,000 ads on one test account and is comparing fields."
)

// twoRunsChat is the supervising conversation after both patrols ran: first's prompt and its answer, then second's
// prompt and its answer, in turns of their own; queued: second's prompt came into first's turn while it ran.
func twoRunsChat(codex, queued bool, first, second string) *runChat {
	chat := newRunChat(codex)
	chat.user(hubRunPrompt(first, ""), true)
	if queued {
		chat.queued(hubRunPrompt(second, ""))
		chat.reply(backfillReply + " " + otherReply)
		return chat
	}
	chat.reply(backfillReply)
	chat.user(hubRunPrompt(second, ""), true)
	chat.reply(otherReply)
	return chat
}

func TestRunReplyIsTheRunsOwn(t *testing.T) {
	for format, codex := range chatFormats() {
		t.Run(format, func(t *testing.T) {
			items := twoRunsChat(codex, false, "key-first", "key-second").page(t)
			if reply, found := runReply(items, "key-first"); !found || reply != backfillReply {
				t.Fatalf("first run: %q (found %v), want its own answer", reply, found)
			}
			if reply, found := runReply(items, "key-second"); !found || reply != otherReply {
				t.Fatalf("second run: %q (found %v), want its own answer", reply, found)
			}
			if reply, found := runReply(items, "key-elsewhere"); found || reply != "" {
				t.Fatalf("a run whose prompt is not on the page: %q (found %v)", reply, found)
			}
			if reply, found := runReply(items, ""); found || reply != "" {
				t.Fatalf("a run with no key: %q (found %v)", reply, found)
			}
			chat := twoRunsChat(codex, false, "key-first", "key-second")
			chat.user("what changed since this morning?", false)
			chat.reply("Nothing else changed.")
			if reply, _ := runReply(chat.page(t), "key-second"); reply != otherReply {
				t.Fatalf("the owner's next message took over the run's reply: %q", reply)
			}
		})
	}
	t.Run("claude/queued into one turn", func(t *testing.T) {
		items := twoRunsChat(false, true, "key-first", "key-second").page(t)
		for _, key := range []string{"key-first", "key-second"} {
			if reply, found := runReply(items, key); !found || reply != "" {
				t.Fatalf("%s: %q (found %v): a turn that answered both runs is neither's", key, reply, found)
			}
		}
	})
}

// sourceRun runs finishScheduledTodo for task, whose run went into its source conversation (conv1 on server, which
// pages chat) with the prompt that hands out key, and returns the run's assessment.
func sourceRun(t *testing.T, s *Service, f *fakeHub, task Task, key string, chat *runChat) ScheduleTodoAssessment {
	t.Helper()
	page := chat.page(t)
	f.mu.Lock()
	f.page = func(int) []proto.Item { return page }
	f.mu.Unlock()
	task.stateRunKey = key
	before, _ := s.scheduledTodoSnapshot(t.Context(), task, ScheduleRun{})
	task.todoBefore = &before
	run := ScheduleRun{RefID: task.ID, At: time.Now().UTC(), Status: runOK, MachineID: "server", SessionID: "conv1",
		Conversation: "source", TodoProgress: &ScheduleTodoAssessment{TodoID: task.TodoID, State: "pending"}}
	run.ID = s.recordRun(run)
	s.finishScheduledTodo(t.Context(), task, run)
	_, runs := scheduleState(s)
	for _, r := range runs {
		if r.ID == run.ID && r.TodoProgress != nil {
			return *r.TodoProgress
		}
	}
	t.Fatal("missing assessment")
	return ScheduleTodoAssessment{}
}

func TestLinkedRunsInOneConversationKeepTheirOwnReasons(t *testing.T) {
	s, f, backfill := scheduledTodoFixture(t)
	other := Todo{ID: "todo-other", Title: "Validate the first ad", Origin: "conv1", Supervision: &Supervision{Supervisor: "conv1"}}
	putTodo(s, other)
	backfillTask := todoProgressTask(backfill.ID)
	backfillTask.SessionID, backfillTask.MachineID = "conv1", "server"
	otherTask := Task{ID: "other-check", Title: "First ad check", Prompt: "Advance the validation", TodoID: other.ID,
		Repeat: repeatInterval, EveryMinutes: 10, Enabled: true, Push: pushAsked, SessionID: "conv1", MachineID: "server"}
	setTasks(s, backfillTask, otherTask)

	for round := 1; round <= 2; round++ {
		chat := twoRunsChat(false, false, "backfill-"+string(rune('0'+round)), "other-"+string(rune('0'+round)))
		// Both watchers read the conversation after the second answer; each run takes the reply to its own prompt.
		mine := sourceRun(t, s, f, backfillTask, "backfill-"+string(rune('0'+round)), chat)
		theirs := sourceRun(t, s, f, otherTask, "other-"+string(rune('0'+round)), chat)
		if mine.State != "no_progress" || mine.NoProgress != round || mine.Reason != backfillReply {
			t.Fatalf("round %d: the backfill run's assessment %+v, want its own reply as the reason", round, mine)
		}
		if theirs.Reason != otherReply {
			t.Fatalf("round %d: the other run's assessment %+v, want its own reply as the reason", round, theirs)
		}
	}
	// The second no-progress run asks the owner, with the backfill run's own words, never the other task's.
	waitFor(t, "the owner decision", func() bool { return todoNow(s, backfill.ID).Supervision.State == reviewNeedsUser })
	reviews := todoNow(s, backfill.ID).Supervision.Reviews
	review := reviews[len(reviews)-1]
	if !strings.Contains(review.Reason, strings.TrimSuffix(backfillReply, ".")) || strings.Contains(review.Reason, "930,000") ||
		strings.Contains(review.Note, "930,000") {
		t.Fatalf("the backfill to-do's decision is about another task: reason %q, note %q", review.Reason, review.Note)
	}
}

// A turn that answered both runs (the second prompt queued into the first's turn) is neither's: in the shared
// conversation the assessment says what the hub saw. A run in a conversation of its own, whose prompt is not on the
// page, still has the latest turn's reply.
func TestLinkedRunWithoutItsOwnTurn(t *testing.T) {
	s, f, backfill := scheduledTodoFixture(t)
	task := todoProgressTask(backfill.ID)
	setTasks(s, task)
	got := sourceRun(t, s, f, task, "key-first", twoRunsChat(false, true, "key-first", "key-second"))
	if want := i18n.T(s.lang(context.Background()), "assistant-schedule.todo_no_evidence"); got.Reason != want {
		t.Fatalf("a shared turn's reply became the run's reason: %q, want %q", got.Reason, want)
	}

	f.mu.Lock()
	f.page = nil
	f.mu.Unlock()
	f.setItems(proto.Item{Kind: proto.KindText, Role: "assistant", Text: "Nothing was started because execution slots are busy."})
	_, own := assessTodoRun(t, s, task.ID, nil)
	if own.Reason != "Nothing was started because execution slots are busy." {
		t.Fatalf("a run in its own conversation lost its latest turn's reply: %q", own.Reason)
	}
}
