package extmgr

import "github.com/nwverse/persona/server/internal/feishubot"

// A message belongs to the bot and owner that received it, in one chat.
// The same id in another chat or after replacing the bot cannot recall it.
type botMessageRef struct {
	bot, domain     string
	owner, chat, id string
}

// Credentials may be refreshed without changing the bot. Keep the identity
// stable across those reconnects, while separating apps and API domains.
func botMessageReference(api *feishubot.Client, owner, chat, id string) botMessageRef {
	if api == nil {
		return botMessageRef{}
	}
	return botMessageRef{bot: api.AppID(), domain: api.Domain(), owner: owner, chat: chat, id: id}
}

type botQueuedMessage struct {
	ref      botMessageRef
	recalled bool
	handed   bool
}

// Unknown DM recalls still reach the assistant (they may precede delivery),
// but a known message from another context must not use that fallback: the
// assistant's recall payload carries only the message id.
func (b *botHub) messageInOtherContextLocked(ref botMessageRef) bool {
	for known := range b.queuedMessages {
		if known.id == ref.id && known != ref {
			return true
		}
	}
	for known := range b.handedMessages {
		if known.id == ref.id && known != ref {
			return true
		}
	}
	return false
}

// queueMessageLocked records ownership before the goroutine waits in line.
// This is recall evidence only: botMessage still applies the current receive
// policy after the wait, including topic commands and required mentions.
func (b *botHub) queueMessageLocked(ev feishubot.MessageEvent) *botQueuedMessage {
	msg := ev.Message
	if b.api == nil || b.set.Owner == nil || ev.Sender.SenderType != "user" ||
		ev.Sender.SenderID.OpenID != b.set.Owner.OpenID ||
		!botChatAllowed(b.set, msg.ChatID) || msg.ChatType != "p2p" && msg.ThreadID == "" {
		return nil
	}
	ref := botMessageReference(b.api, b.set.Owner.OpenID, msg.ChatID, msg.MessageID)
	queued := &botQueuedMessage{ref: ref}
	if b.queuedMessages == nil {
		b.queuedMessages = make(map[botMessageRef]*botQueuedMessage)
	}
	b.queuedMessages[ref] = queued
	return queued
}

// beginBotHandoff and botRecalled share the lock: a recall either cancels a
// pending handoff or is forwarded to the assistant's existing recall handling.
func (h *Manager) beginBotHandoff(api *feishubot.Client, ev feishubot.MessageEvent, inThread bool) bool {
	b := &h.bots
	b.mu.Lock()
	defer b.mu.Unlock()
	ref := botMessageReference(api, ev.Sender.SenderID.OpenID, ev.Message.ChatID, ev.Message.MessageID)
	if b.api == nil || b.set.Owner == nil || ref != botMessageReference(b.api, b.set.Owner.OpenID, ev.Message.ChatID, ev.Message.MessageID) {
		return false
	}
	if queued := b.queuedMessages[ref]; queued != nil {
		if queued.recalled {
			return false
		}
		queued.handed = true
	}
	if inThread || ev.Message.ChatType == "p2p" {
		if b.handedMessages == nil {
			b.handedMessages = make(map[botMessageRef]bool)
		}
		if _, exists := b.handedMessages[ref]; !exists {
			b.handedMessages[ref] = inThread
			b.handedMessageOrder = append(b.handedMessageOrder, ref)
			if len(b.handedMessageOrder) > botMsgChatKeep {
				delete(b.handedMessages, b.handedMessageOrder[0])
				b.handedMessageOrder = b.handedMessageOrder[1:]
			}
		}
	}
	return true
}
