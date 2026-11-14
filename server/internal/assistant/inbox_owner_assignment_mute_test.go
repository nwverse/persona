package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/connectorwire"
)

const (
	ownerAssignmentThread        = "synthetic-owner-assignment-thread"
	ownerAssignmentLast          = "synthetic-owner-assignment-message"
	ownerAssignmentUnflagged     = "synthetic-unflagged-thread"
	ownerAssignmentUnflaggedLast = "synthetic-unflagged-message"
	ownerAssignmentSender        = "synthetic-alert-sender"
	ownerAssignmentChat          = "synthetic-owner-chat"
	ownerAssignmentSource        = "larkcli"
	ownerAssignmentOtherSource   = "synthetic-connector"
)

func ownerAssignmentThing(thread, last string, assigned bool, at time.Time) connectorwire.InboxThing {
	return connectorwire.InboxThing{
		Thread: thread, Last: last, Kind: "mention", Title: "Example Responsibility Alert",
		Sender: "Example Alert Sender", Context: "Example Owner is responsible for this alert",
		At: at, Tier: connectorwire.TierAction, OwnerAssigned: assigned,
		Tags: []connector.Tag{
			{Key: connector.TagChat, Value: ownerAssignmentChat, Name: "Example Chat"},
			{Key: connector.TagSender, Value: ownerAssignmentSender, Name: "Example Alert Sender"},
		},
	}
}

func ownerAssignmentRound(source string, seq uint64, things ...connectorwire.InboxThing) assistantwire.InboxRound {
	return ownerAssignmentRoundMode(source, seq, true, things...)
}

func ownerAssignmentRoundMode(source string, seq uint64, replay bool, things ...connectorwire.InboxThing) assistantwire.InboxRound {
	return assistantwire.InboxRound{
		Replay: replay, Epoch: "synthetic-owner-assignment-epoch", Seq: seq, Round: assistantwire.RoundPoll,
		Names:   map[string]string{source: "Example Connector"},
		Sources: []assistantwire.InboxSource{{ID: source, Name: "Example Connector", Things: things}},
	}
}

type ownerAssignmentItemState struct {
	found, open, done, assigned bool
	doneBy, tier                string
	id                          string
}

func ownerAssignmentState(h *inboxHub, source, thread string) ownerAssignmentItemState {
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	for _, it := range h.inbox.items {
		if it.Source == source && it.Connector != nil && it.Connector.Thread == thread {
			return ownerAssignmentItemState{
				found: true, open: it.open(), done: it.DoneAt != nil, doneBy: it.DoneBy, tier: it.Tier,
				assigned: it.Connector.OwnerAssigned, id: it.ID,
			}
		}
	}
	return ownerAssignmentItemState{}
}

func ownerAssignmentMutes(h *inboxHub) []InboxMute {
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	return append([]InboxMute(nil), h.inbox.mutes...)
}

func TestInboxOwnerAssignedSenderMuteExceptionInNormalCollection(t *testing.T) {
	mute := InboxMute{ID: "synthetic-sender-mute", Source: ownerAssignmentSource, Tag: connector.TagSender,
		Value: ownerAssignmentSender, Name: "Example Alert Sender"}
	h := newReplayMuteHandoffHub(t, mute)
	if err := h.Service.InboxCollected(context.Background(), ownerAssignmentRoundMode(ownerAssignmentSource, 1, false,
		ownerAssignmentThing(ownerAssignmentThread, ownerAssignmentLast, true, time.Now().UTC()),
	)); err != nil {
		t.Fatal(err)
	}
	got := ownerAssignmentState(h, ownerAssignmentSource, ownerAssignmentThread)
	if !got.found || !got.open || got.done || got.tier != TierAction || !got.assigned {
		t.Fatalf("normal collection did not preserve the explicitly assigned card: %+v", got)
	}
	if mutes := ownerAssignmentMutes(h); len(mutes) != 1 || mutes[0] != mute {
		t.Fatalf("normal collection changed sender mute settings: %+v", mutes)
	}
}

func TestInboxOwnerAssignedMuteDoesNotReopenAlreadyHandledCard(t *testing.T) {
	h := newReplayMuteHandoffHub(t)
	thing := ownerAssignmentThing(ownerAssignmentThread, ownerAssignmentLast, true, time.Now().UTC())
	if err := h.Service.InboxCollected(context.Background(), ownerAssignmentRound(ownerAssignmentSource, 1, thing)); err != nil {
		t.Fatal(err)
	}
	initial := ownerAssignmentState(h, ownerAssignmentSource, ownerAssignmentThread)
	if !initial.found || !initial.open || !initial.assigned {
		t.Fatalf("initial assigned card was not open: %+v", initial)
	}
	if code, out := inboxCall(t, h.routes(), "POST", "/api/v1/inbox/"+initial.id+"/done", `{"done":true}`); code != 200 {
		t.Fatalf("mark the synthetic item handled: %d %v", code, out)
	}

	mute := InboxMute{ID: "synthetic-sender-mute", Source: ownerAssignmentSource, Tag: connector.TagSender,
		Value: ownerAssignmentSender, Name: "Example Alert Sender"}
	h.inbox.mu.Lock()
	h.inbox.mutes = []InboxMute{mute}
	h.inbox.mu.Unlock()
	if err := h.Service.InboxCollected(context.Background(), ownerAssignmentRound(ownerAssignmentSource, 2, thing)); err != nil {
		t.Fatal(err)
	}
	got := ownerAssignmentState(h, ownerAssignmentSource, ownerAssignmentThread)
	if !got.found || got.open || !got.done || got.doneBy != "user" || !got.assigned {
		t.Fatalf("already-handled assigned card was reopened or overwritten by the mute: %+v", got)
	}
	if mutes := ownerAssignmentMutes(h); len(mutes) != 1 || mutes[0] != mute {
		t.Fatalf("replay changed sender mute settings: %+v", mutes)
	}
}

func TestInboxOwnerAssignedBypassesOnlyLarkCLISenderMute(t *testing.T) {
	mute := InboxMute{ID: "synthetic-sender-mute", Source: ownerAssignmentSource, Tag: connector.TagSender,
		Value: ownerAssignmentSender, Name: "Example Alert Sender"}
	h := newReplayMuteHandoffHub(t, mute)
	now := time.Now().UTC()
	if err := h.Service.InboxCollected(context.Background(), ownerAssignmentRound(ownerAssignmentSource, 1,
		ownerAssignmentThing(ownerAssignmentThread, ownerAssignmentLast, true, now),
		ownerAssignmentThing(ownerAssignmentUnflagged, ownerAssignmentUnflaggedLast, false, now),
	)); err != nil {
		t.Fatal(err)
	}

	assigned := ownerAssignmentState(h, ownerAssignmentSource, ownerAssignmentThread)
	ordinary := ownerAssignmentState(h, ownerAssignmentSource, ownerAssignmentUnflagged)
	if !assigned.found || !assigned.open || assigned.done || assigned.tier != TierAction || !assigned.assigned {
		t.Fatalf("explicitly assigned responsibility card did not stay actionable/open: %+v", assigned)
	}
	if !ordinary.found || ordinary.open || !ordinary.done || ordinary.doneBy != "read" || ordinary.tier != TierAction || ordinary.assigned {
		t.Fatalf("otherwise-identical ordinary card escaped its sender mute: %+v", ordinary)
	}
	if got := ownerAssignmentMutes(h); len(got) != 1 || got[0] != mute {
		t.Fatalf("sender mute settings changed: %+v", got)
	}

	// A later source move that no longer names an owner replaces the flag and is covered by the same sender mute.
	if err := h.Service.InboxCollected(context.Background(), ownerAssignmentRound(ownerAssignmentSource, 2,
		ownerAssignmentThing(ownerAssignmentThread, "synthetic-owner-assignment-followup", false, now.Add(time.Second)),
	)); err != nil {
		t.Fatal(err)
	}
	updated := ownerAssignmentState(h, ownerAssignmentSource, ownerAssignmentThread)
	if !updated.found || updated.open || !updated.done || updated.doneBy != "read" || updated.assigned {
		t.Fatalf("unassigned update retained the old owner-assigned flag or escaped the mute: %+v", updated)
	}
	if got := ownerAssignmentMutes(h); len(got) != 1 || got[0] != mute {
		t.Fatalf("update changed sender mute settings: %+v", got)
	}
}

func TestInboxOwnerAssignedDoesNotBypassChatMute(t *testing.T) {
	mute := InboxMute{ID: "synthetic-chat-mute", Source: ownerAssignmentSource, Tag: connector.TagChat,
		Value: ownerAssignmentChat, Name: "Example Chat"}
	h := newReplayMuteHandoffHub(t, mute)
	if err := h.Service.InboxCollected(context.Background(), ownerAssignmentRound(ownerAssignmentSource, 1,
		ownerAssignmentThing(ownerAssignmentThread, ownerAssignmentLast, true, time.Now().UTC()),
	)); err != nil {
		t.Fatal(err)
	}
	got := ownerAssignmentState(h, ownerAssignmentSource, ownerAssignmentThread)
	if !got.found || got.open || !got.done || got.doneBy != "read" || !got.assigned {
		t.Fatalf("owner assignment bypassed the matching chat mute: %+v", got)
	}
	if mutes := ownerAssignmentMutes(h); len(mutes) != 1 || mutes[0] != mute {
		t.Fatalf("chat mute settings changed: %+v", mutes)
	}
}

func TestInboxOwnerAssignedFromOtherSourceStillMatchesSenderMute(t *testing.T) {
	mute := InboxMute{ID: "synthetic-other-source-mute", Source: ownerAssignmentOtherSource, Tag: connector.TagSender,
		Value: ownerAssignmentSender, Name: "Example Alert Sender"}
	h := newReplayMuteHandoffHub(t, mute)
	if err := h.Service.InboxCollected(context.Background(), ownerAssignmentRound(ownerAssignmentOtherSource, 1,
		ownerAssignmentThing(ownerAssignmentThread, ownerAssignmentLast, true, time.Now().UTC()),
	)); err != nil {
		t.Fatal(err)
	}
	got := ownerAssignmentState(h, ownerAssignmentOtherSource, ownerAssignmentThread)
	if !got.found || got.open || !got.done || got.doneBy != "read" || !got.assigned {
		t.Fatalf("owner assignment bypassed a sender mute for another source: %+v", got)
	}
	if mutes := ownerAssignmentMutes(h); len(mutes) != 1 || mutes[0] != mute {
		t.Fatalf("other-source mute settings changed: %+v", mutes)
	}
}

func TestInboxOwnerAssignedWireFieldAndConnectorCopy(t *testing.T) {
	thing := ownerAssignmentThing(ownerAssignmentThread, ownerAssignmentLast, true, time.Now().UTC())
	raw, err := json.Marshal(thing)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped connectorwire.InboxThing
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var wireFlag bool
	if err := json.Unmarshal(fields["owner_assigned"], &wireFlag); err != nil || !wireFlag || !roundTripped.OwnerAssigned {
		t.Fatalf("owner_assigned wire field did not round trip: json=%s value=%+v err=%v", raw, roundTripped, err)
	}

	item := &InboxItem{}
	fillConnectorItem(item, roundTripped)
	if item.Connector == nil || !item.Connector.OwnerAssigned {
		t.Fatalf("connector item did not retain owner assignment: %+v", item.Connector)
	}
}
