package larkgate

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// FetchCardContext reads only the message's own chat and, for a reply, the message it answers through read-only
// lark-cli calls. ok is false when metadata could not be read; the caller then keeps its fallback summary instead of
// delaying the card. No gate decision depends on it.
func (*Gate) FetchCardContext(ctx context.Context, cfg proto.AssistantLark, kind, chatID, messageID string) (chat, replySender, replyText string, inTopic, ok bool) {
	if kind == "" {
		return "", "", "", false, false
	}
	bin := "lark-cli"
	if cfg.CLI != nil && cfg.CLI.Bin != "" {
		bin = cfg.CLI.Bin
	}
	if !filepath.IsAbs(bin) {
		p, err := exec.LookPath(bin)
		if err != nil {
			return "", "", "", false, false
		}
		bin = p
	}
	read := func(apiPath string, out any) bool {
		cli := cfg.CLI
		if cli == nil {
			cli = &proto.LarkCLI{}
		}
		args := append(cliProfileArgs(cli), "api", "GET", apiPath)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = cliEnv(bin)
		data, err := cmd.Output()
		if err != nil || json.Unmarshal(data, out) != nil {
			return false
		}
		return true
	}
	chat = chatID
	if kind == "reply" {
		var answer struct {
			Message struct {
				ChatID   string `json:"chat_id"`
				ThreadID string `json:"thread_id"`
				Sender   struct {
					Name string `json:"name"`
					ID   string `json:"id"`
				} `json:"sender"`
				Body struct {
					Content string `json:"content"`
				} `json:"body"`
			} `json:"message"`
		}
		if !read("/open-apis/im/v1/messages/"+messageID, &answer) {
			return "", "", "", false, false
		}
		text := nestedText(answer.Message.Body.Content)
		if text == "" {
			return "", "", "", false, false
		}
		if answer.Message.ChatID != "" {
			chat = answer.Message.ChatID
		}
		sender := answer.Message.Sender.Name
		if sender == "" {
			sender = answer.Message.Sender.ID
		}
		replySender = sender
		replyText = excerpt(text, 160)
		inTopic = answer.Message.ThreadID != ""
		var info struct {
			Chat struct {
				Name string `json:"name"`
			} `json:"chat"`
		}
		if chat != "" && read("/open-apis/im/v1/chats/"+chat, &info) && info.Chat.Name != "" {
			chat = info.Chat.Name
		}
		return chat, replySender, replyText, inTopic, chat != ""
	}
	var info struct {
		Chat struct {
			Name string `json:"name"`
		} `json:"chat"`
	}
	if chat == "" || !read("/open-apis/im/v1/chats/"+chat, &info) {
		return "", "", "", false, false
	}
	if info.Chat.Name != "" {
		chat = info.Chat.Name
	}
	return chat, "", "", false, true
}

// FormatMessageSummary renders a send or reply without its HTTP route or internal JSON fields. ok is false when
// metadata was unavailable or body carries no display text, in which case the caller keeps the old summary.
func (*Gate) FormatMessageSummary(kind, body, chat, replySender, replyText string, inTopic, ctxOK bool, lang i18n.Lang) (string, bool) {
	if kind == "" || !ctxOK {
		return "", false
	}
	text := bodyText(body)
	if text == "" {
		return "", false
	}
	var b strings.Builder
	if kind == "send" {
		b.WriteString(i18n.T(lang, "node-larkgate.send_to", i18n.Args{"chat": chat}))
	} else if replySender != "" {
		b.WriteString(i18n.T(lang, "node-larkgate.reply_to", i18n.Args{"chat": chat, "sender": replySender}))
	} else {
		b.WriteString(i18n.T(lang, "node-larkgate.reply_in_chat", i18n.Args{"chat": chat}))
	}
	if inTopic {
		b.WriteString(i18n.T(lang, "node-larkgate.topic_suffix"))
	}
	if kind == "reply" && replyText != "" {
		b.WriteString("\n" + i18n.T(lang, "node-larkgate.original", i18n.Args{"text": replyText}))
	}
	b.WriteString("\n" + i18n.T(lang, "node-larkgate.outgoing", i18n.Args{"text": excerpt(text, 2000)}))
	return b.String(), true
}

func bodyText(body string) string {
	var v map[string]any
	if json.Unmarshal([]byte(body), &v) != nil {
		return ""
	}
	for _, key := range []string{"text", "content"} {
		if s, ok := v[key].(string); ok {
			if key == "content" {
				return nestedText(s)
			}
			return strings.TrimSpace(s)
		}
		if obj, ok := v[key].(map[string]any); ok {
			if s, ok := obj["text"].(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func nestedText(content string) string {
	var v struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(content), &v) == nil && v.Text != "" {
		return strings.TrimSpace(v.Text)
	}
	return strings.TrimSpace(content)
}

func excerpt(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return text
}
