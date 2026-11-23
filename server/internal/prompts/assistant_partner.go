package prompts

// Partner hubs (docs/server/partner-hubs.md): what a conversation is told of the messages between this hub's assistant
// and a paired hub's. Language is the owner's (the assistant's answerLanguage: "" Chinese, "English", "日本語").

// PartnerMessage is a message the assistant of partner hub Hub (HubID) sent, as the main conversation gets it:
// MessageID is what an answer names as reply_to, Text the other assistant's words.
type PartnerMessage struct {
	Language                    string
	Hub, HubID, MessageID, Text string
}

func RenderPartnerMessage(d PartnerMessage) string { return render("assistant/partner_message", d) }

// PartnerReply is partner hub Hub's answer to a message this hub sent, as the conversation that sent it gets it: Sent
// is how that message began, MessageID the answer's own (what an answer to it names as reply_to).
type PartnerReply struct {
	Language                          string
	Hub, HubID, MessageID, Sent, Text string
}

func RenderPartnerReply(d PartnerReply) string { return render("assistant/partner_reply", d) }

// PartnerNotice tells the conversation that sent a message to partner hub Hub what became of it (Event): declined (the
// owner said no on the card), expired (nobody allowed it in time), stale (the hub could not be reached for days),
// refused (the hub refused it, Code), unpaired (the hubs are no longer paired), undelivered (the hub took it but could
// not deliver it, Code). Excerpt is how the message began.
type PartnerNotice struct {
	Language                         string
	Event, Hub, HubID, Code, Excerpt string
}

func RenderPartnerNotice(d PartnerNotice) string { return render("assistant/partner_notice", d) }
