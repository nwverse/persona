package assistant

import (
	"context"
	"testing"
)

// A command is still a command when a line break, a tab or a full-width space follows it, or when it starts with a
// full-width slash; what comes after it goes on whole, line breaks inside included (#3897).
func TestBotCommandSplitsOnAnyWhiteSpace(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, text string
		handled    bool
		rest       string
	}{
		{"line break", "/new\n第一句话", false, "第一句话"},
		{"full-width space", "/new\u3000第一句话", false, "第一句话"},
		{"tab", "/new\t第一句话", false, "第一句话"},
		{"full-width slash", "／new 第一句话", false, "第一句话"},
		{"white space around", " \u3000/new 第一句话 \n", false, "第一句话"},
		{"multi-line rest", "/new\n第一行\n\n  第二行\n", false, "第一行\n\n  第二行"},
		{"bare after a line break", "/new\n", true, ""},
		{"stop alias", "/停止", true, ""},
		{"short stop alias", "/停", true, ""},
		{"full-width stop", "／stop", true, ""},
		{"stop then a line break", "/stop\n", true, ""},
		{"unknown command goes on as it is", "/compact", false, "/compact"},
		{"a word glued to the command", "/stopnow", false, "/stopnow"},
		{"a slash mid-text", "看看 /stop 是什么", false, "看看 /stop 是什么"},
		{"a path", "/tmp/a 是什么", false, "/tmp/a 是什么"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newTestService(t, newFakeHub().withMachine())
			h, rest := s.botCommand(ctx, FeishuFeatures{Main: true}, "om", c.text, "", "")
			if h != c.handled || rest != c.rest {
				t.Fatalf("botCommand(%q) = %v, %q; want %v, %q", c.text, h, rest, c.handled, c.rest)
			}
		})
	}
}

func TestParseBotCommand(t *testing.T) {
	for _, c := range []struct {
		text, cmd, rest string
		ok              bool
	}{
		{"/new 第一句话", "/new", "第一句话", true},
		{"/new\n第一句话", "/new", "第一句话", true},
		{"/new\r\n第一句话", "/new", "第一句话", true},
		{"/new\u3000第一句话", "/new", "第一句话", true},
		{"/new\u3000\u3000第一句话\u3000", "/new", "第一句话", true},
		{"／new 第一句话", "/new", "第一句话", true},
		{"／STOP", "/stop", "", true},
		{"\u3000\n /help", "/help", "", true},
		{"/new 第一行\n第二行\n\n第三行", "/new", "第一行\n第二行\n\n第三行", true},
		{"/agents\tweb\u3000app", "/agents", "web\u3000app", true},
		{"/", "/", "", true},
		{"/tmp/a 是什么", "/tmp/a", "是什么", true},
		{"看看 /stop 是什么", "", "", false},
		{"第一行\n/stop", "", "", false},
		{"停止", "", "", false},
		{"\\stop", "", "", false},
		{"", "", "", false},
		{"\u3000\n", "", "", false},
	} {
		cmd, rest, ok := parseBotCommand(c.text)
		if cmd != c.cmd || rest != c.rest || ok != c.ok {
			t.Errorf("parseBotCommand(%q) = %q, %q, %v; want %q, %q, %v", c.text, cmd, rest, ok, c.cmd, c.rest, c.ok)
		}
	}
}
