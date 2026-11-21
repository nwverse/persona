package assistant

import (
	"context"
	"strings"
	"testing"
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
