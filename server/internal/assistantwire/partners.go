package assistantwire

import "time"

// Partner hubs (docs/server/partner-hubs.md): two independent hubs, paired by their owner, whose assistants send each
// other messages. The hubs carry the messages over one pinned link and nothing else: no group, no records, no App
// requests, no node streams. The managers own everything about a message: the owner's confirmation, scrubbing, the
// durable queue keyed by message_id, delivery into a conversation, replies and their states. The hub stamps who a
// message came from (the link's credential) and never reads the text.

// CapabilityPartnerHubs is named in manager.hello by a manager that takes partner.receive and sends with partner.send.
// The hub delivers a partner's message only to a default manager that names it, and lists the partner_hubs feature
// only then.
const CapabilityPartnerHubs = "partner_hubs"

const (
	// MethodPartnerSend is partner.send PartnerSend → PartnerResult (a manager → the hub): hand one envelope to a paired
	// hub over the live link. Only the hub's default manager may call it. With no live link the call fails with
	// CodePartnerOffline (keep the envelope queued and send it again later, with the same message_id); a hub that is
	// not paired (or partner hubs are off) fails with CodePartnerUnknown; a partner whose manager cannot take it now
	// fails with CodePartnerUnavailable. An answer that never came is in doubt: send again, the partner deduplicates.
	MethodPartnerSend = "partner.send"
	// MethodPartnerHubs is partner.hubs {} → PartnerHubs (a manager → the hub): the paired hubs now.
	MethodPartnerHubs = "partner.hubs"
	// MethodPartnerReceive is partner.receive PartnerReceive → PartnerResult (the hub → its default manager): an
	// envelope a paired hub sent. FromHub comes from the link's credential and FromName from the hub's own pairing
	// record, never from the envelope. The manager answers once it has stored the envelope (accepted), or already had
	// it (duplicate), or will not take it (refused, with a PartnerCode*); it delivers it after answering.
	MethodPartnerReceive = "partner.receive"
	// EventPartnerHubs is partner.changed PartnerHubs (one way, the hub → its default manager): the paired hubs or
	// their links changed. The hub also sends it right after the manager's hello, so a manager always has the list.
	EventPartnerHubs = "partner.changed"
)

// Error codes partner.send fails with (rpc.Error.Code).
const (
	CodePartnerOffline     = "partner_offline"     // no live link to that hub now: keep it queued
	CodePartnerUnknown     = "partner_unknown"     // not a paired hub (unpaired, or partner hubs are off): stop
	CodePartnerUnavailable = "partner_unavailable" // the partner's hub has no manager that takes it now: keep it queued
)

// Envelope kinds.
const (
	PartnerKindMessage = "message" // an assistant's message to the partner's main conversation
	PartnerKindReply   = "reply"   // an answer to a message the partner sent (ReplyTo)
	PartnerKindStatus  = "status"  // what became of a message the partner sent (ReplyTo, State, Code); no text
)

// States a status envelope reports for the message it names.
const (
	PartnerStateDelivered = "delivered" // it is in the receiving conversation (or durably waiting for it)
	PartnerStateFailed    = "failed"    // it could not be delivered there (Code says why)
)

// PartnerResult.Status values.
const (
	PartnerAccepted  = "accepted"  // stored: the receiving manager now answers for it
	PartnerDuplicate = "duplicate" // it had it already (a resend): nothing new happens
	PartnerRefused   = "refused"   // it will not take it (Code says why): do not send it again
)

// Why a partner refused an envelope or failed to deliver it (PartnerResult.Code, PartnerEnvelope.Code).
const (
	PartnerCodeInvalid     = "invalid"      // the envelope does not read (an unknown kind, a bad message_id)
	PartnerCodeTooLong     = "too_long"     // the text is over PartnerTextMax
	PartnerCodeHopLimit    = "hop_limit"    // the exchange went past PartnerHopMax
	PartnerCodeRateLimited = "rate_limited" // the partner had too many messages from this hub in a short while
	PartnerCodeNoMain      = "no_main"      // the partner has no main conversation to deliver it to
	PartnerCodeUnpaired    = "unpaired"     // the hubs are no longer paired
	PartnerCodeUndelivered = "undelivered"  // the receiving conversation could not take it
)

// Limits both managers hold an envelope to.
const (
	// PartnerTextMax is the most characters (runes) a message or reply carries.
	PartnerTextMax = 12000
	// PartnerHopMax is how long an exchange may get: a new message is hop 1, its reply hop 2, a reply to that 3, ...
	PartnerHopMax = 6
)

// PartnerEnvelope is one message between two partner hubs' managers. MessageID ("pm_" and 32 lowercase hex digits) is
// minted by the sending manager and is the same on every resend: the receiver keeps it and takes each id once.
type PartnerEnvelope struct {
	MessageID string    `json:"message_id"`
	Kind      string    `json:"kind"`               // PartnerKind*
	ReplyTo   string    `json:"reply_to,omitempty"` // reply / status: the message it is about
	Hop       int       `json:"hop,omitempty"`      // message: 1; reply: the message's hop + 1
	Text      string    `json:"text,omitempty"`     // message / reply: what the assistant wrote, already scrubbed
	State     string    `json:"state,omitempty"`    // status: PartnerState*
	Code      string    `json:"code,omitempty"`     // status failed: PartnerCode*
	SentAt    time.Time `json:"sent_at"`
}

// PartnerSend is partner.send's params: To is the paired hub's hub_id.
type PartnerSend struct {
	To       string          `json:"to"`
	Envelope PartnerEnvelope `json:"envelope"`
}

// PartnerReceive is partner.receive's params.
type PartnerReceive struct {
	FromHub  string          `json:"from_hub"`
	FromName string          `json:"from_name"`
	Envelope PartnerEnvelope `json:"envelope"`
}

// PartnerResult is partner.send's and partner.receive's answer.
type PartnerResult struct {
	Status string `json:"status"`         // PartnerAccepted / PartnerDuplicate / PartnerRefused
	Code   string `json:"code,omitempty"` // refused: PartnerCode*
}

// PartnerHub is one paired hub as the manager sees it.
type PartnerHub struct {
	HubID  string `json:"hub_id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`         // a link is up now
	Dial   string `json:"dial,omitempty"` // PartnerDialAuto / PartnerDialNever: whether this hub dials it
	// PairedAt is when this pairing was made. A hub paired again under the same hub_id (after an unpair) has a later
	// one: what the manager kept for the earlier pairing (its "ask me before sending" setting, messages still waiting)
	// is not this one's.
	PairedAt time.Time `json:"paired_at,omitzero"`
}

// PartnerHubs is partner.hubs's answer and partner.changed's params.
type PartnerHubs struct {
	// Enabled: hub.json partners.enabled is not false and this hub is not a linked-hubs (mesh) member.
	Enabled bool         `json:"enabled"`
	Hubs    []PartnerHub `json:"hubs"`
}

// Whether a hub dials a partner.
const (
	PartnerDialAuto  = "auto"  // dial until there is a link (the partner may dial too: one link is kept)
	PartnerDialNever = "never" // never dial: wait for the partner to (it cannot be reached from here)
)

// partnerRoutes are the managers' partner-hub routes (docs/server/partner-hubs.md "Routes"): the recent messages and
// the per-hub "ask me before sending" setting. The pairing itself is the hub's (/api/v1/partner-hubs).
var partnerRoutes = []string{
	"GET /api/v1/partner-messages",
	"GET /api/v1/partner-messages/prefs",
	"PUT /api/v1/partner-messages/prefs/{hub_id}",
}

func init() { Routes = append(Routes, partnerRoutes...) }
