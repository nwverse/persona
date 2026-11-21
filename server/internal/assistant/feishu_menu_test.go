package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// The bot's menu (feishu_menu.go): an entry's key is one of the bot's commands, answered in the private chat as if
// the owner had sent it; a key it does not know is nothing, and a workbench entry with the workbench off says how to
// turn it on.
func TestBotMenuRunsItsCommand(t *testing.T) {
	e := newWorkbenchEnv(t, workbenchOnFeatures)
	ctx := context.Background()
	posts := func() int { p, _, _, _ := e.counts(); return p }
	if err := e.s.FeishuMenu(ctx, FeishuMenu{Key: "today"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the today card", func() bool { return posts() == 1 })
	if card := e.postJSON(0); !strings.Contains(card, `"template":"wathet"`) && !strings.Contains(card, `"template":"blue"`) {
		t.Fatalf("the menu's today: %s", card)
	}
	e.mu.Lock()
	reply := e.posts[0]["reply_to"]
	e.mu.Unlock()
	if reply != "" && reply != nil {
		t.Errorf("the menu's card replies to %v: there is no message to reply to", reply)
	}

	if err := e.s.FeishuMenu(ctx, FeishuMenu{Key: "nonsense"}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.FeishuMenu(ctx, FeishuMenu{Key: "help"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the help card", func() bool { return posts() == 2 })

	off := newWorkbenchEnv(t, FeishuFeatures{Main: true, Agents: true})
	if err := off.s.FeishuMenu(ctx, FeishuMenu{Key: "home"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the note that the workbench is off", func() bool {
		off.mu.Lock()
		defer off.mu.Unlock()
		return len(off.texts) == 1
	})
	off.mu.Lock()
	note, _ := off.texts[0]["text"].(string)
	off.mu.Unlock()
	if !strings.Contains(note, "工作台") {
		t.Fatalf("note = %q", note)
	}
	if p, _, _, _ := off.counts(); p != 0 {
		t.Fatalf("posted %d cards with the workbench off", p)
	}
}

// A press takes its place in the chat's line, as a message does, and is answered in turn: a message sent right after
// it waits for it (here, its card held up on the way out), so a /new pressed and a message sent at once are answered
// in the order they came. A press made again with the same event id (a handover cut off and repeated) is answered once.
func TestBotMenuTakesItsPlaceInLine(t *testing.T) {
	e := newWorkbenchEnv(t, workbenchOnFeatures)
	ctx := context.Background()
	help := i18n.T("", "assistant-feishu_cards.help_title")
	release := make(chan struct{})
	var mu sync.Mutex
	var cards []string
	e.f.answerAppBlocking(AppFeishu, FeishuPost, func(p json.RawMessage) (any, error) {
		var m map[string]any
		_ = json.Unmarshal(p, &m)
		card, _ := m["card"].(string)
		if strings.Contains(card, help) {
			<-release
		}
		mu.Lock()
		cards = append(cards, card)
		n := len(cards)
		mu.Unlock()
		return map[string]any{"message_id": fmt.Sprintf("om_line_%d", n)}, nil
	})
	if err := e.s.FeishuMenu(ctx, FeishuMenu{Key: "help", EventID: "ev_help"}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.FeishuMenu(ctx, FeishuMenu{Key: "help", EventID: "ev_help"}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.FeishuMessage(ctx, feishuEvent("om_today", "/today")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	early := len(cards)
	mu.Unlock()
	close(release)
	waitFor(t, "the help and today cards", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(cards) >= 2
	})
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if early != 0 || len(cards) != 2 || !strings.Contains(cards[0], help) || strings.Contains(cards[1], help) {
		t.Fatalf("%d card(s) before the press was through, then %d: the press's help card first, once, then the message's", early, len(cards))
	}
}

// A press waits for the message before it: the message's card held up on the way out, the press's card comes after.
func TestBotMenuWaitsForTheMessageBeforeIt(t *testing.T) {
	e := newWorkbenchEnv(t, workbenchOnFeatures)
	ctx := context.Background()
	help := i18n.T("", "assistant-feishu_cards.help_title")
	release := make(chan struct{})
	var mu sync.Mutex
	var cards []string
	e.f.answerAppBlocking(AppFeishu, FeishuPost, func(p json.RawMessage) (any, error) {
		var m map[string]any
		_ = json.Unmarshal(p, &m)
		card, _ := m["card"].(string)
		if !strings.Contains(card, help) {
			<-release // the message's card
		}
		mu.Lock()
		cards = append(cards, card)
		n := len(cards)
		mu.Unlock()
		return map[string]any{"message_id": fmt.Sprintf("om_line_%d", n)}, nil
	})
	if err := e.s.FeishuMessage(ctx, feishuEvent("om_today", "/today")); err != nil {
		t.Fatal(err)
	}
	if err := e.s.FeishuMenu(ctx, FeishuMenu{Key: "help", EventID: "ev_help_after"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	early := len(cards)
	mu.Unlock()
	close(release)
	waitFor(t, "the today and help cards", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(cards) == 2
	})
	mu.Lock()
	defer mu.Unlock()
	if early != 0 || strings.Contains(cards[0], help) || !strings.Contains(cards[1], help) {
		t.Fatalf("%d card(s) before the message was through; want the message's card first, then the press's", early)
	}
}
