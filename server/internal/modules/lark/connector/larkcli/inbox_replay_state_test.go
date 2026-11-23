package larkcli

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/modules/lark/larkcli"
)

type retryInboxStateStore struct {
	inboxStateStore
	err           error
	reads, writes int
}

func (s *retryInboxStateStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	s.reads++
	if s.reads == 1 {
		return false, s.err
	}
	return s.inboxStateStore.Setting(ctx, key, out)
}

func (s *retryInboxStateStore) PutSetting(ctx context.Context, key string, value any) error {
	s.writes++
	return s.inboxStateStore.PutSetting(ctx, key, value)
}

func TestInboxStateLoadFailureRetriesAcrossReplayAndPoll(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		firstReplay, retryReplay bool
	}{
		{"replay then replay", true, true},
		{"replay then poll", true, false},
		{"poll then replay", false, true},
		{"poll then poll", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := addressedMessage("om_example_selected", "ou_example_peer", "Example Peer",
				`"Follow-up in a previously joined topic"`, "omt_example_topic", nil)
			selected.CreateTime = "2030-01-01T00:01:00Z"
			seen := selected
			seen.MessageID, seen.CreateTime = "om_example_seen", "2030-01-01T00:00:00Z"
			c := addressedTestConnector(t, []adapter.Message{seen, selected}, []adapter.Message{seen, selected})
			state := persistedInboxState{
				Cursor: "2030-01-01T00:00:00Z", PageToken: "example-page", TopicsSeeded: true,
				Topics: []string{"omt_example_topic"}, Seen: []string{seen.MessageID},
				Things: map[string]connectorwire.InboxThing{"retained": {Thread: "retained", Last: "om_example_retained"}},
			}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("example transient state read")
			store := &retryInboxStateStore{inboxStateStore: inboxStateStore{raw: raw}, err: failure}
			c.SetStateStore(store)
			run := func(replay bool) ([]connectorwire.InboxThing, error) {
				if replay {
					return c.ReplayMessages(context.Background(), selected.ChatID, "2030-01-01T00:00:00Z",
						"2030-01-01T00:05:00Z", []string{selected.MessageID})
				}
				if _, err := c.pollChat(context.Background()); err != nil {
					return nil, err
				}
				return c.Collect(context.Background())
			}
			if items, err := run(tc.firstReplay); !errors.Is(err, failure) || len(items) != 0 {
				t.Fatalf("first read did not fail cleanly: items=%+v err=%v", items, err)
			}
			if c.inboxStateLoaded || store.reads != 1 || store.writes != 0 || string(store.raw) != string(raw) {
				t.Fatal("failed state load was cached or persisted")
			}
			items, err := run(tc.retryReplay)
			if err != nil || !slices.ContainsFunc(items, func(it connectorwire.InboxThing) bool { return it.Last == selected.MessageID }) {
				t.Fatalf("retry did not restore topic eligibility: items=%+v err=%v", items, err)
			}
			_, topicLoaded := c.inboxTopics["omt_example_topic"]
			_, seenLoaded := c.inboxSeen[seen.MessageID]
			if !c.inboxStateLoaded || store.reads != 2 || !topicLoaded || !seenLoaded ||
				c.inboxThings["retained"].Last != "om_example_retained" {
				t.Fatal("retry used an incomplete persisted state")
			}
			if tc.retryReplay {
				if store.writes != 0 || string(store.raw) != string(raw) || c.inboxState.Cursor != state.Cursor ||
					c.inboxState.PageToken != state.PageToken || len(c.inboxSeen) != 1 {
					t.Fatal("replay retry changed the saved live collection state")
				}
			} else if store.writes == 0 {
				t.Fatal("ordinary polling did not save its normal collection state")
			}
		})
	}
}
