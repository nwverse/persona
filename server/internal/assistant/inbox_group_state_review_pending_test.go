package assistant

// Review tests for state reports that failed on 296210b09c; they move into inbox_group_state_review_test.go once the
// implementation is fixed. The agreed behavior (lane lead, 11-13): a read of Persona's own confirmed reply, and a read
// after the same position's self_replied, only set Read — no note, no wake, no state change.

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/connector"
)

// A reply from a notification (read false, replied true), then the chat is opened: the read report at the same position
// must not queue a second note that says the owner did not reply here. The conversation gets one story.
func TestInboxGroupStateReadAfterRepliedDoesNotRepeatTheNote(t *testing.T) {
	h := inboxGroupStateTestHub(t, true)
	h.f.hello.Owner = "示例用户"
	it := inboxGroupStateTestItem(h, "event-replied-then-read", true)
	replied := inboxGroupStateReport(it, false, connector.DoneReplied)
	read := inboxGroupStateReport(it, true, "")
	if _, changed := h.applyGroupStates(context.Background(), inboxGroupStateTestSource(), []connector.InboxThing{replied}); len(changed) != 1 {
		t.Fatalf("the reply was not applied: %+v", changed)
	}
	handling := it.Handling.State
	_, _ = h.applyGroupStates(context.Background(), inboxGroupStateTestSource(), []connector.InboxThing{read})
	h.deliverPending(context.Background())
	notes := notesOf(h.f, "session-1")
	if len(notes) != 1 || strings.Contains(notes[0], "看过这件事的最新消息") {
		t.Fatalf("the conversation got both stories: %q", notes)
	}
	if it.Handling.State != handling || it.Agent.Wake {
		t.Fatalf("the read moved the event again: handling=%+v agent=%+v", it.Handling, it.Agent)
	}
	if len(inboxGroupStateSelfReplies(it)) != 1 {
		t.Fatalf("self_replied entries = %+v", it.Chain)
	}
}

// After Persona sends a confirmed reply the chat is no longer unread, so the connector reports the event read at the
// same position (its own rule). That is not the owner reading in the source: Read is set, but the event keeps waiting
// on the other side, with no self_read note and no wake.
func TestInboxGroupStateOwnSendReadIsNotTheOwnersRead(t *testing.T) {
	h := inboxGroupStateTestHub(t, true)
	h.f.hello.Owner = "示例用户"
	it := inboxGroupStateTestItem(h, "event-own-send-read", true)
	it.Handling.State = HandlingWaiting
	it.Agent.SentOf = it.Last
	report := inboxGroupStateReport(it, true, "")

	_, _ = h.applyGroupStates(context.Background(), inboxGroupStateTestSource(), []connector.InboxThing{report})
	h.deliverPending(context.Background())
	if !it.Read {
		t.Fatalf("the read of Persona's own reply was not recorded: %+v", it)
	}
	if it.Handling.State != HandlingWaiting || it.Agent.Wake || len(it.Agent.Pending) != 0 {
		t.Fatalf("the waiting event was woken and moved: handling=%+v agent=%+v", it.Handling, it.Agent)
	}
	if notes := notesOf(h.f, "session-1"); len(notes) != 0 {
		t.Fatalf("the conversation was told the owner read it: %q", notes)
	}
}
