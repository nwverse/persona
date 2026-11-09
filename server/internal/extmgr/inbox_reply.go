package extmgr

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/mail"
	"github.com/nwverse/persona/server/internal/rpc"
)

// inboxSendWait bounds one reply's send.
const inboxSendWait = 2 * time.Minute

// errReplyInDoubt: a send under this id was cut off before (the process stopped or a link on the way dropped during
// it, or it ran out of time): it may have gone out, so it is not sent again.
var errReplyInDoubt = &rpc.Error{Code: assistantwire.CodeAssistantInDoubt, Message: "这条回复上次发送时中断了，可能已经发出去了：先看看再决定要不要重发"}

// sendInboxOnce sends one of the manager's replies at most once per send id, with the guard the apps' messages have
// (sent_messages, kept for good): the same id after it went out is already sent (nothing goes again), after a send that was
// cut off it is in doubt (errReplyInDoubt). A send that failed is forgotten: asked again, it is sent. A send once
// begun runs to its end (inboxSendWait) even when the manager stops waiting or drops: what it did is recorded for the
// retry. Without an id or the database it is just sent.
func (h *Manager) sendInboxOnce(ctx context.Context, sendID, source string, send func(context.Context) error) (already bool, err error) {
	st, _ := h.store.(sendStore)
	if sendID == "" || st == nil {
		return false, send(ctx)
	}
	id := "inbox:" + sendID
	state, err := st.ClaimSend(ctx, id, "inbox", source)
	switch {
	case err != nil:
		return false, err
	case state == sendDelivered:
		return true, nil
	case state != "":
		return false, errReplyInDoubt
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inboxSendWait)
	defer cancel()
	err = send(sctx)
	outcome := inboxSendOutcome(err)
	if serr := st.SettleSend(context.WithoutCancel(ctx), id, outcome); serr != nil {
		h.logf("inbox: settle reply %s: %v", sendID, serr)
	}
	if outcome == sendUnknown {
		return false, &rpc.Error{Code: assistantwire.CodeAssistantInDoubt, Message: err.Error()}
	}
	return false, err
}

// inboxSendOutcome is what a reply's error says of it (sent_messages' state): cut off (the time ran out, a link on the
// way dropped) it may have gone out; refused it did not ("": forgotten).
func inboxSendOutcome(err error) string {
	switch {
	case err == nil:
		return sendDelivered
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), errors.Is(err, rpc.ErrDropped):
		return sendUnknown
	}
	return ""
}

// replyMail answers a mail from the account that received it.
func (h *Manager) replyMail(ctx context.Context, ref assistantwire.InboxMailRef, text string) error {
	if h.mail == nil {
		return errCannotReply
	}
	orig, err := h.mail.GetMessage(ctx, ref.AccountID, ref.UID, ref.Mailbox)
	if err != nil {
		return err
	}
	return h.mail.Send(ctx, mailReply(h.userLang(), ref.AccountID, orig, text))
}

// mailReply is a reply to orig: Re: subject, threading headers, the original quoted below.
func mailReply(lang i18n.Lang, accountID string, orig *mail.MailMessage, text string) mail.SendRequest {
	subject := orig.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	refs := append(append([]string{}, orig.References...), orig.MessageID)
	var quoted []string
	for i, l := range strings.Split(orig.Text, "\n") {
		if i >= 40 {
			break
		}
		quoted = append(quoted, "> "+l)
	}
	from := orig.From.Name
	if from == "" {
		from = orig.From.Email
	}
	body := text + "\n\n" + i18n.T(lang, "hub-inbox.reply_attribution", i18n.Args{"date": orig.Date, "from": from}) + "\n" + strings.Join(quoted, "\n")
	return mail.SendRequest{AccountID: accountID, To: []mail.Address{orig.From}, Subject: subject, Text: body,
		InReplyTo: orig.MessageID, References: mail.MessageIDs(strings.Join(refs, " "))}
}
