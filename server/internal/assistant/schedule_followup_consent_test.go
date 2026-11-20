package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

const followupOwnerWords = "Yes, please set up a recurring follow-up."

// Reuse the normal SendParams entry point, including the app's attestation,
// before populating the fake node's transcript with exactly the same message.
func attestFollowupRequest(t *testing.T, s *Service, session string) {
	t.Helper()
	ctx := t.Context()
	clientID := "owner-" + randomHex(8)
	s.originSet(ctx, "server", session, OriginUser, "", "")
	if _, err := s.SendParams(ctx, SendParams{MachineID: "server", SessionID: session, Human: true,
		Params: map[string]any{"client_msg_id": clientID, "text": followupOwnerWords}}); err != nil {
		t.Fatal(err)
	}
	s.hub.(*fakeHub).setItems(proto.Item{ID: "u1", Role: "user", Kind: proto.KindText, ClientMsgID: clientID, Text: followupOwnerWords})
}

func TestFollowupRecurrenceRequiresOwnerTurnAndRequest(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		setup      func(*testing.T, *Service, *fakeHub, string)
	}{
		{
			name: "owner turn alone is not a recurring request",
			args: `{"every":"30m","prompt":"Check the deployment"}`,
			setup: func(t *testing.T, s *Service, _ *fakeHub, session string) {
				attestFollowupRequest(t, s, session)
			},
		},
		{
			name: "claimed request without a verified owner turn",
			args: `{"every":"30m","prompt":"Check the deployment","user_requested":true}`,
			setup: func(t *testing.T, s *Service, f *fakeHub, session string) {
				s.originSet(t.Context(), "server", session, OriginUser, "", "")
				f.setItems(proto.Item{ID: "u1", Role: "user", Kind: proto.KindText, ClientMsgID: "unattested",
					Text: followupOwnerWords})
			},
		},
		{
			name: "scheduled round despite a stale owner transcript",
			args: `{"every":"30m","prompt":"Check the deployment","user_requested":true}`,
			setup: func(t *testing.T, s *Service, _ *fakeHub, session string) {
				attestFollowupRequest(t, s, session)
				s.noteRound("server", session, "scheduled", "", time.Now())
			},
		},
		{
			name: "scheduled activity starts during the transcript read",
			args: `{"every":"30m","prompt":"Check the deployment","user_requested":true}`,
			setup: func(t *testing.T, s *Service, f *fakeHub, session string) {
				attestFollowupRequest(t, s, session)
				f.answerNode(DeviceSessionItems, func(_ string, _ json.RawMessage) (any, error) {
					s.noteRound("server", session, "scheduled", "", time.Now())
					return map[string]any{"items": f.items}, nil
				})
			},
		},
		{
			name: "relayed prompt after an attested owner message",
			args: `{"cron":"0 10 * * *","prompt":"Check the deployment","user_requested":true}`,
			setup: func(t *testing.T, s *Service, f *fakeHub, session string) {
				attestFollowupRequest(t, s, session)
				f.mu.Lock()
				records := append([]proto.Item(nil), f.items...)
				f.mu.Unlock()
				f.setItems(append(records, proto.Item{ID: "u2", Role: "user", Kind: proto.KindText, Text: followupOwnerWords,
					Source: &proto.MessageSource{Kind: proto.SourceSupervision}})...)
			},
		},
		{
			name: "extension-opened conversation",
			args: `{"every":"30m","prompt":"Check the deployment","user_requested":true}`,
			setup: func(t *testing.T, s *Service, _ *fakeHub, session string) {
				attestFollowupRequest(t, s, session)
				s.originSet(t.Context(), "server", session, OriginUser, "", "synthetic-extension")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHub().withMachine()
			s := newStorelessService(t, f)
			setTasks(s)
			const session = "followup-chat"
			tc.setup(t, s, f, session)
			if _, err := s.runTool(t.Context(), "server", session, "schedule_followup", json.RawMessage(tc.args)); !errors.Is(err, errFollowupRecurrenceConsent) {
				t.Fatalf("expected recurrence consent refusal: %v", err)
			}
			if tasks := s.tasksView(t.Context(), nil); len(tasks) != 0 {
				t.Fatalf("denial saved a schedule: %+v", tasks)
			}
		})
	}
}

func TestFollowupRecurrenceCanBeApprovedInLaterOwnerTurn(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newStorelessService(t, f)
	setTasks(s)
	const session = "followup-chat"
	args := json.RawMessage(`{"every":"30m","title":"Review","prompt":"Check the deployment","user_requested":true}`)
	if _, err := s.runTool(t.Context(), "server", session, "schedule_followup", args); !errors.Is(err, errFollowupRecurrenceConsent) {
		t.Fatalf("flag alone: %v", err)
	}
	attestFollowupRequest(t, s, session)
	if _, err := s.runTool(t.Context(), "server", session, "schedule_followup", args); err != nil {
		t.Fatal(err)
	}
	tasks := s.tasksView(t.Context(), nil)
	if len(tasks) != 1 || tasks[0].Repeat != repeatInterval || tasks[0].EveryMinutes != 30 || tasks[0].SessionID != session {
		t.Fatalf("approved schedule: %+v", tasks)
	}
}

func TestFollowupRecurrenceDenialCannotMutateASavedOneOff(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newStorelessService(t, f)
	ctx := t.Context()
	old := Task{ID: "followup", Title: "Deployment", Prompt: "Look once", Repeat: repeatOnce,
		MachineID: "server", At: time.Now().Add(-time.Hour), LastRun: time.Now(), CreatedBy: "assistant"}
	setTasks(s, old)
	const session = "previous-run"
	s.recordRun(ScheduleRun{RefID: old.ID, SessionID: session, MachineID: "server", Status: runOK})
	before, _ := taskByID(s, old.ID)
	args := json.RawMessage(`{"every":"30m","title":"Deployment","prompt":"Replace the prompt","push":"asked","user_requested":true}`)
	if _, err := s.runTool(ctx, "server", session, "schedule_followup", args); !errors.Is(err, errFollowupRecurrenceConsent) {
		t.Fatalf("unattended promotion: %v", err)
	}
	got, _ := taskByID(s, old.ID)
	if !reflect.DeepEqual(got, before) {
		t.Fatalf("denial modified the old task: %+v", got)
	}

	out, err := s.runTool(ctx, "server", session, "schedule_followup", json.RawMessage(`{"at":"30m","title":"Deployment","prompt":"Look once","user_requested":true}`))
	if err != nil || !strings.Contains(out, "Rescheduled") {
		t.Fatalf("regular one-off rearm: %q %v", out, err)
	}
	got, _ = taskByID(s, old.ID)
	if !got.Enabled || got.Repeat != repeatOnce || got.EveryMinutes != 0 || time.Until(got.At) < 29*time.Minute {
		t.Fatalf("relative rearm changed recurrence or due time: %+v", got)
	}
}

func TestFollowupRecurrenceRequiresFreshProofAfterManagerRestart(t *testing.T) {
	store := &memStore{}
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, store)
	const session = "owner-chat"
	attestFollowupRequest(t, s, session)
	reloaded := newServiceOn(t, f, store)
	setTasks(reloaded)
	args := json.RawMessage(`{"cron":"0 10 * * *","prompt":"Restore the previous reply","user_requested":true}`)
	if _, err := reloaded.toolScheduleFollowup(context.Background(), "server", session, args); !errors.Is(err, errFollowupRecurrenceConsent) {
		t.Fatalf("old transcript alone gave consent after restart: %v", err)
	}
	if tasks := reloaded.tasksView(t.Context(), nil); len(tasks) != 0 {
		t.Fatalf("unverified approval saved a schedule: %+v", tasks)
	}
}
