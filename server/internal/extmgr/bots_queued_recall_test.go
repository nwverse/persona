package extmgr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestBotQueuedRecallSurvivesRecentEviction(t *testing.T) {
	for _, chat := range []string{topicChat, "oc_p2p"} {
		t.Run(chat, func(t *testing.T) {
			w := newBotWorld(t)
			w.pair(t)
			w.topicsOn(t, topicChat)
			unblock := blockBotHandoff(t, w, "om_first")
			event := groupEvent("om_recalled", "ou_me", "obsolete request", chat, "omt_example")
			if chat == "oc_p2p" {
				event = botMessageEvent("om_recalled", "ou_me", "text", textContent("obsolete request"), nil)
			}
			w.send(t, event)
			w.send(t, botRecallEvent(chat, "om_recalled", "message_owner"))
			// Ordinary incoming events evict the canceled id from the recent ring
			// while its original handler still waits behind the first handoff.
			for i := 0; i < botSeenKeep; i++ {
				w.send(t, groupEvent(fmt.Sprintf("om_filler_%d", i), "ou_stranger", "ordinary message", topicChat, "omt_example"))
			}
			w.send(t, event)
			unblock()
			finishBotQueue(t, w)
			if got, want := w.handed(), []string{"om_first", "om_marker"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("redelivery revived a recalled pending request: handed %v, want %v", got, want)
			}
		})
	}
}

func TestBotQueuedRecallStaysRecentAfterCleanup(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	w.topicsOn(t, topicChat)
	unblock := blockBotHandoff(t, w, "om_first")
	event := groupEvent("om_recalled", "ou_me", "obsolete request", topicChat, "omt_example")
	w.send(t, event)
	w.send(t, botRecallEvent(topicChat, "om_recalled", "message_owner"))
	for i := 0; i < botSeenKeep; i++ {
		w.send(t, groupEvent(fmt.Sprintf("om_filler_%d", i), "ou_stranger", "ordinary message", topicChat, "omt_example"))
	}
	unblock()
	finishBotQueue(t, w)
	// This delivery arrives after pending ownership has been released. It must
	// still meet bounded recent history rather than recreate the canceled work.
	w.send(t, event)
	w.send(t, groupEvent("om_after_cleanup", "ou_me", "ordinary next request", topicChat, "omt_example"))
	eventually(t, 5*time.Second, "message after cleanup handed", func() bool {
		got := w.handed()
		return len(got) > 0 && got[len(got)-1] == "om_after_cleanup"
	})
	if got, want := w.handed(), []string{"om_first", "om_marker", "om_after_cleanup"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("released recall restarted on immediate redelivery: %v", got)
	}
	w.h.bots.mu.Lock()
	recent := len(w.h.bots.seen)
	w.h.bots.mu.Unlock()
	if recent > botSeenKeep {
		t.Fatalf("completed history grew past its bound: %d", recent)
	}
}

func TestBotPendingDedupAndReleaseAfterRecentEviction(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(fmt.Sprintf("refused_%t", refused), func(t *testing.T) {
			w := newBotWorld(t)
			w.pair(t)
			w.topicsOn(t, topicChat)
			unblock := blockBotHandoff(t, w, "om_first")
			if refused {
				w.a.on(assistantwire.MethodFeishuMessage, func(raw json.RawMessage) (any, error) {
					if bytes.Contains(raw, []byte("om_second")) {
						return nil, &rpc.Error{Code: "fixture_refused", Message: "synthetic refusal"}
					}
					return map[string]any{}, nil
				})
			}
			event := groupEvent("om_second", "ou_me", "ordinary request", topicChat, "omt_example")
			w.send(t, event)
			for i := 0; i < botSeenKeep; i++ {
				w.send(t, groupEvent(fmt.Sprintf("om_filler_%d", i), "ou_stranger", "ordinary message", topicChat, "omt_example"))
			}
			var deliveries sync.WaitGroup
			for i := 0; i < 12; i++ {
				deliveries.Go(func() { w.send(t, event) })
			}
			deliveries.Wait()
			unblock()
			finishBotQueue(t, w)
			if got, want := w.handed(), []string{"om_first", "om_second", "om_marker"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("pending redelivery changed handoff or queue release: %v", got)
			}
		})
	}
}

func withBotEnvelopeAppID(payload []byte, appID string) []byte {
	var env map[string]any
	_ = json.Unmarshal(payload, &env)
	env["header"].(map[string]any)["app_id"] = appID
	out, _ := json.Marshal(env)
	return out
}

func TestBotConnectionCallbackRejectsReplacedSource(t *testing.T) {
	for _, sameApp := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_app_%t", sameApp), func(t *testing.T) {
			w := newBotWorld(t)
			w.pair(t)
			w.topicsOn(t, topicChat)
			oldAPI, _ := w.h.botAPI()
			oldCallback := w.h.botEventHandler(oldAPI)
			body := `{"app_secret":"refreshed-secret"}`
			if !sameApp {
				body = `{"app_id":"cli_replacement","app_secret":"replacement-secret"}`
			}
			if code, out := w.do(t, "PUT", "/api/v1/bots/feishu", body); code != 200 {
				t.Fatalf("replace connection: %d %v", code, out)
			}
			if !sameApp {
				w.a.mu.Lock()
				w.a.told = nil
				w.a.mu.Unlock()
				w.pair(t)
				w.topicsOn(t, topicChat)
			}
			api, _ := w.h.botAPI()
			if api == oldAPI {
				t.Fatal("credential update did not replace the production client")
			}
			currentCallback := w.h.botEventHandler(api)
			call := func(callback feishubot.Handler, payload []byte) {
				t.Helper()
				if _, err := callback(w.ctx, payload); err != nil {
					t.Fatal(err)
				}
			}
			unblock := blockBotHandoff(t, w, "om_first")
			w.send(t, groupEvent("om_second", "ou_me", "ordinary request", topicChat, "omt_example"))
			w.send(t, groupEvent("om_recalled", "ou_me", "obsolete request", topicChat, "omt_example"))
			// These are the callback factory used by restartBot, retained from
			// the real prior credential lifecycle; no queue state is fabricated.
			call(oldCallback, groupEvent("om_old_source", "ou_me", "delayed old event", topicChat, "omt_example"))
			call(oldCallback, withBotEnvelopeAppID(botRecallEvent(topicChat, "om_second", "message_owner"), api.AppID()))
			call(currentCallback, withBotEnvelopeAppID(groupEvent("om_wrong_header", "ou_me", "wrong app event", topicChat, "omt_example"), "cli_unrelated"))
			call(currentCallback, withBotEnvelopeAppID(botRecallEvent(topicChat, "om_second", "message_owner"), "cli_unrelated"))
			call(currentCallback, withBotEnvelopeAppID(botRecallEvent(topicChat, "om_recalled", "message_owner"), api.AppID()))
			unblock()
			finishBotQueue(t, w)
			if got, want := w.handed(), []string{"om_first", "om_second", "om_marker"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("stale callback or mismatched envelope changed the new queue: %v", got)
			}
			if w.h.messageChat("om_old_source") != "" || w.h.messageChat("om_wrong_header") != "" {
				t.Fatal("a rejected callback altered new message-chat evidence")
			}
		})
	}
}

func TestBotPendingRecallSurvivesProductionCredentialRefresh(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	w.topicsOn(t, topicChat)
	oldAPI, _ := w.h.botAPI()
	oldCallback := w.h.botEventHandler(oldAPI)
	unblock := blockBotHandoff(t, w, "om_first")
	w.send(t, groupEvent("om_recalled", "ou_me", "obsolete request", topicChat, "omt_example"))
	w.send(t, groupEvent("om_second", "ou_me", "ordinary request", topicChat, "omt_example"))
	if code, out := w.do(t, "PUT", "/api/v1/bots/feishu", `{"app_secret":"refreshed-secret"}`); code != 200 {
		t.Fatalf("refresh credentials: %d %v", code, out)
	}
	api, _ := w.h.botAPI()
	if api == oldAPI {
		t.Fatal("production credential refresh kept the old client")
	}
	if _, err := oldCallback(w.ctx, botRecallEvent(topicChat, "om_second", "message_owner")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.h.botEventHandler(api)(w.ctx, withBotEnvelopeAppID(botRecallEvent(topicChat, "om_recalled", "message_owner"), api.AppID())); err != nil {
		t.Fatal(err)
	}
	unblock()
	finishBotQueue(t, w)
	if got, want := w.handed(), []string{"om_first", "om_second", "om_marker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("credential refresh lost pending recall or delivery: %v", got)
	}
}

func botRecallEvent(chat, id, kind string) []byte {
	b, _ := json.Marshal(map[string]any{
		"schema": "2.0",
		"header": map[string]any{"event_id": "ev_recall_" + id, "event_type": "im.message.recalled_v1"},
		"event":  map[string]any{"message_id": id, "chat_id": chat, "recall_type": kind},
	})
	return b
}

// Exercise the real inbound queue: the assistant is busy taking the first
// message when the owner sends and recalls the second one.
func TestTopicQueuedRecallDoesNotStartMessage(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	w.topicsOn(t, topicChat)
	unblock := blockBotHandoff(t, w, "om_first")
	w.send(t, groupEvent("om_second", "ou_me", "obsolete request", topicChat, "omt_example"))
	w.send(t, botRecallEvent(topicChat, "om_second", "message_owner"))
	unblock()
	w.send(t, groupEvent("om_next", "ou_me", "next request", topicChat, "omt_example"))
	eventually(t, 5*time.Second, "next message handed", func() bool {
		got := w.handed()
		return len(got) > 0 && got[len(got)-1] == "om_next"
	})
	if got, want := w.handed(), []string{"om_first", "om_next"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("handed %v, want %v: the recalled queued request must not start", got, want)
	}
}

// blockBotHandoff blocks an actual assistant call, rather than bypassing the
// inbound queue or manually reserving a queue slot.
func blockBotHandoff(t *testing.T, w *botWorld, id string) func() {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	w.a.on(assistantwire.MethodFeishuMessage, func(raw json.RawMessage) (any, error) {
		var m assistantwire.FeishuMessage
		_ = json.Unmarshal(raw, &m)
		var ev struct {
			Message struct {
				ID string `json:"message_id"`
			} `json:"message"`
		}
		_ = json.Unmarshal(m.Event, &ev)
		if ev.Message.ID == id {
			close(entered)
			<-release
		}
		return map[string]any{}, nil
	})
	w.send(t, groupEvent(id, "ou_me", "first request", topicChat, "omt_example"))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first message did not enter the handoff")
	}
	return unblock
}

func finishBotQueue(t *testing.T, w *botWorld) {
	t.Helper()
	w.send(t, groupEvent("om_marker", "ou_me", "next request", topicChat, "omt_example"))
	eventually(t, 5*time.Second, "queue marker handed", func() bool {
		got := w.handed()
		return len(got) > 0 && got[len(got)-1] == "om_marker"
	})
	w.h.bots.mu.Lock()
	done := w.h.bots.line
	w.h.bots.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("queue marker did not finish")
	}
	eventually(t, 5*time.Second, "pending records cleaned", func() bool {
		w.h.bots.mu.Lock()
		defer w.h.bots.mu.Unlock()
		return len(w.h.bots.queuedMessages) == 0
	})
}

func TestBotQueuedRecallScope(t *testing.T) {
	for _, tc := range []struct {
		name, chat, id, kind string
		wantSecond           bool
	}{
		{"owner recall", topicChat, "om_second", "message_owner", false},
		{"legacy recall type", topicChat, "om_second", "", false},
		{"another opted in chat", topicOther, "om_second", "message_owner", true},
		{"private chat", "oc_p2p", "om_second", "message_owner", true},
		{"unlisted chat", "oc_unlisted", "om_second", "message_owner", true},
		{"another message", topicChat, "om_else", "message_owner", true},
		{"empty message", topicChat, "", "message_owner", true},
		{"administrator recall", topicChat, "om_second", "group_manager", true},
		{"no recall", "", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newBotWorld(t)
			w.pair(t)
			w.topicsOn(t, topicChat, topicOther)
			unblock := blockBotHandoff(t, w, "om_first")
			event := groupEvent("om_second", "ou_me", "second request", topicChat, "omt_example")
			w.send(t, event)
			w.send(t, event) // redelivery retains the same pending cancellation
			if tc.chat != "" {
				w.send(t, botRecallEvent(tc.chat, tc.id, tc.kind))
			}
			unblock()
			finishBotQueue(t, w)
			want := []string{"om_first", "om_marker"}
			if tc.wantSecond {
				want = []string{"om_first", "om_second", "om_marker"}
			}
			if got := w.handed(); !reflect.DeepEqual(got, want) {
				t.Fatalf("handed %v, want %v", got, want)
			}
			if w.a.wasTold(assistantwire.EventFeishuRecalled) {
				t.Fatal("a queued or mismatched recall escaped to the assistant")
			}
		})
	}
}

func TestBotQueuedRecallKeepsReceivePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, sender, chat, thread string
		app, strict                bool
	}{
		{"stranger", "ou_stranger", topicChat, "omt_example", false, false},
		{"bot sender", "ou_me", topicChat, "omt_example", true, false},
		{"unlisted group", "ou_me", "oc_unlisted", "omt_example", false, false},
		{"outside topic", "ou_me", topicChat, "", false, false},
		{"required mention missing", "ou_me", topicOther, "omt_example", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newBotWorld(t)
			w.pair(t)
			w.topicsOn(t, topicChat, topicOther)
			if tc.strict {
				if code, out := w.do(t, "PUT", "/api/v1/bots/feishu", `{"topic_chats":[{"chat_id":"oc_topic"},{"chat_id":"oc_other","require_owner_mention":true}]}`); code != 200 {
					t.Fatalf("strict policy: %d %v", code, out)
				}
			}
			unblock := blockBotHandoff(t, w, "om_first")
			for _, id := range []string{"om_recalled", "om_ordinary"} {
				ev := groupEvent(id, tc.sender, "ordinary request", tc.chat, tc.thread)
				if tc.app {
					ev = bytes.Replace(ev, []byte(`"sender_type":"user"`), []byte(`"sender_type":"app"`), 1)
				}
				w.send(t, ev)
			}
			w.send(t, botRecallEvent(tc.chat, "om_recalled", "message_owner"))
			unblock()
			finishBotQueue(t, w)
			if got, want := w.handed(), []string{"om_first", "om_marker"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("denied messages reached the assistant: %v", got)
			}
			if w.a.wasTold(assistantwire.EventFeishuRecalled) {
				t.Fatal("a never-admitted message acquired assistant recall permission")
			}
		})
	}
}

func TestBotQueuedRecallPrivateChat(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	w.topicsOn(t, topicChat)
	unblock := blockBotHandoff(t, w, "om_first")
	w.send(t, botMessageEvent("om_dm", "ou_me", "text", textContent("obsolete private request"), nil))
	w.send(t, botRecallEvent("oc_p2p", "om_dm", "message_owner"))
	unblock()
	finishBotQueue(t, w)
	if got, want := w.handed(), []string{"om_first", "om_marker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("handed %v, want %v", got, want)
	}
}

func TestBotQueuedRecallIdentityBinding(t *testing.T) {
	for _, identity := range []string{"bot", "owner"} {
		t.Run(identity, func(t *testing.T) {
			w := newBotWorld(t)
			w.pair(t)
			w.topicsOn(t, topicChat)
			unblock := blockBotHandoff(t, w, "om_first")
			w.send(t, groupEvent("om_second", "ou_me", "second request", topicChat, "omt_example"))
			b := &w.h.bots
			b.mu.Lock()
			api, owner := b.api, b.set.Owner
			if identity == "bot" {
				b.api = feishubot.New("cli_other", "example-secret", w.api.srv.URL)
			} else {
				b.set.Owner = &BotOwner{OpenID: "ou_other_owner", ChatID: "oc_p2p"}
			}
			b.mu.Unlock()
			w.send(t, botRecallEvent(topicChat, "om_second", "message_owner"))
			w.send(t, botRecallEvent(topicChat, "om_first", "message_owner"))
			b.mu.Lock()
			for _, pending := range b.queuedMessages {
				if pending.recalled {
					t.Error("another bot or owner cancelled the original pending message")
				}
			}
			b.api, b.set.Owner = api, owner
			b.mu.Unlock()
			if w.a.wasTold(assistantwire.EventFeishuRecalled) {
				t.Error("another bot or owner recalled an original handed message")
			}
			unblock()
			finishBotQueue(t, w)
			if got, want := w.handed(), []string{"om_first", "om_second", "om_marker"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("handed %v, want %v", got, want)
			}
		})
	}
}

func TestBotRecallDuringAndAfterHandoff(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	w.topicsOn(t, topicChat, topicOther)
	unblock := blockBotHandoff(t, w, "om_first")
	w.send(t, botRecallEvent(topicOther, "om_first", "message_owner"))
	w.send(t, botRecallEvent("oc_p2p", "om_first", "message_owner"))
	if w.a.wasTold(assistantwire.EventFeishuRecalled) {
		t.Fatal("another opted-in chat recalled the handed message")
	}
	w.send(t, botRecallEvent(topicChat, "om_first", "message_owner"))
	if !w.a.wasTold(assistantwire.EventFeishuRecalled) {
		t.Fatal("recall during handoff did not reach the assistant")
	}
	unblock()
	finishBotQueue(t, w)
	w.a.mu.Lock()
	w.a.told = nil
	w.a.mu.Unlock()
	w.send(t, botRecallEvent(topicOther, "om_first", "message_owner"))
	w.send(t, botRecallEvent("oc_p2p", "om_first", "message_owner"))
	if w.a.wasTold(assistantwire.EventFeishuRecalled) {
		t.Fatal("another opted-in chat recalled a completed handoff")
	}
	w.send(t, botRecallEvent(topicChat, "om_first", "message_owner"))
	if !w.a.wasTold(assistantwire.EventFeishuRecalled) {
		t.Fatal("recall after handoff did not reach the assistant")
	}
}

func TestBotQueuedMessageDoesNotMoveToReplacementIdentity(t *testing.T) {
	for _, identity := range []string{"bot", "owner"} {
		t.Run(identity, func(t *testing.T) {
			w := newBotWorld(t)
			w.pair(t)
			w.topicsOn(t, topicChat)
			unblock := blockBotHandoff(t, w, "om_first")
			w.send(t, groupEvent("om_second", "ou_me", "old request", topicChat, "omt_example"))
			b := &w.h.bots
			b.mu.Lock()
			api, owner, done := b.api, b.set.Owner, b.line
			if identity == "bot" {
				b.api = feishubot.New("cli_other", "example-secret", w.api.srv.URL)
			} else {
				b.set.Owner = &BotOwner{OpenID: "ou_other_owner", ChatID: "oc_p2p"}
			}
			b.mu.Unlock()
			unblock()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("old queue did not finish")
			}
			b.mu.Lock()
			b.api, b.set.Owner = api, owner
			b.mu.Unlock()
			finishBotQueue(t, w)
			if got, want := w.handed(), []string{"om_first", "om_marker"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("old message moved to a replacement identity: %v", got)
			}
		})
	}
}

func TestBotQueuedMessageKeepsSameBotReconnect(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	w.topicsOn(t, topicChat)
	unblock := blockBotHandoff(t, w, "om_first")
	w.send(t, groupEvent("om_recalled", "ou_me", "obsolete request", topicChat, "omt_example"))
	w.send(t, groupEvent("om_second", "ou_me", "ordinary request", topicChat, "omt_example"))
	b := &w.h.bots
	b.mu.Lock()
	b.api = feishubot.New("cli_test", "refreshed-secret", w.api.srv.URL)
	b.mu.Unlock()
	w.send(t, botRecallEvent(topicChat, "om_recalled", "message_owner"))
	unblock()
	finishBotQueue(t, w)
	if got, want := w.handed(), []string{"om_first", "om_second", "om_marker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("same-bot reconnect changed queued delivery or recall: %v", got)
	}
}

func TestBotHandedDMRecallIdentityBinding(t *testing.T) {
	w := newBotWorld(t)
	w.pair(t)
	w.topicsOn(t, topicChat)
	w.send(t, botMessageEvent("om_dm", "ou_me", "text", textContent("private request"), nil))
	finishBotQueue(t, w)
	b := &w.h.bots
	b.mu.Lock()
	api := b.api
	b.api = feishubot.New("cli_other", "example-secret", w.api.srv.URL)
	b.mu.Unlock()
	w.send(t, botRecallEvent("oc_p2p", "om_dm", "message_owner"))
	if w.a.wasTold(assistantwire.EventFeishuRecalled) {
		t.Fatal("a replacement bot recalled the original private message")
	}
	b.mu.Lock()
	b.api = api
	b.mu.Unlock()
	w.send(t, botRecallEvent("oc_p2p", "om_dm", "message_owner"))
	if !w.a.wasTold(assistantwire.EventFeishuRecalled) {
		t.Fatal("the original bot's private recall was lost")
	}
}
