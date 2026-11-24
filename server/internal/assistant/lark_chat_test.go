package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
)

// The Feishu chat is the lark module's (lark_chat.go): these tests reach it as the core does, through the port, and
// read its state as it is stored (the settings row botStateKey, in the shape below).

// botStateKey is the chat's state, the settings row the module keeps it in.
const botStateKey = "bots.feishu.state"

// botState is the chat's state as it is stored (the module's chat_state.go): what these tests read and seed of it.
type botState struct {
	Current botTarget              `json:"current"`
	Threads map[string]*botThread  `json:"threads,omitempty"`
	Runs    []map[string]any       `json:"runs,omitempty"`
	Seen    []string               `json:"seen,omitempty"`
	Cards   map[string]*botCardRef `json:"cards,omitempty"`
	Pairing *string                `json:"pairing,omitempty"`
	Topics  map[string]*struct {
		MessageID string    `json:"message_id"`
		At        time.Time `json:"at"`
	} `json:"topics,omitempty"`
	Board      *botBoardRef   `json:"board,omitempty"`
	Companions []CompanionBot `json:"companions,omitempty"`
	NudgeOffAt time.Time      `json:"nudge_off_at,omitzero"`
}

// botThread is a message the bot posted for a target, as the chat's state keeps it.
type botThread struct {
	Target botTarget `json:"target"`
	Title  string    `json:"title,omitempty"`
	At     time.Time `json:"at"`
}

// botCardRef is a card kept in the chat's state.
type botCardRef = assistantwire.ChatCardRef

// setLarkNow sets the chat's clock (nil: the time now).
func (s *Service) setLarkNow(now func() time.Time) {
	s.lark.mu.Lock()
	defer s.lark.mu.Unlock()
	s.lark.now = now
}

// chatCard is the card asking interaction id in the chat's records.
func chatCard(s *Service, id string) (botCard, bool) {
	var c botCard
	ok := false
	s.larkModule().Book(context.Background(), func(b assistantwire.ChatBook) { c, ok = b.Card(id) })
	return c, ok
}

// chatCards are the cards in the chat's records.
func chatCards(s *Service) map[string]botCard {
	var cards map[string]botCard
	s.larkModule().Book(context.Background(), func(b assistantwire.ChatBook) { cards = b.Cards() })
	return cards
}

// keepChatCard records the card asking interaction id (nil forgets it).
func keepChatCard(s *Service, id string, c *botCard) {
	s.larkModule().Book(context.Background(), func(b assistantwire.ChatBook) { b.KeepCard(id, c) })
}

// chatCardRef is the card asking interaction id as the chat's state keeps it.
func chatCardRef(s *Service, id string) (botCardRef, bool) {
	var ref botCardRef
	ok := false
	s.larkModule().Book(context.Background(), func(b assistantwire.ChatBook) { ref, ok = b.CardRef(id) })
	return ref, ok
}

// feishuSay hands ev to the chat as the hub does (feishu.message), action being the owner's action for what it
// answers ("" none), and returns once the chat has handled it. With st the bot's status is st from then on (the
// hub's feishu.status); nil keeps the one the fake answers.
func feishuSay(t testing.TB, s *Service, f *fakeHub, st *FeishuStatus, ev botcard.MessageEvent, action string) {
	t.Helper()
	if st != nil {
		cur := *st
		f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) { return cur, nil })
		s.larkModule().ForgetStatus()
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FeishuMessage(context.Background(), FeishuMessage{Event: raw, Action: action}); err != nil {
		t.Fatalf("feishu.message %s: %v", ev.Message.MessageID, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.larkModule().Wait(ctx); err != nil {
		t.Fatalf("the chat did not handle %s: %v", ev.Message.MessageID, err)
	}
}

// botSay is the owner's text message mid in the bot's private chat, replying to parent in the thread rooted at root,
// handed over with the bot's switches ff (the status's owner and pairing kept): feishuSay.
func botSay(t testing.TB, s *Service, f *fakeHub, ff FeishuFeatures, mid, text, root, parent string) {
	t.Helper()
	feishuSay(t, s, f, withFeatures(f, ff), p2pText(mid, text, root, parent), "")
}

// withFeatures is the status the fake answers now, with the bot's switches ff.
func withFeatures(f *fakeHub, ff FeishuFeatures) *FeishuStatus {
	var st FeishuStatus
	f.mu.Lock()
	answer := f.apps[AppFeishu+" "+FeishuStatusGet]
	f.mu.Unlock()
	if answer != nil {
		if v, err := answer(nil); err == nil {
			raw, _ := json.Marshal(v)
			_ = json.Unmarshal(raw, &st)
		}
	}
	st.Features = ff
	return &st
}

// p2pText is a text message mid in the bot's private chat, replying to parent in the thread rooted at root.
func p2pText(mid, text, root, parent string) botcard.MessageEvent {
	var ev botcard.MessageEvent
	ev.Message.MessageID, ev.Message.ChatType, ev.Message.MessageType = mid, "p2p", "text"
	ev.Message.RootID, ev.Message.ParentID = root, parent
	content, _ := json.Marshal(map[string]string{"text": text})
	ev.Message.Content = string(content)
	ev.Sender.SenderType = "user"
	return ev
}
